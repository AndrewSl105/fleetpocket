package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataDirPerOS(t *testing.T) {
	home := func() (string, error) { return "/home/u", nil }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	tests := []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"darwin", nil, "/home/u/Library/Application Support/JungleMonkey"},
		{"linux", nil, "/home/u/.local/share/jungle-monkey"},
		{"linux", map[string]string{"XDG_DATA_HOME": "/xdg"}, "/xdg/jungle-monkey"},
		{"windows", map[string]string{"LOCALAPPDATA": "/lad"}, filepath.Join("/lad", "JungleMonkey")},
		{"darwin", map[string]string{"JM_DATA_DIR": "/custom"}, "/custom"},
	}
	for _, tt := range tests {
		got, err := dataDir(tt.goos, env(tt.env), home)
		if err != nil || got != tt.want {
			t.Errorf("%s %v: got %q, %v; want %q", tt.goos, tt.env, got, err, tt.want)
		}
	}
	if _, err := dataDir("linux", env(nil), func() (string, error) { return "", errors.New("no home") }); err == nil {
		t.Error("want error without a home directory")
	}
}

func TestServerModeDefaultsUnchanged(t *testing.T) {
	t.Setenv("MODE", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != ModeServer || c.HTTPAddr != ":8080" || c.DockerHost != "tcp://docker-proxy:2375" ||
		c.WorkspacesDir != "/srv/fleetpocket/workspaces" || c.DataDir != "" {
		t.Fatalf("server defaults changed: %+v", c)
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("server mode must still require DATABASE_URL, got %v", err)
	}
}

func TestLocalModeDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MODE", "local")
	t.Setenv("JM_DATA_DIR", dir)
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("GITHUB_APP_ID", "42")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.Local() || c.DataDir != dir {
		t.Fatalf("mode/data dir: %+v", c)
	}
	if !strings.HasPrefix(c.HTTPAddr, "127.0.0.1:") {
		t.Errorf("must bind loopback only, got %q", c.HTTPAddr)
	}
	if c.SessionSecret == "" || !c.CookieInsecure || c.DockerHost != "" || c.GitHubAppID != 0 {
		t.Errorf("computed defaults: %+v", c)
	}
	if c.WorkspacesDir != filepath.Join(dir, "workspaces") || c.AttachmentsDir != filepath.Join(dir, "attachments") {
		t.Errorf("dirs: %q %q", c.WorkspacesDir, c.AttachmentsDir)
	}
	for _, d := range []string{"db", "workspaces", "attachments", "logs"} {
		if fi, err := os.Stat(filepath.Join(dir, d)); err != nil || !fi.IsDir() {
			t.Errorf("%s not created: %v", d, err)
		}
	}
	fi, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config.json: %v %v", fi, err)
	}
	// The second run reuses the port and secret.
	c2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c2.HTTPAddr != c.HTTPAddr || c2.SessionSecret != c.SessionSecret {
		t.Errorf("state not reused: %q/%q vs %q/%q", c2.HTTPAddr, c2.SessionSecret, c.HTTPAddr, c.SessionSecret)
	}
	// Validate needs only the database URL the CLI fills in.
	c.DatabaseURL = "postgres://127.0.0.1/x"
	if err := c.Validate(); err != nil {
		t.Errorf("local Validate: %v", err)
	}
}

func TestUnknownMode(t *testing.T) {
	t.Setenv("MODE", "cloud")
	if _, err := Load(); err == nil {
		t.Fatal("want error")
	}
}

func TestRunnerImageFor(t *testing.T) {
	if got := RunnerImageFor("v1.2.3"); got != "ghcr.io/andrewsl105/fleetpocket-agent:v1.2.3" {
		t.Error(got)
	}
	if got := RunnerImageFor("dev"); !strings.HasSuffix(got, ":latest") {
		t.Error(got)
	}
}
