package setup

import (
	"strings"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

func TestCode(t *testing.T) {
	w := New(true, "https://fp.example.com/")
	if !w.Active() {
		t.Fatal("owner missing: setup mode expected")
	}
	if len(w.code) < 10 || strings.Trim(w.code, codeAlphabet) != "" {
		t.Fatalf("code %q", w.code)
	}
	if w.CheckCode("WRONGCODE123") || w.CheckCode("") {
		t.Fatal("wrong code accepted")
	}
	if !w.CheckCode(" " + strings.ToLower(w.code) + " ") {
		t.Fatal("code not accepted")
	}
	code := w.code
	w.Finish()
	if w.Active() || w.CheckCode(code) {
		t.Fatal("code must expire with setup mode")
	}
	if New(true, "").code == code {
		t.Fatal("restart must make a new code")
	}
	if New(false, "").Active() {
		t.Fatal("owner exists: no setup mode")
	}
}

func TestGitHubState(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	w := New(false, "")
	w.Now = func() time.Time { return now }
	s := w.NewGitHubState("sess-a")
	if w.TakeGitHubState(s, "sess-b") {
		t.Fatal("state of another session accepted")
	}
	if w.TakeGitHubState(s, "sess-a") {
		t.Fatal("state must be single use, even after a mismatch")
	}
	s = w.NewGitHubState("sess-a")
	if !w.TakeGitHubState(s, "sess-a") || w.TakeGitHubState(s, "sess-a") {
		t.Fatal("state must be valid exactly once")
	}
	s = w.NewGitHubState("sess-a")
	now = now.Add(StateTTL + time.Second)
	if w.TakeGitHubState(s, "sess-a") || w.TakeGitHubState("nope", "sess-a") {
		t.Fatal("expired or unknown state accepted")
	}
}

func TestResolveOwner(t *testing.T) {
	db := map[string]string{store.SettingOwnerPasswordHash: "db-hash", store.SettingOwnerTOTPSecret: "db-totp"}
	if o := ResolveOwner("env-hash", "env-totp", db); o.PasswordHash != "env-hash" || o.TOTPSecret != "env-totp" {
		t.Fatalf("env must win: %+v", o)
	}
	if o := ResolveOwner("", "", db); o.PasswordHash != "db-hash" || o.TOTPSecret != "db-totp" {
		t.Fatalf("db owner: %+v", o)
	}
	if o := ResolveOwner("", "", map[string]string{store.SettingOwnerPasswordHash: "h"}); o != (Owner{}) {
		t.Fatalf("half an owner is no owner: %+v", o)
	}
}
