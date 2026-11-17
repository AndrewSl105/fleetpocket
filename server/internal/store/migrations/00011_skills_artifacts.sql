-- +goose Up
-- Skill library: SKILL.md folders delivered to the runner (SPEC-server, "Skill library").
CREATE TABLE skills (
  id                  bigserial PRIMARY KEY,
  slug                text NOT NULL UNIQUE CHECK (slug ~ '^[a-z][a-z0-9-]{1,63}$'),
  name                text NOT NULL,
  description         text NOT NULL,
  body                text NOT NULL,
  files               jsonb NOT NULL DEFAULT '[]',
  enabled_by_default  boolean NOT NULL DEFAULT false,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE project_skills (
  project_id  text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  skill_id    bigint NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
  PRIMARY KEY (project_id, skill_id)
);

-- Per-task overrides (slugs) and the resolved list the last run used.
ALTER TABLE tasks ADD COLUMN skills_add text[] NOT NULL DEFAULT '{}';
ALTER TABLE tasks ADD COLUMN skills_remove text[] NOT NULL DEFAULT '{}';
ALTER TABLE tasks ADD COLUMN skills text[] NOT NULL DEFAULT '{}';

-- Task output: today's behaviour is 'pr'.
ALTER TABLE tasks ADD COLUMN output text NOT NULL DEFAULT 'pr'
  CHECK (output IN ('pr', 'artifacts', 'pr_and_artifacts'));
ALTER TABLE projects ADD COLUMN default_output text
  CHECK (default_output IN ('pr', 'artifacts', 'pr_and_artifacts'));

CREATE TABLE task_artifacts (
  id          bigserial PRIMARY KEY,
  task_id     bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id      bigint REFERENCES runs(id) ON DELETE SET NULL,
  path        text NOT NULL,
  kind        text NOT NULL CHECK (kind IN ('mermaid', 'dbml', 'd2', 'plantuml', 'svg', 'markdown', 'sql')),
  title       text NOT NULL,
  summary     text NOT NULL DEFAULT '',
  confidence  text NOT NULL CHECK (confidence IN ('exact', 'derived')),
  sources     jsonb NOT NULL DEFAULT '[]',
  content     text NOT NULL,
  sha256      text NOT NULL,
  version     int NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (task_id, path, version)
);
CREATE INDEX task_artifacts_task ON task_artifacts (task_id, path, version DESC);

-- +goose Down
DROP TABLE task_artifacts;
ALTER TABLE projects DROP COLUMN default_output;
ALTER TABLE tasks DROP COLUMN output;
ALTER TABLE tasks DROP COLUMN skills;
ALTER TABLE tasks DROP COLUMN skills_remove;
ALTER TABLE tasks DROP COLUMN skills_add;
DROP TABLE project_skills;
DROP TABLE skills;
