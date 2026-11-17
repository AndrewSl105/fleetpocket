// Package secretbox encrypts owner-entered secrets (MCP credentials) for
// storage in Postgres: AES-256-GCM under SECRETS_KEY, a random nonce per
// value, and the owning record bound in as additional data so a ciphertext
// cannot be moved to another row. Plaintext never leaves the process except
// to the component that needs it (the MCP gateway, a stdio MCP sidecar's env).
//
// Not to be confused with internal/secrets, local mode's keychain store.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// version prefixes every sealed value, so the format can change later.
const version byte = 1

// ErrDisabled is returned when SECRETS_KEY is not set: the features that
// store secrets are off, nothing else is affected.
var ErrDisabled = errors.New("SECRETS_KEY is not set: features that store secrets (MCP servers) are disabled; rerun deploy/install.sh or set SECRETS_KEY (openssl rand -hex 32)")

// ErrOpen is returned for a value that does not decrypt (wrong key, moved or
// tampered with).
var ErrOpen = errors.New("secretbox: cannot decrypt (wrong SECRETS_KEY or corrupted value)")

// Box seals and opens values. A nil *Box is valid and disabled.
type Box struct {
	aead cipher.AEAD
}

// ParseKey accepts 32 bytes as 64 hex characters or standard/URL base64.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("SECRETS_KEY must be 32 bytes as 64 hex characters or base64 (openssl rand -hex 32)")
}

// New returns a Box for key (see ParseKey); "" returns nil (disabled).
func New(key string) (*Box, error) {
	if strings.TrimSpace(key) == "" {
		return nil, nil
	}
	k, err := ParseKey(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// GenerateKey returns a new random key as 64 hex characters.
func GenerateKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Enabled reports whether secrets can be stored.
func (b *Box) Enabled() bool { return b != nil }

// Seal encrypts plaintext; aad names the owner (e.g. "mcp_server:12") and
// must be given again to Open.
func (b *Box) Seal(plaintext []byte, aad string) ([]byte, error) {
	if b == nil {
		return nil, ErrDisabled
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+b.aead.Overhead())
	out = append(out, version)
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, plaintext, []byte(aad)), nil
}

// Open decrypts a value sealed with the same aad.
func (b *Box) Open(sealed []byte, aad string) ([]byte, error) {
	if b == nil {
		return nil, ErrDisabled
	}
	n := b.aead.NonceSize()
	if len(sealed) < 1+n+b.aead.Overhead() || sealed[0] != version {
		return nil, ErrOpen
	}
	pt, err := b.aead.Open(nil, sealed[1:1+n], sealed[1+n:], []byte(aad))
	if err != nil {
		return nil, ErrOpen
	}
	return pt, nil
}

// Hint is what the API shows of a secret: its last 4 characters (fewer for
// short values), never more.
func Hint(v string) string {
	r := []rune(v)
	switch {
	case len(r) >= 12:
		return "…" + string(r[len(r)-4:])
	case len(r) >= 6:
		return "…" + string(r[len(r)-2:])
	default:
		return "…"
	}
}

// String keeps a Box out of logs.
func (b *Box) String() string { return fmt.Sprintf("secretbox(enabled=%t)", b != nil) }
