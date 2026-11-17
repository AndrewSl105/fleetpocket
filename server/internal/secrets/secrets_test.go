package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

type memKeyring struct {
	m    map[string]string
	fail bool
}

func (k *memKeyring) Get(s, u string) (string, error) {
	if k.fail {
		return "", errors.New("no secret service")
	}
	v, ok := k.m[s+"/"+u]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (k *memKeyring) Set(s, u, v string) error {
	if k.fail {
		return errors.New("no secret service")
	}
	k.m[s+"/"+u] = v
	return nil
}

func (k *memKeyring) Delete(s, u string) error { delete(k.m, s+"/"+u); return nil }

func TestKeychain(t *testing.T) {
	kr := &memKeyring{m: map[string]string{NodeService + "/OPENAI_API_KEY": "from-node"}}
	s := &Store{FallbackFile: filepath.Join(t.TempDir(), "secrets.json"), Keyring: kr}
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv(GitHubToken, "")
	if !s.KeychainAvailable() {
		t.Fatal("keychain should be available")
	}
	if fb, err := s.Set(GitHubToken, "gho_x"); err != nil || fb {
		t.Fatalf("set: %v %v", fb, err)
	}
	if got := s.Get(GitHubToken); got != "gho_x" {
		t.Errorf("get: %q", got)
	}
	if got := s.Get("OPENAI_API_KEY"); got != "from-node" {
		t.Errorf("node keychain not reused: %q", got)
	}
	if _, err := os.Stat(s.FallbackFile); !os.IsNotExist(err) {
		t.Error("fallback file written although the keychain works")
	}
	t.Setenv(GitHubToken, "from-env")
	if got := s.Get(GitHubToken); got != "from-env" {
		t.Errorf("env must win: %q", got)
	}
}

func TestFallbackFile(t *testing.T) {
	s := &Store{FallbackFile: filepath.Join(t.TempDir(), "secrets.json"), Keyring: &memKeyring{fail: true}}
	t.Setenv("ANTHROPIC_API_KEY", "")
	if s.KeychainAvailable() {
		t.Fatal("keychain should be unavailable")
	}
	fb, err := s.Set("ANTHROPIC_API_KEY", "sk-ant")
	if err != nil || !fb {
		t.Fatalf("set: %v %v", fb, err)
	}
	fi, err := os.Stat(s.FallbackFile)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("fallback file: %v %v", fi, err)
	}
	if got := s.Get("ANTHROPIC_API_KEY"); got != "sk-ant" {
		t.Errorf("get: %q", got)
	}
	if err := s.Delete("ANTHROPIC_API_KEY"); err != nil || s.Get("ANTHROPIC_API_KEY") != "" {
		t.Errorf("delete: %v", err)
	}
}
