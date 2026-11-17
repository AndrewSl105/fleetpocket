package secretbox

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSealOpen(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(key)
	if err != nil || !b.Enabled() {
		t.Fatalf("new: %v", err)
	}
	sealed, err := b.Seal([]byte("lin_api_secret"), "mcp_server:1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("lin_api_secret")) {
		t.Fatal("plaintext in sealed value")
	}
	got, err := b.Open(sealed, "mcp_server:1")
	if err != nil || string(got) != "lin_api_secret" {
		t.Fatalf("open: %q %v", got, err)
	}
	// Another row's aad, another key, or a flipped bit: no plaintext.
	if _, err := b.Open(sealed, "mcp_server:2"); !errors.Is(err, ErrOpen) {
		t.Fatalf("moved value opened: %v", err)
	}
	other, _ := GenerateKey()
	b2, _ := New(other)
	if _, err := b2.Open(sealed, "mcp_server:1"); !errors.Is(err, ErrOpen) {
		t.Fatalf("wrong key opened: %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := b.Open(sealed, "mcp_server:1"); !errors.Is(err, ErrOpen) {
		t.Fatalf("tampered value opened: %v", err)
	}
	// Two seals of one value differ (random nonce).
	s1, _ := b.Seal([]byte("x"), "a")
	s2, _ := b.Seal([]byte("x"), "a")
	if bytes.Equal(s1, s2) {
		t.Fatal("nonce reused")
	}
}

func TestDisabledAndKeys(t *testing.T) {
	b, err := New("")
	if err != nil || b.Enabled() {
		t.Fatalf("empty key: %v %v", b, err)
	}
	if _, err := b.Seal([]byte("x"), ""); !errors.Is(err, ErrDisabled) {
		t.Fatalf("seal without key: %v", err)
	}
	if _, err := New("short"); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := New(strings.Repeat("ab", 32)); err != nil {
		t.Fatalf("hex key: %v", err)
	}
	if _, err := New("q83vEjRWeJCrze8SNFZ4kKvN7xI0VniQq83vEjRWeJA="); err != nil {
		t.Fatalf("base64 key: %v", err)
	}
}

func TestHint(t *testing.T) {
	for in, want := range map[string]string{"lin_api_0123456789abcd": "…abcd", "abcdefg": "…fg", "abc": "…"} {
		if got := Hint(in); got != want {
			t.Errorf("Hint(%q) = %q, want %q", in, got, want)
		}
	}
}
