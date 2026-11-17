// Package secrets keeps local-mode credentials (the GitHub token, agent keys)
// in the OS keychain: macOS Keychain, Windows Credential Manager or the
// Secret Service on Linux. Without a keychain (headless Linux) it falls back
// to a 0600 JSON file in the data directory, which `doctor` reports.
package secrets

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// Service is the keychain service name of local mode.
const Service = "jungle-monkey"

// NodeService is where fleetpocket-node keeps the same keys on a Mac; local
// mode reads it too so a node's logins are reused.
const NodeService = "fleetpocket-node"

// Keyring is the subset of go-keyring the store uses (tests swap it).
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, value string) error
	Delete(service, user string) error
}

type osKeyring struct{}

func (osKeyring) Get(s, u string) (string, error) { return keyring.Get(s, u) }
func (osKeyring) Set(s, u, v string) error        { return keyring.Set(s, u, v) }
func (osKeyring) Delete(s, u string) error        { return keyring.Delete(s, u) }

type Store struct {
	// FallbackFile is <data dir>/secrets.json.
	FallbackFile string
	Keyring      Keyring

	mu sync.Mutex
}

func New(dataDir string) *Store {
	return &Store{FallbackFile: filepath.Join(dataDir, "secrets.json"), Keyring: osKeyring{}}
}

// KeychainAvailable probes the keychain with a harmless lookup.
func (s *Store) KeychainAvailable() bool {
	_, err := s.Keyring.Get(Service, "probe")
	return err == nil || errors.Is(err, keyring.ErrNotFound)
}

// Get returns a secret: environment first, then the keychain (ours, then the
// node's), then the fallback file. "" when not set.
func (s *Store) Get(name string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	for _, svc := range []string{Service, NodeService} {
		if v, err := s.Keyring.Get(svc, name); err == nil && v != "" {
			return v
		}
	}
	m, _ := s.readFile()
	return m[name]
}

// Set stores a secret in the keychain, or in the fallback file when there is
// no keychain. It reports whether the fallback was used.
func (s *Store) Set(name, value string) (fallback bool, err error) {
	if err := s.Keyring.Set(Service, name, value); err == nil {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readFile()
	if err != nil {
		return true, err
	}
	m[name] = value
	return true, s.writeFile(m)
}

// Delete removes a secret from the keychain and the fallback file.
func (s *Store) Delete(name string) error {
	_ = s.Keyring.Delete(Service, name)
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readFile()
	if err != nil {
		return err
	}
	if _, ok := m[name]; !ok {
		return nil
	}
	delete(m, name)
	return s.writeFile(m)
}

func (s *Store) readFile() (map[string]string, error) {
	m := map[string]string{}
	b, err := os.ReadFile(s.FallbackFile)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

func (s *Store) writeFile(m map[string]string) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := s.FallbackFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.FallbackFile)
}

// GitHubToken is the name of the local-mode GitHub user token.
const GitHubToken = "GITHUB_TOKEN"

// AgentKeys are the variables that enable agents (config.AgentEnv).
var AgentKeys = []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CURSOR_API_KEY"}
