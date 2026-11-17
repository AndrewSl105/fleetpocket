-- +goose Up
-- Costs and budgets (docs/SPEC-server.md section "Costs and budgets").
-- Additive only: NULL columns and an empty table are today's behaviour (no
-- monthly caps, costs as reported).

-- What a run used: the agent and model, tokens (input includes the cache
-- reads and writes, which are also counted on their own when the agent
-- reports them), where cost_usd came from (reported by the agent,
-- estimated from the price table, or unknown) and how the agent was
-- authenticated (a Claude subscription makes the cost "API-equivalent").
ALTER TABLE runs ADD COLUMN agent text;
ALTER TABLE runs ADD COLUMN model text;
ALTER TABLE runs ADD COLUMN input_tokens bigint;
ALTER TABLE runs ADD COLUMN output_tokens bigint;
ALTER TABLE runs ADD COLUMN cache_read_tokens bigint;
ALTER TABLE runs ADD COLUMN cache_write_tokens bigint;
ALTER TABLE runs ADD COLUMN cost_source text CHECK (cost_source IN ('reported', 'estimated', 'unknown'));
ALTER TABLE runs ADD COLUMN auth text CHECK (auth IN ('api_key', 'subscription'));
-- Month-to-date sums select runs by start time.
CREATE INDEX runs_started_at_idx ON runs (started_at);

-- Task totals kept with cost_usd_total: the auto-fix runs' share, whether
-- any run's cost is an estimate or API-equivalent.
ALTER TABLE tasks ADD COLUMN autofix_cost_usd numeric(10,4);
ALTER TABLE tasks ADD COLUMN cost_estimated boolean;
ALTER TABLE tasks ADD COLUMN cost_api_equivalent boolean;
-- The budget scope ('global' or 'project') a queued task waits for, and
-- when the owner let it start anyway (good for the month it was given in).
ALTER TABLE tasks ADD COLUMN budget_wait text;
ALTER TABLE tasks ADD COLUMN budget_override_at timestamptz;

-- A project's monthly cap ({"monthly_usd": n}); NULL: none.
ALTER TABLE projects ADD COLUMN budget jsonb;

-- Budget notifications already sent: one per scope, month and level (80,
-- 100). scope_kind is global | project (schedule later); scope_id is ''
-- for global.
CREATE TABLE budget_alerts (
    scope_kind  text NOT NULL,
    scope_id    text NOT NULL,
    month       date NOT NULL,
    level       int NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope_kind, scope_id, month, level)
);

-- +goose Down
DROP TABLE budget_alerts;
ALTER TABLE projects DROP COLUMN budget;
ALTER TABLE tasks DROP COLUMN budget_override_at;
ALTER TABLE tasks DROP COLUMN budget_wait;
ALTER TABLE tasks DROP COLUMN cost_api_equivalent;
ALTER TABLE tasks DROP COLUMN cost_estimated;
ALTER TABLE tasks DROP COLUMN autofix_cost_usd;
DROP INDEX runs_started_at_idx;
ALTER TABLE runs DROP COLUMN auth;
ALTER TABLE runs DROP COLUMN cost_source;
ALTER TABLE runs DROP COLUMN cache_write_tokens;
ALTER TABLE runs DROP COLUMN cache_read_tokens;
ALTER TABLE runs DROP COLUMN output_tokens;
ALTER TABLE runs DROP COLUMN input_tokens;
ALTER TABLE runs DROP COLUMN model;
ALTER TABLE runs DROP COLUMN agent;
