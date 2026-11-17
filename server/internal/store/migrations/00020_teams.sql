-- +goose Up
-- Teams for self-hosted installs (docs/SPEC-server.md "Teams"). Additive
-- only: with no `team` settings row every new column stays NULL and the
-- server behaves exactly as the single-owner server did.

CREATE TABLE users (
  id                 bigserial PRIMARY KEY,
  email              text NOT NULL UNIQUE CHECK (email = lower(email)),
  name               text NOT NULL,
  role               text NOT NULL CHECK (role IN ('admin', 'member', 'viewer')),
  password_hash      text NOT NULL,
  -- secretbox-sealed (aad "user_totp:<id>"); NULL while an invited user has
  -- not confirmed TOTP enrolment (the account is not active yet).
  totp_secret        bytea,
  -- User #1, created from the single owner. Their row starts with a copy of
  -- the owner's hash and TOTP secret; the env/wizard owner credentials keep
  -- logging in as this user (break-glass admin).
  is_owner           boolean NOT NULL DEFAULT false,
  github_login       text,
  commit_email       text,
  telegram_user_id   bigint UNIQUE,
  -- Optional monthly budget per user (USD); NULL: none.
  monthly_budget_usd numeric(10,2),
  -- Mine | Everyone filter default ('mine' | 'everyone'); NULL: by role.
  task_scope         text,
  disabled_at        timestamptz,
  created_at         timestamptz NOT NULL DEFAULT now(),
  last_login_at      timestamptz
);

CREATE TABLE invites (
  id          bigserial PRIMARY KEY,
  token_hash  bytea NOT NULL UNIQUE,
  role        text NOT NULL CHECK (role IN ('admin', 'member', 'viewer')),
  email       text,
  created_by  bigint REFERENCES users(id) ON DELETE SET NULL,
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz,
  used_by     bigint REFERENCES users(id) ON DELETE SET NULL,
  revoked_at  timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
  id             bigserial PRIMARY KEY,
  at             timestamptz NOT NULL DEFAULT now(),
  actor_user_id  bigint REFERENCES users(id) ON DELETE SET NULL,
  action         text NOT NULL,
  target_type    text NOT NULL DEFAULT '',
  target_id      text NOT NULL DEFAULT '',
  meta           jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_log_at ON audit_log (id DESC);
CREATE INDEX audit_log_actor ON audit_log (actor_user_id, id DESC);

-- Personal agent keys: one per user and provider, secretbox-sealed (aad
-- "user_credential:<user_id>:<provider>").
CREATE TABLE user_credentials (
  user_id          bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider         text NOT NULL CHECK (provider IN ('claude', 'codex', 'cursor')),
  kind             text NOT NULL CHECK (kind IN ('oauth_token', 'api_key')),
  value_encrypted  bytea NOT NULL,
  hint             text NOT NULL DEFAULT '',
  label            text NOT NULL DEFAULT '',
  created_at       timestamptz NOT NULL DEFAULT now(),
  last_used_at     timestamptz,
  PRIMARY KEY (user_id, provider)
);

-- One-time Telegram link codes (/link 123456), 10 minutes.
CREATE TABLE telegram_link_codes (
  code        text PRIMARY KEY,
  user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at  timestamptz NOT NULL
);

ALTER TABLE auth_sessions ADD COLUMN user_id bigint REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE tasks ADD COLUMN created_by bigint REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX tasks_created_by ON tasks (created_by) WHERE created_by IS NOT NULL;
ALTER TABLE schedules ADD COLUMN created_by bigint REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE ideas ADD COLUMN created_by bigint REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE mcp_servers ADD COLUMN created_by bigint REFERENCES users(id) ON DELETE SET NULL;
-- A skill with personal = true is visible to its owner only.
ALTER TABLE skills ADD COLUMN owner_user_id bigint REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE skills ADD COLUMN personal boolean NOT NULL DEFAULT false;
ALTER TABLE nodes ADD COLUMN owner_user_id bigint REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE nodes ADD COLUMN shared boolean NOT NULL DEFAULT false;
-- Who a run's cost is charged to (the task creator at the time of the run).
ALTER TABLE runs ADD COLUMN user_id bigint REFERENCES users(id) ON DELETE SET NULL;
-- When a question was also sent to the admins (escalation after the timeout).
ALTER TABLE questions ADD COLUMN escalated_at timestamptz;

-- +goose Down
ALTER TABLE questions DROP COLUMN escalated_at;
ALTER TABLE runs DROP COLUMN user_id;
ALTER TABLE nodes DROP COLUMN shared;
ALTER TABLE nodes DROP COLUMN owner_user_id;
ALTER TABLE skills DROP COLUMN personal;
ALTER TABLE skills DROP COLUMN owner_user_id;
ALTER TABLE mcp_servers DROP COLUMN created_by;
ALTER TABLE ideas DROP COLUMN created_by;
ALTER TABLE schedules DROP COLUMN created_by;
DROP INDEX tasks_created_by;
ALTER TABLE tasks DROP COLUMN created_by;
ALTER TABLE auth_sessions DROP COLUMN user_id;
DELETE FROM settings WHERE key IN ('team');
-- needs_key exists only with team mode; such tasks go back to failed.
UPDATE tasks SET status = 'failed' WHERE status = 'needs_key';
DROP TABLE telegram_link_codes;
DROP TABLE user_credentials;
DROP TABLE audit_log;
DROP TABLE invites;
DROP TABLE users;
