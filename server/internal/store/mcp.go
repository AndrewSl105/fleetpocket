package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// MCP server kinds.
const (
	MCPRemote   = "remote"   // Streamable HTTP or SSE URL
	MCPStdio    = "stdio"    // a package or image, run as a sidecar
	MCPPostgres = "postgres" // the bundled read-only Postgres server
)

// MCPServer is a row of mcp_servers. Secrets is the sealed JSON object of
// its credentials; only the gateway opens it.
type MCPServer struct {
	ID          int64             `json:"id"`
	ProjectID   *string           `json:"project_id"`
	Name        string            `json:"name"`
	Kind        string            `json:"kind"`
	Transport   *string           `json:"transport"`
	URL         *string           `json:"url"`
	Runtime     *string           `json:"runtime"`
	Package     *string           `json:"package"`
	Command     *string           `json:"command"`
	Args        []string          `json:"args"`
	Secrets     []byte            `json:"-"`
	SecretHints map[string]string `json:"secret_hints"`
	ToolsMode   string            `json:"tools_mode"`
	Tools       []string          `json:"tools"`
	ReadOnly    bool              `json:"read_only"`
	MaxCalls    *int              `json:"max_calls"`
	Enabled     bool              `json:"enabled"`
	LastUsedAt  *time.Time        `json:"last_used_at"`
	LastStatus  *string           `json:"last_status"`
	LastError   *string           `json:"last_error"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

const mcpCols = `id, project_id, name, kind, transport, url, runtime, package, command, args, secrets,
	secret_hints, tools_mode, tools, read_only, max_calls, enabled, last_used_at, last_status, last_error,
	created_at, updated_at`

func scanMCP(row interface{ Scan(...any) error }) (*MCPServer, error) {
	var m MCPServer
	var args, hints, tools []byte
	err := row.Scan(&m.ID, &m.ProjectID, &m.Name, &m.Kind, &m.Transport, &m.URL, &m.Runtime, &m.Package,
		&m.Command, &args, &m.Secrets, &hints, &m.ToolsMode, &tools, &m.ReadOnly, &m.MaxCalls, &m.Enabled,
		&m.LastUsedAt, &m.LastStatus, &m.LastError, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	m.Args, m.Tools, m.SecretHints = []string{}, []string{}, map[string]string{}
	for _, f := range []struct {
		b []byte
		v any
	}{{args, &m.Args}, {tools, &m.Tools}, {hints, &m.SecretHints}} {
		if len(f.b) > 0 {
			if err := json.Unmarshal(f.b, f.v); err != nil {
				return nil, fmt.Errorf("mcp server %d: %w", m.ID, err)
			}
		}
	}
	return &m, nil
}

func collectMCP(rows pgx.Rows, err error) ([]*MCPServer, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MCPServer{}
	for rows.Next() {
		m, err := scanMCP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func mcpArgs(m *MCPServer) []any {
	args, _ := json.Marshal(nonNil(m.Args))
	tools, _ := json.Marshal(nonNil(m.Tools))
	hints, _ := json.Marshal(m.SecretHints)
	if m.SecretHints == nil {
		hints = []byte("{}")
	}
	return []any{m.ProjectID, m.Name, m.Kind, m.Transport, m.URL, m.Runtime, m.Package, m.Command,
		args, m.Secrets, hints, m.ToolsMode, tools, m.ReadOnly, m.MaxCalls, m.Enabled}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// CreateMCPServer inserts m (ID and timestamps are set from the row).
func (s *Store) CreateMCPServer(ctx context.Context, m *MCPServer) (*MCPServer, error) {
	out, err := scanMCP(s.Pool.QueryRow(ctx, `INSERT INTO mcp_servers (project_id, name, kind, transport, url,
		runtime, package, command, args, secrets, secret_hints, tools_mode, tools, read_only, max_calls, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING `+mcpCols, mcpArgs(m)...))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: an MCP server named %q exists", ErrConflict, m.Name)
	}
	return out, err
}

// UpdateMCPServer replaces every writable column of m.ID (project_id stays).
func (s *Store) UpdateMCPServer(ctx context.Context, m *MCPServer) (*MCPServer, error) {
	args := append([]any{m.ID}, mcpArgs(m)[1:]...)
	out, err := scanMCP(s.Pool.QueryRow(ctx, `UPDATE mcp_servers SET name=$2, kind=$3, transport=$4, url=$5,
		runtime=$6, package=$7, command=$8, args=$9, secrets=$10, secret_hints=$11, tools_mode=$12, tools=$13,
		read_only=$14, max_calls=$15, enabled=$16, updated_at=now() WHERE id=$1 RETURNING `+mcpCols, args...))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: an MCP server named %q exists", ErrConflict, m.Name)
	}
	return out, notFound(err)
}

func (s *Store) GetMCPServer(ctx context.Context, id int64) (*MCPServer, error) {
	m, err := scanMCP(s.Pool.QueryRow(ctx, `SELECT `+mcpCols+` FROM mcp_servers WHERE id = $1`, id))
	return m, notFound(err)
}

func (s *Store) DeleteMCPServer(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM mcp_servers WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ListMCPServers returns a project's own servers, or the global ones when
// projectID is "".
func (s *Store) ListMCPServers(ctx context.Context, projectID string) ([]*MCPServer, error) {
	if projectID == "" {
		return collectMCP(s.Pool.Query(ctx, `SELECT `+mcpCols+` FROM mcp_servers WHERE project_id IS NULL ORDER BY name`))
	}
	return collectMCP(s.Pool.Query(ctx, `SELECT `+mcpCols+` FROM mcp_servers WHERE project_id = $1 ORDER BY name`, projectID))
}

// MCPServersByID returns the servers with these ids (any order).
func (s *Store) MCPServersByID(ctx context.Context, ids []int64) ([]*MCPServer, error) {
	return collectMCP(s.Pool.Query(ctx, `SELECT `+mcpCols+` FROM mcp_servers WHERE id = ANY($1) ORDER BY name`, ids))
}

// EffectiveMCPServers are the enabled servers a project's tasks get: its own
// and the global ones it uses.
func (s *Store) EffectiveMCPServers(ctx context.Context, projectID string) ([]*MCPServer, error) {
	return collectMCP(s.Pool.Query(ctx, `SELECT `+mcpCols+` FROM mcp_servers m WHERE m.enabled AND (m.project_id = $1
		OR (m.project_id IS NULL AND EXISTS (SELECT 1 FROM project_mcp_globals g WHERE g.project_id = $1 AND g.server_id = m.id)))
		ORDER BY m.name`, projectID))
}

// ProjectMCPGlobals lists the ids of the global servers the project uses.
func (s *Store) ProjectMCPGlobals(ctx context.Context, projectID string) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT server_id FROM project_mcp_globals WHERE project_id = $1 ORDER BY server_id`, projectID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if ids == nil {
		ids = []int64{}
	}
	return ids, err
}

// SetProjectMCPGlobals replaces the global servers the project uses; ids
// that are not global servers are rejected.
func (s *Store) SetProjectMCPGlobals(ctx context.Context, projectID string, ids []int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM project_mcp_globals WHERE project_id = $1`, projectID); err != nil {
		return err
	}
	for _, id := range ids {
		tag, err := tx.Exec(ctx, `INSERT INTO project_mcp_globals (project_id, server_id)
			SELECT $1, id FROM mcp_servers WHERE id = $2 AND project_id IS NULL ON CONFLICT DO NOTHING`, projectID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: %d is not a global MCP server", ErrNotFound, id)
		}
	}
	return tx.Commit(ctx)
}

// MarkMCPUsed records the outcome of the server's last call or test.
func (s *Store) MarkMCPUsed(ctx context.Context, id int64, callErr string) error {
	var status, msg any = "ok", nil
	if callErr != "" {
		status, msg = "error", callErr
	}
	_, err := s.Pool.Exec(ctx, `UPDATE mcp_servers SET last_used_at = now(), last_status = $2, last_error = $3 WHERE id = $1`, id, status, msg)
	return err
}

// ---- egress ----

// Egress modes (projects.egress; NULL is open).
const (
	EgressOpen      = "open"
	EgressAllowlist = "allowlist"
)

// Egress is a project's network policy for its runners.
type Egress struct {
	Mode    string   `json:"mode"`
	Presets []string `json:"presets"`
	Hosts   []string `json:"hosts"`
}

// ProjectEgress returns the project's egress policy (open when unset).
func (s *Store) ProjectEgress(ctx context.Context, projectID string) (Egress, error) {
	var b []byte
	e := Egress{Mode: EgressOpen, Presets: []string{}, Hosts: []string{}}
	if err := s.Pool.QueryRow(ctx, `SELECT egress FROM projects WHERE id = $1`, projectID).Scan(&b); err != nil {
		return e, notFound(err)
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &e); err != nil {
			return e, err
		}
	}
	e.Presets, e.Hosts = nonNil(e.Presets), nonNil(e.Hosts)
	if e.Mode == "" {
		e.Mode = EgressOpen
	}
	return e, nil
}

// SetProjectEgress stores the policy; open with no hosts or presets is NULL.
func (s *Store) SetProjectEgress(ctx context.Context, projectID string, e Egress) error {
	var v any
	if e.Mode != EgressOpen || len(e.Presets) > 0 || len(e.Hosts) > 0 {
		b, _ := json.Marshal(Egress{Mode: e.Mode, Presets: nonNil(e.Presets), Hosts: nonNil(e.Hosts)})
		v = b
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE projects SET egress = $2, updated_at = now() WHERE id = $1`, projectID, v)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AddEgressHost appends host to the project's allowlist (no duplicates) and
// returns the policy.
func (s *Store) AddEgressHost(ctx context.Context, projectID, host string) (Egress, error) {
	e, err := s.ProjectEgress(ctx, projectID)
	if err != nil {
		return e, err
	}
	for _, h := range e.Hosts {
		if h == host {
			return e, nil
		}
	}
	e.Hosts = append(e.Hosts, host)
	return e, s.SetProjectEgress(ctx, projectID, e)
}

// ---- run gateway tokens ----

// RunGateway is what a valid gateway token resolves to.
type RunGateway struct {
	RunID     int64
	TaskID    int64
	ProjectID string
	ServerIDs []int64
	ExpiresAt time.Time
}

// SetRunGateway stores the run's token hash, expiry and server scope.
func (s *Store) SetRunGateway(ctx context.Context, runID int64, tokenHash string, expires time.Time, serverIDs []int64) error {
	if serverIDs == nil {
		serverIDs = []int64{}
	}
	_, err := s.Pool.Exec(ctx, `UPDATE runs SET gateway_token_hash = $2, gateway_expires_at = $3, mcp_server_ids = $4
		WHERE id = $1 AND finished_at IS NULL`, runID, tokenHash, expires, serverIDs)
	return err
}

// RunByGatewayToken resolves a token hash of an unfinished, unexpired run.
func (s *Store) RunByGatewayToken(ctx context.Context, tokenHash string) (*RunGateway, error) {
	var g RunGateway
	err := s.Pool.QueryRow(ctx, `SELECT r.id, r.task_id, t.project_id, coalesce(r.mcp_server_ids, '{}'), r.gateway_expires_at
		FROM runs r JOIN tasks t ON t.id = r.task_id
		WHERE r.gateway_token_hash = $1 AND r.finished_at IS NULL AND r.gateway_expires_at > now()`, tokenHash).
		Scan(&g.RunID, &g.TaskID, &g.ProjectID, &g.ServerIDs, &g.ExpiresAt)
	return &g, notFound(err)
}

// ClearRunGateway revokes the run's token.
func (s *Store) ClearRunGateway(ctx context.Context, runID int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE runs SET gateway_token_hash = NULL WHERE id = $1`, runID)
	return err
}
