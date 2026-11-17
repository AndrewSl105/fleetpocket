package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/localrepos"
	"github.com/andrewsl105/fleetpocket/server/internal/pgembed"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/secrets"
)

type check struct {
	name string
	ok   bool
	info string // shown after the name
	fix  string // shown when !ok
}

func runDoctor(args []string) error {
	if _, _, err := parseFlags("doctor", args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := config.LocalPaths{Root: cfg.DataDir}
	var checks []check
	add := func(c check) { checks = append(checks, c) }

	if runtime.GOOS == "windows" {
		add(check{name: "Windows", ok: true, info: "supported with Docker Desktop (Linux containers); service install uses Task Scheduler"})
	}

	// Docker
	host := dockerHost()
	dc, err := runnermgr.NewDockerClient(host)
	var dockerOK bool
	if err == nil {
		var v client.ServerVersionResult
		if v, err = dc.ServerVersion(ctx, client.ServerVersionOptions{}); err == nil {
			dockerOK = true
			add(check{name: "Docker", ok: true, info: v.Version + " " + dim(orDefault(host, "default socket"))})
		}
	}
	if !dockerOK {
		add(check{name: "Docker", fix: "start Docker Desktop / OrbStack / Colima, or set DOCKER_HOST (" + errText(err) + ")"})
	}

	// Disk
	if free, err := freeDisk(cfg.DataDir); err == nil {
		add(check{name: "Disk", ok: free > 5<<30, info: fmt.Sprintf("%d GB free in %s", free>>30, cfg.DataDir),
			fix: "free at least 5 GB (agent image, Postgres, workspaces)"})
	}

	// Runner image
	if dockerOK {
		m := &runnermgr.Manager{Docker: dc}
		err := m.HasImage(ctx, cfg.RunnerImage)
		add(check{name: "Agent image", ok: err == nil, info: cfg.RunnerImage, fix: "`jungle-monkey start` pulls it, or: docker pull " + cfg.RunnerImage})
	}

	// Postgres
	db := &pgembed.DB{Dir: p.DB()}
	pgErr := pgembed.LiveLock(p.DB())
	switch {
	case db.NeedsDownload():
		add(check{name: "Postgres", ok: true, info: "not downloaded yet (first start downloads ~25 MB)"})
	case pgErr != nil:
		add(check{name: "Postgres", ok: isRunning(p), info: pgErr.Error(), fix: "stop the other instance (`jungle-monkey stop`)"})
	default:
		add(check{name: "Postgres", ok: true, info: "binaries cached in " + p.DB()})
	}

	// Credentials
	sec := secrets.New(cfg.DataDir)
	fillAgentKeys(cfg, sec)
	agents := cfg.EnabledAgents()
	add(check{name: "Agent credentials", ok: len(agents) > 0, info: strings.Join(agents, ", "),
		fix: "run `claude setup-token` and add the token in the web app (Settings → Agents), or set ANTHROPIC_API_KEY / OPENAI_API_KEY / CURSOR_API_KEY"})

	// Local repositories (what the folder picker would suggest).
	if home, err := os.UserHomeDir(); err == nil {
		cwd, _ := os.Getwd()
		lr := &localrepos.Service{Home: home, Start: workTree(cwd)}
		found, _ := lr.Suggestions(ctx)
		info := fmt.Sprintf("%d git repositories found in your code folders", len(found))
		if len(found) == 0 {
			info = "none found in ~/code, ~/Projects, ~/dev, ~/src, ~/repos… (pick any folder in the web app, or `jungle-monkey add <path>`)"
		}
		add(check{name: "Local repos", ok: true, info: info})
	}

	// GitHub: optional in local mode (push and open PRs).
	clockChecked := false
	if tok := sec.Get(secrets.GitHubToken); tok != "" {
		scopes, date, err := githubWhoami(ctx, tok)
		info := "connected, token scopes: " + orDefault(scopes, "fine-grained")
		if err == nil && scopes != "" && !hasScope(scopes, "repo") {
			info += " (no `repo` scope: pushing to private repositories will fail)"
		}
		add(check{name: "GitHub", ok: err == nil, info: info,
			fix: "reconnect GitHub in the web app, Settings → GitHub (" + errText(err) + ")"})
		if !date.IsZero() {
			clockChecked = true
			addClock(add, date)
		}
	} else {
		add(check{name: "GitHub", ok: true, info: "not connected (optional: connect it in the web app to push and open PRs)"})
	}

	// Keychain
	if sec.KeychainAvailable() {
		add(check{name: "Keychain", ok: true, info: "secrets in the OS keychain"})
	} else {
		add(check{name: "Keychain", ok: true, info: "no keychain: secrets in " + sec.FallbackFile + " (0600)"})
	}

	// Port
	if st := cfg.LocalState; st != nil {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", st.Port))
		switch {
		case err == nil:
			_ = l.Close()
			add(check{name: "Port", ok: true, info: fmt.Sprintf("127.0.0.1:%d free", st.Port)})
		case isRunning(p):
			add(check{name: "Port", ok: true, info: fmt.Sprintf("127.0.0.1:%d used by Jungle Monkey", st.Port)})
		default:
			add(check{name: "Port", fix: fmt.Sprintf("127.0.0.1:%d is taken: start with --port N", st.Port)})
		}
	}

	if !clockChecked {
		if date, err := httpDate(ctx, "https://api.github.com"); err == nil {
			addClock(add, date)
		}
	}

	failed := 0
	for _, c := range checks {
		if c.ok {
			fmt.Printf("%s %-18s %s\n", green("✓"), c.name, dim(c.info))
			continue
		}
		failed++
		fmt.Printf("%s %-18s %s\n", red("✕"), c.name, c.info)
		fmt.Printf("  %s %s\n", dim("fix:"), c.fix)
	}
	if failed > 0 {
		return &exitError{code: 1, msg: fmt.Sprintf("\n%d check(s) failed", failed)}
	}
	return nil
}

func addClock(add func(check), server time.Time) {
	skew := time.Since(server)
	if skew < 0 {
		skew = -skew
	}
	add(check{name: "Clock", ok: skew < time.Minute, info: fmt.Sprintf("skew %s", skew.Round(time.Second)),
		fix: "sync the system clock (GitHub tokens and TOTP need it)"})
}

// githubWhoami checks the token and returns its classic scopes (empty for
// fine-grained tokens) and the server's Date header.
func githubWhoami(ctx context.Context, token string) (string, time.Time, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	date, _ := http.ParseTime(resp.Header.Get("Date"))
	if resp.StatusCode != http.StatusOK {
		return "", date, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.Header.Get("X-OAuth-Scopes"), date, nil
}

func httpDate(ctx context.Context, url string) (time.Time, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	_ = resp.Body.Close()
	return http.ParseTime(resp.Header.Get("Date"))
}

func isRunning(p config.LocalPaths) bool {
	c := newControl(p)
	return c != nil && c.ping()
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

// hasScope reports whether a comma-separated X-OAuth-Scopes list has scope.
func hasScope(list, scope string) bool {
	for _, s := range strings.Split(list, ",") {
		if strings.TrimSpace(s) == scope {
			return true
		}
	}
	return false
}
