package mcpgateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// StdioSpec is a stdio MCP server to run as a sidecar container.
type StdioSpec struct {
	TaskID  int64 // 0: a connection test
	Project string
	Name    string
	Runtime string // npm | pip | image
	Package string
	Command string
	Args    []string
	// Env holds the server's secrets: they go into the sidecar's env only.
	Env map[string]string
}

// StdioLauncher starts a stdio MCP sidecar on the server's Docker and
// returns its attached stdout and stdin; stop removes it.
type StdioLauncher interface {
	StartStdio(ctx context.Context, spec StdioSpec) (stdout io.ReadCloser, stdin io.WriteCloser, stop func(), err error)
}

// ErrStdioUnavailable: no Docker for stdio sidecars.
var ErrStdioUnavailable = errors.New("stdio MCP servers need the server's Docker")

// headerTransport adds the server's credentials to every upstream request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.headers {
		r.Header.Set(k, v)
	}
	return t.base.RoundTrip(r)
}

// upstream is an open client session to one server, and how to close it.
type upstream struct {
	session *mcp.ClientSession
	close   func()
}

func (u *upstream) Close() {
	if u == nil {
		return
	}
	_ = u.session.Close()
	if u.close != nil {
		u.close()
	}
}

var clientImpl = &mcp.Implementation{Name: "jungle-monkey-gateway", Version: "1"}

// connect opens a client session to the server with its secrets.
func (g *Gateway) connect(ctx context.Context, m *store.MCPServer, secrets map[string]string, taskID int64, project string) (*upstream, error) {
	client := mcp.NewClient(clientImpl, nil)
	var transport mcp.Transport
	var closeFn func()
	switch m.Kind {
	case store.MCPRemote:
		headers := map[string]string{}
		for k, v := range secrets {
			if k == SecretBearer {
				headers["Authorization"] = "Bearer " + v
			} else {
				headers[k] = v
			}
		}
		hc := &http.Client{Transport: &headerTransport{base: g.httpTransport(), headers: headers}, Timeout: 0}
		if deref(m.Transport) == "sse" {
			transport = &mcp.SSEClientTransport{Endpoint: deref(m.URL), HTTPClient: hc}
		} else {
			transport = &mcp.StreamableClientTransport{Endpoint: deref(m.URL), HTTPClient: hc, MaxRetries: 1, DisableStandaloneSSE: true}
		}
	case store.MCPStdio:
		if g.Stdio == nil {
			return nil, ErrStdioUnavailable
		}
		out, in, stop, err := g.Stdio.StartStdio(ctx, StdioSpec{
			TaskID: taskID, Project: project, Name: m.Name, Runtime: deref(m.Runtime),
			Package: deref(m.Package), Command: deref(m.Command), Args: m.Args, Env: secrets,
		})
		if err != nil {
			return nil, fmt.Errorf("start %s: %w", m.Name, err)
		}
		transport, closeFn = &mcp.IOTransport{Reader: out, Writer: in}, stop
	case store.MCPPostgres:
		srv := newPostgresServer(secrets[SecretDatabaseURL], m.ReadOnly)
		ct, st := mcp.NewInMemoryTransports()
		ss, err := srv.Connect(ctx, st, nil)
		if err != nil {
			return nil, err
		}
		transport, closeFn = ct, func() { _ = ss.Close() }
	default:
		return nil, fmt.Errorf("unknown kind %q", m.Kind)
	}
	cctx, cancel := context.WithTimeout(ctx, g.connectTimeout())
	defer cancel()
	cs, err := client.Connect(cctx, transport, nil)
	if err != nil {
		if closeFn != nil {
			closeFn()
		}
		return nil, err
	}
	return &upstream{session: cs, close: closeFn}, nil
}

// listTools returns all the upstream's tools (every page).
func listTools(ctx context.Context, cs *mcp.ClientSession) ([]*mcp.Tool, error) {
	var out []*mcp.Tool
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (g *Gateway) connectTimeout() time.Duration {
	if g.ConnectTimeout > 0 {
		return g.ConnectTimeout
	}
	// npx/pip installs on first start can take a while.
	return 2 * time.Minute
}

func (g *Gateway) httpTransport() http.RoundTripper {
	if g.Transport != nil {
		return g.Transport
	}
	return http.DefaultTransport
}
