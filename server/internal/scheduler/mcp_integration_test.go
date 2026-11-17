package scheduler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/andrewsl105/fleetpocket/server/internal/mcpgateway"
	"github.com/andrewsl105/fleetpocket/server/internal/runnermgr"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
	"github.com/andrewsl105/fleetpocket/server/internal/tasks"
)

const mcpSecret = "sk_live_upstream_secret_9f8e7d6c5b4a"

// listenAll serves h on all interfaces so containers reach it through
// host.docker.internal; returns the port.
func listenAll(t *testing.T, h http.Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

// withGateway gives the env's scheduler an MCP gateway reachable from
// containers, and a project MCP server backed by an upstream that requires
// mcpSecret.
func withGateway(t *testing.T, e *env) *mcpgateway.Gateway {
	t.Helper()
	ctx := context.Background()
	up := mcp.NewServer(&mcp.Implementation{Name: "up", Version: "1"}, nil)
	type in struct {
		Text string `json:"text"`
	}
	mcp.AddTool(up, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, a in) (*mcp.CallToolResult, map[string]string, error) {
		return nil, map[string]string{"echo": a.Text}, nil
	})
	mcp.AddTool(up, &mcp.Tool{Name: "delete_all"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, map[string]string, error) {
		t.Error("denied tool reached the upstream")
		return nil, nil, nil
	})
	uh := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return up }, &mcp.StreamableHTTPOptions{DisableLocalhostProtection: true})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+mcpSecret {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		uh.ServeHTTP(w, r)
	}))
	t.Cleanup(upstream.Close)

	key, _ := secretbox.GenerateKey()
	box, _ := secretbox.New(key)
	gw := &mcpgateway.Gateway{Store: e.st, Box: box, Hub: e.hub, ConnectTimeout: 10 * time.Second}
	port := listenAll(t, gw)
	e.sched.Cfg.MCPGatewayURL = "http://host.docker.internal:" + port
	e.sched.Gateway = gw
	demo, tr, u := "demo", "http", upstream.URL
	secret := mcpSecret
	if _, err := gw.Create(ctx, mcpgateway.Input{ProjectID: &demo, Name: "linear", Kind: store.MCPRemote, Transport: &tr, URL: &u,
		Secrets: map[string]*string{"bearer": &secret}, ToolsMode: mcpgateway.ToolsDeny, Tools: []string{"delete_*"}}); err != nil {
		t.Fatal(err)
	}
	return gw
}

func (e *env) toolResults(id int64) map[string]string {
	evs, _ := e.st.ListEvents(context.Background(), id, 0, 500)
	out := map[string]string{}
	for _, ev := range evs {
		if ev.Type != "tool_result" {
			continue
		}
		var p struct {
			Tool    string `json:"tool"`
			OK      bool   `json:"ok"`
			Summary string `json:"summary"`
		}
		raw, _ := json.Marshal(ev.Payload)
		_ = json.Unmarshal(raw, &p)
		out[p.Tool+"|"+p.Summary] = fmt.Sprint(p.OK)
	}
	return out
}

func hasResult(m map[string]string, toolPrefix, contains, ok string) bool {
	for k, v := range m {
		tool, sum, _ := strings.Cut(k, "|")
		if strings.HasPrefix(tool, toolPrefix) && strings.Contains(sum, contains) && v == ok {
			return true
		}
	}
	return false
}

// The runner reaches its MCP server through the gateway with the run token;
// the upstream credential is nowhere in the runner's container spec.
func TestMCPThroughGatewayNoSecretsInRunner(t *testing.T) {
	e := newEnv(t, 5*time.Minute, []string{})
	withGateway(t, e)
	ctx := context.Background()
	v, err := e.svc.Create(ctx, tasks.CreateInput{ProjectID: "demo",
		Prompt: `mcp-call linear echo {"text":"hi"} mcp-call linear delete_all {}`})
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(v.ID, tasks.WaitingInput, 90*time.Second)

	// Inspect the running runner: env, mounts, command and labels.
	in, err := e.docker.Docker.ContainerInspect(ctx, runnermgr.RunnerName(v.ID), client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := json.Marshal(struct {
		Env, Cmd, Entrypoint, Binds []string
		Labels                      map[string]string
	}{in.Container.Config.Env, in.Container.Config.Cmd, in.Container.Config.Entrypoint, in.Container.HostConfig.Binds, in.Container.Config.Labels})
	if strings.Contains(string(spec), mcpSecret) || strings.Contains(string(spec), "9f8e7d6c") {
		t.Fatalf("upstream secret in the runner spec: %s", spec)
	}
	for _, kv := range in.Container.Config.Env {
		if strings.HasPrefix(kv, "HTTP_PROXY=") || strings.HasPrefix(kv, "HTTPS_PROXY=") {
			t.Fatalf("open mode got a proxy: %s", kv)
		}
	}
	// Nor in the workspace the runner sees.
	_ = filepath.Walk(e.ws.Path(v.ID), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Size() < 1<<20 {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), mcpSecret) {
				t.Errorf("secret in %s", p)
			}
		}
		return nil
	})

	res := e.toolResults(v.ID)
	if !hasResult(res, "mcp__linear__list", "tools: echo", "true") {
		t.Fatalf("list through the gateway: %v", res)
	}
	if !hasResult(res, "mcp__linear__echo", `"echo":"hi"`, "true") {
		t.Fatalf("echo call: %v", res)
	}
	if !hasResult(res, "mcp__linear__delete_all", "not allowed", "false") {
		t.Fatalf("denied call: %v", res)
	}
	if !slices.Contains(e.eventTypes(v.ID), "integrations") || !slices.Contains(e.eventTypes(v.ID), "mcp_call") {
		t.Fatalf("events: %v", e.eventTypes(v.ID))
	}

	q, _ := e.st.OpenQuestion(ctx, v.ID)
	if _, err := e.svc.Answer(ctx, v.ID, q.ID, "hello", "test"); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(v.ID, tasks.Completed, 90*time.Second)
	// The run's token died with it.
	var live int
	_ = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE task_id = $1 AND gateway_token_hash IS NOT NULL`, v.ID).Scan(&live)
	if live != 0 {
		t.Fatalf("%d runs still hold a gateway token", live)
	}
}

// proxyBinary cross-compiles the server binary for the Docker daemon, so
// the egress proxy sidecar runs this checkout's code (mounted into the
// agent image) without building the server image.
func proxyBinary(t *testing.T, e *env) string {
	t.Helper()
	info, err := e.docker.Docker.Info(context.Background(), client.InfoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	arch := map[string]string{"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64"}[info.Info.Architecture]
	if arch == "" {
		t.Skipf("docker architecture %s", info.Info.Architecture)
	}
	out := filepath.Join(t.TempDir(), "fleetpocket")
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.Command("go", "build", "-o", out, "./cmd/fleetpocket")
	cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build proxy: %v\n%s", err, b)
	}
	return out
}

// Allowlist mode: internal task network, the runner's only way out is the
// proxy, which lets listed hosts through and reports the rest.
func TestEgressAllowlist(t *testing.T) {
	e := newEnv(t, 5*time.Minute, []string{})
	withGateway(t, e)
	bin := proxyBinary(t, e)
	e.sched.Cfg.EgressProxyImage = runnerImage()
	e.sched.EgressBinary = "/opt/jm/fleetpocket"
	e.sched.EgressBinds = []string{bin + ":/opt/jm/fleetpocket:ro"}
	ctx := context.Background()

	var mu sync.Mutex
	hits := 0
	target := listenAll(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	if err := e.st.SetProjectEgress(ctx, "demo", store.Egress{Mode: store.EgressAllowlist, Presets: []string{}, Hosts: []string{"host.docker.internal:" + target}}); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", Prompt: fmt.Sprintf(
		`mcp-call linear echo {"text":"via-proxy"} egress-probe http://host.docker.internal:%s/ok egress-probe http://blocked.example/`, target)})
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(v.ID, tasks.WaitingInput, 120*time.Second)

	nw, err := e.docker.Docker.NetworkInspect(ctx, runnermgr.NetworkName(v.ID), client.NetworkInspectOptions{})
	if err != nil || !nw.Network.Internal {
		t.Fatalf("task network not internal: %v %v", nw.Network.Internal, err)
	}
	in, _ := e.docker.Docker.ContainerInspect(ctx, runnermgr.RunnerName(v.ID), client.ContainerInspectOptions{})
	if !slices.Contains(in.Container.Config.Env, "HTTPS_PROXY=http://egress-proxy:3128") {
		t.Fatalf("runner env: %v", in.Container.Config.Env)
	}
	if strings.Contains(strings.Join(in.Container.Config.Env, " "), mcpSecret) {
		t.Fatal("secret in runner env")
	}

	res := e.toolResults(v.ID)
	if !hasResult(res, "Bash", "/ok 200", "true") {
		t.Fatalf("allowed host: %v", res)
	}
	if !hasResult(res, "Bash", "blocked.example/ 403", "false") {
		t.Fatalf("blocked host: %v", res)
	}
	if !hasResult(res, "mcp__linear__echo", "via-proxy", "true") {
		t.Fatalf("gateway through the proxy: %v", res)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !slices.Contains(e.eventTypes(v.ID), "egress_blocked") {
		if time.Now().After(deadline) {
			t.Fatalf("no egress_blocked event: %v", e.eventTypes(v.ID))
		}
		time.Sleep(200 * time.Millisecond)
	}
	mu.Lock()
	if hits != 1 {
		t.Errorf("target hits: %d", hits)
	}
	mu.Unlock()
	q, _ := e.st.OpenQuestion(ctx, v.ID)
	_, _ = e.svc.Answer(ctx, v.ID, q.ID, "hello", "test")
	e.waitStatus(v.ID, tasks.Completed, 90*time.Second)
}

// With no MCP servers and open egress, a run is what it was before: a
// plain bridge network, no proxy, no extra containers or events.
func TestOpenModeUnchanged(t *testing.T) {
	e := newEnv(t, 5*time.Minute, []string{})
	key, _ := secretbox.GenerateKey()
	box, _ := secretbox.New(key)
	e.sched.Gateway = &mcpgateway.Gateway{Store: e.st, Box: box, Hub: e.hub}
	e.sched.Cfg.MCPGatewayURL = "http://host.docker.internal:1"
	ctx := context.Background()
	v, err := e.svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", Prompt: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(v.ID, tasks.WaitingInput, 90*time.Second)
	nw, err := e.docker.Docker.NetworkInspect(ctx, runnermgr.NetworkName(v.ID), client.NetworkInspectOptions{})
	if err != nil || nw.Network.Internal {
		t.Fatalf("network: internal=%v %v", nw.Network.Internal, err)
	}
	in, _ := e.docker.Docker.ContainerInspect(ctx, runnermgr.RunnerName(v.ID), client.ContainerInspectOptions{})
	for _, kv := range in.Container.Config.Env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.Contains(strings.ToUpper(k), "PROXY") || strings.Contains(k, "MCP") {
			t.Fatalf("unexpected env %s", k)
		}
	}
	if len(in.Container.HostConfig.ExtraHosts) != 0 {
		t.Fatalf("extra hosts: %v", in.Container.HostConfig.ExtraHosts)
	}
	cs, _ := e.docker.Docker.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", runnermgr.LabelTaskID+"="+fmt.Sprint(v.ID))})
	if len(cs.Items) != 1 {
		t.Fatalf("containers of the task: %d", len(cs.Items))
	}
	if slices.Contains(e.eventTypes(v.ID), "integrations") {
		t.Fatal("integrations event in open mode")
	}
	var tokens int
	_ = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE task_id = $1 AND gateway_token_hash IS NOT NULL`, v.ID).Scan(&tokens)
	if tokens != 0 {
		t.Fatal("open mode issued a gateway token")
	}
	q, _ := e.st.OpenQuestion(ctx, v.ID)
	_, _ = e.svc.Answer(ctx, v.ID, q.ID, "hello", "test")
	e.waitStatus(v.ID, tasks.Completed, 90*time.Second)
}

// A minimal stdio MCP server in node: it reports the length of its secret.
const stdioServer = `const rl=require('readline').createInterface({input:process.stdin});
const out=o=>process.stdout.write(JSON.stringify(o)+"\n");
rl.on('line',l=>{const m=JSON.parse(l);if(m.id===undefined)return;
if(m.method==='initialize')out({jsonrpc:'2.0',id:m.id,result:{protocolVersion:m.params.protocolVersion,capabilities:{tools:{}},serverInfo:{name:'s',version:'1'}}});
else if(m.method==='tools/list')out({jsonrpc:'2.0',id:m.id,result:{tools:[{name:'whoami',inputSchema:{type:'object'}}]}});
else if(m.method==='tools/call')out({jsonrpc:'2.0',id:m.id,result:{content:[{type:'text',text:'secret-len-'+(process.env.STDIO_SECRET||'').length}]}});
else out({jsonrpc:'2.0',id:m.id,result:{}});});`

type nodeStdio struct{ docker *runnermgr.Manager }

func (l nodeStdio) StartStdio(ctx context.Context, s mcpgateway.StdioSpec) (io.ReadCloser, io.WriteCloser, func(), error) {
	return l.docker.StartStdio(ctx, runnermgr.StdioSpec{TaskID: s.TaskID, Project: s.Project, Name: s.Name,
		Image: runnerImage(), Entrypoint: []string{"node", "-e", stdioServer}, Env: s.Env, User: "1000:1000"})
}

// A stdio server runs as a sidecar with its secret in its own env, reached
// by the gateway over its attached stdio, and is removed when the run ends.
func TestStdioMCPSidecar(t *testing.T) {
	e := newEnv(t, 5*time.Minute, []string{})
	gw := withGateway(t, e)
	gw.Stdio = nodeStdio{docker: e.docker}
	ctx := context.Background()
	demo, rt, pkg := "demo", "image", "unused"
	secret := "stdio-secret-value-123456"
	if _, err := gw.Create(ctx, mcpgateway.Input{ProjectID: &demo, Name: "tools", Kind: store.MCPStdio, Runtime: &rt, Package: &pkg,
		Secrets: map[string]*string{"STDIO_SECRET": &secret}}); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.Create(ctx, tasks.CreateInput{ProjectID: "demo", Prompt: `mcp-call tools whoami {}`})
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(v.ID, tasks.WaitingInput, 120*time.Second)
	res := e.toolResults(v.ID)
	if !hasResult(res, "mcp__tools__whoami", fmt.Sprintf("secret-len-%d", len(secret)), "true") {
		t.Fatalf("stdio call: %v", res)
	}
	in, _ := e.docker.Docker.ContainerInspect(ctx, runnermgr.RunnerName(v.ID), client.ContainerInspectOptions{})
	if strings.Contains(strings.Join(in.Container.Config.Env, " "), secret) {
		t.Fatal("stdio secret in the runner env")
	}
	side, err := e.docker.Docker.ContainerInspect(ctx, runnermgr.MCPSidecarName(v.ID, "tools"), client.ContainerInspectOptions{})
	if err != nil || !slices.Contains(side.Container.Config.Env, "STDIO_SECRET="+secret) {
		t.Fatalf("sidecar env: %v", err)
	}
	if _, onTask := side.Container.NetworkSettings.Networks[runnermgr.NetworkName(v.ID)]; onTask {
		t.Fatal("the stdio sidecar is on the runner's network")
	}
	q, _ := e.st.OpenQuestion(ctx, v.ID)
	_, _ = e.svc.Answer(ctx, v.ID, q.ID, "hello", "test")
	e.waitStatus(v.ID, tasks.Completed, 90*time.Second)
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := e.docker.Docker.ContainerInspect(ctx, runnermgr.MCPSidecarName(v.ID, "tools"), client.ContainerInspectOptions{}); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stdio sidecar still there after the run")
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// Switching a task between open and allowlist recreates its network.
func TestTaskNetworkFollowsEgressMode(t *testing.T) {
	e := newEnv(t, time.Minute, []string{})
	ctx := context.Background()
	const id = 987_654_321
	t.Cleanup(func() { _ = e.docker.RemoveAll(context.Background(), id) })
	for _, internal := range []bool{false, true, true, false} {
		if _, err := e.docker.EnsureNetwork(ctx, id, "demo", internal); err != nil {
			t.Fatal(err)
		}
		nw, err := e.docker.Docker.NetworkInspect(ctx, runnermgr.NetworkName(id), client.NetworkInspectOptions{})
		if err != nil || nw.Network.Internal != internal {
			t.Fatalf("want internal=%v, got %v (%v)", internal, nw.Network.Internal, err)
		}
	}
}
