// Package api serves the REST API, the WebSocket and the GitHub webhook.
package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/mcpgateway"
	"github.com/andrewsl105/fleetpocket/server/internal/nodes"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/setup"
	"github.com/andrewsl105/fleetpocket/server/internal/skills"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

var errGitHubDisabled = errors.New("GitHub App is not configured")

// GitHubRepo is one entry of the "add repo" picker.
type GitHubRepo struct {
	FullName       string `json:"full_name"`
	DefaultBranch  string `json:"default_branch"`
	Private        bool   `json:"private"`
	InstallationID int64  `json:"installation_id"`
}

// GitHub lists repositories the App can see. Nil when the App is not configured.
type GitHub interface {
	ListRepositories(ctx context.Context) ([]GitHubRepo, error)
}

// DiffFunc returns `git diff base...branch` for a task, capped by the caller.
type DiffFunc func(ctx context.Context, t *store.Task, repo *store.Repository, limit int) (diff string, truncated bool, err error)

type Server struct {
	Cfg      *config.Config
	Store    *store.Store
	Tasks    *tasks.Service
	Projects *projects.Service
	// SkillScanner is the secrets scan for skill files; nil skips it (SECRETS_SCAN=off).
	SkillScanner skills.SecretsScanner
	Auth         *auth.Manager
	Hub          *events.Hub
	// Nodes registers computers that run tasks; nil disables the endpoints.
	Nodes   *nodes.Service
	GitHub  GitHub
	Diff    DiffFunc
	Webhook http.Handler
	Metrics http.Handler
	Health  func(ctx context.Context) map[string]error
	Limiter *auth.FailLimiter
	Tickets *auth.Tickets
	// Files serves attachments; nil disables the download endpoint.
	Files *attachments.Files
	// Setup is the first-run wizard; nil or inactive once the owner exists.
	Setup *setup.Wizard
	// Restart restarts the process after the wizard created the GitHub App.
	Restart func()
	// UI serves the embedded web app at / (builds with the embedweb tag);
	// nil when nginx serves it (server mode default).
	UI http.Handler
	// Local is set in local mode only.
	Local *Local
	// Gateway is the MCP gateway (also the MCP server registry); its own
	// endpoints under /api/mcp-gateway/ take the run token, no session.
	Gateway *mcpgateway.Gateway
	Now     func() time.Time
}

func (s *Server) githubConfigured() bool {
	return s.GitHub != nil || (s.Setup != nil && s.Setup.GitHubCreated())
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) Handler() http.Handler {
	if s.Limiter == nil {
		s.Limiter = auth.NewFailLimiter(5, 15*time.Minute)
	}
	if s.Tickets == nil {
		s.Tickets = auth.NewTickets()
	}
	mux := http.NewServeMux()
	a := s.requireSession

	mux.HandleFunc("GET /api/v1/setup/status", s.setupStatus)
	mux.HandleFunc("POST /api/v1/setup/owner", s.setupOwner)
	mux.HandleFunc("POST /api/v1/setup/owner/confirm", s.setupOwnerConfirm)
	mux.HandleFunc("POST /api/v1/setup/github/manifest", a(s.setupGitHubManifest))
	mux.HandleFunc("GET /api/v1/setup/github/callback", a(s.setupGitHubCallback))
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/telegram", s.loginTelegram)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("GET /api/v1/me", a(s.me))

	mux.HandleFunc("GET /api/v1/projects", a(s.listProjects))
	mux.HandleFunc("POST /api/v1/projects", a(s.createProject))
	mux.HandleFunc("GET /api/v1/projects/{id}", a(s.getProject))
	mux.HandleFunc("PATCH /api/v1/projects/{id}", a(s.updateProject))
	mux.HandleFunc("DELETE /api/v1/projects/{id}", a(s.archiveProject))
	mux.HandleFunc("POST /api/v1/projects/{id}/repositories", a(s.addRepository))
	mux.HandleFunc("PATCH /api/v1/projects/{id}/repositories/{repoId}", a(s.updateRepository))
	mux.HandleFunc("DELETE /api/v1/projects/{id}/repositories/{repoId}", a(s.deleteRepository))
	mux.HandleFunc("GET /api/v1/github/repositories", a(s.githubRepositories))

	mux.HandleFunc("GET /api/v1/skills", a(s.listSkills))
	mux.HandleFunc("POST /api/v1/skills", a(s.createSkill))
	mux.HandleFunc("POST /api/v1/skills/import", a(s.importSkill))
	mux.HandleFunc("GET /api/v1/skills/{id}", a(s.getSkill))
	mux.HandleFunc("PUT /api/v1/skills/{id}", a(s.updateSkill))
	mux.HandleFunc("DELETE /api/v1/skills/{id}", a(s.deleteSkill))
	mux.HandleFunc("POST /api/v1/skills/{id}/duplicate", a(s.duplicateSkill))
	mux.HandleFunc("GET /api/v1/skills/{id}/export", a(s.exportSkill))

	mux.HandleFunc("GET /api/v1/tasks", a(s.listTasks))
	mux.HandleFunc("POST /api/v1/tasks", a(s.createTask))
	mux.HandleFunc("GET /api/v1/tasks/{id}", a(s.getTask))
	mux.HandleFunc("GET /api/v1/tasks/{id}/events", a(s.taskEvents))
	mux.HandleFunc("GET /api/v1/tasks/{id}/runs", a(s.taskRuns))
	mux.HandleFunc("GET /api/v1/tasks/{id}/diff", a(s.taskDiff))
	mux.HandleFunc("GET /api/v1/tasks/{id}/attachments/{attachmentId}", a(s.attachment))
	mux.HandleFunc("POST /api/v1/tasks/{id}/answer", a(s.answer))
	mux.HandleFunc("POST /api/v1/tasks/{id}/message", a(s.message))
	mux.HandleFunc("POST /api/v1/tasks/{id}/interrupt", a(s.control(s.Tasks.Interrupt)))
	mux.HandleFunc("POST /api/v1/tasks/{id}/cancel", a(s.control(s.Tasks.Cancel)))
	mux.HandleFunc("POST /api/v1/tasks/{id}/retry", a(s.control(s.Tasks.Retry)))
	mux.HandleFunc("POST /api/v1/tasks/{id}/merge", a(s.control(s.Tasks.Merge)))
	mux.HandleFunc("GET /api/v1/stats", a(s.stats))
	mux.HandleFunc("GET /api/v1/mcp-servers", a(s.listMCPServers))
	mux.HandleFunc("POST /api/v1/mcp-servers", a(s.createMCPServer))
	mux.HandleFunc("GET /api/v1/mcp-servers/{id}", a(s.getMCPServer))
	mux.HandleFunc("PUT /api/v1/mcp-servers/{id}", a(s.updateMCPServer))
	mux.HandleFunc("DELETE /api/v1/mcp-servers/{id}", a(s.deleteMCPServer))
	mux.HandleFunc("POST /api/v1/mcp-servers/{id}/test", a(s.testMCPServer))
	mux.HandleFunc("GET /api/v1/projects/{id}/integrations", a(s.getIntegrations))
	mux.HandleFunc("PUT /api/v1/projects/{id}/integrations", a(s.putIntegrations))
	mux.HandleFunc("POST /api/v1/projects/{id}/egress/allow", a(s.allowEgress))
	mux.HandleFunc("GET /api/v1/settings/ci-autofix", a(s.getAutoFixDefaults))
	mux.HandleFunc("PUT /api/v1/settings/ci-autofix", a(s.putAutoFixDefaults))
	mux.HandleFunc("GET /api/v1/settings/costs", a(s.getCostSettings))
	mux.HandleFunc("PUT /api/v1/settings/costs", a(s.putCostSettings))
	mux.HandleFunc("GET /api/v1/costs", a(s.getCosts))
	mux.HandleFunc("POST /api/v1/tasks/{id}/budget-override", a(s.budgetOverride))
	if s.Nodes != nil {
		mux.HandleFunc("GET /api/v1/nodes", a(s.listNodes))
		mux.HandleFunc("POST /api/v1/nodes", a(s.createNode))
		mux.HandleFunc("PATCH /api/v1/nodes/{id}", a(s.updateNode))
		mux.HandleFunc("POST /api/v1/nodes/{id}/token", a(s.rotateNodeToken))
		mux.HandleFunc("DELETE /api/v1/nodes/{id}", a(s.deleteNode))
		mux.HandleFunc("POST /api/v1/nodes/pair", a(s.approvePairing))
		mux.HandleFunc("GET /api/v1/nodes/pair/{code}", a(s.lookupPairing))
		// fleetpocket-node setup: no session (see nodes/pairing.go).
		mux.HandleFunc("POST /api/v1/node/pair", s.startPairing)
		mux.HandleFunc("GET /api/v1/node/pair/{id}", s.pollPairing)
		mux.HandleFunc("GET /api/v1/node/install", s.installScript)
		// The node's own WebSocket: bearer node token, no session.
		mux.HandleFunc("GET /api/v1/node/connect", s.Nodes.Serve)
	}
	if s.Local != nil {
		mux.HandleFunc("GET /auth/local", s.localLogin)
		mux.HandleFunc("POST /api/v1/local/login-code", s.requireControl(s.localLoginCode))
		mux.HandleFunc("GET /api/v1/local/status", s.requireControl(s.localStatus))
		mux.HandleFunc("POST /api/v1/local/shutdown", s.requireControl(s.localShutdown))
	}
	mux.HandleFunc("POST /api/v1/ws/ticket", a(s.wsTicket))
	mux.HandleFunc("GET /api/v1/ws", s.requireSessionOrTicket(s.websocket))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})

	if s.Webhook != nil {
		mux.Handle("POST /webhooks/github", s.Webhook)
	}
	mux.HandleFunc("GET /healthz", s.healthz)
	if s.Metrics != nil {
		mux.Handle("GET /metrics", internalOnly(s.Metrics))
	}

	// The webhook carries its own signature auth and must not need the CSRF header.
	api := s.cors(csrf(mux))
	root := http.NewServeMux()
	root.Handle("/webhooks/", mux)
	if s.UI != nil {
		root.Handle("/api/", api)
		root.Handle("/auth/", api)
		root.Handle("/healthz", api)
		root.Handle("/metrics", api)
		root.Handle("/", s.UI)
	} else {
		root.Handle("/", api)
	}
	h := http.Handler(s.setupGate(root))
	if s.Local != nil {
		h = s.localHostCheck(h)
	}
	if s.Gateway != nil {
		// Runners and egress proxies: the run token is the credential, so no
		// session, CSRF, setup or (local mode) Host check applies.
		outer := http.NewServeMux()
		outer.Handle(mcpgateway.PathPrefix, s.Gateway)
		outer.Handle("/", h)
		h = outer
	}
	return logRequests(h)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{}
	ok := true
	if s.Health != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		for name, err := range s.Health(ctx) {
			if err != nil {
				ok = false
				checks[name] = err.Error()
			} else {
				checks[name] = "ok"
			}
		}
	}
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"ok": ok, "checks": checks})
}
