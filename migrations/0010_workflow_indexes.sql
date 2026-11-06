-- +goose Up
-- Retention cleanup scans by created_at independently of run status.
CREATE INDEX IF NOT EXISTS idx_workflow_runs_created ON workflow_runs(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_workflow_runs_created;
