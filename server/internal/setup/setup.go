// Package setup holds the in-memory state of the first-run setup wizard: the
// one-time setup code, the owner being created, and the GitHub manifest
// flow's state values. Nothing here is persisted; a restart starts over.
package setup

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// codeAlphabet leaves out 0/O and 1/I/L so the code survives being retyped.
const (
	codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	codeLength   = 12
	// StateTTL matches GitHub's one-hour limit for the manifest flow.
	StateTTL = time.Hour
)

// Owner is a password hash and TOTP secret waiting for their first code.
type Owner struct {
	PasswordHash string
	TOTPSecret   string
}

// ResolveOwner picks the owner login: the environment wins over the setup
// wizard's settings rows, so existing deployments never see setup mode. An
// empty result means there is no owner yet.
func ResolveOwner(envHash, envTOTP string, settings map[string]string) Owner {
	if envHash != "" && envTOTP != "" {
		return Owner{PasswordHash: envHash, TOTPSecret: envTOTP}
	}
	o := Owner{PasswordHash: settings[store.SettingOwnerPasswordHash], TOTPSecret: settings[store.SettingOwnerTOTPSecret]}
	if o.PasswordHash == "" || o.TOTPSecret == "" {
		return Owner{}
	}
	return o
}

type Wizard struct {
	mu      sync.Mutex
	code    string // "" once the owner exists
	pending *Owner
	states  map[string]githubState
	// githubCreated: the App exists but is wired only after the restart.
	githubCreated bool
	Now           func() time.Time
}

type githubState struct {
	session string
	expires time.Time
}

// New returns a wizard. With ownerMissing it generates a setup code and logs
// it once at WARN with the setup URL.
func New(ownerMissing bool, publicBaseURL string) *Wizard {
	w := &Wizard{states: map[string]githubState{}}
	if ownerMissing {
		w.code = NewCode()
		slog.Warn("setup required: open this link to create the owner login (the code changes on every restart)",
			"url", w.URL(publicBaseURL))
	}
	return w
}

// URL is the setup link with the current code ("" outside setup mode).
func (w *Wizard) URL(publicBaseURL string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.code == "" {
		return ""
	}
	return strings.TrimRight(publicBaseURL, "/") + "/setup?code=" + w.code
}

func (w *Wizard) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// NewCode returns a random setup code from codeAlphabet.
func NewCode() string {
	b := make([]byte, codeLength)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	for i := range b {
		// 256 % 31 != 0; the small bias is irrelevant for a 59-bit code.
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

// Active reports whether the server is in setup mode (no owner yet).
func (w *Wizard) Active() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.code != ""
}

// CheckCode compares a code with the current one in constant time.
func (w *Wizard) CheckCode(code string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	code = strings.ToUpper(strings.TrimSpace(code))
	return w.code != "" && subtle.ConstantTimeCompare([]byte(code), []byte(w.code)) == 1
}

// SetPending keeps the owner until its first TOTP code is confirmed; a new
// call replaces it.
func (w *Wizard) SetPending(o Owner) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = &o
}

func (w *Wizard) Pending() (Owner, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending == nil {
		return Owner{}, false
	}
	return *w.pending, true
}

// Finish leaves setup mode; the code stops working.
func (w *Wizard) Finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.code, w.pending = "", nil
}

// NewGitHubState returns a state value for the manifest flow, bound to a
// session ID.
func (w *Wizard) NewGitHubState(session string) string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	for k, v := range w.states {
		if now.After(v.expires) {
			delete(w.states, k)
		}
	}
	w.states[s] = githubState{session: session, expires: now.Add(StateTTL)}
	return s
}

// TakeGitHubState consumes a state value; it is valid once, for the session
// it was issued to, within StateTTL.
func (w *Wizard) TakeGitHubState(state, session string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	v, ok := w.states[state]
	if !ok {
		return false
	}
	delete(w.states, state)
	return v.session == session && !w.now().After(v.expires)
}

func (w *Wizard) SetGitHubCreated() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.githubCreated = true
}

func (w *Wizard) GitHubCreated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.githubCreated
}
