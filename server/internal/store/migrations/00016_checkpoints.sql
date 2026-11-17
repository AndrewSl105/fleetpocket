-- +goose Up
-- Checkpoints (SPEC-server, "Checkpoints"): one row per runner `checkpoint`
-- event. The commit itself lives only in the task workspace at
-- refs/jm/checkpoints/<run_id>/<n> and goes away with it (WORKSPACE_RETENTION).
CREATE TABLE checkpoints (
  id             bigserial PRIMARY KEY,
  task_id        bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id         bigint NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  -- The timeline row of the checkpoint (NULL once event retention removed it).
  event_id       bigint REFERENCES events(id) ON DELETE SET NULL,
  n              int NOT NULL,
  ref_sha        text NOT NULL,
  turn           int NOT NULL DEFAULT 0,
  session_ref    text NOT NULL DEFAULT '',
  files_changed  int NOT NULL DEFAULT 0,
  reason         text NOT NULL DEFAULT 'turn',
  created_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_id, n)
);
CREATE INDEX checkpoints_task ON checkpoints (task_id, id);

-- The checkpoint a `restore` run starts from (NULL for every other run).
ALTER TABLE runs ADD COLUMN restore_checkpoint_id bigint REFERENCES checkpoints(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE runs DROP COLUMN restore_checkpoint_id;
DROP TABLE checkpoints;
