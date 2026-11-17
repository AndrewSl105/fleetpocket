-- +goose Up
-- MCP servers through the gateway and the egress allowlist
-- (docs/SPEC-server.md section "MCP gateway and egress"). Additive only:
-- no rows and NULL columns are today's behaviour (no MCP, open egress).

-- An MCP server of a project, or a global one (project_id NULL) that
-- projects enable in project_mcp_globals. secrets holds the credentials
-- (header values, env values, the database URL) as one JSON object sealed
-- with SECRETS_KEY (internal/secretbox); secret_hints keeps the last
-- characters per key for the API, never the values.
CREATE TABLE mcp_servers (
    id           bigserial PRIMARY KEY,
    project_id   text REFERENCES projects (id) ON DELETE CASCADE,
    name         text NOT NULL,
    kind         text NOT NULL,            -- remote | stdio | postgres
    transport    text,                     -- remote: http | sse
    url          text,                     -- remote
    runtime      text,                     -- stdio: npm | pip | image
    package      text,                     -- stdio: npm/pip package or image
    command      text,                     -- stdio pip: executable (default: package)
    args         jsonb NOT NULL DEFAULT '[]',
    secrets      bytea,
    secret_hints jsonb NOT NULL DEFAULT '{}',
    tools_mode   text NOT NULL DEFAULT 'all', -- all | allow | deny
    tools        jsonb NOT NULL DEFAULT '[]',
    read_only    boolean NOT NULL DEFAULT false,
    max_calls    int,
    enabled      boolean NOT NULL DEFAULT true,
    last_used_at timestamptz,
    last_status  text,                     -- ok | error (last call or test)
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX mcp_servers_name ON mcp_servers (coalesce(project_id, ''), name);

-- Global servers a project uses.
CREATE TABLE project_mcp_globals (
    project_id text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    server_id  bigint NOT NULL REFERENCES mcp_servers (id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, server_id)
);

-- {"mode": "open"|"allowlist", "presets": [...], "hosts": [...]}; NULL: open.
ALTER TABLE projects ADD COLUMN egress jsonb;

-- The run's gateway token (SHA-256, hex) and the servers it may use. The
-- token dies when the run finishes (FinishRun clears it) or expires.
ALTER TABLE runs ADD COLUMN gateway_token_hash text;
ALTER TABLE runs ADD COLUMN gateway_expires_at timestamptz;
ALTER TABLE runs ADD COLUMN mcp_server_ids bigint[];
CREATE UNIQUE INDEX runs_gateway_token ON runs (gateway_token_hash) WHERE gateway_token_hash IS NOT NULL;

-- +goose Down
DROP INDEX runs_gateway_token;
ALTER TABLE runs DROP COLUMN mcp_server_ids;
ALTER TABLE runs DROP COLUMN gateway_expires_at;
ALTER TABLE runs DROP COLUMN gateway_token_hash;
ALTER TABLE projects DROP COLUMN egress;
DROP TABLE project_mcp_globals;
DROP TABLE mcp_servers;
