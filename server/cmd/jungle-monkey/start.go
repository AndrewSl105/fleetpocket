package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/api"
	"github.com/andrewsl105/fleetpocket/server/internal/app"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/pgembed"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/secrets"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/webui"
)

const dockerMissing = `✕ Docker is not running.

  Jungle Monkey runs each task in a Docker container. Start Docker and try again.
  No Docker yet? Install one of:
    Docker Desktop  https://www.docker.com/products/docker-desktop/
    OrbStack (Mac)  https://orbstack.dev/`

func runStart(args []string) error {
	f, _, err := parseFlags("start", args)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	p := config.LocalPaths{Root: cfg.DataDir}
	st := cfg.LocalState
	if c := newControl(p); c != nil && c.ping() {
		fmt.Fprintf(os.Stderr, "Jungle Monkey is already running at %s\n", c.base)
		if f.noOpen {
			return nil
		}
		return runOpen(nil)
	}
	if f.port != 0 && f.port != st.Port {
		st.Port = f.port
	} else if config.PickPort(st.Port) != st.Port {
		st.Port = config.PickPort(config.DefaultLocalPort) // the remembered port is taken
	}
	if err := config.SaveLocalState(p, st); err != nil {
		return err
	}
	cfg.HTTPAddr = fmt.Sprintf("127.0.0.1:%d", st.Port)
	cfg.PublicBaseURL = fmt.Sprintf("http://127.0.0.1:%d", st.Port)

	logf, err := os.OpenFile(filepath.Join(p.Logs(), "jungle-monkey.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	var logw io.Writer = logf
	if f.verbose {
		logw = io.MultiWriter(logf, os.Stderr)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(logw, &slog.HandlerOptions{Level: slog.LevelInfo})))

	fmt.Fprintf(os.Stderr, "\n%s %s\n\n", bold("🐒 Jungle Monkey"), dim(config.Version))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. Docker
	s := startStep("Docker")
	cfg.DockerHost = dockerHost()
	dc, err := runnermgr.NewDockerClient(cfg.DockerHost)
	if err == nil {
		pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = dc.Ping(pctx, dockerPingOptions)
		pcancel()
	}
	if s.finish(err) != nil {
		return &exitError{code: 2, msg: dockerMissing}
	}
	docker := &runnermgr.Manager{Docker: dc}

	// 2. Credentials: agent keys from the keychain (or env), as fleetpocket-node does.
	sec := secrets.New(cfg.DataDir)
	fillAgentKeys(cfg, sec)
	if err := ensureSecretsKey(cfg, sec); err != nil {
		return fmt.Errorf("secrets key: %w", err)
	}

	// 3. Database
	label := "Database"
	db := &pgembed.DB{Dir: p.DB(), Password: st.DBPassword, Log: logf}
	if db.NeedsDownload() {
		label = "Database (first run: downloading Postgres)"
	}
	s = startStep(label)
	db.Progress = s.progress
	if err := s.finish(db.Start()); err != nil {
		return fmt.Errorf("database: %w (details in %s)", err, filepath.Join(p.Logs(), "jungle-monkey.log"))
	}
	defer func() { _ = db.Stop() }()
	cfg.DatabaseURL = db.URL()
	if err := cfg.Validate(); err != nil {
		return err
	}

	// 4. Agent image, pinned to this binary's version.
	label = "Agent image"
	if docker.HasImage(ctx, cfg.RunnerImage) != nil {
		label = "Agent image (first run: downloading " + cfg.RunnerImage + ")"
	}
	s = startStep(label)
	if err := s.finish(docker.PullImageProgress(ctx, cfg.RunnerImage, s.progress)); err != nil {
		return err
	}

	// 5. Server
	statusStore, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer statusStore.Close()
	local := &api.Local{Port: st.Port, ControlToken: st.ControlToken, Shutdown: cancel}
	local.Status = func(ctx context.Context) map[string]any {
		out := map[string]any{"version": config.Version, "url": cfg.PublicBaseURL, "pid": os.Getpid(),
			"docker": errString(docker.Ping(ctx)), "postgres": errString(statusStore.Ping(ctx))}
		if counts, err := statusStore.CountByStatus(ctx); err == nil {
			for _, c := range counts {
				out["tasks_"+c.Status] = c.Count
			}
		}
		return out
	}
	var ui = webui.Dist()
	opts := app.Options{Local: local}
	if ui != nil {
		opts.UI = webui.Handler(ui)
	}
	opts.Listening = func() {
		_ = os.WriteFile(p.PIDFile(), []byte(strconv.Itoa(os.Getpid())), 0o600)
		code, err := local.IssueLoginCode()
		if err != nil {
			slog.Error("login code", "err", err)
			return
		}
		u := local.LoginURL(code)
		fmt.Fprintf(os.Stderr, "\n  %s %s\n", green("Ready:"), bold(cfg.PublicBaseURL))
		if ui == nil {
			fmt.Fprintln(os.Stderr, dim("  (built without the embedweb tag: run the web app with `npm run dev` in web/)"))
		}
		if !f.noOpen && openBrowser(u) == nil {
			fmt.Fprintln(os.Stderr, dim("  Opened your browser. Ctrl-C stops Jungle Monkey."))
		} else {
			fmt.Fprintf(os.Stderr, "  Log in: %s %s\n", u, dim("(valid 60 s; `jungle-monkey open` makes a new one)"))
		}
	}
	opts.Stopping = func(ctx context.Context, _ *store.Store) {
		fmt.Fprintln(os.Stderr, "\n  Stopping…")
		if st.KeepTasksRunning {
			return
		}
		runners, err := docker.ListRunners(ctx)
		if err != nil {
			return
		}
		for _, r := range runners {
			if r.Running {
				_ = docker.Stop(ctx, r.ID, 10*time.Second)
			}
		}
	}
	defer func() { _ = os.Remove(p.PIDFile()) }()
	return app.Serve(ctx, cancel, cfg, opts)
}

// fillAgentKeys takes agent credentials the environment did not set from the
// keychain (ours or fleetpocket-node's) or the fallback file.
func fillAgentKeys(cfg *config.Config, sec *secrets.Store) {
	for _, k := range []struct {
		dst  *string
		name string
	}{
		{&cfg.ClaudeOAuthToken, secrets.AgentKeys[0]},
		{&cfg.AnthropicAPIKey, secrets.AgentKeys[1]},
		{&cfg.OpenAIAPIKey, secrets.AgentKeys[2]},
		{&cfg.CursorAPIKey, secrets.AgentKeys[3]},
	} {
		if *k.dst == "" {
			*k.dst = sec.Get(k.name)
		}
	}
}

// ensureSecretsKey gives local mode its SECRETS_KEY: the environment's, or
// the one kept in the keychain (generated on first use), like the agent keys.
// It stays out of config.json, so the key is not next to the database.
func ensureSecretsKey(cfg *config.Config, sec *secrets.Store) error {
	if cfg.SecretsKey != "" {
		return nil
	}
	if v := sec.Get(secrets.SecretsKey); v != "" {
		cfg.SecretsKey = v
		return nil
	}
	key, err := secretbox.GenerateKey()
	if err != nil {
		return err
	}
	if _, err := sec.Set(secrets.SecretsKey, key); err != nil {
		return err
	}
	cfg.SecretsKey = key
	return nil
}

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return "ok"
}
