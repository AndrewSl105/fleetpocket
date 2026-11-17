package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/andrewsl105/fleetpocket/server/internal/api"
	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/nodes"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/schedules"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/team"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

type teamEnv struct {
	t      *testing.T
	st     *store.Store
	srv    *api.Server
	base   string
	secret string // the owner's TOTP secret
}

func (e *teamEnv) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: e.t, base: e.base, http: &http.Client{Jar: jar}}
}

func code(t *testing.T, secret string) string {
	c, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func setupTeam(t *testing.T, mode string) *teamEnv {
	st := testutil.DB(t)
	hash, _ := auth.HashPassword("pw-for-tests")
	secret, _, _ := auth.NewTOTP("t")
	key, _ := secretbox.GenerateKey()
	box, _ := secretbox.New(key)
	cfg := &config.Config{ClaudeOAuthToken: "server-token", DefaultAgent: "claude", Mode: mode}
	hub := events.NewHub()
	am := &auth.Manager{Store: st, PasswordHash: hash, TOTPSecret: secret, SessionSecret: []byte("s"), CookieInsecure: true}
	ts := &team.Service{Store: st, Box: box, Cfg: cfg, Owner: am}
	taskSvc := &tasks.Service{Store: st, Hub: hub, DefaultAgent: cfg.DefaultAgent, AgentEnabled: cfg.AgentEnabled,
		TeamOn: ts.Enabled, NeedsKey: func(ctx context.Context, tk *store.Task) string {
			if _, err := ts.AgentEnv(ctx, tk); err != nil {
				return team.NeedsKeyReason(tk.Agent)
			}
			return ""
		}}
	srv := &api.Server{
		Cfg: cfg, Store: st, Hub: hub, Tasks: taskSvc, Team: ts,
		Schedules: &schedules.Service{Store: st, Tasks: taskSvc, Hub: hub},
		Projects:  &projects.Service{Store: st},
		Nodes:     &nodes.Service{Store: st, Hub: hub, Registry: &hosts.Registry{}},
		Auth:      am,
		KeyTester: fakeTester{},
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return &teamEnv{t: t, st: st, srv: srv, base: hs.URL, secret: secret}
}

type fakeTester struct{}

func (fakeTester) Test(_ context.Context, provider string, k *team.PersonalKey) team.TestResult {
	return team.TestResult{Status: "ok", Message: "fake " + provider}
}

// ownerLogin is the pre-team login (and, later, the break-glass login).
func (e *teamEnv) ownerLogin() *client {
	c := e.client()
	if st, body := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "pw-for-tests", "totp": code(e.t, e.secret)}); st != 200 {
		e.t.Fatalf("owner login: %d %v", st, body)
	}
	return c
}

// invite + signup; returns the new user's client.
func (e *teamEnv) join(admin *client, role, email string) *client {
	st, inv := admin.do("POST", "/api/v1/invites", map[string]any{"role": role})
	if st != 201 {
		e.t.Fatalf("invite: %d %v", st, inv)
	}
	tok := inv["token"].(string)
	if !strings.Contains(inv["url"].(string), "/signup?token="+tok) {
		e.t.Fatalf("invite url: %v", inv["url"])
	}
	c := e.client()
	st, en := c.do("POST", "/api/v1/signup/start", map[string]any{"token": tok})
	if st != 200 || en["role"] != role {
		e.t.Fatalf("signup start: %d %v", st, en)
	}
	body := map[string]any{"token": tok, "enrollment_id": en["enrollment_id"], "name": "Pat " + role, "email": email,
		"password": "a-long-password", "totp": "000000"}
	if st, b := c.do("POST", "/api/v1/signup", body); st != 400 || teamErrCode(b) != "bad_code" {
		e.t.Fatalf("signup without TOTP: %d %v", st, b)
	}
	body["totp"] = code(e.t, en["totp_secret"].(string))
	if st, b := c.do("POST", "/api/v1/signup", body); st != 201 || b["user"].(map[string]any)["role"] != role {
		e.t.Fatalf("signup: %d %v", st, b)
	}
	return c
}

func teamErrCode(b map[string]any) string {
	if e, ok := b["error"].(map[string]any); ok {
		return fmt.Sprint(e["code"])
	}
	return ""
}

func TestTeamLocalModeRejected(t *testing.T) {
	e := setupTeam(t, config.ModeLocal)
	c := e.client()
	// Local mode logs in with the CLI's link; the guard answers before auth for signup.
	if st, b := c.do("POST", "/api/v1/signup/start", map[string]any{"token": "x"}); st != 400 || teamErrCode(b) != "not_available_in_local_mode" {
		t.Fatalf("signup in local mode: %d %v", st, b)
	}
	admin := e.ownerLogin()
	for _, r := range []struct{ m, p string }{{"GET", "/api/v1/team"}, {"POST", "/api/v1/team/enable"}, {"GET", "/api/v1/users"},
		{"POST", "/api/v1/invites"}, {"GET", "/api/v1/me/credentials"}, {"POST", "/api/v1/me/telegram/link"}, {"GET", "/api/v1/audit"}} {
		if st, b := admin.do(r.m, r.p, map[string]any{}); st != 400 || teamErrCode(b) != "not_available_in_local_mode" {
			t.Fatalf("%s %s: %d %v", r.m, r.p, st, b)
		}
	}
}

func TestTeamSingleOwnerUntilEnabled(t *testing.T) {
	e := setupTeam(t, "")
	admin := e.ownerLogin()
	st, me := admin.do("GET", "/api/v1/me", nil)
	if st != 200 || me["team_enabled"] != false || me["user"] != nil {
		t.Fatalf("me: %v", me)
	}
	if st, b := admin.do("GET", "/api/v1/users", nil); st != 409 || teamErrCode(b) != "team_off" {
		t.Fatalf("users while off: %d %v", st, b)
	}
	if st, b := admin.do("GET", "/api/v1/team", nil); st != 200 || b["enabled"] != false {
		t.Fatalf("team status: %d %v", st, b)
	}
}

// TestTeamFlow: enable → invite → signup → key → task → needs_key → admin
// sees it with the member's initials; keys never come back.
func TestTeamFlow(t *testing.T) {
	e := setupTeam(t, "")
	ctx := context.Background()
	admin := e.ownerLogin()
	admin.do("POST", "/api/v1/projects", map[string]any{"id": "demo", "name": "Demo"})
	admin.do("POST", "/api/v1/projects/demo/repositories", map[string]any{"full_name": "o/r", "installation_id": 1, "base_branch": "main"})
	if st, b := admin.do("POST", "/api/v1/team/enable", map[string]any{"email": "Ann@Example.com", "name": "Ann Admin"}); st != 200 {
		t.Fatalf("enable: %d %v", st, b)
	}
	// The existing session now belongs to user #1.
	st, me := admin.do("GET", "/api/v1/me", nil)
	if st != 200 || me["team_enabled"] != true || me["user"].(map[string]any)["role"] != "admin" || me["user"].(map[string]any)["initials"] != "AA" {
		t.Fatalf("me after enable: %v", me)
	}
	// Break-glass: the owner credentials without an email still log in as user #1.
	if st, b := e.client().do("POST", "/api/v1/auth/login", map[string]string{"password": "pw-for-tests", "totp": code(t, e.secret)}); st != 200 || b["user"].(map[string]any)["email"] != "ann@example.com" {
		t.Fatalf("break-glass: %d %v", st, b)
	}
	member := e.join(admin, "member", "mia@example.com")
	// Member logs in with email.
	if st, b := e.client().do("POST", "/api/v1/auth/login", map[string]string{"email": "mia@example.com", "password": "a-long-password", "totp": "000000"}); st != 401 {
		t.Fatalf("bad member login: %d %v", st, b)
	}

	// No key yet: the task fails fast with needs_key.
	st, task := member.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "Add a button"})
	if st != 201 || task["status"] != "needs_key" || !strings.Contains(fmt.Sprint(task["status_reason"]), "Add your Claude key") {
		t.Fatalf("needs_key: %d %v", st, task)
	}
	id := int64(task["id"].(float64))
	cb := task["created_by"].(map[string]any)
	if cb["name"] != "Pat member" || cb["initials"] != "PM" {
		t.Fatalf("created_by: %v", cb)
	}
	// Adds a key: write-only, the response shows the last 4 only.
	secret := "sk-ant-api03-MEMBER-SECRET-4321"
	st, k := member.do("PUT", "/api/v1/me/credentials/claude", map[string]any{"kind": "api_key", "value": secret, "label": "work"})
	if st != 200 || k["last4"] != "…4321" {
		t.Fatalf("put key: %d %v", st, k)
	}
	if st, b := member.do("POST", "/api/v1/me/credentials/claude/test", nil); st != 200 || b["status"] != "ok" {
		t.Fatalf("test key: %d %v", st, b)
	}
	st, list := member.do("GET", "/api/v1/me/credentials", nil)
	if st != 200 || strings.Contains(fmt.Sprint(list), secret) || len(list["items"].([]any)) != 1 {
		t.Fatalf("list keys: %v", list)
	}
	// Retry queues it again.
	if st, b := member.do("POST", fmt.Sprintf("/api/v1/tasks/%d/retry", id), nil); st != 200 || b["status"] != "queued" {
		t.Fatalf("retry: %d %v", st, b)
	}
	// The admin sees it with the member's initials.
	st, page := admin.do("GET", "/api/v1/tasks", nil)
	found := false
	for _, it := range page["items"].([]any) {
		m := it.(map[string]any)
		if int64(m["id"].(float64)) == id && m["created_by"].(map[string]any)["initials"] == "PM" {
			found = true
		}
	}
	if st != 200 || !found {
		t.Fatalf("admin list: %v", page)
	}
	// Mine filter.
	if _, mine := admin.do("GET", "/api/v1/tasks?created_by=me", nil); len(mine["items"].([]any)) != 0 {
		t.Fatalf("admin's own tasks: %v", mine)
	}
	if _, mine := member.do("GET", "/api/v1/tasks?created_by=me", nil); len(mine["items"].([]any)) != 1 {
		t.Fatalf("member's own tasks: %v", mine)
	}
	// The member answers the agent's question on their task.
	run, _ := e.st.PendingRun(ctx, id)
	_ = e.st.MarkRunStarting(ctx, run.ID)
	if _, err := e.st.SetStatus(ctx, id, nil, tasks.WaitingInput, "", false); err != nil {
		t.Fatal(err)
	}
	qid := tasks.QuestionID(run.ID, "q1")
	if _, err := e.st.CreateQuestion(ctx, store.Question{ID: qid, TaskID: id, RunID: run.ID, Text: "Which?", Options: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	viewer := e.join(admin, "viewer", "vic@example.com")
	if st, _ := viewer.do("POST", fmt.Sprintf("/api/v1/tasks/%d/answer", id), map[string]any{"question_id": qid, "text": "a"}); st != 403 {
		t.Fatalf("viewer answered: %d", st)
	}
	if st, b := member.do("POST", fmt.Sprintf("/api/v1/tasks/%d/answer", id), map[string]any{"question_id": qid, "text": "a"}); st != 200 {
		t.Fatalf("member answer: %d %v", st, b)
	}
	// Admin acting on the member's task is audit-logged.
	admin.do("POST", fmt.Sprintf("/api/v1/tasks/%d/cancel", id), nil)
	_, audit := admin.do("GET", "/api/v1/audit?action=task.admin_action", nil)
	if len(audit["items"].([]any)) == 0 {
		t.Fatal("admin action not audited")
	}
	// Nothing anywhere returns the key.
	for _, p := range []string{"/api/v1/me", fmt.Sprintf("/api/v1/tasks/%d", id), fmt.Sprintf("/api/v1/tasks/%d/events", id), "/api/v1/audit", "/api/v1/users"} {
		if _, b := admin.do("GET", p, nil); strings.Contains(fmt.Sprint(b), secret) {
			t.Fatalf("%s leaks the key", p)
		}
	}
	// Disabling team mode is refused with more than one active user.
	if st, b := admin.do("POST", "/api/v1/team/disable", nil); st != 409 {
		t.Fatalf("disable: %d %v", st, b)
	}
	// The owner cannot be demoted.
	st, users := admin.do("GET", "/api/v1/users", nil)
	ownerID := users["items"].([]any)[0].(map[string]any)["id"]
	if st, _ := admin.do("PATCH", fmt.Sprintf("/api/v1/users/%v", ownerID), map[string]any{"role": "member"}); st != 409 {
		t.Fatalf("demote owner: %d", st)
	}
	_ = st
}

// TestRoleMatrix checks every session route group against each role:
// viewers only read, members cannot use admin routes nor act on others'
// tasks, schedules and ideas.
func TestRoleMatrix(t *testing.T) {
	e := setupTeam(t, "")
	ctx := context.Background()
	admin := e.ownerLogin()
	admin.do("POST", "/api/v1/projects", map[string]any{"id": "demo", "name": "Demo"})
	admin.do("POST", "/api/v1/projects/demo/repositories", map[string]any{"full_name": "o/r", "installation_id": 1, "base_branch": "main"})
	admin.do("POST", "/api/v1/team/enable", map[string]any{"email": "ann@example.com", "name": "Ann"})
	_, _ = admin.do("PATCH", "/api/v1/team", map[string]any{"shared_key_fallback": true})
	_, task := admin.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "admin task"})
	taskID := int64(task["id"].(float64))
	_, sc := admin.do("POST", "/api/v1/schedules", map[string]any{"name": "nightly", "project_id": "demo", "prompt": "p", "cron": "@daily", "timezone": "UTC"})
	schedID := int64(sc["id"].(float64))
	idea, err := e.st.CreateIdea(ctx, store.IdeaFields{Title: "i", Column: "inbox"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, me := admin.do("GET", "/api/v1/me", nil)
	_ = e.st.SetAuthor(ctx, "ideas", idea.ID, int64(me["user"].(map[string]any)["id"].(float64)))
	member := e.join(admin, "member", "mia@example.com")
	viewer := e.join(admin, "viewer", "vic@example.com")

	path := func(p string, pol api.Policy) string {
		id := "999999"
		switch pol {
		case api.PolicyOwnTask:
			id = fmt.Sprint(taskID)
		case api.PolicyOwnSchedule:
			id = fmt.Sprint(schedID)
		case api.PolicyOwnIdea:
			id = fmt.Sprint(idea.ID)
		}
		r := strings.NewReplacer("{id}", id, "{repoId}", "1", "{attachmentId}", "1", "{checkpointId}", "1", "{provider}", "claude", "{code}", "123456")
		return r.Replace(p)
	}
	routes := e.srv.Routes()
	if len(routes) < 100 {
		t.Fatalf("only %d routes registered", len(routes))
	}
	groups := map[api.Policy]int{}
	for _, rt := range routes {
		method, p, _ := strings.Cut(rt.Pattern, " ")
		if rt.Pattern == "POST /api/v1/team/disable" || rt.Pattern == "DELETE /api/v1/me/telegram/link" {
			continue // state-changing for the caller; covered in TestTeamFlow
		}
		groups[rt.Policy]++
		u := path(p, rt.Policy)
		vs, _ := viewer.do(method, u, map[string]any{})
		ms, _ := member.do(method, u, map[string]any{})
		viewerOK := rt.Policy == api.PolicyRead || rt.Policy == api.PolicySelf
		memberOK := rt.Policy != api.PolicyAdmin && rt.Policy != api.PolicyOwnTask && rt.Policy != api.PolicyOwnSchedule && rt.Policy != api.PolicyOwnIdea
		if (vs == 403) == viewerOK {
			t.Errorf("viewer %s: %d (policy %d)", rt.Pattern, vs, rt.Policy)
		}
		if (ms == 403) == memberOK {
			t.Errorf("member %s: %d (policy %d)", rt.Pattern, ms, rt.Policy)
		}
		if vs == 401 || ms == 401 {
			t.Errorf("%s: unauthenticated", rt.Pattern)
		}
	}
	t.Logf("checked %v route groups", groups)
	for _, p := range []api.Policy{api.PolicyRead, api.PolicySelf, api.PolicyWrite, api.PolicyOwnTask, api.PolicyOwnSchedule, api.PolicyOwnIdea, api.PolicyAdmin} {
		if groups[p] == 0 {
			t.Errorf("no route with policy %d", p)
		}
	}
	// Members act on their own tasks; admins on anyone's.
	_, own := member.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "mine"})
	ownID := int64(own["id"].(float64))
	if st, b := member.do("POST", fmt.Sprintf("/api/v1/tasks/%d/cancel", ownID), nil); st != 200 {
		t.Fatalf("member cancels own: %d %v", st, b)
	}
	// Last-admin protection over HTTP.
	_, users := admin.do("GET", "/api/v1/users", nil)
	for _, it := range users["items"].([]any) {
		u := it.(map[string]any)
		if u["email"] == "mia@example.com" {
			if st, b := admin.do("PATCH", fmt.Sprintf("/api/v1/users/%v", u["id"]), map[string]any{"disabled": true, "live_tasks": "cancel"}); st != 200 {
				t.Fatalf("disable member: %d %v", st, b)
			}
		}
	}
	if st, _ := member.do("GET", "/api/v1/me", nil); st != 401 {
		t.Fatalf("disabled member still signed in: %d", st)
	}
}
