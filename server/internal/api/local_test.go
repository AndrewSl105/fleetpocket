package api_test

import (
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/andrewsl105/fleetpocket/server/internal/api"
	"github.com/andrewsl105/fleetpocket/server/internal/auth"
	"github.com/andrewsl105/fleetpocket/server/internal/config"
	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
	"github.com/andrewsl105/fleetpocket/server/internal/webui"
)

func localSetup(t *testing.T) (*httptest.Server, *api.Local) {
	st := testutil.DB(t)
	cfg := &config.Config{Mode: config.ModeLocal, DefaultAgent: "claude", RunnerFake: true}
	hub := events.NewHub()
	ts := httptest.NewUnstartedServer(nil)
	port, _ := strconv.Atoi(strings.Split(ts.Listener.Addr().String(), ":")[1])
	local := &api.Local{Port: port, ControlToken: "ctl"}
	srv := &api.Server{
		Cfg: cfg, Store: st, Hub: hub,
		Tasks:    &tasks.Service{Store: st, Hub: hub, DefaultAgent: "claude", AgentEnabled: cfg.AgentEnabled},
		Projects: &projects.Service{Store: st},
		Auth:     &auth.Manager{Store: st, SessionSecret: []byte("s"), CookieInsecure: true, SameSiteStrict: true},
		Local:    local,
		UI:       webui.Handler(fstest.MapFS{"index.html": {Data: []byte("<html>ui</html>")}}),
	}
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(ts.Close)
	return ts, local
}

func noRedirect(jar http.CookieJar) *http.Client {
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestLocalOneTimeLogin(t *testing.T) {
	ts, local := localSetup(t)
	jar, _ := cookiejar.New(nil)
	c := noRedirect(jar)
	get := func(path string) *http.Response {
		resp, err := c.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	if r := get("/api/v1/me"); r.StatusCode != 401 {
		t.Fatalf("me before login: %d", r.StatusCode)
	}
	code, err := local.IssueLoginCode()
	if err != nil {
		t.Fatal(err)
	}
	r := get("/auth/local?code=" + code)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/" {
		t.Fatalf("login: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if sc := r.Header.Get("Set-Cookie"); !strings.Contains(sc, "SameSite=Strict") || !strings.Contains(sc, "HttpOnly") {
		t.Errorf("cookie: %q", sc)
	}
	if r := get("/api/v1/me"); r.StatusCode != 200 {
		t.Fatalf("me after login: %d", r.StatusCode)
	}
	// Single use.
	jar2, _ := cookiejar.New(nil)
	c = noRedirect(jar2)
	if r := get("/auth/local?code=" + code); r.StatusCode != 401 {
		t.Fatalf("reused code: %d", r.StatusCode)
	}
	if r := get("/auth/local?code=guess"); r.StatusCode != 401 {
		t.Fatalf("bad code: %d", r.StatusCode)
	}
	// The UI is served at / and client routes fall back to index.html.
	resp, err := http.Get(ts.URL + "/tasks/3")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "<html>ui</html>" {
		t.Errorf("ui: %d %q", resp.StatusCode, b)
	}
}

func TestLocalHostCheck(t *testing.T) {
	ts, local := localSetup(t)
	_, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	for host, want := range map[string]int{
		"127.0.0.1:" + port:    200,
		"localhost:" + port:    200,
		"evil.example:" + port: http.StatusMisdirectedRequest,
		"127.0.0.1:1":          http.StatusMisdirectedRequest,
		"127.0.0.1":            http.StatusMisdirectedRequest,
	} {
		req, _ := http.NewRequest("GET", ts.URL+"/healthz", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Host %q: %d, want %d", host, resp.StatusCode, want)
		}
	}
	_ = local
}

func TestLocalControlEndpoints(t *testing.T) {
	ts, _ := localSetup(t)
	call := func(token string, csrf bool) int {
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/local/login-code", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if csrf {
			req.Header.Set("X-Requested-With", "fleetpocket")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := call("ctl", false); got != 403 {
		t.Errorf("without CSRF header: %d", got)
	}
	if got := call("wrong", true); got != 401 {
		t.Errorf("wrong token: %d", got)
	}
	if got := call("ctl", true); got != 200 {
		t.Errorf("control token: %d", got)
	}
}
