-- +goose Up
-- CI auto-fix (docs/SPEC-server.md section "CI auto-fix"). Everything is
-- nullable: NULL is "off" / "nothing happened", today's behaviour.

-- A project's overrides of the global default (settings key ci_autofix);
-- NULL fields inherit it.
ALTER TABLE projects ADD COLUMN ci_autofix jsonb;

-- The last commit Jungle Monkey pushed to the task branch (a failing check on
-- any other commit is a human push), and the state of the auto-fix loop.
ALTER TABLE tasks ADD COLUMN last_pushed_sha text;
ALTER TABLE tasks ADD COLUMN autofix_state text;
ALTER TABLE tasks ADD COLUMN autofix_attempts int;
ALTER TABLE tasks ADD COLUMN autofix_max_attempts int;
ALTER TABLE tasks ADD COLUMN autofix_signature text;
-- The head commit whose failure was handled, so the check_suite and
-- workflow_run deliveries of one failure start one attempt.
ALTER TABLE tasks ADD COLUMN autofix_handled_sha text;

-- A run started by auto-fix: its attempt number and its own budget.
ALTER TABLE runs ADD COLUMN autofix_attempt int;
ALTER TABLE runs ADD COLUMN max_budget_usd numeric(8,2);

-- +goose Down
ALTER TABLE runs DROP COLUMN max_budget_usd;
ALTER TABLE runs DROP COLUMN autofix_attempt;
ALTER TABLE tasks DROP COLUMN autofix_handled_sha;
ALTER TABLE tasks DROP COLUMN autofix_signature;
ALTER TABLE tasks DROP COLUMN autofix_max_attempts;
ALTER TABLE tasks DROP COLUMN autofix_attempts;
ALTER TABLE tasks DROP COLUMN autofix_state;
ALTER TABLE tasks DROP COLUMN last_pushed_sha;
ALTER TABLE projects DROP COLUMN ci_autofix;
