-- +goose Up
-- Subagents of the lead agent (Claude only, docs/SPEC-server.md "Subagents"):
-- on by default; off sends no subagents in the run command.
ALTER TABLE projects ADD COLUMN subagents boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE projects DROP COLUMN subagents;
