-- +goose Up
-- Ideas board (SPEC-server, "Ideas board"). Off by default: the settings row
-- `ideas` ({enabled, columns}) is missing until the owner turns it on.
CREATE TABLE ideas (
  id           bigserial PRIMARY KEY,
  project_id   text REFERENCES projects(id) ON DELETE SET NULL,
  title        text NOT NULL,
  body         text NOT NULL DEFAULT '',
  -- A column id from the `ideas` setting (inbox, shaping, ready,
  -- in_progress, done, or an id the owner added).
  column_id    text NOT NULL DEFAULT 'inbox',
  -- Fractional order inside the column (ascending).
  position     double precision NOT NULL,
  tags         text[] NOT NULL DEFAULT '{}',
  -- What moved the card last without the owner ("Moved to Done: task #12, PR merged").
  auto_note    text,
  source       text NOT NULL DEFAULT 'web',
  archived_at  timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ideas_board ON ideas (column_id, position) WHERE archived_at IS NULL;

-- Files, voice notes and links of an idea. Files live at
-- <ATTACHMENTS_DIR>/ideas/<idea_id>/<id>.<ext> (thumbnails in .thumbs/).
CREATE TABLE idea_attachments (
  id            bigserial PRIMARY KEY,
  idea_id       bigint NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  kind          text NOT NULL,           -- file | voice | link
  original_name text NOT NULL DEFAULT '',
  mime          text NOT NULL DEFAULT '',
  size_bytes    bigint NOT NULL DEFAULT 0,
  width         int NOT NULL DEFAULT 0,
  height        int NOT NULL DEFAULT 0,
  duration_ms   int,
  transcript    text,
  url           text,
  link_title    text,
  has_thumb     boolean NOT NULL DEFAULT false,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON idea_attachments (idea_id, id);

-- Tasks started from an idea.
CREATE TABLE idea_tasks (
  idea_id     bigint NOT NULL REFERENCES ideas(id) ON DELETE CASCADE,
  task_id     bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (idea_id, task_id)
);
CREATE INDEX ON idea_tasks (task_id);

-- +goose Down
DROP TABLE idea_tasks;
DROP TABLE idea_attachments;
DROP TABLE ideas;
