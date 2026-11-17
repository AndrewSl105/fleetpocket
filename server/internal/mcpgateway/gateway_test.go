package mcpgateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/andrewsl105/fleetpocket/server/internal/mcpgateway"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/testutil"
)

const upstreamSecret = "lin_api_supersecret_value_1234"

type echoIn struct {
	Text string `json:"text"`
}

// upstream is a remote MCP server that requires the secret header.
func upstream(t *testing.T, seenAuth *[]string, mu *sync.Mutex) string {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "up", Version: "1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, map[string]string, error) {
			return nil, map[string]string{"echo": in.Text}, nil
		})
	mcp.AddTool(srv, &mcp.Tool{Name: "delete_all"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, map[string]string, error) {
			t.Error("delete_all reached the upstream")
			return nil, nil, nil
		})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{DisableLocalhostProtection: true})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*seenAuth = append(*seenAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+upstreamSecret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

type fixture struct {
	st     *store.Store
	gw     *mcpgateway.Gateway
	url    string
	taskID int64
	runID  int64
	server *store.MCPServer
}

func setup(t *testing.T, mutate func(*store.MCPServer)) *fixture {
	t.Helper()
	st := testutil.DB(t)
	ctx := context.Background()
	key, _ := secretbox.GenerateKey()
	box, _ := secretbox.New(key)
	var seen []string
	var mu sync.Mutex
	upURL := upstream(t, &seen, &mu)

	if _, err := st.CreateProject(ctx, "demo", store.ProjectFields{}); err != nil {
		t.Fatal(err)
	}
	repo, _ := st.AddRepository(ctx, store.Repository{ProjectID: "demo", FullName: "o/r", BaseBranch: "main", InstallationID: 1})
	task, err := st.CreateTask(ctx, store.Task{ProjectID: "demo", RepositoryID: repo.ID, Title: "t", Prompt: "p", Source: "api"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := st.PendingRun(ctx, task.ID)
	_ = st.MarkRunStarting(ctx, run.ID)

	demo, tr := "demo", "http"
	m := &store.MCPServer{ProjectID: &demo, Name: "linear", Kind: store.MCPRemote, Transport: &tr, URL: &upURL,
		ToolsMode: mcpgateway.ToolsDeny, Tools: []string{"delete_*"}, Enabled: true}
	if mutate != nil {
		mutate(m)
	}
	m, err = st.CreateMCPServer(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	m.Secrets, m.SecretHints, _ = mcpgateway.SealSecrets(box, m.ID, map[string]string{"bearer": upstreamSecret})
	if m, err = st.UpdateMCPServer(ctx, m); err != nil {
		t.Fatal(err)
	}
	gw := &mcpgateway.Gateway{Store: st, Box: box, ConnectTimeout: 10 * time.Second}
	ts := httptest.NewServer(gw)
	t.Cleanup(ts.Close)
	return &fixture{st: st, gw: gw, url: ts.URL, taskID: task.ID, runID: run.ID, server: m}
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func (f *fixture) client(t *testing.T, token, name string) (*mcp.ClientSession, error) {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "runner", Version: "1"}, nil)
	return c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   f.url + mcpgateway.PathPrefix + "mcp/" + name,
		HTTPClient: &http.Client{Transport: bearer{token}}, MaxRetries: -1, DisableStandaloneSSE: true,
	}, nil)
}

func (f *fixture) events(t *testing.T, typ string) []map[string]any {
	t.Helper()
	evs, err := f.st.ListEvents(context.Background(), f.taskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range evs {
		if e.Type != typ {
			continue
		}
		raw, _ := json.Marshal(e.Payload)
		if strings.Contains(string(raw), upstreamSecret) {
			t.Fatalf("secret in event: %s", raw)
		}
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		out = append(out, m)
	}
	return out
}

func TestGatewayInjectsCredentialsAndFiltersTools(t *testing.T) {
	f := setup(t, func(m *store.MCPServer) { two := 2; m.MaxCalls = &two })
	ctx := context.Background()
	token, err := f.gw.Issue(ctx, f.runID, []int64{f.server.ID}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := f.client(t, token, "linear")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = cs.Close() }()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
		t.Fatalf("tools: %+v", tools.Tools)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "hi"}})
	if err != nil || res.IsError {
		t.Fatalf("echo: %+v %v", res, err)
	}
	// A denied tool never reaches the upstream and is recorded.
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "delete_all", Arguments: map[string]any{}}); err == nil {
		t.Fatal("denied tool succeeded")
	}
	// Second allowed call is the last within max_calls=2; the third is refused.
	if res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "2"}}); err != nil || res.IsError {
		t.Fatalf("second call: %v", err)
	}
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "3"}})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "call limit") {
		t.Fatalf("over the limit: %+v %v", res, err)
	}

	evs := f.events(t, mcpgateway.EventCall)
	if len(evs) != 4 {
		t.Fatalf("events: %+v", evs)
	}
	if evs[0]["tool"] != "echo" || evs[0]["ok"] != true || evs[0]["args_bytes"].(float64) == 0 || evs[0]["result_bytes"].(float64) == 0 {
		t.Fatalf("call event: %+v", evs[0])
	}
	if evs[1]["tool"] != "delete_all" || evs[1]["denied"] != "denied" {
		t.Fatalf("deny event: %+v", evs[1])
	}
	if evs[3]["denied"] != "limit" {
		t.Fatalf("limit event: %+v", evs[3])
	}
	got, _ := f.st.GetMCPServer(ctx, f.server.ID)
	if got.LastUsedAt == nil || got.LastStatus == nil || *got.LastStatus != "ok" {
		t.Fatalf("last used: %+v", got)
	}
}

func TestGatewayTokenScopeAndExpiry(t *testing.T) {
	f := setup(t, nil)
	ctx := context.Background()
	post := func(token, name string) int {
		req, _ := http.NewRequest("POST", f.url+mcpgateway.PathPrefix+"mcp/"+name,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post("", "linear"); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	if code := post("jmg_forged", "linear"); code != http.StatusUnauthorized {
		t.Fatalf("forged token: %d", code)
	}
	// A token scoped to no servers cannot reach linear.
	scoped, _ := f.gw.Issue(ctx, f.runID, nil, time.Hour)
	if code := post(scoped, "linear"); code != http.StatusNotFound {
		t.Fatalf("server outside the scope: %d", code)
	}
	// Expired.
	expired, _ := f.gw.Issue(ctx, f.runID, []int64{f.server.ID}, -time.Second)
	if code := post(expired, "linear"); code != http.StatusUnauthorized {
		t.Fatalf("expired token: %d", code)
	}
	tok, _ := f.gw.Issue(ctx, f.runID, []int64{f.server.ID}, time.Hour)
	if code := post(tok, "linear"); code != http.StatusOK {
		t.Fatalf("valid token: %d", code)
	}
	// A newer token replaces the old one; finishing the run revokes it.
	if code := post(scoped, "linear"); code != http.StatusUnauthorized {
		t.Fatalf("replaced token: %d", code)
	}
	if _, err := f.st.FinishRun(ctx, f.runID, 0); err != nil {
		t.Fatal(err)
	}
	f.gw.EndRun(ctx, f.runID)
	if code := post(tok, "linear"); code != http.StatusUnauthorized {
		t.Fatalf("token after the run: %d", code)
	}
}

func TestGatewayEgressReportsDeduplicated(t *testing.T) {
	f := setup(t, nil)
	ctx := context.Background()
	var calls []string
	f.gw.Hooks.Blocked = func(_ context.Context, _, _ int64, project, host string) { calls = append(calls, project+":"+host) }
	tok, _ := f.gw.Issue(ctx, f.runID, nil, time.Hour)
	for _, h := range []string{"example.com:443", "EXAMPLE.com", "evil.test"} {
		req, _ := http.NewRequest("POST", f.url+mcpgateway.PathPrefix+"egress/blocked", strings.NewReader(`{"host":"`+h+`"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := http.DefaultClient.Do(req)
		if err != nil || res.StatusCode != http.StatusNoContent {
			t.Fatalf("report %s: %v %v", h, res, err)
		}
		res.Body.Close()
	}
	evs := f.events(t, mcpgateway.EventBlocked)
	if len(evs) != 2 || evs[0]["host"] != "example.com" || len(calls) != 2 || calls[1] != "demo:evil.test" {
		t.Fatalf("blocked: %+v %v", evs, calls)
	}
}

func TestPostgresReadOnlyEnforced(t *testing.T) {
	f := setup(t, func(m *store.MCPServer) {
		m.Name, m.Kind, m.Transport, m.URL, m.ReadOnly = "db", store.MCPPostgres, nil, nil, true
		m.ToolsMode, m.Tools = mcpgateway.ToolsAll, nil
	})
	ctx := context.Background()
	dbURL := f.st.Pool.Config().ConnString()
	// The fixture sealed a bearer secret; give it the database URL instead.
	key, _ := secretbox.GenerateKey()
	f.gw.Box, _ = secretbox.New(key)
	f.server.Secrets, f.server.SecretHints, _ = mcpgateway.SealSecrets(f.gw.Box, f.server.ID, map[string]string{"DATABASE_URL": dbURL})
	if _, err := f.st.UpdateMCPServer(ctx, f.server); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS ro_probe (v int); TRUNCATE ro_probe`); err != nil {
		t.Fatal(err)
	}
	tok, _ := f.gw.Issue(ctx, f.runID, []int64{f.server.ID}, time.Hour)
	cs, err := f.client(t, tok, "db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	call := func(sql string) (*mcp.CallToolResult, error) {
		return cs.CallTool(ctx, &mcp.CallToolParams{Name: "query", Arguments: map[string]any{"sql": sql}})
	}
	if res, err := call("SELECT 1 AS one"); err != nil || res.IsError {
		t.Fatalf("select: %+v %v", res, err)
	}
	for _, sql := range []string{
		"CREATE TABLE ro_escape (v int)",
		"COMMIT; CREATE TABLE ro_escape (v int)",
		"SET TRANSACTION READ WRITE; INSERT INTO ro_probe VALUES (1)",
		"INSERT INTO ro_probe VALUES (1)",
	} {
		res, err := call(sql)
		if err == nil && !res.IsError {
			t.Errorf("write allowed: %s", sql)
		}
	}
	// Even a statement that turns read-only off for the session changes nothing later.
	_, _ = call("SELECT set_config('default_transaction_read_only','off',false)")
	_, _ = call("SET TRANSACTION READ WRITE")
	if res, err := call("INSERT INTO ro_probe VALUES (2)"); err == nil && !res.IsError {
		t.Error("write allowed after set_config")
	}
	var n int
	if err := f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM ro_probe`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	var escaped bool
	_ = f.st.Pool.QueryRow(ctx, `SELECT to_regclass('ro_escape') IS NOT NULL`).Scan(&escaped)
	if escaped {
		t.Fatal("ro_escape was created")
	}
	if n != 0 {
		t.Fatalf("rows written through the read-only server: %d", n)
	}
}
