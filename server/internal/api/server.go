// Package api serves the REST API, the WebSocket and the GitHub webhook.
package api

import (
	"context"
	"errors"
	"github.com/andrewsl105/fleetpocket/server/internal/localrepos"
	"net/http"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/attachments"
	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/ideas"
	"github.com/andrewsl105/fleetpocket/server/internal/mcpgateway"
	"github.com/andrewsl105/fleetpocket/server/internal/nodes"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/schedules"
	"github.com/andrewsl105/fleetpocket/server/internal/setup"
	"github.com/andrewsl105/fleetpocket/server/internal/skills"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/team"
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
	// LocalRepos serves the /local repository endpoints; nil (server mode)
	// answers them with 400 local_mode_only.
	LocalRepos *localrepos.Service
	// LocalGitHub connects GitHub with the owner's token (local mode only).
	LocalGitHub LocalGitHub
	// ResumeDeliveries retries deliveries that waited for a moved folder.
	ResumeDeliveries func(ctx context.Context, repoID int64)
	// Gateway is the MCP gateway (also the MCP server registry); its own
	// endpoints under /api/mcp-gateway/ take the run token, no session.
	Gateway *mcpgateway.Gateway
	// Schedules fires recurring tasks.
	Schedules *schedules.Service
	// Ideas is the Ideas board; nil answers its endpoints 409 ideas_disabled.
	Ideas *ideas.Service
	// Team is team mode (users, roles, invites, keys); nil or off is
	// single-owner mode, unchanged.
	Team *team.Service
	// KeyTester runs the "Test" button of My keys (nil: team.DefaultTester).
	KeyTester team.KeyTester
	routes    []Route
	Now       func() time.Time
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
	s.routes = nil
	// Every session route names the role it needs (Policy); in single-owner
	// mode every policy passes.
	route := func(pattern string, p Policy, h http.HandlerFunc) {
		s.routes = append(s.routes, Route{Pattern: pattern, Policy: p})
		if action, ok := auditedRoutes[pattern]; ok {
			h = s.audited(action, h)
		}
		mux.HandleFunc(pattern, s.requireSession(s.policy(p, h)))
	}

	mux.HandleFunc("GET /api/v1/setup/status", s.setupStatus)
	mux.HandleFunc("POST /api/v1/setup/owner", s.setupOwner)
	mux.HandleFunc("POST /api/v1/setup/owner/confirm", s.setupOwnerConfirm)
	route("POST /api/v1/setup/github/manifest", PolicyAdmin, s.setupGitHubManifest)
	route("GET /api/v1/setup/github/callback", PolicyAdmin, s.setupGitHubCallback)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/telegram", s.loginTelegram)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	route("GET /api/v1/me", PolicySelf, s.me)

	route("GET /api/v1/projects", PolicyRead, s.listProjects)
	route("POST /api/v1/projects", PolicyAdmin, s.createProject)
	route("GET /api/v1/projects/{id}", PolicyRead, s.getProject)
	route("PATCH /api/v1/projects/{id}", PolicyAdmin, s.updateProject)
	route("DELETE /api/v1/projects/{id}", PolicyAdmin, s.archiveProject)
	route("POST /api/v1/projects/{id}/repositories", PolicyAdmin, s.addRepository)
	route("PATCH /api/v1/projects/{id}/repositories/{repoId}", PolicyAdmin, s.updateRepository)
	route("DELETE /api/v1/projects/{id}/repositories/{repoId}", PolicyAdmin, s.deleteRepository)
	route("GET /api/v1/github/repositories", PolicyAdmin, s.githubRepositories)

	route("GET /api/v1/skills", PolicyRead, s.listSkills)
	route("POST /api/v1/skills", PolicyWrite, s.createSkill)
	route("POST /api/v1/skills/import", PolicyWrite, s.importSkill)
	route("GET /api/v1/skills/{id}", PolicyRead, s.getSkill)
	route("PUT /api/v1/skills/{id}", PolicyWrite, s.updateSkill)
	route("DELETE /api/v1/skills/{id}", PolicyWrite, s.deleteSkill)
	route("POST /api/v1/skills/{id}/duplicate", PolicyWrite, s.duplicateSkill)
	route("GET /api/v1/skills/{id}/export", PolicyRead, s.exportSkill)

	route("GET /api/v1/schedules", PolicyRead, s.listSchedules)
	route("POST /api/v1/schedules", PolicyWrite, s.createSchedule)
	route("GET /api/v1/schedules/{id}", PolicyRead, s.getSchedule)
	route("PUT /api/v1/schedules/{id}", PolicyOwnSchedule, s.updateSchedule)
	route("DELETE /api/v1/schedules/{id}", PolicyOwnSchedule, s.deleteSchedule)
	route("POST /api/v1/schedules/{id}/enable", PolicyOwnSchedule, s.setScheduleEnabled(true))
	route("POST /api/v1/schedules/{id}/disable", PolicyOwnSchedule, s.setScheduleEnabled(false))
	route("POST /api/v1/schedules/{id}/run", PolicyOwnSchedule, s.runSchedule)
	route("POST /api/v1/schedules/{id}/duplicate", PolicyWrite, s.duplicateSchedule)
	route("GET /api/v1/schedules/{id}/firings", PolicyRead, s.scheduleFirings)

	route("GET /api/v1/settings/ideas", PolicyRead, s.getIdeaSettings)
	route("PUT /api/v1/settings/ideas", PolicyAdmin, s.putIdeaSettings)
	route("GET /api/v1/ideas", PolicyRead, s.listIdeas)
	route("POST /api/v1/ideas", PolicyWrite, s.createIdea)
	route("POST /api/v1/ideas/voice", PolicyWrite, s.createVoiceIdea)
	route("GET /api/v1/ideas/{id}", PolicyRead, s.ideaHandler(s.getIdea))
	route("PATCH /api/v1/ideas/{id}", PolicyWrite, s.ideaHandler(s.updateIdea))
	route("DELETE /api/v1/ideas/{id}", PolicyOwnIdea, s.ideaHandler(s.deleteIdea))
	route("POST /api/v1/ideas/{id}/move", PolicyWrite, s.ideaHandler(s.moveIdea))
	route("POST /api/v1/ideas/{id}/archive", PolicyOwnIdea, s.ideaHandler(s.archiveIdea(true)))
	route("POST /api/v1/ideas/{id}/unarchive", PolicyOwnIdea, s.ideaHandler(s.archiveIdea(false)))
	route("POST /api/v1/ideas/{id}/attachments", PolicyWrite, s.ideaHandler(s.addIdeaFiles))
	route("POST /api/v1/ideas/{id}/voice", PolicyWrite, s.ideaHandler(s.addIdeaVoice))
	route("POST /api/v1/ideas/{id}/links", PolicyWrite, s.ideaHandler(s.addIdeaLink))
	route("GET /api/v1/ideas/{id}/attachments/{attachmentId}", PolicyRead, s.ideaHandler(s.ideaAttachment))
	route("DELETE /api/v1/ideas/{id}/attachments/{attachmentId}", PolicyWrite, s.ideaHandler(s.deleteIdeaAttachment))

	route("GET /api/v1/tasks", PolicyRead, s.listTasks)
	route("POST /api/v1/tasks", PolicyWrite, s.createTask)
	route("GET /api/v1/tasks/{id}", PolicyRead, s.getTask)
	route("GET /api/v1/tasks/{id}/events", PolicyRead, s.taskEvents)
	route("GET /api/v1/tasks/{id}/runs", PolicyRead, s.taskRuns)
	route("GET /api/v1/tasks/{id}/diff", PolicyRead, s.taskDiff)
	route("GET /api/v1/tasks/{id}/attachments/{attachmentId}", PolicyRead, s.attachment)
	route("POST /api/v1/tasks/{id}/answer", PolicyOwnTask, s.answer)
	route("POST /api/v1/tasks/{id}/message", PolicyOwnTask, s.message)
	route("POST /api/v1/tasks/{id}/interrupt", PolicyOwnTask, s.control(s.Tasks.Interrupt))
	route("POST /api/v1/tasks/{id}/cancel", PolicyOwnTask, s.control(s.Tasks.Cancel))
	route("POST /api/v1/tasks/{id}/retry", PolicyOwnTask, s.control(s.Tasks.Retry))
	route("POST /api/v1/tasks/{id}/merge", PolicyOwnTask, s.control(s.Tasks.Merge))
	route("POST /api/v1/tasks/{id}/branch/merge", PolicyOwnTask, s.control(s.Tasks.MergeBranch))
	route("POST /api/v1/tasks/{id}/branch/checkout", PolicyOwnTask, s.control(s.Tasks.CheckoutBranch))
	route("POST /api/v1/tasks/{id}/branch/discard", PolicyOwnTask, s.control(s.Tasks.DiscardBranch))
	route("POST /api/v1/tasks/{id}/branch/push", PolicyOwnTask, s.control(s.Tasks.PushBranch))
	route("GET /api/v1/local/repos/suggestions", PolicyAdmin, s.localRepos(s.localSuggestions))
	route("GET /api/v1/local/fs", PolicyAdmin, s.localRepos(s.localBrowse))
	mux.HandleFunc("POST /api/v1/local/repos", s.sessionOrControl(s.policy(PolicyAdmin, s.localRepos(s.addLocalRepo))))
	route("GET /api/v1/local/repos/{id}/status", PolicyAdmin, s.localRepos(s.localRepoStatus))
	route("POST /api/v1/local/repos/{id}/locate", PolicyAdmin, s.localRepos(s.locateLocalRepo))
	route("GET /api/v1/local/github", PolicyAdmin, s.localGitHub(s.githubStatus))
	route("DELETE /api/v1/local/github", PolicyAdmin, s.localGitHub(s.githubDisconnect))
	route("POST /api/v1/local/github/device", PolicyAdmin, s.localGitHub(s.githubDeviceStart))
	route("POST /api/v1/local/github/device/poll", PolicyAdmin, s.localGitHub(s.githubDevicePoll))
	route("POST /api/v1/local/github/gh", PolicyAdmin, s.localGitHub(s.githubImportGH))
	route("POST /api/v1/local/github/token", PolicyAdmin, s.localGitHub(s.githubSetToken))
	route("GET /api/v1/stats", PolicyRead, s.stats)
	route("GET /api/v1/mcp-servers", PolicyRead, s.listMCPServers)
	route("POST /api/v1/mcp-servers", PolicyAdmin, s.createMCPServer)
	route("GET /api/v1/mcp-servers/{id}", PolicyRead, s.getMCPServer)
	route("PUT /api/v1/mcp-servers/{id}", PolicyAdmin, s.updateMCPServer)
	route("DELETE /api/v1/mcp-servers/{id}", PolicyAdmin, s.deleteMCPServer)
	route("POST /api/v1/mcp-servers/{id}/test", PolicyAdmin, s.testMCPServer)
	route("GET /api/v1/projects/{id}/integrations", PolicyRead, s.getIntegrations)
	route("PUT /api/v1/projects/{id}/integrations", PolicyAdmin, s.putIntegrations)
	route("POST /api/v1/projects/{id}/egress/allow", PolicyAdmin, s.allowEgress)
	route("GET /api/v1/settings/ci-autofix", PolicyRead, s.getAutoFixDefaults)
	route("PUT /api/v1/settings/ci-autofix", PolicyAdmin, s.putAutoFixDefaults)
	route("GET /api/v1/settings/costs", PolicyRead, s.getCostSettings)
	route("PUT /api/v1/settings/costs", PolicyAdmin, s.putCostSettings)
	route("GET /api/v1/costs", PolicyRead, s.getCosts)
	route("POST /api/v1/tasks/{id}/budget-override", PolicyAdmin, s.budgetOverride)
	route("GET /api/v1/settings/checkpoints", PolicyRead, s.getCheckpointSettings)
	route("PUT /api/v1/settings/checkpoints", PolicyAdmin, s.putCheckpointSettings)
	route("GET /api/v1/tasks/{id}/checkpoints", PolicyRead, s.listCheckpoints)
	route("GET /api/v1/tasks/{id}/checkpoints/{checkpointId}", PolicyRead, s.previewCheckpoint)
	route("POST /api/v1/tasks/{id}/checkpoints/{checkpointId}/restore", PolicyOwnTask, s.restoreCheckpoint)
	if s.Nodes != nil {
		route("GET /api/v1/nodes", PolicyRead, s.listNodes)
		route("POST /api/v1/nodes", PolicyWrite, s.createNode)
		route("PATCH /api/v1/nodes/{id}", PolicyWrite, s.updateNode)
		route("POST /api/v1/nodes/{id}/token", PolicyWrite, s.rotateNodeToken)
		route("DELETE /api/v1/nodes/{id}", PolicyWrite, s.deleteNode)
		route("POST /api/v1/nodes/pair", PolicyWrite, s.approvePairing)
		route("GET /api/v1/nodes/pair/{code}", PolicyWrite, s.lookupPairing)
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
	route("POST /api/v1/ws/ticket", PolicySelf, s.wsTicket)
	mux.HandleFunc("GET /api/v1/ws", s.requireSessionOrTicket(s.policy(PolicyRead, s.websocket)))

	// Teams (docs/SPEC-server.md "Teams"): 400 not_available_in_local_mode
	// in local mode.
	tg := s.teamGuard
	mux.HandleFunc("POST /api/v1/signup/start", tg(s.signupStart))
	mux.HandleFunc("POST /api/v1/signup", tg(s.signup))
	route("GET /api/v1/team", PolicyAdmin, tg(s.teamStatus))
	route("POST /api/v1/team/enable", PolicyAdmin, tg(s.teamEnable))
	route("POST /api/v1/team/disable", PolicyAdmin, tg(s.teamDisable))
	route("PATCH /api/v1/team", PolicyAdmin, tg(s.teamSettings))
	route("GET /api/v1/users", PolicyRead, tg(s.listUsers))
	route("PATCH /api/v1/users/{id}", PolicyAdmin, tg(s.updateUser))
	route("DELETE /api/v1/users/{id}", PolicyAdmin, tg(s.removeUser))
	route("GET /api/v1/invites", PolicyAdmin, tg(s.listInvites))
	route("POST /api/v1/invites", PolicyAdmin, tg(s.createInvite))
	route("DELETE /api/v1/invites/{id}", PolicyAdmin, tg(s.revokeInvite))
	route("GET /api/v1/audit", PolicyAdmin, tg(s.listAudit))
	route("PATCH /api/v1/me", PolicySelf, tg(s.updateMe))
	route("POST /api/v1/me/totp", PolicySelf, tg(s.totpResetStart))
	route("POST /api/v1/me/totp/confirm", PolicySelf, tg(s.totpResetFinish))
	route("GET /api/v1/me/credentials", PolicySelf, tg(s.listCredentials))
	route("PUT /api/v1/me/credentials/{provider}", PolicySelf, tg(s.putCredential))
	route("DELETE /api/v1/me/credentials/{provider}", PolicySelf, tg(s.deleteCredential))
	route("POST /api/v1/me/credentials/{provider}/test", PolicySelf, tg(s.testCredential))
	route("POST /api/v1/me/telegram/link", PolicySelf, tg(s.telegramLink))
	route("DELETE /api/v1/me/telegram/link", PolicySelf, tg(s.telegramUnlink))
	route("POST /api/v1/skills/{id}/promote", PolicyAdmin, s.promoteSkill)
	route("PUT /api/v1/nodes/{id}/shared", PolicyAdmin, s.shareNode)

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
