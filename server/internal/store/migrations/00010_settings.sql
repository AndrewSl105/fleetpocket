-- +goose Up
-- Settings written by the first-run setup wizard (owner login, GitHub App).
-- Environment variables, when set, win over these rows.
CREATE TABLE settings (
  key         text PRIMARY KEY,
  value       text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE settings;
