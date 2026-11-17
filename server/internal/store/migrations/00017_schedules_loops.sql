-- +goose Up
-- Schedules and loops (docs/SPEC-server.md, "Schedules" and "Loops").
-- Additive only: no schedule rows and NULL task columns are today's
-- behaviour (no scheduled tasks, no loops).

CREATE TABLE schedules (
  id                  bigserial PRIMARY KEY,
  name                text NOT NULL,
  project_id          text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  -- NULL: the project's default repository at firing time.
  repository_id       bigint REFERENCES repositories(id) ON DELETE SET NULL,
  prompt              text NOT NULL,
  -- NULL: the project's agent.
  agent               text,
  -- Skill slugs added to the project's skills.
  skills              text[] NOT NULL DEFAULT '{}',
  -- MCP server names (a subset of the project's); NULL: all of them.
  mcp_servers         text[],
  cron                text NOT NULL,
  timezone            text NOT NULL,
  run_budget_usd      numeric(10,4),
  monthly_budget_usd  numeric(10,4),
  concurrency         text NOT NULL DEFAULT 'skip' CHECK (concurrency IN ('skip', 'queue')),
  pr_only_if_changes  boolean NOT NULL DEFAULT true,
  ends_at             timestamptz,
  -- Loop settings given to each task ({"check_command", "max_iterations", "budget_usd"}); NULL: no loop.
  loop                jsonb,
  enabled             boolean NOT NULL DEFAULT true,
  -- The next firing; NULL while disabled or after ends_at.
  next_run_at         timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX schedules_due ON schedules (next_run_at) WHERE enabled;

-- One row per firing: started (task_id), queued (waits for the previous
-- task, task_id NULL until it starts), skipped, missed (server down for
-- more than one interval) or failed (the task could not be created).
CREATE TABLE schedule_firings (
  id             bigserial PRIMARY KEY,
  schedule_id    bigint NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
  scheduled_for  timestamptz NOT NULL,
  trigger        text NOT NULL CHECK (trigger IN ('cron', 'manual')),
  outcome        text NOT NULL CHECK (outcome IN ('started', 'queued', 'skipped', 'missed', 'failed')),
  reason         text,
  task_id        bigint REFERENCES tasks(id) ON DELETE SET NULL,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX schedule_firings_schedule ON schedule_firings (schedule_id, id DESC);

-- The schedule a task was fired by, and what the schedule gives it: MCP
-- server names (NULL: the project's), a per-run budget (NULL: the
-- project's) and pr_always (an empty commit when nothing changed).
ALTER TABLE tasks ADD COLUMN schedule_id bigint REFERENCES schedules(id) ON DELETE SET NULL;
CREATE INDEX tasks_schedule ON tasks (schedule_id) WHERE schedule_id IS NOT NULL;
ALTER TABLE tasks ADD COLUMN mcp_servers text[];
ALTER TABLE tasks ADD COLUMN run_budget_usd numeric(10,4);
ALTER TABLE tasks ADD COLUMN pr_always boolean;

-- Loop until a check passes: the settings, the state (running, or why it
-- ended), the current iteration and how many iterations in a row changed
-- no files.
ALTER TABLE tasks ADD COLUMN loop jsonb;
ALTER TABLE tasks ADD COLUMN loop_state text
  CHECK (loop_state IN ('running', 'passed', 'max_iterations', 'budget', 'no_changes', 'stopped'));
ALTER TABLE tasks ADD COLUMN loop_iteration int;
ALTER TABLE tasks ADD COLUMN loop_idle int;
ALTER TABLE runs ADD COLUMN loop_iteration int;

-- +goose Down
ALTER TABLE runs DROP COLUMN loop_iteration;
ALTER TABLE tasks DROP COLUMN loop_idle;
ALTER TABLE tasks DROP COLUMN loop_iteration;
ALTER TABLE tasks DROP COLUMN loop_state;
ALTER TABLE tasks DROP COLUMN loop;
ALTER TABLE tasks DROP COLUMN pr_always;
ALTER TABLE tasks DROP COLUMN run_budget_usd;
ALTER TABLE tasks DROP COLUMN mcp_servers;
DROP INDEX tasks_schedule;
ALTER TABLE tasks DROP COLUMN schedule_id;
DROP TABLE schedule_firings;
DROP TABLE schedules;
