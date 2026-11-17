package setup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestManifestSnapshot(t *testing.T) {
	m := NewManifest("https://fp.example.com/")
	if !regexp.MustCompile(`^fleetpocket-[a-z2-9]{6}$`).MatchString(m.Name) {
		t.Fatalf("name %q", m.Name)
	}
	m.Name = "fleetpocket-xxxxxx"
	got, _ := json.MarshalIndent(m, "", "  ")
	want := `{
  "name": "fleetpocket-xxxxxx",
  "url": "https://fp.example.com",
  "hook_attributes": {
    "url": "https://fp.example.com/webhooks/github",
    "active": true
  },
  "redirect_url": "https://fp.example.com/api/v1/setup/github/callback",
  "setup_url": "https://fp.example.com/setup?step=repo",
  "description": "Jungle Monkey: coding agents that open pull requests.",
  "public": false,
  "default_events": [
    "pull_request",
    "check_suite",
    "workflow_run"
  ],
  "default_permissions": {
    "actions": "read",
    "checks": "read",
    "contents": "write",
    "metadata": "read",
    "pull_requests": "write"
  }
}`
	if string(got) != want {
		t.Fatalf("manifest changed:\n%s", got)
	}
}

func TestManifestFormURL(t *testing.T) {
	if u, _ := ManifestFormURL("", "s t"); u != "https://github.com/settings/apps/new?state=s+t" {
		t.Fatal(u)
	}
	if u, _ := ManifestFormURL("my-org", "s"); u != "https://github.com/organizations/my-org/settings/apps/new?state=s" {
		t.Fatal(u)
	}
	for _, bad := range []string{"-x", "a/b", "a--b", "evil.com/x", strings.Repeat("a", 40)} {
		if _, err := ManifestFormURL(bad, "s"); err == nil {
			t.Fatalf("org %q accepted", bad)
		}
	}
}

func TestConvertManifest(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/app-manifests/good/conversions":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 42, "slug": "fleetpocket-abc", "html_url": "https://github.com/apps/fleetpocket-abc",
				"webhook_secret": "whsec", "pem": "-----BEGIN RSA PRIVATE KEY-----\nx\n-----END RSA PRIVATE KEY-----\n",
				"client_id": "Iv1.x", "client_secret": "cs"}`))
		case r.URL.Path == "/app-manifests/partial/conversions":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 42, "slug": "s"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message": "Not Found"}`))
		}
	}))
	defer gh.Close()

	app, err := ConvertManifest(t.Context(), gh.URL, "good")
	if err != nil {
		t.Fatal(err)
	}
	if app.ID != 42 || app.WebhookSecret != "whsec" || app.InstallURL() != "https://github.com/apps/fleetpocket-abc/installations/new" {
		t.Fatalf("%+v", app)
	}
	if s := app.Settings(); s[store.SettingGitHubAppID] != "42" || s[store.SettingGitHubWebhookSecret] != "whsec" {
		t.Fatalf("settings %v", s)
	}
	if _, err := ConvertManifest(t.Context(), gh.URL, "expired"); err == nil {
		t.Fatal("404 (expired code) must fail")
	}
	if _, err := ConvertManifest(t.Context(), gh.URL, "partial"); err == nil {
		t.Fatal("incomplete response must fail")
	}
}

func TestWritePEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "github-app.pem")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WritePEM(path, "new"); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	b, _ := os.ReadFile(path)
	if fi.Mode().Perm() != 0o600 || string(b) != "new" {
		t.Fatalf("mode %v, content %q", fi.Mode(), b)
	}
}

func TestResolveGitHub(t *testing.T) {
	db := map[string]string{store.SettingGitHubAppID: "7", store.SettingGitHubWebhookSecret: "db"}
	if id, s := ResolveGitHub(9, "env", db); id != 9 || s != "env" {
		t.Fatalf("env must win: %d %s", id, s)
	}
	if id, s := ResolveGitHub(0, "", db); id != 7 || s != "db" {
		t.Fatalf("db: %d %s", id, s)
	}
	if id, _ := ResolveGitHub(0, "", nil); id != 0 {
		t.Fatal("no app expected")
	}
}
