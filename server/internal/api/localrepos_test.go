package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/andrewsl105/fleetpocket/server/internal/api"
	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/localrepos"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func localErrCode(body map[string]any) string {
	if e, ok := body["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

// Server mode (the self-hosted default) refuses local repositories with a
// clear error, everywhere they could come in.
func TestServerModeRejectsLocalRepositories(t *testing.T) {
	c, _, secret := setup(t)
	otp, _ := totp.GenerateCode(secret, time.Now())
	if code, body := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "pw-for-tests", "totp": otp}); code != 200 {
		t.Fatalf("login: %d %v", code, body)
	}
	if code, body := c.do("POST", "/api/v1/projects", map[string]any{"id": "demo", "name": "Demo"}); code != 201 {
		t.Fatalf("project: %d %v", code, body)
	}
	dir := t.TempDir()
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/projects/demo/repositories"},
		{"POST", "/api/v1/local/repos"},
		{"GET", "/api/v1/local/repos/suggestions"},
		{"GET", "/api/v1/local/fs"},
		{"GET", "/api/v1/local/repos/1/status"},
		{"POST", "/api/v1/local/repos/1/locate"},
		{"GET", "/api/v1/local/github"},
		{"POST", "/api/v1/local/github/device"},
		{"POST", "/api/v1/local/github/token"},
	} {
		var body any
		if tc.method == "POST" {
			body = map[string]any{"path": dir}
		}
		if strings.HasSuffix(tc.path, "/repositories") {
			body = map[string]any{"source": "local", "path": dir}
		}
		code, out := c.do(tc.method, tc.path, body)
		if code != 400 || localErrCode(out) != "local_mode_only" {
			t.Errorf("%s %s: %d %v", tc.method, tc.path, code, out)
		}
	}
	// GitHub repositories work as before; an unknown source is refused.
	if code, body := c.do("POST", "/api/v1/projects/demo/repositories", map[string]any{"full_name": "o/r", "installation_id": 1}); code != 201 || body["source"] != "github" {
		t.Fatalf("github repo: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/v1/projects/demo/repositories", map[string]any{"full_name": "o/r2", "installation_id": 1, "source": "gitlab"}); code != 400 {
		t.Fatalf("unknown source: %d", code)
	}
}

type localEnv struct {
	*client
	home, dataDir string
	ctl           func(method, path string, body any) (int, map[string]any)
	st            *store.Store
}

func localRepoSetup(t *testing.T) *localEnv {
	st := testutil.DB(t)
	cfg := &config.Config{Mode: config.ModeLocal, DefaultAgent: "claude", RunnerFake: true}
	hub := events.NewHub()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	dataDir := filepath.Join(home, "Library", "JungleMonkey")
	ts := httptest.NewUnstartedServer(nil)
	port, _ := strconv.Atoi(strings.Split(ts.Listener.Addr().String(), ":")[1])
	local := &api.Local{Port: port, ControlToken: "ctl"}
	ps := &projects.Service{Store: st}
	srv := &api.Server{
		Cfg: cfg, Store: st, Hub: hub,
		Tasks:    &tasks.Service{Store: st, Hub: hub, DefaultAgent: "claude", AgentEnabled: cfg.AgentEnabled},
		Projects: ps,
		Auth:     &auth.Manager{Store: st, SessionSecret: []byte("s"), CookieInsecure: true, SameSiteStrict: true},
		Local:    local,
		LocalRepos: &localrepos.Service{Store: st, Home: home, DataDir: dataDir,
			CreateProject: func(ctx context.Context, id, name string) (*store.Project, error) {
				return ps.Create(ctx, projects.CreateInput{ID: id, ProjectFields: store.ProjectFields{Name: &name}})
			}},
	}
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(ts.Close)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: base, http: &http.Client{Jar: jar}}
	code, _ := local.IssueLoginCode()
	if resp, err := noRedirect(jar).Get(local.LoginURL(code)); err != nil || resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %v %v", resp, err)
	}
	ctl := func(method, path string, body any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, base+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer ctl")
		req.Header.Set("X-Requested-With", "fleetpocket")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	return &localEnv{client: c, home: home, dataDir: dataDir, ctl: ctl, st: st}
}

func gitRepo(t *testing.T, dir string) string {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", dir},
		{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", dir, "remote", "add", "origin", "git@github.com:me/site.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

func TestLocalRepositoriesAPI(t *testing.T) {
	e := localRepoSetup(t)
	repo := gitRepo(t, filepath.Join(e.home, "code", "site"))

	code, body := e.do("GET", "/api/v1/local/repos/suggestions", nil)
	items, _ := body["items"].([]any)
	if code != 200 || len(items) != 1 || items[0].(map[string]any)["path"] != repo || items[0].(map[string]any)["added"] != false {
		t.Fatalf("suggestions: %d %v", code, body)
	}

	// The folder browser stays in the home directory.
	if code, body := e.do("GET", "/api/v1/local/fs?path="+filepath.Join(e.home, "code"), nil); code != 200 ||
		len(body["entries"].([]any)) != 1 || body["entries"].([]any)[0].(map[string]any)["is_git"] != true {
		t.Fatalf("browse: %d %v", code, body)
	}
	if code, body := e.do("GET", "/api/v1/local/fs?path="+filepath.Join(e.home, ".."), nil); code != 403 || localErrCode(body) != "outside_home" {
		t.Fatalf("browse outside home: %d %v", code, body)
	}

	// Added from the CLI (control token): a project named after the folder.
	code, body = e.ctl("POST", "/api/v1/local/repos", map[string]any{"path": repo})
	if code != 201 || body["created"] != true {
		t.Fatalf("add: %d %v", code, body)
	}
	r := body["repository"].(map[string]any)
	if r["source"] != "local" || r["path"] != repo || r["base_branch"] != "main" || r["remote_url"] != "git@github.com:me/site.git" ||
		body["project"].(map[string]any)["id"] != "site" {
		t.Fatalf("added: %v", body)
	}
	// Idempotent, from the browser too.
	code, again := e.do("POST", "/api/v1/local/repos", map[string]any{"path": repo + "/"})
	if code != 200 || again["created"] != false || again["repository"].(map[string]any)["id"] != r["id"] {
		t.Fatalf("add again: %d %v", code, again)
	}
	// Without a session or the control token: 401.
	anon, _ := http.NewRequest("POST", e.base+"/api/v1/local/repos", strings.NewReader(`{"path":"`+repo+`"}`))
	anon.Header.Set("X-Requested-With", "fleetpocket")
	anon.Header.Set("Authorization", "Bearer wrong")
	resp, _ := http.DefaultClient.Do(anon)
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous add: %d", resp.StatusCode)
	}
	// Refusals: a plain folder, the data directory, a relative path.
	plain := filepath.Join(e.home, "notes")
	_ = os.MkdirAll(plain, 0o755)
	inData := gitRepo(t, filepath.Join(e.dataDir, "workspaces", "1"))
	for _, p := range []string{plain, inData, "code/site"} {
		if code, body := e.do("POST", "/api/v1/local/repos", map[string]any{"path": p}); code != 400 {
			t.Errorf("add %s: %d %v", p, code, body)
		}
	}
	if _, body := e.do("GET", "/api/v1/local/repos/suggestions", nil); body["items"].([]any)[0].(map[string]any)["added"] != true {
		t.Fatalf("suggestion not marked added: %v", body)
	}

	// Status for the project page, then the folder moves and is located.
	id := int64(r["id"].(float64))
	if code, body := e.do("GET", fmt.Sprintf("/api/v1/local/repos/%d/status", id), nil); code != 200 || body["branch"] != "main" || body["dirty"] != false || body["missing"] != false {
		t.Fatalf("status: %d %v", code, body)
	}
	moved := filepath.Join(e.home, "code", "site-2")
	if err := os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}
	if _, body := e.do("GET", fmt.Sprintf("/api/v1/local/repos/%d/status", id), nil); body["missing"] != true {
		t.Fatalf("missing: %v", body)
	}
	if code, body := e.do("POST", fmt.Sprintf("/api/v1/local/repos/%d/locate", id), map[string]any{"path": moved}); code != 200 || body["path"] != moved {
		t.Fatalf("locate: %d %v", code, body)
	}

	// Tasks: start_from is validated; working_tree is accepted for a local repository.
	if code, _ := e.do("POST", "/api/v1/tasks", map[string]any{"project_id": "site", "prompt": "x", "start_from": "yesterday"}); code != 400 {
		t.Fatalf("bad start_from: %d", code)
	}
	code, task := e.do("POST", "/api/v1/tasks", map[string]any{"project_id": "site", "prompt": "Fix it", "start_from": "working_tree"})
	if code != 201 || task["start_from"] != "working_tree" || task["local_branch"] != nil {
		t.Fatalf("task: %d %v", code, task)
	}
	// Branch actions need branch_ready.
	tid := int64(task["id"].(float64))
	for _, a := range []string{"merge", "checkout", "discard", "push"} {
		if code, body := e.do("POST", fmt.Sprintf("/api/v1/tasks/%d/branch/%s", tid, a), nil); code != 409 {
			t.Errorf("%s on a queued task: %d %v", a, code, body)
		}
	}
}

func TestLocalLoginNext(t *testing.T) {
	ts, local := localSetup(t)
	jar, _ := cookiejar.New(nil)
	c := noRedirect(jar)
	for next, want := range map[string]string{
		"/projects/site":   "/projects/site",
		"//evil.example/x": "/",
		"https://evil/x":   "/",
		"/\\evil":          "/",
	} {
		code, _ := local.IssueLoginCode()
		resp, err := c.Get(ts.URL + "/auth/local?code=" + code + "&next=" + url.QueryEscape(next))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if got := resp.Header.Get("Location"); got != want {
			t.Errorf("next %q: redirect %q, want %q", next, got, want)
		}
	}
	if u := local.LoginURLTo("abc", "/projects/x"); !strings.HasSuffix(u, "code=abc&next=%2Fprojects%2Fx") {
		t.Errorf("LoginURLTo: %s", u)
	}
}
