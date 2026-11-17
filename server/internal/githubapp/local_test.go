package githubapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/hosts"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/scheduler"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
	"github.com/andrewsl105/fleetpocket/server/internal/workspace"
)

// memStore is the keychain in tests.
type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) Get(n string) string { s.mu.Lock(); defer s.mu.Unlock(); return s.m[n] }
func (s *memStore) Set(n, v string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[n] = v
	return false, nil
}
func (s *memStore) Delete(n string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, n)
	return nil
}

// userGitHub is github.com + api.github.com for a user token: the device
// flow, GET /user, and the PR and check-run reads the poller makes. Other
// requests go to the shared fake (PR creation, comments).
type userGitHub struct {
	*fakeGitHub
	mu        sync.Mutex
	polls     int
	prState   string // open | closed
	merged    bool
	checks    string // JSON check_runs
	etag      string
	notModify int // 304 answers given
	tokens    []string
}

func newUserGitHub(t *testing.T) (*userGitHub, *httptest.Server) {
	f, _ := newFakeGitHub(t)
	u := &userGitHub{fakeGitHub: f, prState: "open", checks: `[]`, etag: `"v1"`}
	srv := httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(srv.Close)
	return u, srv
}

func (u *userGitHub) serve(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if a := r.Header.Get("Authorization"); a != "" {
		u.tokens = append(u.tokens, a)
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/login/device/code":
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "Iv1.test" || r.Form.Get("scope") != "repo" || r.Header.Get("Accept") != "application/json" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"WDJB-MJHT","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`))
	case r.Method == "POST" && r.URL.Path == "/login/oauth/access_token":
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" || r.Form.Get("device_code") != "dc" {
			_, _ = w.Write([]byte(`{"error":"incorrect_device_code"}`))
			return
		}
		u.polls++
		switch u.polls {
		case 1:
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
		case 2:
			_, _ = w.Write([]byte(`{"error":"slow_down","interval":10}`))
		default:
			_, _ = w.Write([]byte(`{"access_token":"gho_device","token_type":"bearer","scope":"repo"}`))
		}
	case r.Method == "GET" && r.URL.Path == "/user":
		if r.Header.Get("Authorization") == "Bearer bad" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
			return
		}
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer gho_") {
			w.Header().Set("X-OAuth-Scopes", "repo")
		}
		_, _ = w.Write([]byte(`{"login":"octo"}`))
	case r.Method == "GET" && r.URL.Path == "/repos/o/r/pulls/5":
		if r.Header.Get("If-None-Match") == u.etag {
			u.notModify++
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", u.etag)
		_, _ = fmt.Fprintf(w, `{"state":%q,"merged":%v,"head":{"sha":"abc123"}}`, u.prState, u.merged)
	case r.Method == "GET" && r.URL.Path == "/repos/o/r/commits/abc123/check-runs":
		_, _ = fmt.Fprintf(w, `{"total_count":1,"check_runs":%s}`, u.checks)
	default:
		u.mu.Unlock()
		u.fakeGitHub.serve(w, r)
		u.mu.Lock()
	}
}

func TestConnectorDeviceFlow(t *testing.T) {
	_, srv := newUserGitHub(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	keys := &memStore{}
	changed := 0
	c := &Connector{ClientID: "Iv1.test", BaseURL: srv.URL, Store: keys, Client: NewUserClient(func(context.Context) (string, error) { return "", nil }, srv.URL),
		Now: func() time.Time { return now }, OnChange: func() { changed++ }}
	if st, _ := c.PollDevice(ctx); st != DeviceNone {
		t.Fatalf("poll before start: %s", st)
	}
	code, err := c.StartDevice(ctx)
	if err != nil || code.UserCode != "WDJB-MJHT" || code.VerificationURI != "https://github.com/login/device" || code.Interval != 5 {
		t.Fatalf("%+v %v", code, err)
	}
	step := func(d time.Duration, want string) {
		t.Helper()
		now = now.Add(d)
		got, err := c.PollDevice(ctx)
		if err != nil || got != want {
			t.Fatalf("poll: %s %v, want %s", got, err, want)
		}
	}
	step(0, DevicePending)             // authorization_pending
	step(time.Second, DevicePending)   // too early: GitHub is not asked
	step(5*time.Second, DevicePending) // slow_down: interval becomes 10 s
	step(6*time.Second, DevicePending) // too early for the new interval
	step(5*time.Second, DeviceConnected)
	if keys.Get(TokenName) != "gho_device" || !c.Connected() || changed != 1 {
		t.Fatalf("token %q connected %v changed %d", keys.Get(TokenName), c.Connected(), changed)
	}
	st := c.Status(ctx)
	if !st.Connected || st.Login != "octo" || len(st.Scopes) != 1 || st.Scopes[0] != "repo" || !st.DeviceFlow {
		t.Fatalf("status %+v", st)
	}
	if err := c.Disconnect(); err != nil || c.Connected() || keys.Get(TokenName) != "" {
		t.Fatal("disconnect")
	}

	// Expiry, and no client id.
	if _, err := c.StartDevice(ctx); err != nil {
		t.Fatal(err)
	}
	step(901*time.Second, DeviceExpired)
	if _, err := (&Connector{Store: keys}).StartDevice(ctx); err == nil {
		t.Fatal("device flow without a client id")
	}
}

func TestConnectorTokenAndGHCLI(t *testing.T) {
	_, srv := newUserGitHub(t)
	ctx := context.Background()
	keys := &memStore{}
	c := &Connector{Store: keys, Client: NewUserClient(func(context.Context) (string, error) { return "", nil }, srv.URL)}
	if err := c.SetToken(ctx, "bad"); err == nil || keys.Get(TokenName) != "" {
		t.Fatalf("a rejected token was stored: %v", err)
	}
	if err := c.SetToken(ctx, " github_pat_x \n"); err != nil || keys.Get(TokenName) != "github_pat_x" {
		t.Fatalf("pat: %v", err)
	}
	// Fine-grained tokens have no listed scopes.
	if st := c.Status(ctx); !st.Connected || len(st.Scopes) != 0 {
		t.Fatalf("%+v", st)
	}
	c.GHToken = func(context.Context) (string, error) { return "gho_from_gh", nil }
	if err := c.ImportGHToken(ctx); err != nil || keys.Get(TokenName) != "gho_from_gh" {
		t.Fatalf("gh: %v", err)
	}
	c.GHToken = func(context.Context) (string, error) { return "", errors.New("not logged in") }
	if err := c.ImportGHToken(ctx); err == nil {
		t.Fatal("gh failure")
	}
}

func TestUserClientNotConnected(t *testing.T) {
	u := NewUserClient(func(context.Context) (string, error) { return "", nil }, "http://127.0.0.1:1")
	if _, err := u.CreatePR(context.Background(), "o/r", 0, "h", "b", "t", "x"); !errors.Is(err, tasks.ErrGitHubDisabled) {
		t.Fatalf("%v", err)
	}
	if u.Connected(context.Background()) {
		t.Fatal("connected without a token")
	}
	p := &Publisher{App: u, Connected: func() bool { return false }}
	if p.Enabled() {
		t.Fatal("publisher enabled without GitHub")
	}
	if !(&Publisher{}).Enabled() {
		t.Fatal("server mode publisher must always be enabled")
	}
}

// The poller feeds pr_open tasks through the webhook code paths: a CI
// failure reaches OnCIFailed once, unchanged answers are 304s, and a merged
// PR completes the task.
func TestPollerFeedsWebhookPaths(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	gh, srv := newUserGitHub(t)
	hub := events.NewHub()
	svc := &tasks.Service{Store: st, Hub: hub}
	ps := &projects.Service{Store: st}
	name := "L"
	_, _ = ps.Create(ctx, projects.CreateInput{ID: "l", ProjectFields: store.ProjectFields{Name: &name}})
	repo, err := st.AddRepository(ctx, store.Repository{ProjectID: "l", FullName: "site", BaseBranch: "main", Source: store.SourceLocal,
		Path: t.TempDir(), RemoteURL: "https://github.com/o/r.git"})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := st.CreateTask(ctx, store.Task{ProjectID: "l", RepositoryID: repo.ID, Title: "T", Prompt: "T", Source: "api"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SetPR(ctx, tk.ID, 5, "https://github.com/o/r/pull/5")
	if _, err := st.SetStatus(ctx, tk.ID, nil, tasks.PROpen, "", false); err != nil {
		t.Fatal(err)
	}
	var failed []CIFailure
	hook := &Webhook{Store: st, Hub: hub, Tasks: svc, OnCIFailed: func(_ context.Context, f CIFailure) { failed = append(failed, f) }}
	p := &Poller{Store: st, Hook: hook, APIURL: srv.URL, Token: func(context.Context) (string, error) { return "gho_x", nil }}

	gh.mu.Lock()
	gh.checks = `[{"name":"test","status":"completed","conclusion":"failure","html_url":"https://ci/1"}]`
	gh.mu.Unlock()
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(failed) != 1 || failed[0].Name != "test" || failed[0].HeadSHA != "abc123" {
		t.Fatalf("OnCIFailed calls: %+v", failed)
	}
	got, _ := st.GetTask(ctx, tk.ID)
	if got.CIStatus == nil || *got.CIStatus != "failure" {
		t.Fatalf("ci status %v", got.CIStatus)
	}
	if gh.notModify == 0 {
		t.Fatal("the second poll did not use If-None-Match")
	}
	for _, a := range gh.tokens {
		if a != "Bearer gho_x" {
			t.Fatalf("unexpected auth %q", a)
		}
	}
	// Merged on GitHub: the task completes.
	gh.mu.Lock()
	gh.prState, gh.merged, gh.etag = "closed", true, `"v2"`
	gh.mu.Unlock()
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetTask(ctx, tk.ID)
	if got.Status != tasks.Completed || deref(got.StatusReason) != "PR merged" {
		t.Fatalf("after merge: %s %q", got.Status, deref(got.StatusReason))
	}
	// Not connected: nothing is polled.
	p.Token = func(context.Context) (string, error) { return "", nil }
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// "Push & open PR" on a branch_ready local task: the branch goes to the
// GitHub remote with the owner's token, a PR is opened and the task joins
// the PR flow.
func TestPushAndOpenPRWithUserToken(t *testing.T) {
	st := testutil.DB(t)
	ctx := context.Background()
	dc, err := runnermgr.NewDockerClient("")
	if err != nil {
		t.Skip(err)
	}
	docker := &runnermgr.Manager{Docker: dc, SidecarMemory: "256m"}
	image := os.Getenv("FLEETPOCKET_TEST_RUNNER_IMAGE")
	if image == "" {
		image = "fleetpocket-agent:dev"
	}
	if docker.Ping(ctx) != nil || docker.HasImage(ctx, image) != nil {
		t.Skip("docker or runner image unavailable")
	}
	gh, srv := newUserGitHub(t)
	bare := bareRepo(t) // the GitHub remote, branch dev
	// The owner's clone of it, on dev, with origin pointing at GitHub.
	owner := filepath.Join(t.TempDir(), "r")
	if out, err := exec.Command("git", "clone", "-q", "-b", "dev", bare, owner).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	_ = exec.Command("git", "-C", owner, "remote", "set-url", "origin", "git@github.com:o/r.git").Run()
	owner, _ = filepath.EvalSymlinks(owner)

	keys := &memStore{}
	_, _ = keys.Set(TokenName, "gho_owner")
	conn := &Connector{Store: keys}
	user := NewUserClient(conn.Token, srv.URL)
	ws := &workspace.Manager{Dir: filepath.Join(t.TempDir(), "ws"), RemoteURL: func(full string) string {
		if full != "o/r" {
			t.Errorf("push to %q", full)
		}
		return bare
	}, Auth: user.Token}
	cfg := &config.Config{Mode: config.ModeLocal, RunnerImage: image, RunnerFake: true, MaxConcurrentTasks: 1, RunnerMemory: "512m", RunnerCPUs: 1,
		DefaultMaxTurns: 5, DefaultMaxBudgetUSD: 1, DefaultMaxDuration: time.Minute, QuestionTimeout: time.Minute,
		RunnerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())}
	hub := events.NewHub()
	svc := &tasks.Service{Store: st, Hub: hub}
	local := &hosts.Local{Docker: docker, Workspace: ws, AttachmentsDir: func(int64) string { return t.TempDir() }}
	reg := &hosts.Registry{Local: local}
	sched := scheduler.New(&scheduler.Scheduler{Cfg: cfg, Store: st, Hub: hub, Tasks: svc, Hosts: reg,
		Publisher: &Publisher{App: user, Connected: conn.Connected}})
	svc.Executor = sched
	svc.GitHub = user
	svc.LocalHost = func(t *store.Task) tasks.LocalBranches { return reg.For(t.Node) }
	svc.PushLocal = sched.PushLocal
	ps := &projects.Service{Store: st}
	name := "L"
	_, _ = ps.Create(ctx, projects.CreateInput{ID: "l", ProjectFields: store.ProjectFields{Name: &name}})
	repo, err := st.AddRepository(ctx, store.Repository{ProjectID: "l", FullName: "r", BaseBranch: "dev", Source: store.SourceLocal,
		Path: owner, RemoteURL: "git@github.com:o/r.git"})
	if err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = sched.Run(runCtx); close(done) }()
	var taskID int64
	defer func() { cancel(); <-done; _ = docker.RemoveAll(ctx, taskID); _ = docker.RemoveVolume(ctx, taskID) }()
	wait := func(id int64, want string) *store.Task {
		t.Helper()
		deadline := time.Now().Add(2 * time.Minute)
		for {
			tk, _ := st.GetTask(ctx, id)
			if tk.Status == want {
				return tk
			}
			if time.Now().After(deadline) || tk.Status == tasks.Failed {
				t.Fatalf("status %s (%v), want %s", tk.Status, deref(tk.StatusReason), want)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	v, err := svc.Create(ctx, tasks.CreateInput{ProjectID: "l", RepositoryID: repo.ID, Prompt: "Say hello"})
	if err != nil {
		t.Fatal(err)
	}
	taskID = v.ID
	wait(v.ID, tasks.WaitingInput)
	view, _ := svc.Get(ctx, v.ID)
	if _, err := svc.Answer(ctx, v.ID, view.OpenQuestion.ID, "hello", "web"); err != nil {
		t.Fatal(err)
	}
	wait(v.ID, tasks.BranchReady)
	// Nothing went to GitHub yet.
	if gh.count("POST /repos/o/r/pulls") != 0 {
		t.Fatal("a PR was opened before Push & open PR")
	}
	out, err := svc.PushBranch(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != tasks.PROpen || out.PRNumber == nil || *out.PRNumber != 5 {
		t.Fatalf("after push: %s %v", out.Status, out.PRNumber)
	}
	branch := *out.Branch
	if b, err := exec.Command("git", "-C", bare, "rev-parse", "--verify", branch).Output(); err != nil || len(b) == 0 {
		t.Fatalf("branch %s not on the GitHub remote: %v", branch, err)
	}
	gh.mu.Lock()
	for _, a := range gh.tokens {
		if a != "Bearer gho_owner" {
			t.Errorf("GitHub saw %q, want the owner's token", a)
		}
	}
	gh.mu.Unlock()
}
