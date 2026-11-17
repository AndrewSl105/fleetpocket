package mcpgateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/andrewsl105/fleetpocket/server/internal/events"
	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// PathPrefix is where the server mounts the gateway. It is under /api/ so
// the existing reverse-proxy route reaches it, and outside /api/v1 so no
// session or CSRF check applies: the run token is the only credential.
const PathPrefix = "/api/mcp-gateway/"

// Timeline event types the gateway writes.
const (
	EventCall    = "mcp_call"
	EventBlocked = "egress_blocked"
)

// Hooks observe the gateway (metrics, Telegram).
type Hooks struct {
	// Call counts gateway calls: outcome is ok, error, denied or limit.
	Call func(server, outcome string, d time.Duration)
	// Blocked counts denied egress and notifies the owner.
	Blocked func(ctx context.Context, taskID, runID int64, project, host string)
}

type Gateway struct {
	Store *store.Store
	Box   *secretbox.Box
	Hub   *events.Hub
	// Stdio runs stdio servers; nil: they are unavailable.
	Stdio StdioLauncher
	Hooks Hooks
	// Transport for remote upstreams (tests); nil: http.DefaultTransport.
	Transport      http.RoundTripper
	ConnectTimeout time.Duration

	mu   sync.Mutex
	runs map[int64]*runState

	once sync.Once
	mcpH *mcp.StreamableHTTPHandler
}

// runState is a run's open upstream sessions, call counts and blocked hosts
// (in memory: a server restart reconnects and restarts the counts).
type runState struct {
	mu      sync.Mutex
	servers map[int64]*exposed
	calls   map[int64]int
	blocked map[string]bool
}

// exposed is one server as the runner sees it: the gateway's MCP server
// with the allowed tools, backed by an upstream session.
type exposed struct {
	ready chan struct{}
	srv   *mcp.Server
	up    *upstream
	tools []string
	err   error
}

// Enabled reports whether MCP servers can be used (SECRETS_KEY is set).
func (g *Gateway) Enabled() bool { return g != nil && g.Box.Enabled() }

// HashToken is how tokens are stored and looked up.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Issue creates the run's token, scoped to serverIDs and valid until ttl
// passes or the run finishes.
func (g *Gateway) Issue(ctx context.Context, runID int64, serverIDs []int64, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := "jmg_" + base64.RawURLEncoding.EncodeToString(b)
	if err := g.Store.SetRunGateway(ctx, runID, HashToken(token), time.Now().Add(ttl), serverIDs); err != nil {
		return "", err
	}
	return token, nil
}

// EndRun revokes the run's token and closes its upstreams (stopping its
// stdio sidecars).
func (g *Gateway) EndRun(ctx context.Context, runID int64) {
	if g == nil {
		return
	}
	_ = g.Store.ClearRunGateway(ctx, runID)
	g.mu.Lock()
	rs := g.runs[runID]
	delete(g.runs, runID)
	g.mu.Unlock()
	if rs == nil {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, e := range rs.servers {
		go func() {
			<-e.ready
			e.up.Close()
		}()
	}
}

func (g *Gateway) run(runID int64) *runState {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.runs == nil {
		g.runs = map[int64]*runState{}
	}
	rs := g.runs[runID]
	if rs == nil {
		rs = &runState{servers: map[int64]*exposed{}, calls: map[int64]int{}, blocked: map[string]bool{}}
		g.runs[runID] = rs
	}
	return rs
}

// ---- HTTP ----

type ctxKey struct{}

// ServeHTTP serves PathPrefix: mcp/<server> (Streamable HTTP) and
// egress/blocked (reports of the egress proxy).
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, PathPrefix)
	run, err := g.authenticate(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="jungle-monkey-gateway"`)
		httpError(w, http.StatusUnauthorized, "invalid or expired gateway token")
		return
	}
	switch {
	case rest == "egress/blocked" && r.Method == http.MethodPost:
		g.blocked(w, r, run)
	case strings.HasPrefix(rest, "mcp/"):
		g.serveMCP(w, r, run, strings.TrimSuffix(strings.TrimPrefix(rest, "mcp/"), "/"))
	default:
		httpError(w, http.StatusNotFound, "no such gateway endpoint")
	}
}

func (g *Gateway) authenticate(r *http.Request) (*store.RunGateway, error) {
	h := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || !strings.HasPrefix(token, "jmg_") || len(token) > 200 {
		return nil, errors.New("no token")
	}
	// Looked up by its SHA-256, so the comparison never touches the token.
	return g.Store.RunByGatewayToken(r.Context(), HashToken(token))
}

func (g *Gateway) serveMCP(w http.ResponseWriter, r *http.Request, run *store.RunGateway, name string) {
	if !g.Enabled() {
		httpError(w, http.StatusServiceUnavailable, secretbox.ErrDisabled.Error())
		return
	}
	servers, err := g.Store.MCPServersByID(r.Context(), run.ServerIDs)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "gateway error")
		return
	}
	var m *store.MCPServer
	for _, s := range servers {
		if s.Name == name && s.Enabled {
			m = s
		}
	}
	if m == nil {
		// Outside this run's scope looks the same as nonexistent.
		httpError(w, http.StatusNotFound, "no MCP server "+name+" for this run")
		return
	}
	e := g.expose(r.Context(), run, m)
	if e.err != nil {
		httpError(w, http.StatusBadGateway, fmt.Sprintf("%s: %s", m.Name, e.err))
		return
	}
	g.once.Do(func() {
		g.mcpH = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
			srv, _ := r.Context().Value(ctxKey{}).(*mcp.Server)
			return srv
		}, &mcp.StreamableHTTPOptions{
			Stateless: true, JSONResponse: true,
			// Requests come through the reverse proxy or host.docker.internal;
			// the run token is what authenticates them.
			DisableLocalhostProtection: true,
		})
	})
	g.mcpH.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, e.srv)))
}

// expose returns the run's view of server m, connecting on first use.
func (g *Gateway) expose(ctx context.Context, run *store.RunGateway, m *store.MCPServer) *exposed {
	rs := g.run(run.RunID)
	rs.mu.Lock()
	e := rs.servers[m.ID]
	if e != nil {
		rs.mu.Unlock()
		<-e.ready
		return e
	}
	e = &exposed{ready: make(chan struct{})}
	rs.servers[m.ID] = e
	rs.mu.Unlock()
	defer close(e.ready)

	// Detached from the request: the session outlives it.
	cctx := context.WithoutCancel(ctx)
	secrets, err := OpenSecrets(g.Box, m)
	if err == nil {
		e.up, err = g.connect(cctx, m, secrets, run.TaskID, run.ProjectID)
	}
	var tools []*mcp.Tool
	if err == nil {
		tools, err = listTools(cctx, e.up.session)
	}
	if err != nil {
		e.err = errors.New(redactAll(err.Error(), secrets))
		e.up.Close()
		e.up = nil
		_ = g.Store.MarkMCPUsed(cctx, m.ID, e.err.Error())
		g.event(cctx, run, EventCall, map[string]any{"server": m.Name, "tool": "", "ok": false, "error": "connect: " + truncate(e.err.Error(), 300)})
		// Retry on the next request.
		rs.mu.Lock()
		delete(rs.servers, m.ID)
		rs.mu.Unlock()
		return e
	}
	e.srv = mcp.NewServer(&mcp.Implementation{Name: "jungle-monkey/" + m.Name, Version: "1"}, nil)
	for _, t := range tools {
		if !ToolAllowed(m, t) {
			continue
		}
		e.tools = append(e.tools, t.Name)
		func() {
			defer func() {
				if p := recover(); p != nil {
					slog.Warn("mcp gateway: tool skipped", "server", m.Name, "tool", t.Name, "err", p)
				}
			}()
			e.srv.AddTool(t, g.callHandler(run, m, e.up, secrets))
		}()
	}
	e.srv.AddReceivingMiddleware(g.denyMiddleware(run, m, e))
	return e
}

// denyMiddleware records calls to tools the policy hides (the SDK would
// refuse them as unknown without a trace).
func (g *Gateway) denyMiddleware(run *store.RunGateway, m *store.MCPServer, e *exposed) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && !slices.Contains(e.tools, p.Name) {
					g.record(ctx, run, m, p.Name, "denied", 0, len(p.Arguments), 0, "tool not allowed for this project")
					return nil, fmt.Errorf("tool %q is not allowed by this project's MCP settings", p.Name)
				}
			}
			return next(ctx, method, req)
		}
	}
}

func (g *Gateway) callHandler(run *store.RunGateway, m *store.MCPServer, up *upstream, secrets map[string]string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := req.Params.Name
		argBytes := len(req.Params.Arguments)
		if !g.takeCall(run.RunID, m) {
			msg := fmt.Sprintf("call limit reached: %d calls to %s per run", *m.MaxCalls, m.Name)
			g.record(ctx, run, m, name, "limit", 0, argBytes, 0, msg)
			res := &mcp.CallToolResult{IsError: true}
			res.Content = []mcp.Content{&mcp.TextContent{Text: msg}}
			return res, nil
		}
		start := time.Now()
		res, err := up.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: req.Params.Arguments})
		d := time.Since(start)
		if err != nil {
			msg := redactAll(err.Error(), secrets)
			g.record(ctx, run, m, name, "error", d, argBytes, 0, msg)
			return nil, errors.New(msg)
		}
		b, _ := json.Marshal(res)
		outcome, errText := "ok", ""
		if res.IsError {
			outcome, errText = "error", "the tool returned an error"
		}
		g.record(ctx, run, m, name, outcome, d, argBytes, len(b), errText)
		return res, nil
	}
}

// takeCall counts one call against the server's per-run limit.
func (g *Gateway) takeCall(runID int64, m *store.MCPServer) bool {
	rs := g.run(runID)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if m.MaxCalls != nil && rs.calls[m.ID] >= *m.MaxCalls {
		return false
	}
	rs.calls[m.ID]++
	return true
}

// record writes the mcp_call event: sizes and timing, never arguments,
// results or credentials.
func (g *Gateway) record(ctx context.Context, run *store.RunGateway, m *store.MCPServer, tool, outcome string, d time.Duration, argBytes, resBytes int, errText string) {
	ctx = context.WithoutCancel(ctx)
	p := map[string]any{
		"server": m.Name, "tool": tool, "ok": outcome == "ok", "duration_ms": d.Milliseconds(),
		"args_bytes": argBytes, "result_bytes": resBytes,
	}
	if outcome == "denied" || outcome == "limit" {
		p["denied"] = outcome
	}
	if errText != "" {
		p["error"] = truncate(errText, 300)
	}
	g.event(ctx, run, EventCall, p)
	if outcome == "ok" || outcome == "error" {
		_ = g.Store.MarkMCPUsed(ctx, m.ID, errText)
	}
	if g.Hooks.Call != nil {
		g.Hooks.Call(m.Name, outcome, d)
	}
}

func (g *Gateway) event(ctx context.Context, run *store.RunGateway, typ string, payload any) {
	ev, err := g.Store.AddBackendEvent(ctx, run.TaskID, &run.RunID, typ, payload)
	if err != nil {
		slog.Error("mcp gateway: event", "task_id", run.TaskID, "err", err)
		return
	}
	if g.Hub != nil {
		g.Hub.PublishEvent(ev)
	}
}

// ---- egress reports ----

func (g *Gateway) blocked(w http.ResponseWriter, r *http.Request, run *store.RunGateway) {
	var in struct {
		Host string `json:"host"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		httpError(w, http.StatusBadRequest, "bad body")
		return
	}
	host := strings.ToLower(strings.TrimSpace(in.Host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" || len(host) > 253 {
		httpError(w, http.StatusBadRequest, "bad host")
		return
	}
	rs := g.run(run.RunID)
	rs.mu.Lock()
	seen := rs.blocked[host] || len(rs.blocked) >= 50
	rs.blocked[host] = true
	rs.mu.Unlock()
	if !seen {
		g.event(r.Context(), run, EventBlocked, map[string]any{"host": host})
		if g.Hooks.Blocked != nil {
			g.Hooks.Blocked(context.WithoutCancel(r.Context()), run.TaskID, run.RunID, run.ProjectID, host)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg}})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func replaceAll(s, old, repl string) string {
	if old == "" {
		return s
	}
	return strings.ReplaceAll(s, old, repl)
}

// redactAll removes every secret value from a message.
func redactAll(msg string, secrets map[string]string) string {
	for _, v := range secrets {
		if len(v) >= 4 {
			msg = replaceAll(msg, v, "<secret>")
		}
	}
	return msg
}
