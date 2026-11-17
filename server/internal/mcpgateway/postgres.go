package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The bundled Postgres MCP server: runs inside the gateway with the
// database URL the owner stored, so neither the URL nor the password reach
// the runner. Read-only mode is enforced, not hinted:
//   - one fresh connection per call, so no session state carries over;
//   - the extended query protocol (pgx's default), which refuses more than
//     one statement, so "COMMIT; DROP ..." cannot run;
//   - the statement runs inside BEGIN READ ONLY and the transaction is
//     always rolled back, so even "SET TRANSACTION READ WRITE" changes
//     nothing that persists.
// Owners can add a read-only database role on top (docs/OPERATIONS.md).

const (
	pgMaxRows    = 200
	pgMaxBytes   = 64 << 10
	pgStatementT = 30 * time.Second
)

type pgQueryIn struct {
	SQL string `json:"sql" jsonschema:"one SQL statement"`
}

type pgOut struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated,omitempty"`
	Command   string   `json:"command,omitempty"`
}

func newPostgresServer(dbURL string, readOnly bool) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "jungle-monkey-postgres", Version: "1"}, nil)
	desc := "Run one SQL statement and return up to 200 rows."
	if readOnly {
		desc = "Run one read-only SQL statement (SELECT, EXPLAIN, SHOW) and return up to 200 rows. Writes are refused."
	}
	mcp.AddTool(s, &mcp.Tool{Name: "query", Description: desc, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in pgQueryIn) (*mcp.CallToolResult, pgOut, error) {
			out, err := pgRun(ctx, dbURL, readOnly, in.SQL)
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_tables", Description: "List tables and views with their schema.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, pgOut, error) {
			out, err := pgRun(ctx, dbURL, true, `SELECT table_schema, table_name, table_type FROM information_schema.tables
				WHERE table_schema NOT IN ('pg_catalog', 'information_schema') ORDER BY 1, 2`)
			return nil, out, err
		})
	return s
}

// pgRun executes one statement in its own connection and transaction.
func pgRun(ctx context.Context, dbURL string, readOnly bool, sql string) (pgOut, error) {
	out := pgOut{Columns: []string{}, Rows: [][]any{}}
	if dbURL == "" {
		return out, fmt.Errorf("no %s stored for this server", SecretDatabaseURL)
	}
	ctx, cancel := context.WithTimeout(ctx, pgStatementT+5*time.Second)
	defer cancel()
	cfg, err := pgx.ParseConfig(dbURL)
	if err != nil {
		return out, fmt.Errorf("database URL: invalid")
	}
	// Never the simple protocol: it would accept several statements.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	cfg.RuntimeParams["statement_timeout"] = fmt.Sprint(pgStatementT.Milliseconds())
	if readOnly {
		cfg.RuntimeParams["default_transaction_read_only"] = "on"
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return out, fmt.Errorf("connect: %v", redact(err, dbURL))
	}
	defer conn.Close(context.Background())
	mode := pgx.ReadWrite
	if readOnly {
		mode = pgx.ReadOnly
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: mode})
	if err != nil {
		return out, err
	}
	// Read-only: always rolled back. Read-write: committed on success.
	defer func() { _ = tx.Rollback(context.Background()) }()
	rows, err := tx.Query(ctx, sql)
	if err != nil {
		return out, err
	}
	for _, f := range rows.FieldDescriptions() {
		out.Columns = append(out.Columns, f.Name)
	}
	size := 0
	for rows.Next() {
		if len(out.Rows) >= pgMaxRows || size > pgMaxBytes {
			out.Truncated = true
			break
		}
		vals, err := rows.Values()
		if err != nil {
			rows.Close()
			return out, err
		}
		for i, v := range vals {
			if _, err := json.Marshal(v); err != nil {
				vals[i] = fmt.Sprint(v)
			}
		}
		b, _ := json.Marshal(vals)
		size += len(b)
		out.Rows = append(out.Rows, vals)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Command = rows.CommandTag().String()
	if !readOnly {
		return out, tx.Commit(ctx)
	}
	return out, nil
}

// redact keeps the database URL (and its password) out of error messages.
func redact(err error, secret string) string {
	msg := err.Error()
	if secret != "" {
		msg = replaceAll(msg, secret, "<database url>")
	}
	if cfg, perr := pgx.ParseConfig(secret); perr == nil && cfg.Password != "" {
		msg = replaceAll(msg, cfg.Password, "<password>")
	}
	return msg
}
