-- +goose Up
-- Durable workflow definitions and runs. Definitions are versioned snapshots on
-- each run so editing a workflow never changes an in-flight execution.
CREATE TABLE workflows (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  definition_json TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  trigger_type TEXT NOT NULL DEFAULT 'manual' CHECK (trigger_type IN ('manual','schedule')),
  schedule TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE workflow_runs (
  id TEXT PRIMARY KEY,
  workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  status TEXT NOT NULL CHECK (status IN ('queued','running','succeeded','failed','partial','canceled')),
  trigger_type TEXT NOT NULL,
  definition_json TEXT NOT NULL,
  owner TEXT,
  error TEXT,
  started_at INTEGER,
  finished_at INTEGER,
  heartbeat_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_workflow_runs_status ON workflow_runs(status, created_at);
CREATE INDEX idx_workflow_runs_workflow ON workflow_runs(workflow_id, created_at DESC);
CREATE UNIQUE INDEX idx_workflow_runs_active ON workflow_runs(workflow_id)
  WHERE status IN ('queued','running');

CREATE TABLE workflow_step_runs (
  id TEXT PRIMARY KEY,
  run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
  step_id TEXT NOT NULL,
  type TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed','skipped','canceled')),
  attempts INTEGER NOT NULL DEFAULT 0,
  job_ids_json TEXT NOT NULL DEFAULT '[]',
  output_json TEXT NOT NULL DEFAULT '{}',
  error TEXT,
  started_at INTEGER,
  finished_at INTEGER,
  created_at INTEGER NOT NULL,
  UNIQUE (run_id, step_id)
);
CREATE INDEX idx_workflow_steps_run ON workflow_step_runs(run_id, step_id);

-- +goose Down
DROP TABLE IF EXISTS workflow_step_runs;
DROP TABLE IF EXISTS workflow_runs;
DROP TABLE IF EXISTS workflows;
