// Package app wires the service together: the fleetpocket server (server
// mode) and the jungle-monkey CLI (local mode) both run Serve.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mymmrac/telego"

	"github.com/andrewsl105/fleetpocket/server/internal/api"
	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/cifix"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/githubapp"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/localrepos"
	"github.com/andrewsl105/fleetpocket/server/internal/maintenance"
	"github.com/andrewsl105/fleetpocket/server/internal/mcpgateway"
	"github.com/andrewsl105/fleetpocket/server/internal/metrics"
	"github.com/andrewsl105/fleetpocket/server/internal/nodes"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/scheduler"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/setup"
	"github.com/andrewsl105/fleetpocket/server/internal/skills"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/telegram"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// Options are what the jungle-monkey CLI adds in local mode; server mode
// passes the zero value.
type Options struct {
	// UI is the embedded web app (nil: nginx serves it).
	UI http.Handler
	// Local enables the one-time login link, the Host check and the CLI's
	// control endpoints.
	Local *api.Local
	// Listening is called once the HTTP port accepts connections.
	Listening func()
	// Stopping is called after the HTTP server stopped, before the scheduler
	// and its runners are waited for.
	Stopping func(ctx context.Context, st *store.Store)

	// Local repositories (local mode): the owner's home directory (scanned
	// and browsable), the directory the CLI was started in and the data
	// directory (which cannot be added).
	Home     string
	StartDir string
	DataDir  string
	// Secrets is the keychain holding the owner's GitHub token (local mode).
	Secrets githubapp.TokenStore
}

// Serve runs the service until ctx ends or cancel is called (restart after
// the setup wizard). cfg must be loaded and validated.
func Serve(ctx context.Context, cancel context.CancelFunc, cfg *config.Config, opts Options) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	if err := st.SeedSkills(ctx, seedSkills()); err != nil {
		return fmt.Errorf("seed skills: %w", err)
	}

	settings, err := st.Settings(ctx)
	if err != nil {
		return err
	}
	owner := setup.ResolveOwner(cfg.AdminPasswordHash, cfg.AdminTOTPSecret, settings)
	// Local mode logs in with the CLI's one-time link: no owner to create.
	wizard := setup.New(owner.PasswordHash == "" && !cfg.Local(), cfg.PublicBaseURL)
	cfg.GitHubAppID, cfg.GitHubWebhookSecret = setup.ResolveGitHub(cfg.GitHubAppID, cfg.GitHubWebhookSecret, settings)
	if cfg.GitHubAppID != 0 && cfg.GitHubPrivateKeyPath == "" {
		cfg.GitHubPrivateKeyPath = setup.DefaultPEMPath
	}

	dc, err := runnermgr.NewDockerClient(cfg.DockerHost)
	if err != nil {
		return err
	}
	docker := &runnermgr.Manager{Docker: dc, SidecarMemory: cfg.SidecarMemory}
	ws := &workspace.Manager{Dir: cfg.WorkspacesDir}
	files := &attachments.Files{Dir: cfg.AttachmentsDir}

	local := &hosts.Local{
		Docker: docker, Workspace: ws, AttachmentsDir: files.TaskDir, AgentEnv: cfg.AgentEnv,
	}
	if cfg.SecretsScan == "gitleaks" {
		local.Scanner = workspace.Gitleaks("gitleaks")
	}
	registry := &hosts.Registry{Local: local, MaxSeq: st.MaxSeq}

	met := metrics.New(st)
	hub := events.NewHub()
	taskSvc := &tasks.Service{Store: st, Hub: hub, Files: files, DefaultAgent: cfg.DefaultAgent, AgentEnabled: cfg.AgentEnabled,
		OnBudgetOverride: met.BudgetOverride, WorkspaceRetention: cfg.WorkspaceRetention,
		CheckpointDiff: func(ctx context.Context, t *store.Task, ref, sha string, limit int) (string, bool, error) {
			d, trunc, err := registry.For(t.Node).CheckpointDiff(ctx, t.ID, ref, sha, limit)
			if errors.Is(err, workspace.ErrCheckpointGone) {
				err = fmt.Errorf("%w: %v", tasks.ErrCheckpointGone, err)
			}
			return d, trunc, err
		}}
	sched := scheduler.New(&scheduler.Scheduler{
		Cfg: cfg, Store: st, Hub: hub, Tasks: taskSvc, Hosts: registry, Files: files,
		Hooks: scheduler.Hooks{RunFinished: met.RunFinished, AutoFixOutcome: met.AutoFixOutcome,
			BudgetHeld: met.BudgetHeld, BudgetAlert: met.BudgetAlert},
	})
	// MCP gateway: off (no servers can be added) without SECRETS_KEY.
	box, err := secretbox.New(cfg.SecretsKey)
	if err != nil {
		return err
	}
	if !box.Enabled() {
		slog.Warn("SECRETS_KEY is not set: MCP servers are disabled (rerun deploy/install.sh to generate it)")
	}
	gateway := &mcpgateway.Gateway{
		Store: st, Box: box, Hub: hub, Stdio: &stdioLauncher{docker: docker, agentImage: cfg.RunnerImage},
		Hooks: mcpgateway.Hooks{
			Call: met.MCPCall,
			Blocked: func(_ context.Context, _, _ int64, project, _ string) {
				met.EgressBlocked(project)
			},
		},
	}
	sched.Gateway = gateway
	taskSvc.Executor = sched
	nodeSvc := &nodes.Service{Store: st, Hub: hub, Registry: registry, RunnerImage: cfg.RunnerImage}
	registry.OnChange = func(id string, online bool) {
		nodeSvc.Changed(id, online)
		if online {
			sched.Wake() // its queued tasks may start now
		}
	}
	registry.OnConnect = func(ctx context.Context, h *hosts.Remote, runners []runnermgr.ContainerInfo) {
		sched.ReconcileNode(ctx, h, runners)
	}
	// hostOf resolves a task's host; offline nodes report false.
	hostOf := func(t *store.Task) (maintenance.Host, bool) {
		if t.Node != nil && !registry.IsOnline(*t.Node) {
			return nil, false
		}
		return registry.For(t.Node), true
	}

	var gh *githubapp.App
	var webhook http.Handler
	if cfg.GitHubEnabled() {
		gh, err = githubapp.New(cfg.GitHubAppID, cfg.GitHubPrivateKeyPath, cfg.GitHubAPIURL)
		if err != nil {
			return err
		}
		gh.OnError = met.GitHubError
		taskSvc.GitHub = gh
		ws.Auth = gh.Token
		registry.Auth = gh.Token
		sched.Publisher = &githubapp.Publisher{App: gh, RequireScanner: cfg.SecretsScan == "gitleaks", TaskURL: taskURL(cfg)}
		// CI auto-fix (off unless enabled in Settings or a project).
		fixer := &cifix.Service{
			Store: st, Hub: hub, GitHub: gh, Files: files, Wake: sched.Wake, Holder: sched,
			DefaultBudgetUSD: cfg.DefaultMaxBudgetUSD, Attempt: met.AutoFixAttempt, Outcome: met.AutoFixOutcome,
		}
		taskSvc.AutoFix = fixer
		webhook = &githubapp.Webhook{
			Secret: []byte(cfg.GitHubWebhookSecret), Store: st, Hub: hub, Tasks: taskSvc,
			Cleanup: func(ctx context.Context, id int64) {
				if t, err := st.GetTask(ctx, id); err == nil {
					if h, ok := hostOf(t); ok {
						_ = h.RemoveAll(ctx, id)
					}
				}
			},
			OnCIFailed: fixer.Failed,
			OnCI:       fixer.Changed,
			OnPRClosed: fixer.PRClosed,
		}
	} else if !cfg.Local() {
		slog.Warn("GitHub App not configured: commits stay in local workspaces")
	}

	var skillScan skills.SecretsScanner
	if cfg.SecretsScan == "gitleaks" {
		skillScan = skills.Gitleaks("gitleaks")
	}
	srv := &api.Server{
		SkillScanner: skillScan,
		Webhook:      webhook,
		Metrics:      met.Handler(),
		Cfg:          cfg,
		Store:        st,
		Tasks:        taskSvc,
		Projects:     &projects.Service{Store: st},
		Auth: &auth.Manager{
			Store: st, PasswordHash: owner.PasswordHash, TOTPSecret: owner.TOTPSecret,
			SessionSecret: []byte(cfg.SessionSecret), CookieInsecure: cfg.CookieInsecure,
			SameSiteStrict: cfg.Local(),
		},
		UI:      opts.UI,
		Local:   opts.Local,
		Gateway: gateway,
		Hub:     hub,
		Nodes:   nodeSvc,
		Files:   files,
		Setup:   wizard,
		// Exit after the response is sent; Docker's restart policy starts the
		// server again with the new GitHub App.
		Restart: func() {
			slog.Warn("restarting to enable the new GitHub App")
			time.AfterFunc(2*time.Second, cancel)
		},
		Diff: func(ctx context.Context, t *store.Task, repo *store.Repository, limit int) (string, bool, error) {
			return registry.For(t.Node).Diff(ctx, t.ID, repo.BaseBranch, limit)
		},
		Health: func(ctx context.Context) map[string]error {
			return map[string]error{"database": st.Ping(ctx), "docker": docker.Ping(ctx)}
		},
	}

	if gh != nil {
		srv.GitHub = gh
	}
	if cfg.Local() {
		projSvc := srv.Projects
		srv.LocalRepos = &localrepos.Service{
			Store: st, Home: opts.Home, Start: opts.StartDir, DataDir: opts.DataDir,
			CreateProject: func(ctx context.Context, id, name string) (*store.Project, error) {
				return projSvc.Create(ctx, projects.CreateInput{ID: id, ProjectFields: store.ProjectFields{Name: &name}})
			},
		}
		srv.ResumeDeliveries = sched.ResumeDeliveries
		taskSvc.LocalHost = func(t *store.Task) tasks.LocalBranches { return registry.For(t.Node) }
		taskSvc.PushLocal = sched.PushLocal
	}
	var poller *githubapp.Poller
	if cfg.Local() && opts.Secrets != nil {
		// GitHub with the owner's token: optional, for "Push & open PR" and
		// the PR/CI flow after it. PRs are polled instead of webhooks.
		conn := &githubapp.Connector{ClientID: cfg.GitHubClientID, Store: opts.Secrets}
		user := githubapp.NewUserClient(conn.Token, cfg.GitHubAPIURL)
		user.OnError = met.GitHubError
		conn.Client = user
		// Not connected: git runs without a token, as before.
		ws.Auth = func(ctx context.Context, full string, inst int64) (string, error) {
			if !conn.Connected() {
				return "", nil
			}
			return user.Token(ctx, full, inst)
		}
		taskSvc.GitHub = user
		sched.Publisher = &githubapp.Publisher{App: user, Connected: conn.Connected,
			RequireScanner: cfg.SecretsScan == "gitleaks", TaskURL: taskURL(cfg)}
		fixer := &cifix.Service{
			Store: st, Hub: hub, GitHub: user, Files: files, Wake: sched.Wake, Holder: sched,
			DefaultBudgetUSD: cfg.DefaultMaxBudgetUSD, Attempt: met.AutoFixAttempt, Outcome: met.AutoFixOutcome,
		}
		taskSvc.AutoFix = fixer
		hook := &githubapp.Webhook{
			Store: st, Hub: hub, Tasks: taskSvc,
			Cleanup: func(ctx context.Context, id int64) {
				if t, err := st.GetTask(ctx, id); err == nil {
					_ = registry.For(t.Node).RemoveAll(ctx, id)
				}
			},
			OnCIFailed: fixer.Failed, OnCI: fixer.Changed, OnPRClosed: fixer.PRClosed,
		}
		poller = &githubapp.Poller{Store: st, Hook: hook, Token: conn.Token, APIURL: cfg.GitHubAPIURL}
		srv.LocalGitHub = conn
	}

	var wg sync.WaitGroup
	if cfg.TelegramEnabled() {
		tg, err := telego.NewBot(cfg.TelegramBotToken, telego.WithDiscardLogger())
		if err != nil {
			return fmt.Errorf("telegram: %w", err)
		}
		bot := &telegram.Bot{
			API: tg, Store: st, Tasks: taskSvc, Hub: hub, OwnerID: cfg.TelegramAllowedUserID,
			WebAppURL: cfg.WebAppURL, MiniAppLink: cfg.TelegramMiniAppLink, OnError: met.TelegramError,
			STTEnabled: cfg.STTProvider != "off",
		}
		switch cfg.STTProvider {
		case "openai":
			bot.STT = &telegram.OpenAITranscriber{APIKey: cfg.OpenAIAPIKey}
		case "whispercpp":
			bot.STT = &telegram.WhisperCPPTranscriber{URL: cfg.WhisperCPPURL}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := bot.Run(ctx, tg); err != nil {
				slog.Error("telegram bot stopped", "err", err)
			}
		}()
	}
	if poller != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			poller.Run(ctx)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		(&maintenance.Maintainer{
			Store: st, Host: hostOf, Attachments: files,
			EventRetention: cfg.EventRetention, WorkspaceRetention: cfg.WorkspaceRetention, SidecarIdleTTL: cfg.SidecarIdleTTL,
		}).Run(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		files.Watch(ctx, st, hub)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := sched.Run(ctx); err != nil {
			slog.Error("scheduler stopped", "err", err)
		}
	}()
	err = serveHTTP(ctx, cfg.HTTPAddr, srv.Handler(), opts.Listening)
	if opts.Stopping != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		opts.Stopping(stopCtx, st)
		stopCancel()
	}
	cancel()
	wg.Wait()
	return err
}

func serveHTTP(ctx context.Context, addr string, h http.Handler, listening func()) error {
	hs := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	slog.Info("listening", "addr", addr)
	if listening != nil {
		listening()
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := hs.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// taskURL links PRs and Telegram messages to the task in the web app.
func taskURL(cfg *config.Config) func(int64) string {
	return func(id int64) string {
		base := cfg.WebAppURL
		if base == "" {
			base = cfg.PublicBaseURL
		}
		if base == "" {
			return ""
		}
		return fmt.Sprintf("%s/tasks/%d", base, id)
	}
}

// stdioLauncher runs the gateway's stdio MCP servers on the server's Docker.
type stdioLauncher struct {
	docker     *runnermgr.Manager
	agentImage string
}

func (l *stdioLauncher) StartStdio(ctx context.Context, s mcpgateway.StdioSpec) (io.ReadCloser, io.WriteCloser, func(), error) {
	c, err := mcpgateway.StdioCommand(s, l.agentImage)
	if err != nil {
		return nil, nil, nil, err
	}
	return l.docker.StartStdio(ctx, runnermgr.StdioSpec{
		TaskID: s.TaskID, Project: s.Project, Name: s.Name,
		Image: c.Image, Entrypoint: c.Entrypoint, Cmd: c.Cmd, Env: c.Env, User: c.User,
	})
}

// seedSkills converts the embedded diagram skills for the first-run seed.
func seedSkills() []store.Skill {
	var out []store.Skill
	for _, sk := range skills.Seed() {
		name, desc, _ := skills.Frontmatter(sk.Body)
		files := []store.SkillFile{}
		for _, f := range sk.Files {
			files = append(files, store.SkillFile{Path: f.Path, Content: f.Content})
		}
		out = append(out, store.Skill{Slug: sk.Slug, Name: name, Description: desc, Body: sk.Body, Files: files})
	}
	return out
}
