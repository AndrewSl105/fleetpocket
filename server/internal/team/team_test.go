package team_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/team"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

type owner struct{ hash, secret string }

func (o *owner) Owner() (string, string) { return o.hash, o.secret }
func (o *owner) SetOwner(h, s string)    { o.hash, o.secret = h, s }

type fixture struct {
	ctx   context.Context
	st    *store.Store
	svc   *team.Service
	owner *owner
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	st := testutil.DB(t)
	key, _ := secretbox.GenerateKey()
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("owner-password-1")
	secret, _, _ := auth.NewTOTP("o")
	o := &owner{hash, secret}
	f := &fixture{ctx: context.Background(), st: st, owner: o, now: time.Now()}
	f.svc = &team.Service{Store: st, Box: box, Cfg: &config.Config{ClaudeOAuthToken: "server-token"}, Owner: o, Now: func() time.Time { return f.now }}
	return f
}

func (f *fixture) code(t *testing.T, secret string) string {
	c, err := totp.GenerateCode(secret, f.now)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// seedProject makes a project, a repository and a task (single-owner).
func (f *fixture) seedTask(t *testing.T, by *int64) *store.Task {
	if _, err := f.st.GetProject(f.ctx, "p"); err != nil {
		if _, err := f.st.CreateProject(f.ctx, "p", store.ProjectFields{}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.st.AddRepository(f.ctx, store.Repository{ProjectID: "p", FullName: "o/r", BaseBranch: "main", InstallationID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := f.st.GetProject(f.ctx, "p")
	tk, err := f.st.CreateTask(f.ctx, store.Task{ProjectID: "p", RepositoryID: p.Repositories[0].ID, Title: "t", Prompt: "p", Source: "api", Agent: "claude", CreatedByID: by}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// invite + signup a user with role.
func (f *fixture) addUser(t *testing.T, admin int64, email, role string) *store.User {
	_, tok, err := f.svc.CreateInvite(f.ctx, admin, role, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	e, err := f.svc.SignupStart(f.ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	u, err := f.svc.SignupFinish(f.ctx, team.SignupInput{Token: tok, EnrollmentID: e.ID, Name: "Pat " + role, Email: email,
		Password: "a-long-password", TOTP: f.code(t, e.TOTPSecret)})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestEnableMigratesOwnerSessionsAndAuthorship(t *testing.T) {
	f := newFixture(t)
	if f.svc.Enabled(f.ctx) {
		t.Fatal("team mode must be off by default")
	}
	old := f.seedTask(t, nil)
	if err := f.st.CreateSession(f.ctx, "sess1", "web", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Enable(f.ctx, "bad", "Ann"); !errors.Is(err, team.ErrInvalid) {
		t.Fatalf("invalid email: %v", err)
	}
	u, err := f.svc.Enable(f.ctx, "Ann@Example.com", "Ann Owner")
	if err != nil {
		t.Fatal(err)
	}
	if !f.svc.Enabled(f.ctx) || u.Role != store.RoleAdmin || !u.IsOwner || u.Email != "ann@example.com" || u.PasswordHash != f.owner.hash {
		t.Fatalf("owner: %+v", u)
	}
	// They log in exactly as before (same password, same TOTP secret).
	if _, err := f.svc.Login(f.ctx, "ann@example.com", "owner-password-1", f.code(t, f.owner.secret)); err != nil {
		t.Fatalf("owner login: %v", err)
	}
	if uid, err := f.st.SessionUser(f.ctx, "sess1"); err != nil || uid == nil || *uid != u.ID {
		t.Fatalf("session not attached: %v %v", uid, err)
	}
	tk, _ := f.st.GetTask(f.ctx, old.ID)
	if tk.CreatedBy == nil || tk.CreatedBy.ID != u.ID || tk.CreatedBy.Initials != "AO" {
		t.Fatalf("authorship: %+v", tk.CreatedBy)
	}
	if _, err := f.svc.Enable(f.ctx, "ann@example.com", "Ann"); err == nil {
		t.Fatal("enabling twice must fail")
	}
}

func TestEnableNeedsSecretsKeyAndRejectsLocalMode(t *testing.T) {
	f := newFixture(t)
	f.svc.Box = nil
	if _, err := f.svc.Enable(f.ctx, "a@b.co", "A"); !errors.Is(err, team.ErrNoSecretsKey) {
		t.Fatalf("no key: %v", err)
	}
	f.svc.Cfg.Mode = config.ModeLocal
	if _, err := f.svc.Enable(f.ctx, "a@b.co", "A"); !errors.Is(err, team.ErrLocalMode) {
		t.Fatalf("local: %v", err)
	}
	if f.svc.Enabled(f.ctx) {
		t.Fatal("local mode is never a team")
	}
}

func TestInviteSingleUseExpiryAndTOTP(t *testing.T) {
	f := newFixture(t)
	admin, _ := f.svc.Enable(f.ctx, "a@b.co", "Ann")
	email := "bob@b.co"
	_, tok, err := f.svc.CreateInvite(f.ctx, admin.ID, "member", &email, 0)
	if err != nil {
		t.Fatal(err)
	}
	e, err := f.svc.SignupStart(f.ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	in := team.SignupInput{Token: tok, EnrollmentID: e.ID, Name: "Bob", Email: email, Password: "a-long-password", TOTP: "000000"}
	// Signup requires a valid TOTP code: nothing is created without it.
	if _, err := f.svc.SignupFinish(f.ctx, in); !errors.Is(err, team.ErrBadCode) {
		t.Fatalf("bad code: %v", err)
	}
	if _, err := f.st.UserByEmail(f.ctx, email); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("user created without TOTP")
	}
	// Email lock.
	in.TOTP, in.Email = f.code(t, e.TOTPSecret), "eve@b.co"
	if _, err := f.svc.SignupFinish(f.ctx, in); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("email lock: %v", err)
	}
	in.Email = email
	u, err := f.svc.SignupFinish(f.ctx, in)
	if err != nil || u.Role != "member" || !u.Active() {
		t.Fatalf("signup: %+v %v", u, err)
	}
	// Single use.
	if _, err := f.svc.SignupStart(f.ctx, tok); !errors.Is(err, team.ErrInviteGone) {
		t.Fatalf("reuse: %v", err)
	}
	// Expiry.
	_, tok2, _ := f.svc.CreateInvite(f.ctx, admin.ID, "viewer", nil, time.Hour)
	if _, err := f.st.Pool.Exec(f.ctx, `UPDATE invites SET expires_at = now() - interval '1 minute' WHERE used_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SignupStart(f.ctx, tok2); !errors.Is(err, team.ErrInviteGone) {
		t.Fatalf("expired: %v", err)
	}
	// The token is stored hashed.
	var n int
	_ = f.st.Pool.QueryRow(f.ctx, `SELECT count(*) FROM invites WHERE position($1::bytea in token_hash) > 0`, []byte(tok)).Scan(&n)
	if n != 0 {
		t.Fatal("token stored in clear")
	}
}

func TestDisableTeamAndLastAdmin(t *testing.T) {
	f := newFixture(t)
	admin, _ := f.svc.Enable(f.ctx, "a@b.co", "Ann")
	bob := f.addUser(t, admin.ID, "bob@b.co", "member")
	if err := f.svc.Disable(f.ctx, admin.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("disable with two users: %v", err)
	}
	// The last active admin cannot be demoted or disabled.
	if _, err := f.svc.SetRole(f.ctx, admin.ID, admin.ID, "member"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("demote last admin: %v", err)
	}
	if _, err := f.svc.DisableUser(f.ctx, admin.ID, admin.ID, ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("disable last admin: %v", err)
	}
	if err := f.svc.RemoveUser(f.ctx, admin.ID, admin.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("remove last admin: %v", err)
	}
	// With a second admin, demoting is fine.
	if _, err := f.svc.SetRole(f.ctx, admin.ID, bob.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetRole(f.ctx, admin.ID, bob.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.DisableUser(f.ctx, admin.ID, bob.ID, "finish"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Disable(f.ctx, admin.ID); err != nil {
		t.Fatalf("disable with one active user: %v", err)
	}
	if f.svc.Enabled(f.ctx) {
		t.Fatal("still on")
	}
	entries, _ := f.st.ListAudit(f.ctx, store.AuditFilter{Limit: 50})
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action] = true
	}
	for _, a := range []string{"team.enable", "invite.create", "user.signup", "user.role", "user.disable", "team.disable"} {
		if !seen[a] {
			t.Errorf("audit misses %s: %v", a, seen)
		}
	}
}

func TestDisableUserSideEffects(t *testing.T) {
	f := newFixture(t)
	admin, _ := f.svc.Enable(f.ctx, "a@b.co", "Ann")
	bob := f.addUser(t, admin.ID, "bob@b.co", "member")
	if _, err := f.svc.PutKey(f.ctx, bob.ID, "claude", "api_key", "sk-ant-bob-0000000000", ""); err != nil {
		t.Fatal(err)
	}
	_ = f.st.CreateSession(f.ctx, "bobsess", "web", time.Now().Add(time.Hour))
	_ = f.st.SetSessionUser(f.ctx, "bobsess", bob.ID)
	if _, err := f.st.CreateNode(f.ctx, "bobs-mac", "Bob's Mac", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	_ = f.st.SetNodeOwner(f.ctx, "bobs-mac", bob.ID)
	queued := f.seedTask(t, &bob.ID)
	running := f.seedTask(t, &bob.ID)
	if _, err := f.st.SetStatus(f.ctx, running.ID, nil, "running", "", false); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.DisableUser(f.ctx, admin.ID, bob.ID, "finish")
	if err != nil {
		t.Fatal(err)
	}
	// The API cancels these through the task service (status events, cleanup).
	if len(res.Cancelled) != 1 || res.Cancelled[0] != queued.ID || len(res.Live) != 1 || res.Live[0] != running.ID {
		t.Fatalf("tasks: %+v", res)
	}
	if len(res.Nodes) != 1 || res.Nodes[0] != "bobs-mac" {
		t.Fatalf("nodes to disconnect: %v", res.Nodes)
	}
	if _, err := f.st.TouchSession(f.ctx, "bobsess", time.Hour); err == nil {
		t.Fatal("session survived")
	}
	if creds, _ := f.st.ListCredentials(f.ctx, bob.ID); len(creds) != 0 {
		t.Fatal("keys survived")
	}
	if n, _ := f.st.GetNode(f.ctx, "bobs-mac"); string(n.TokenHash) == "hash" {
		t.Fatal("node token survived")
	}
	if _, err := f.svc.Login(f.ctx, "bob@b.co", "a-long-password", "000000"); err == nil {
		t.Fatal("disabled user logged in")
	}
}

func TestKeysAndResolution(t *testing.T) {
	f := newFixture(t)
	admin, _ := f.svc.Enable(f.ctx, "a@b.co", "Ann")
	bob := f.addUser(t, admin.ID, "bob@b.co", "member")
	task := f.seedTask(t, &bob.ID)
	// No key, fallback off: needs_key.
	if _, err := f.svc.AgentEnv(f.ctx, task); !errors.Is(err, team.ErrNeedsKey) {
		t.Fatalf("needs_key: %v", err)
	}
	// Fallback on: the server's env key.
	if err := f.svc.SetFallback(f.ctx, admin.ID, true); err != nil {
		t.Fatal(err)
	}
	if env, err := f.svc.AgentEnv(f.ctx, task); err != nil || env["CLAUDE_CODE_OAUTH_TOKEN"] != "server-token" {
		t.Fatalf("fallback: %v %v", env, err)
	}
	_ = f.svc.SetFallback(f.ctx, admin.ID, false)
	// Personal key: sealed at rest, only the hint is stored in clear.
	secret := "sk-ant-bob-SECRET-9876"
	c, err := f.svc.PutKey(f.ctx, bob.ID, "claude", "api_key", secret, "work")
	if err != nil || c.Hint != "…9876" {
		t.Fatalf("put: %+v %v", c, err)
	}
	var raw []byte
	_ = f.st.Pool.QueryRow(f.ctx, `SELECT value_encrypted FROM user_credentials WHERE user_id = $1`, bob.ID).Scan(&raw)
	if strings.Contains(string(raw), secret) {
		t.Fatal("key stored in clear")
	}
	env, err := f.svc.AgentEnv(f.ctx, task)
	if err != nil || len(env) != 1 || env["ANTHROPIC_API_KEY"] != secret {
		t.Fatalf("personal: %v %v", env, err)
	}
	// Node tasks use the computer's own logins.
	node := "bobs-mac"
	task.Node = &node
	if env, err := f.svc.AgentEnv(f.ctx, task); err != nil || env != nil {
		t.Fatalf("node: %v %v", env, err)
	}
	// Wrong kinds are refused.
	if _, err := f.svc.PutKey(f.ctx, bob.ID, "codex", "oauth_token", "x", ""); !errors.Is(err, team.ErrInvalid) {
		t.Fatalf("kind: %v", err)
	}
	// Key values never appear in the audit log.
	entries, _ := f.st.ListAudit(f.ctx, store.AuditFilter{Limit: 100})
	for _, e := range entries {
		if strings.Contains(e.Action+e.TargetID+toString(e.Meta), secret) {
			t.Fatal("key in audit log")
		}
	}
}

func toString(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func TestSingleOwnerResolutionUnchanged(t *testing.T) {
	f := newFixture(t)
	task := f.seedTask(t, nil)
	if env, err := f.svc.AgentEnv(f.ctx, task); err != nil || env != nil {
		t.Fatalf("single-owner: %v %v", env, err)
	}
	var nilSvc *team.Service
	if env, err := nilSvc.AgentEnv(f.ctx, task); err != nil || env != nil || nilSvc.Enabled(f.ctx) {
		t.Fatal("nil service")
	}
}

func TestTelegramLink(t *testing.T) {
	f := newFixture(t)
	admin, _ := f.svc.Enable(f.ctx, "a@b.co", "Ann")
	code, exp, err := f.svc.LinkCode(f.ctx, admin.ID)
	if err != nil || len(code) != 6 || exp.Sub(f.now) != team.LinkCodeTTL {
		t.Fatalf("code: %q %v %v", code, exp, err)
	}
	u, err := f.svc.Link(f.ctx, code, 777)
	if err != nil || *u.TelegramUserID != 777 {
		t.Fatalf("link: %+v %v", u, err)
	}
	if _, err := f.svc.Link(f.ctx, code, 778); !errors.Is(err, team.ErrBadCode) {
		t.Fatalf("code reused: %v", err)
	}
	if got, err := f.svc.TelegramUser(f.ctx, 777); err != nil || got.ID != admin.ID {
		t.Fatalf("lookup: %v", err)
	}
	if err := f.svc.Unlink(f.ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.TelegramUser(f.ctx, 777); err == nil {
		t.Fatal("still linked")
	}
}
