package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/setup"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

const minOwnerPassword = 12

// setupGate answers 409 setup_required while no owner exists, except for the
// health check, webhooks and the setup endpoints themselves.
func (s *Server) setupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		// With the embedded UI, its pages must load to show the wizard.
		uiPage := s.UI != nil && !strings.HasPrefix(p, "/api/")
		if s.Setup != nil && s.Setup.Active() && p != "/healthz" && !uiPage &&
			!strings.HasPrefix(p, "/webhooks/") && !strings.HasPrefix(p, "/api/v1/setup/") {
			writeError(w, http.StatusConflict, "setup_required", "open the setup link from the server log to create the owner")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_required":    s.Setup != nil && s.Setup.Active(),
		"github_configured": s.githubConfigured(),
		// The login screen asks for an email only in team mode.
		"team_enabled": s.Team.Enabled(r.Context()),
	})
}

// setupCodeOK rate-limits and checks the one-time setup code; it writes the
// error response itself.
func (s *Server) setupCodeOK(w http.ResponseWriter, r *http.Request, code string) bool {
	ip := clientIP(r)
	if s.Limiter.Blocked(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts, try again later")
		return false
	}
	if s.Setup == nil || !s.Setup.Active() {
		writeError(w, http.StatusConflict, "setup_done", "the owner already exists; sign in instead")
		return false
	}
	if !s.Setup.CheckCode(code) {
		s.Limiter.Fail(ip)
		writeError(w, http.StatusForbidden, "bad_setup_code", "invalid or expired setup code")
		return false
	}
	return true
}

func (s *Server) setupOwner(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) || !s.setupCodeOK(w, r, in.Code) {
		return
	}
	if len([]rune(in.Password)) < minOwnerPassword {
		writeError(w, http.StatusBadRequest, "invalid", "password must have at least 12 characters")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	secret, uri, err := auth.NewTOTP("owner")
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.Setup.SetPending(setup.Owner{PasswordHash: hash, TOTPSecret: secret})
	writeJSON(w, http.StatusOK, map[string]any{"otpauth_uri": uri, "secret": secret})
}

func (s *Server) setupOwnerConfirm(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
		TOTP string `json:"totp"`
	}
	if !decode(w, r, &in) || !s.setupCodeOK(w, r, in.Code) {
		return
	}
	owner, ok := s.Setup.Pending()
	if !ok {
		writeError(w, http.StatusConflict, "setup_out_of_order", "set the password first")
		return
	}
	if !auth.ValidTOTP(in.TOTP, owner.TOTPSecret, s.now()) {
		s.Limiter.Fail(clientIP(r))
		writeError(w, http.StatusUnauthorized, "bad_credentials", "invalid code")
		return
	}
	err := s.Store.InsertSettings(r.Context(), map[string]string{
		store.SettingOwnerPasswordHash: owner.PasswordHash,
		store.SettingOwnerTOTPSecret:   owner.TOTPSecret,
	})
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "setup_done", "the owner already exists; sign in instead")
		return
	}
	if err != nil {
		writeErr(w, r, err)
		return
	}
	s.Auth.SetOwner(owner.PasswordHash, owner.TOTPSecret)
	s.Setup.Finish()
	if _, err := s.Auth.StartSession(r.Context(), w, "web"); err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sessionInfo(r.Context(), "web"))
}

// setupGitHubManifest starts the GitHub App manifest flow: the client POSTs
// `manifest` (JSON text) as a form to action_url.
func (s *Server) setupGitHubManifest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Organization string `json:"organization"`
	}
	if !decode(w, r, &in) {
		return
	}
	if s.githubConfigured() {
		writeError(w, http.StatusConflict, "github_configured", "the GitHub App is already configured")
		return
	}
	session, _ := r.Context().Value(sessionIDKey).(string)
	action, err := setup.ManifestFormURL(strings.TrimSpace(in.Organization), s.Setup.NewGitHubState(session))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action_url": action, "manifest": setup.NewManifest(s.Cfg.PublicBaseURL)})
}

// setupGitHubCallback is GitHub's redirect after the App was created. It
// stores the App, writes its key, and sends the browser on to install it.
func (s *Server) setupGitHubCallback(w http.ResponseWriter, r *http.Request) {
	back := func(code string) {
		http.Redirect(w, r, "/setup?step=github&github_error="+code, http.StatusSeeOther)
	}
	session, _ := r.Context().Value(sessionIDKey).(string)
	q := r.URL.Query()
	if !s.Setup.TakeGitHubState(q.Get("state"), session) {
		back("state")
		return
	}
	if s.githubConfigured() {
		back("configured")
		return
	}
	app, err := setup.ConvertManifest(r.Context(), s.Cfg.GitHubAPIURL, q.Get("code"))
	if err != nil {
		slog.Error("github app setup", "err", err)
		back("conversion")
		return
	}
	pemPath := s.Cfg.GitHubPrivateKeyPath
	if pemPath == "" {
		pemPath = setup.DefaultPEMPath
	}
	if err := setup.WritePEM(pemPath, app.PEM); err != nil {
		slog.Error("github app setup: write private key", "path", pemPath, "err", err)
		back("key")
		return
	}
	if err := s.Store.InsertSettings(r.Context(), app.Settings()); err != nil {
		slog.Error("github app setup: save", "err", err)
		back("save")
		return
	}
	slog.Info("GitHub App created", "app_id", app.ID, "slug", app.Slug)
	s.Setup.SetGitHubCreated()
	http.Redirect(w, r, app.InstallURL(), http.StatusSeeOther)
	if s.Restart != nil {
		s.Restart() // the GitHub client, webhook and publisher are wired at start
	}
}
