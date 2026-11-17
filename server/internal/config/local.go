package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// Modes: "server" is the self-hosted deployment (env vars, GitHub App,
// webhooks); "local" is one user's laptop (`jungle-monkey`), where every
// setting has a computed default and lives in the data directory.
const (
	ModeServer = "server"
	ModeLocal  = "local"
)

// DefaultLocalPort is tried first on the first local run.
const DefaultLocalPort = 4815

// DefaultGitHubClientID is the Jungle Monkey OAuth App used for the device
// flow in local mode (no client secret is needed). Empty until the owner
// creates the App (docs/LOCAL.md); JM_GITHUB_CLIENT_ID overrides it.
var DefaultGitHubClientID = ""

// LocalState is config.json in the data directory: what the first local run
// chose and generated, reused on every later run.
type LocalState struct {
	Port          int    `json:"port"`
	SessionSecret string `json:"session_secret"`
	// DBPassword protects the embedded Postgres on its loopback port.
	DBPassword string `json:"db_password"`
	// ControlToken authenticates the CLI's open/status/stop calls.
	ControlToken string `json:"control_token"`
	DefaultAgent string `json:"default_agent,omitempty"`
	// KeepTasksRunning leaves runner containers alive when the CLI stops.
	KeepTasksRunning bool `json:"keep_tasks_running,omitempty"`
}

// DefaultDataDir is the per-OS data directory of local mode.
func DefaultDataDir() (string, error) {
	return dataDir(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func dataDir(goos string, getenv func(string) string, home func() (string, error)) (string, error) {
	if d := getenv("JM_DATA_DIR"); d != "" {
		return d, nil
	}
	switch goos {
	case "windows":
		if d := getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "JungleMonkey"), nil
		}
	case "linux", "freebsd", "openbsd", "netbsd":
		if d := getenv("XDG_DATA_HOME"); d != "" {
			return filepath.Join(d, "jungle-monkey"), nil
		}
	}
	h, err := home()
	if err != nil {
		return "", err
	}
	switch goos {
	case "darwin":
		return filepath.Join(h, "Library", "Application Support", "JungleMonkey"), nil
	case "windows":
		return filepath.Join(h, "AppData", "Local", "JungleMonkey"), nil
	}
	return filepath.Join(h, ".local", "share", "jungle-monkey"), nil
}

// LocalPaths are the folders inside the data directory.
type LocalPaths struct{ Root string }

func (p LocalPaths) DB() string          { return filepath.Join(p.Root, "db") }
func (p LocalPaths) Workspaces() string  { return filepath.Join(p.Root, "workspaces") }
func (p LocalPaths) Attachments() string { return filepath.Join(p.Root, "attachments") }
func (p LocalPaths) Logs() string        { return filepath.Join(p.Root, "logs") }
func (p LocalPaths) StateFile() string   { return filepath.Join(p.Root, "config.json") }
func (p LocalPaths) PIDFile() string     { return filepath.Join(p.Root, "jungle-monkey.pid") }

// Ensure creates the data directory and its folders (owner-only).
func (p LocalPaths) Ensure() error {
	for _, d := range []string{p.Root, p.DB(), p.Workspaces(), p.Attachments(), p.Logs()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// LoadLocalState reads config.json, generating and saving missing secrets.
// pickPort is called when no port was chosen yet.
func LoadLocalState(p LocalPaths, pickPort func() int) (*LocalState, error) {
	s := &LocalState{}
	b, err := os.ReadFile(p.StateFile())
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, s); err != nil {
			return nil, fmt.Errorf("%s: %w", p.StateFile(), err)
		}
	}
	changed := false
	for _, v := range []*string{&s.SessionSecret, &s.DBPassword, &s.ControlToken} {
		if *v != "" {
			continue
		}
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		*v, changed = base64.RawURLEncoding.EncodeToString(buf), true
	}
	if s.Port == 0 {
		s.Port, changed = pickPort(), true
	}
	if changed {
		if err := SaveLocalState(p, s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// SaveLocalState writes config.json with owner-only permissions.
func SaveLocalState(p LocalPaths, s *LocalState) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.StateFile() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.StateFile())
}

// applyLocal fills the settings local mode computes. Explicit env vars still
// win where they make sense (agent keys, limits); the listen address is always
// loopback.
func (c *Config) applyLocal(dataDir string, st *LocalState) {
	p := LocalPaths{Root: dataDir}
	c.DataDir = dataDir
	c.HTTPAddr = fmt.Sprintf("127.0.0.1:%d", st.Port)
	c.PublicBaseURL = fmt.Sprintf("http://127.0.0.1:%d", st.Port)
	c.WorkspacesDir = p.Workspaces()
	c.AttachmentsDir = p.Attachments()
	c.SessionSecret = st.SessionSecret
	// Plain http on loopback: the Secure flag would drop the cookie.
	c.CookieInsecure = true
	c.CORSOrigins = nil
	if os.Getenv("DOCKER_HOST") == "" {
		c.DockerHost = "" // the Docker client resolves the socket / context
	}
	if os.Getenv("SECRETS_SCAN") == "" {
		c.SecretsScan = "off" // gitleaks is in the server image, not on laptops
	}
	if st.DefaultAgent != "" && os.Getenv("DEFAULT_AGENT") == "" {
		c.DefaultAgent = st.DefaultAgent
	}
	// A user token, not an App: no webhooks and no App settings.
	c.GitHubAppID, c.GitHubPrivateKeyPath, c.GitHubWebhookSecret = 0, "", ""
}

// Version is the release version, set with -ldflags "-X ...config.Version=v1.2.3".
var Version = "dev"

// RunnerImageFor is the agent image matching this binary: the same version
// tag, or latest for development builds.
func RunnerImageFor(version string) string {
	tag := "latest"
	if version != "" && version != "dev" {
		tag = version
	}
	return "ghcr.io/andrewsl105/fleetpocket-agent:" + tag
}

// loadLocal applies local mode on top of the environment.
func (c *Config) loadLocal() error {
	dir, err := DefaultDataDir()
	if err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	p := LocalPaths{Root: dir}
	if err := p.Ensure(); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	st, err := LoadLocalState(p, func() int { return PickPort(DefaultLocalPort) })
	if err != nil {
		return err
	}
	c.applyLocal(dir, st)
	c.LocalState = st
	if os.Getenv("RUNNER_IMAGE") == "" {
		c.RunnerImage = RunnerImageFor(Version)
	}
	return nil
}

// PickPort returns preferred when it is free on loopback, else a random free port.
func PickPort(preferred int) int {
	if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", preferred)); err == nil {
		_ = l.Close()
		return preferred
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return preferred
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}
