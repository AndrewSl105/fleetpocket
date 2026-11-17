-- +goose Up
-- Local repositories (MODE=local, docs/SPEC-server.md "Repository sources"):
-- a folder on the owner's disk instead of a GitHub repository. `path` is the
-- absolute work tree, `remote_url` the origin remote when there is one
-- (informational; "Push & open PR" uses it). GitHub repositories keep
-- source 'github' and an empty path.
ALTER TABLE repositories ADD COLUMN source text NOT NULL DEFAULT 'github';
ALTER TABLE repositories ADD COLUMN path text NOT NULL DEFAULT '';
ALTER TABLE repositories ADD COLUMN remote_url text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX repositories_local_path ON repositories (project_id, path) WHERE source = 'local';

-- The branch delivered into a local repository (jm/<id>-<slug>[-n]) and
-- where the workspace started: the last commit, or the working tree with
-- its uncommitted changes.
ALTER TABLE tasks ADD COLUMN local_branch text;
ALTER TABLE tasks ADD COLUMN start_from text NOT NULL DEFAULT 'last_commit';

-- +goose Down
ALTER TABLE tasks DROP COLUMN start_from;
ALTER TABLE tasks DROP COLUMN local_branch;
DROP INDEX repositories_local_path;
ALTER TABLE repositories DROP COLUMN remote_url;
ALTER TABLE repositories DROP COLUMN path;
ALTER TABLE repositories DROP COLUMN source;
