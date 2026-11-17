package api_test

import (
	"encoding/json"
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
	"github.com/andrewsl105/fleetpocket/server/internal/mcpgateway"
	"github.com/andrewsl105/fleetpocket/server/internal/projects"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

func setupGateway(t *testing.T, key string) (*client, *store.Store, *mcpgateway.Gateway) {
	t.Helper()
	st := testutil.DB(t)
	hash, _ := auth.HashPassword("pw-for-tests")
	secret, _, _ := auth.NewTOTP("t")
	cfg := &config.Config{DefaultAgent: "claude", ClaudeOAuthToken: "t", PublicBaseURL: "https://jm.example.com"}
	hub := events.NewHub()
	box, _ := secretbox.New(key)
	gw := &mcpgateway.Gateway{Store: st, Box: box, Hub: hub}
	srv := &api.Server{
		Cfg: cfg, Store: st, Hub: hub, Gateway: gw,
		Tasks:    &tasks.Service{Store: st, Hub: hub, DefaultAgent: "claude", AgentEnabled: cfg.AgentEnabled},
		Projects: &projects.Service{Store: st},
		Auth:     &auth.Manager{Store: st, PasswordHash: hash, TOTPSecret: secret, SessionSecret: []byte("s"), CookieInsecure: true},
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: ts.URL, http: &http.Client{Jar: jar}}
	otp, _ := totp.GenerateCode(secret, time.Now())
	if code, _ := c.do("POST", "/api/v1/auth/login", map[string]string{"password": "pw-for-tests", "totp": otp}); code != 200 {
		t.Fatal("login")
	}
	c.do("POST", "/api/v1/projects", map[string]any{"id": "demo", "name": "Demo"})
	return c, st, gw
}

func TestMCPServersAPIKeepsSecretsWriteOnly(t *testing.T) {
	key, _ := secretbox.GenerateKey()
	c, st, _ := setupGateway(t, key)
	const tok = "lin_api_0123456789secret"
	code, m := c.do("POST", "/api/v1/mcp-servers", map[string]any{
		"project_id": "demo", "name": "linear", "kind": "remote", "url": "https://mcp.linear.app/mcp",
		"secrets": map[string]any{"bearer": tok}, "tools_mode": "deny", "tools": []string{"delete_*"}, "max_calls": 20,
	})
	if code != 201 {
		t.Fatalf("create: %d %v", code, m)
	}
	raw := mapString(m)
	if strings.Contains(raw, tok) || strings.Contains(raw, "0123456789") {
		t.Fatalf("secret in the response: %s", raw)
	}
	if m["secret_hints"].(map[string]any)["bearer"] != "…cret" || m["transport"] != "http" {
		t.Fatalf("hints/transport: %v", m)
	}
	// Stored encrypted.
	row, _ := st.GetMCPServer(t.Context(), int64(m["id"].(float64)))
	if strings.Contains(string(row.Secrets), tok) || len(row.Secrets) == 0 {
		t.Fatal("secret stored in plain text")
	}
	id := jsonNum(int64(m["id"].(float64)))
	// Update without secrets keeps them; null removes.
	code, m = c.do("PUT", "/api/v1/mcp-servers/"+id, map[string]any{"name": "linear", "kind": "remote", "url": "https://mcp.linear.app/mcp", "tools_mode": "all"})
	if code != 200 || m["secret_hints"].(map[string]any)["bearer"] == nil {
		t.Fatalf("update kept secrets: %d %v", code, m)
	}
	code, m = c.do("PUT", "/api/v1/mcp-servers/"+id, map[string]any{"name": "linear", "kind": "remote", "url": "https://mcp.linear.app/mcp", "tools_mode": "all", "secrets": map[string]any{"bearer": nil}})
	if code != 200 || len(m["secret_hints"].(map[string]any)) != 0 {
		t.Fatalf("update removed secret: %d %v", code, m)
	}
	// Validation.
	if code, _ := c.do("POST", "/api/v1/mcp-servers", map[string]any{"name": "Bad Name", "kind": "remote", "url": "https://x"}); code != 400 {
		t.Fatalf("bad name: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/mcp-servers", map[string]any{"name": "u", "kind": "remote", "url": "https://user:pw@x"}); code != 400 {
		t.Fatalf("credentials in url: %d", code)
	}
	// A database server is read-only unless told otherwise.
	code, m = c.do("POST", "/api/v1/mcp-servers", map[string]any{"name": "db", "kind": "postgres", "secrets": map[string]any{"DATABASE_URL": "postgres://u:p@h/db"}})
	if code != 201 || m["read_only"] != true || m["project_id"] != nil {
		t.Fatalf("global postgres: %d %v", code, m)
	}
	gid := int64(m["id"].(float64))

	// Integrations: globals the project uses, egress policy, allow.
	code, v := c.do("PUT", "/api/v1/projects/demo/integrations", map[string]any{
		"global_server_ids": []int64{gid},
		"egress":            map[string]any{"mode": "allowlist", "presets": []string{"agent", "npm"}, "hosts": []string{"API.example.com"}},
	})
	if code != 200 || len(v["active"].([]any)) != 2 || v["egress"].(map[string]any)["hosts"].([]any)[0] != "api.example.com" {
		t.Fatalf("integrations: %d %v", code, v)
	}
	if code, _ := c.do("PUT", "/api/v1/projects/demo/integrations", map[string]any{"egress": map[string]any{"mode": "closed"}}); code != 400 {
		t.Fatalf("bad mode: %d", code)
	}
	if code, e := c.do("POST", "/api/v1/projects/demo/egress/allow", map[string]any{"host": "example.org"}); code != 200 || len(e["hosts"].([]any)) != 2 {
		t.Fatalf("allow: %d %v", code, e)
	}
	_, v = c.do("GET", "/api/v1/projects/demo/integrations", nil)
	if v["secrets_enabled"] != true || v["egress_supported"] != true || len(v["agents"].([]any)) != 3 {
		t.Fatalf("view: %v", v)
	}
}

func TestMCPDisabledWithoutSecretsKey(t *testing.T) {
	c, _, _ := setupGateway(t, "")
	code, body := c.do("POST", "/api/v1/mcp-servers", map[string]any{"name": "x", "kind": "remote", "url": "https://x"})
	if code != 503 || !strings.Contains(mapString(body), "SECRETS_KEY") {
		t.Fatalf("without a key: %d %v", code, body)
	}
	// Everything else still works.
	if code, v := c.do("GET", "/api/v1/projects/demo/integrations", nil); code != 200 || v["secrets_enabled"] != false {
		t.Fatalf("integrations without a key: %d %v", code, v)
	}
	if code, _ := c.do("GET", "/api/v1/projects", nil); code != 200 {
		t.Fatal("projects")
	}
}

// The gateway endpoints need the run token and nothing else: no session
// cookie, no CSRF header.
func TestGatewayRouteUsesTokenNotSession(t *testing.T) {
	key, _ := secretbox.GenerateKey()
	c, st, gw := setupGateway(t, key)
	code, task := c.do("POST", "/api/v1/projects/demo/repositories", map[string]any{"full_name": "o/r", "installation_id": 1})
	if code != 201 {
		t.Fatalf("repo: %d %v", code, task)
	}
	code, task = c.do("POST", "/api/v1/tasks", map[string]any{"project_id": "demo", "prompt": "x"})
	if code != 201 {
		t.Fatalf("task: %d %v", code, task)
	}
	run, _ := st.PendingRun(t.Context(), int64(task["id"].(float64)))
	_ = st.MarkRunStarting(t.Context(), run.ID)
	tok, _ := gw.Issue(t.Context(), run.ID, nil, time.Hour)
	post := func(auth string) int {
		req, _ := http.NewRequest("POST", c.base+mcpgateway.PathPrefix+"egress/blocked", strings.NewReader(`{"host":"x.test"}`))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := http.DefaultClient.Do(req) // no cookie jar, no X-Requested-With
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post(""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code := post("Bearer " + tok); code != 204 {
		t.Fatalf("with token: %d", code)
	}
}

func mapString(m map[string]any) string {
	var b strings.Builder
	for k, v := range m {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(strings.ReplaceAll(strings.TrimSpace(fmtAny(v)), "\n", " "))
		b.WriteString(";")
	}
	return b.String()
}

func fmtAny(v any) string {
	switch x := v.(type) {
	case map[string]any:
		return mapString(x)
	case string:
		return x
	default:
		return strings.TrimSpace(strings.Trim(jsonAny(x), `"`))
	}
}

func jsonAny(v any) string { b, _ := json.Marshal(v); return string(b) }
