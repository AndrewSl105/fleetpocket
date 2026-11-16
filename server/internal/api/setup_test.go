package api_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/andrewsl105/fleetpocket/server/internal/api"
	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	firstrun "github.com/andrewsl105/fleetpocket/server/internal/setup"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

// setupServer starts a server without an owner, as `serve` does on first run.
func setupServer(t *testing.T) (*client, *store.Store, *firstrun.Wizard) {
	st := testutil.DB(t)
	cfg := &config.Config{DefaultAgent: "claude", PublicBaseURL: "https://fp.example.com"}
	hub := events.NewHub()
	wiz := firstrun.New(true, cfg.PublicBaseURL)
	srv := &api.Server{
		Cfg: cfg, Store: st, Hub: hub, Setup: wiz,
		Tasks:    &tasks.Service{Store: st, Hub: hub, DefaultAgent: cfg.DefaultAgent, AgentEnabled: cfg.AgentEnabled},
		Projects: &projects.Service{Store: st},
		Auth:     &auth.Manager{Store: st, SessionSecret: []byte("s"), CookieInsecure: true},
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: ts.URL, http: &http.Client{Jar: jar}}, st, wiz
}

// setupCode reads the code from the URL the wizard would log.
func setupCode(t *testing.T, w *firstrun.Wizard) string {
	t.Helper()
	u, _ := url.Parse(w.URL("https://fp.example.com"))
	return u.Query().Get("code")
}

func TestSetupWizard(t *testing.T) {
	c, st, wiz := setupServer(t)
	code := setupCode(t, wiz)

	if s, body := c.do("GET", "/api/v1/projects", nil); s != 409 || apiCode(body) != "setup_required" {
		t.Fatalf("setup mode must block the API: %d %v", s, body)
	}
	if s, _ := c.do("GET", "/healthz", nil); s != 200 {
		t.Fatalf("healthz in setup mode: %d", s)
	}
	if s, body := c.do("GET", "/api/v1/setup/status", nil); s != 200 || body["setup_required"] != true {
		t.Fatalf("status: %d %v", s, body)
	}
	if s, body := c.do("POST", "/api/v1/setup/owner", map[string]string{"code": "WRONGWRONG12", "password": "long-enough-password"}); s != 403 || apiCode(body) != "bad_setup_code" {
		t.Fatalf("wrong code: %d %v", s, body)
	}
	if s, _ := c.do("POST", "/api/v1/setup/owner", map[string]string{"code": code, "password": "short"}); s != 400 {
		t.Fatalf("short password: %d", s)
	}
	if s, body := c.do("POST", "/api/v1/setup/owner/confirm", map[string]string{"code": code, "totp": "123456"}); s != 409 || apiCode(body) != "setup_out_of_order" {
		t.Fatalf("confirm before password: %d %v", s, body)
	}
	s, body := c.do("POST", "/api/v1/setup/owner", map[string]string{"code": code, "password": "long-enough-password"})
	if s != 200 {
		t.Fatalf("owner: %d %v", s, body)
	}
	secret := body["secret"].(string)
	if u, _ := url.Parse(body["otpauth_uri"].(string)); u.Scheme != "otpauth" || u.Query().Get("secret") != secret {
		t.Fatalf("otpauth uri: %v", body["otpauth_uri"])
	}
	if s, _ := c.do("POST", "/api/v1/setup/owner/confirm", map[string]string{"code": code, "totp": "000000"}); s != 401 {
		t.Fatalf("wrong totp: %d", s)
	}
	otp, _ := totp.GenerateCode(secret, time.Now())
	if s, body := c.do("POST", "/api/v1/setup/owner/confirm", map[string]string{"code": code, "totp": otp}); s != 200 || body["kind"] != "web" {
		t.Fatalf("confirm: %d %v", s, body)
	}

	// Setup mode is over: the session works, the code is spent, the owner is in the DB.
	if s, _ := c.do("GET", "/api/v1/me", nil); s != 200 {
		t.Fatalf("me after setup: %d", s)
	}
	if s, body := c.do("POST", "/api/v1/setup/owner", map[string]string{"code": code, "password": "another-long-password"}); s != 409 || apiCode(body) != "setup_done" {
		t.Fatalf("setup twice: %d %v", s, body)
	}
	settings, err := st.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if settings[store.SettingOwnerTOTPSecret] != secret {
		t.Fatalf("owner not persisted: %v", settings)
	}
	// A second wizard (after a restart that lost the owner) cannot overwrite it.
	if err := st.InsertSettings(t.Context(), map[string]string{store.SettingOwnerTOTPSecret: "x"}); err == nil {
		t.Fatal("owner overwritten")
	}
	c.do("POST", "/api/v1/auth/logout", nil)
	otp, _ = totp.GenerateCode(secret, time.Now())
	if s, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "long-enough-password", "totp": otp}); s != 200 {
		t.Fatalf("login with the new owner: %d", s)
	}
}

func TestNoSetupModeWithOwner(t *testing.T) {
	c, _, _ := setup(t)
	if s, body := c.do("GET", "/api/v1/setup/status", nil); s != 200 || body["setup_required"] != false {
		t.Fatalf("status: %d %v", s, body)
	}
	if s, body := c.do("POST", "/api/v1/setup/owner", map[string]string{"code": "ANYTHINGHERE", "password": "long-enough-password"}); s != 409 || apiCode(body) != "setup_done" {
		t.Fatalf("setup with an owner: %d %v", s, body)
	}
	if s, _ := c.do("GET", "/api/v1/me", nil); s != 401 {
		t.Fatalf("API must not be gated: %d", s)
	}
}

func apiCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}
