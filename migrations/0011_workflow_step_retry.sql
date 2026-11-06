-- +goose Up
-- Steps that fail with a transient error are put back into pending state with
-- a backoff, instead of failing the whole workflow immediately.
ALTER TABLE workflow_step_runs ADD COLUMN available_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE workflow_step_runs DROP COLUMN available_at;
