package mcpgateway

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/andrewsl105/fleetpocket/server/internal/secretbox"
	"github.com/andrewsl105/fleetpocket/server/internal/store"
)

// Input is a server as the API writes it. Secrets are write-only: a key with
// a value sets it, a key with null removes it, keys not sent are kept.
type Input struct {
	ProjectID *string            `json:"project_id"`
	Name      string             `json:"name"`
	Kind      string             `json:"kind"`
	Transport *string            `json:"transport"`
	URL       *string            `json:"url"`
	Runtime   *string            `json:"runtime"`
	Package   *string            `json:"package"`
	Command   *string            `json:"command"`
	Args      []string           `json:"args"`
	Secrets   map[string]*string `json:"secrets"`
	ToolsMode string             `json:"tools_mode"`
	Tools     []string           `json:"tools"`
	ReadOnly  *bool              `json:"read_only"`
	MaxCalls  *int               `json:"max_calls"`
	Enabled   *bool              `json:"enabled"`
}

func (in *Input) apply(m *store.MCPServer) {
	m.Name, m.Kind = strings.TrimSpace(in.Name), in.Kind
	m.Transport, m.URL, m.Runtime, m.Package, m.Command = trimmed(in.Transport), trimmed(in.URL), trimmed(in.Runtime), trimmed(in.Package), trimmed(in.Command)
	m.Args, m.Tools = in.Args, nil
	for _, t := range in.Tools {
		if t = strings.TrimSpace(t); t != "" {
			m.Tools = append(m.Tools, t)
		}
	}
	m.ToolsMode = in.ToolsMode
	if m.ToolsMode == "" {
		m.ToolsMode = ToolsAll
	}
	// read_only defaults to on for database servers.
	m.ReadOnly = m.Kind == store.MCPPostgres
	if in.ReadOnly != nil {
		m.ReadOnly = *in.ReadOnly
	}
	m.MaxCalls = in.MaxCalls
	m.Enabled = in.Enabled == nil || *in.Enabled
	if m.Kind == store.MCPRemote && m.Transport == nil {
		http := "http"
		m.Transport = &http
	}
}

// Create adds a server (project or global).
func (g *Gateway) Create(ctx context.Context, in Input) (*store.MCPServer, error) {
	if !g.Enabled() {
		return nil, secretbox.ErrDisabled
	}
	m := &store.MCPServer{ProjectID: in.ProjectID}
	in.apply(m)
	secrets := map[string]string{}
	for k, v := range in.Secrets {
		if v != nil && *v != "" {
			secrets[strings.TrimSpace(k)] = *v
		}
	}
	if err := Validate(m, keys(secrets)); err != nil {
		return nil, err
	}
	created, err := g.Store.CreateMCPServer(ctx, m)
	if err != nil || len(secrets) == 0 {
		return created, err
	}
	// Sealed with the row id as additional data, so the row exists first.
	created.Secrets, created.SecretHints, err = SealSecrets(g.Box, created.ID, secrets)
	if err != nil {
		_ = g.Store.DeleteMCPServer(ctx, created.ID)
		return nil, err
	}
	return g.Store.UpdateMCPServer(ctx, created)
}

// Update replaces a server's settings and merges its secrets.
func (g *Gateway) Update(ctx context.Context, id int64, in Input) (*store.MCPServer, error) {
	if !g.Enabled() {
		return nil, secretbox.ErrDisabled
	}
	m, err := g.Store.GetMCPServer(ctx, id)
	if err != nil {
		return nil, err
	}
	secrets, err := OpenSecrets(g.Box, m)
	if err != nil {
		return nil, err
	}
	in.ProjectID = m.ProjectID
	in.apply(m)
	for k, v := range in.Secrets {
		k = strings.TrimSpace(k)
		if v == nil {
			delete(secrets, k)
		} else if *v != "" {
			secrets[k] = *v
		}
	}
	if err := Validate(m, keys(secrets)); err != nil {
		return nil, err
	}
	m.Secrets, m.SecretHints, err = SealSecrets(g.Box, m.ID, secrets)
	if err != nil {
		return nil, err
	}
	return g.Store.UpdateMCPServer(ctx, m)
}

// ToolInfo is one tool a connection test found.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Allowed     bool   `json:"allowed"`
	ReadOnly    bool   `json:"read_only"`
}

// TestResult is what "Test connection" shows.
type TestResult struct {
	OK    bool       `json:"ok"`
	Error string     `json:"error,omitempty"`
	Tools []ToolInfo `json:"tools"`
}

// Test connects to the server as the gateway would and lists its tools
// (a stdio server is started for the test and removed after).
func (g *Gateway) Test(ctx context.Context, id int64) (*TestResult, error) {
	if !g.Enabled() {
		return nil, secretbox.ErrDisabled
	}
	m, err := g.Store.GetMCPServer(ctx, id)
	if err != nil {
		return nil, err
	}
	secrets, err := OpenSecrets(g.Box, m)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, g.connectTimeout()+10*time.Second)
	defer cancel()
	res := &TestResult{Tools: []ToolInfo{}}
	up, err := g.connect(ctx, m, secrets, 0, deref(m.ProjectID))
	var tools []*mcp.Tool
	if err == nil {
		defer up.Close()
		tools, err = listTools(ctx, up.session)
	}
	if err != nil {
		res.Error = redactAll(err.Error(), secrets)
		_ = g.Store.MarkMCPUsed(ctx, m.ID, res.Error)
		return res, nil
	}
	res.OK = true
	for _, t := range tools {
		ro := t.Annotations != nil && t.Annotations.ReadOnlyHint
		res.Tools = append(res.Tools, ToolInfo{Name: t.Name, Description: truncate(t.Description, 200), Allowed: ToolAllowed(m, t), ReadOnly: ro})
	}
	_ = g.Store.MarkMCPUsed(ctx, m.ID, "")
	return res, nil
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func trimmed(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	return &v
}

// StdioContainer is how a stdio server runs: npm and pip packages in the
// agent image (it has node and python), images as they are.
type StdioContainer struct {
	Image      string
	Entrypoint []string
	Cmd        []string
	Env        map[string]string
	User       string
}

// StdioCommand maps a stdio spec to its container. agentImage is the
// runner image.
func StdioCommand(s StdioSpec, agentImage string) (StdioContainer, error) {
	env := map[string]string{}
	for k, v := range s.Env {
		env[k] = v
	}
	switch s.Runtime {
	case RuntimeNPM:
		env["npm_config_update_notifier"] = "false"
		env["npm_config_loglevel"] = "error"
		return StdioContainer{Image: agentImage, Entrypoint: []string{"npx", "-y", s.Package}, Cmd: s.Args, Env: env, User: "1000:1000"}, nil
	case RuntimePip:
		cmd := s.Command
		if cmd == "" {
			cmd = s.Package
		}
		env["JM_MCP_PACKAGE"], env["JM_MCP_COMMAND"] = s.Package, cmd
		script := `python3 -m venv /tmp/mcp >&2 && /tmp/mcp/bin/pip install -q --disable-pip-version-check "$JM_MCP_PACKAGE" >&2 && exec "/tmp/mcp/bin/$JM_MCP_COMMAND" "$@"`
		return StdioContainer{Image: agentImage, Entrypoint: []string{"sh", "-c", script, "mcp"}, Cmd: s.Args, Env: env, User: "1000:1000"}, nil
	case RuntimeImage:
		return StdioContainer{Image: s.Package, Cmd: s.Args, Env: env}, nil
	}
	return StdioContainer{}, errors.New("unknown stdio runtime " + s.Runtime)
}
