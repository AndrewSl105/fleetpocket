-- +goose Up
-- Pipelines (docs/SPEC-server.md "Pipelines"): a graph of stages, each run
-- as an ordinary task. Additive: tasks outside pipelines keep NULLs.
CREATE TABLE pipelines (
  id              bigserial PRIMARY KEY,
  project_id      text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  repository_id   bigint NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  template        text NOT NULL,
  template_version int NOT NULL DEFAULT 1,
  title           text NOT NULL,
  prompt          text NOT NULL,
  status          text NOT NULL DEFAULT 'running'
                  CHECK (status IN ('running', 'awaiting_approval', 'needs_you', 'completed', 'failed', 'cancelled')),
  status_reason   text,
  budget_usd      numeric(10, 2),
  max_parallel    int NOT NULL DEFAULT 3 CHECK (max_parallel BETWEEN 1 AND 10),
  base_branch     text NOT NULL,
  pipeline_branch text,
  plan            jsonb,
  plan_attempts   int NOT NULL DEFAULT 0,
  created_by      bigint,
  created_at      timestamptz NOT NULL DEFAULT now(),
  finished_at     timestamptz
);
CREATE INDEX pipelines_open ON pipelines (status) WHERE status IN ('running', 'needs_you', 'awaiting_approval');

CREATE TABLE pipeline_stages (
  id          bigserial PRIMARY KEY,
  pipeline_id bigint NOT NULL REFERENCES pipelines(id) ON DELETE CASCADE,
  key         text NOT NULL,
  kind        text NOT NULL CHECK (kind IN ('scout', 'design', 'build', 'integrate', 'review', 'report')),
  title       text NOT NULL,
  depends_on  text[] NOT NULL DEFAULT '{}',
  provider    text NOT NULL,
  model       text,
  output      text NOT NULL CHECK (output IN ('pr', 'artifacts', 'pr_and_artifacts', 'branch')),
  skills      text[] NOT NULL DEFAULT '{}',
  files       text[] NOT NULL DEFAULT '{}',
  prompt      text NOT NULL DEFAULT '',
  done_when   text NOT NULL DEFAULT '',
  node        text,
  task_id     bigint REFERENCES tasks(id) ON DELETE SET NULL,
  status      text NOT NULL DEFAULT 'waiting'
              CHECK (status IN ('waiting', 'ready', 'running', 'done', 'failed', 'skipped', 'cancelled')),
  status_reason text,
  position    int NOT NULL,
  UNIQUE (pipeline_id, key)
);

ALTER TABLE tasks ADD COLUMN pipeline_id bigint REFERENCES pipelines(id) ON DELETE SET NULL;
ALTER TABLE tasks ADD COLUMN stage_id bigint REFERENCES pipeline_stages(id) ON DELETE SET NULL;
-- The branch the task starts from; NULL = the repository's base branch.
ALTER TABLE tasks ADD COLUMN base_ref text;
-- Output "branch": where the task pushes without opening a PR.
ALTER TABLE tasks ADD COLUMN push_branch text;
-- A stage's model (e.g. the cheap model of a scout); NULL = the project's.
ALTER TABLE tasks ADD COLUMN model text;
CREATE INDEX tasks_pipeline ON tasks (pipeline_id) WHERE pipeline_id IS NOT NULL;
-- Widen (not change) the task outputs with 'branch'.
ALTER TABLE tasks DROP CONSTRAINT tasks_output_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_output_check CHECK (output IN ('pr', 'artifacts', 'pr_and_artifacts', 'branch'));
-- Pipeline handoffs include plan.json.
ALTER TABLE task_artifacts DROP CONSTRAINT task_artifacts_kind_check;
ALTER TABLE task_artifacts ADD CONSTRAINT task_artifacts_kind_check
  CHECK (kind IN ('mermaid', 'dbml', 'd2', 'plantuml', 'svg', 'markdown', 'sql', 'json'));

-- +goose Down
DELETE FROM task_artifacts WHERE kind = 'json';
ALTER TABLE task_artifacts DROP CONSTRAINT task_artifacts_kind_check;
ALTER TABLE task_artifacts ADD CONSTRAINT task_artifacts_kind_check
  CHECK (kind IN ('mermaid', 'dbml', 'd2', 'plantuml', 'svg', 'markdown', 'sql'));
UPDATE tasks SET output = 'pr' WHERE output = 'branch';
ALTER TABLE tasks DROP CONSTRAINT tasks_output_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_output_check CHECK (output IN ('pr', 'artifacts', 'pr_and_artifacts'));
DROP INDEX tasks_pipeline;
ALTER TABLE tasks DROP COLUMN model;
ALTER TABLE tasks DROP COLUMN push_branch;
ALTER TABLE tasks DROP COLUMN base_ref;
ALTER TABLE tasks DROP COLUMN stage_id;
ALTER TABLE tasks DROP COLUMN pipeline_id;
DROP TABLE pipeline_stages;
DROP TABLE pipelines;
