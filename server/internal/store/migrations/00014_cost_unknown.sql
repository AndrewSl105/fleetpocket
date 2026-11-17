-- +goose Up
-- Some run of the task reported tokens but its model has no price
-- (cost_source unknown), so its cost reads $? or $0.84+? instead of $0.00.
-- Its own migration: 00013 was already released when this was added.
ALTER TABLE tasks ADD COLUMN cost_unknown boolean;
-- Tasks that ran on the 00013 release before this column existed.
UPDATE tasks SET cost_unknown = true
 WHERE EXISTS (SELECT 1 FROM runs r WHERE r.task_id = tasks.id AND r.cost_source = 'unknown');

-- +goose Down
ALTER TABLE tasks DROP COLUMN cost_unknown;
