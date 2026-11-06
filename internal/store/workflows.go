package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ListWorkflows returns all workflow definitions newest first.
func (s *Store) ListWorkflows(ctx context.Context) ([]Workflow, error) {
	rows, err := s.db.QueryContext(ctx, workflowCols+` ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workflow{}
	for rows.Next() {
		w, err := s.scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// GetWorkflow returns one workflow definition.
func (s *Store) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
	return s.scanWorkflow(s.db.QueryRowContext(ctx, workflowCols+` WHERE id=?`, id))
}

// CreateWorkflow inserts a workflow definition.
func (s *Store) CreateWorkflow(ctx context.Context, w *Workflow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO workflows
		(id,name,description,definition_json,enabled,trigger_type,schedule,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		w.ID, w.Name, w.Description, w.DefinitionJSON, boolInt(w.Enabled), w.TriggerType, w.Schedule, w.CreatedAt, w.UpdatedAt)
	return err
}

// UpdateWorkflow updates a workflow definition.
func (s *Store) UpdateWorkflow(ctx context.Context, w *Workflow) error {
	res, err := s.db.ExecContext(ctx, `UPDATE workflows SET name=?,description=?,definition_json=?,enabled=?,trigger_type=?,schedule=?,updated_at=? WHERE id=?`,
		w.Name, w.Description, w.DefinitionJSON, boolInt(w.Enabled), w.TriggerType, w.Schedule, w.UpdatedAt, w.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWorkflow removes a definition and its runs (FK cascade).
func (s *Store) DeleteWorkflow(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM workflows WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListWorkflowRuns returns recent runs, optionally filtered by workflow.
func (s *Store) ListWorkflowRuns(ctx context.Context, workflowID string, limit int) ([]WorkflowRun, error) {
	q := workflowRunCols + ` `
	var args []any
	if workflowID != "" {
		q += `WHERE workflow_id=? `
		args = append(args, workflowID)
	}
	q += `ORDER BY created_at DESC, rowid DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkflowRun{}
	for rows.Next() {
		r, err := s.scanWorkflowRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListRunningWorkflowRuns returns runs that still need the runner.
func (s *Store) ListRunningWorkflowRuns(ctx context.Context, owner string) ([]WorkflowRun, error) {
	rows, err := s.db.QueryContext(ctx, workflowRunCols+` WHERE status='running' AND owner=? ORDER BY created_at ASC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkflowRun{}
	for rows.Next() {
		r, err := s.scanWorkflowRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetWorkflowRun returns one run.
func (s *Store) GetWorkflowRun(ctx context.Context, id string) (*WorkflowRun, error) {
	return s.scanWorkflowRun(s.db.QueryRowContext(ctx, workflowRunCols+` WHERE id=?`, id))
}

// CreateWorkflowRun inserts a queued run. Only one queued/running run per
// workflow is allowed.
func (s *Store) CreateWorkflowRun(ctx context.Context, r *WorkflowRun) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO workflow_runs
		(id,workflow_id,status,trigger_type,definition_json,owner,error,started_at,finished_at,heartbeat_at,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.WorkflowID, r.Status, r.TriggerType, r.DefinitionJSON, r.Owner, r.Error, r.StartedAt, r.FinishedAt, r.HeartbeatAt, r.CreatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrConflict
	}
	return err
}

// ClaimNextWorkflowRun atomically claims the oldest queued run.
func (s *Store) ClaimNextWorkflowRun(ctx context.Context, now int64, owner string) (*WorkflowRun, error) {
	r, err := s.scanWorkflowRun(s.db.QueryRowContext(ctx, `UPDATE workflow_runs
		SET status='running', started_at=?, heartbeat_at=?, owner=?
		WHERE id = (SELECT id FROM workflow_runs WHERE status='queued' ORDER BY created_at ASC LIMIT 1)
		RETURNING id,workflow_id,status,trigger_type,definition_json,owner,error,started_at,finished_at,heartbeat_at,created_at`, now, now, owner))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return r, err
}

// HeartbeatWorkflowRun marks a running workflow as alive.
func (s *Store) HeartbeatWorkflowRun(ctx context.Context, id string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE workflow_runs SET heartbeat_at=? WHERE id=? AND status='running'`, now, id)
	return err
}

// FinishWorkflowRun stores a terminal workflow state.
func (s *Store) FinishWorkflowRun(ctx context.Context, id, status string, errMsg *string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE workflow_runs SET status=?,error=?,finished_at=?,heartbeat_at=? WHERE id=? AND status='running'`, status, errMsg, now, now, id)
	return err
}

// RecoverWorkflowRuns makes interrupted runs resumable after a restart. Step
// runs with job IDs are rechecked before any new job is enqueued.
func (s *Store) RecoverWorkflowRuns(ctx context.Context, before int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Only steal runs whose owner stopped heartbeating. This lets serve and
	// worker share a database without resetting each other's live runs.
	if _, err := tx.ExecContext(ctx, `UPDATE workflow_step_runs SET status='pending', started_at=NULL, available_at=0
		WHERE status='running' AND run_id IN (
			SELECT id FROM workflow_runs
			WHERE status='running' AND (heartbeat_at IS NULL OR heartbeat_at < ?)
		)`, before); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET status='queued', started_at=NULL, heartbeat_at=NULL, owner=NULL
		WHERE status='running' AND (heartbeat_at IS NULL OR heartbeat_at < ?)`, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, tx.Commit()
}

// RecentWorkflowRun reports whether a workflow was triggered after since. When
// trigger is non-empty only runs with that trigger type are considered, so a
// manual run cannot suppress a scheduled one.
func (s *Store) RecentWorkflowRun(ctx context.Context, workflowID, trigger string, since int64) (bool, error) {
	q := `SELECT COUNT(*) FROM workflow_runs WHERE workflow_id=? AND created_at>=?`
	args := []any{workflowID, since}
	if trigger != "" {
		q += ` AND trigger_type=?`
		args = append(args, trigger)
	}
	var n int
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n > 0, err
}

// ActiveWorkflowRun reports whether a workflow has a queued or running run.
func (s *Store) ActiveWorkflowRun(ctx context.Context, workflowID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs
		WHERE workflow_id=? AND status IN ('queued','running')`, workflowID).Scan(&n)
	return n > 0, err
}

// LatestWorkflowRuns returns the most recent run for every workflow in one
// query. It avoids the N+1 lookups performed by listing runs per workflow.
func (s *Store) LatestWorkflowRuns(ctx context.Context) (map[string]*WorkflowRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,workflow_id,status,trigger_type,definition_json,owner,error,started_at,finished_at,heartbeat_at,created_at
		FROM (
			SELECT *, ROW_NUMBER() OVER (
				PARTITION BY workflow_id ORDER BY created_at DESC, rowid DESC
			) AS rn
			FROM workflow_runs
		) WHERE rn=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*WorkflowRun{}
	for rows.Next() {
		r, err := s.scanWorkflowRun(rows)
		if err != nil {
			return nil, err
		}
		out[r.WorkflowID] = r
	}
	return out, rows.Err()
}

// CleanupWorkflowRuns deletes terminal runs older than cutoff. Step runs are
// removed by the foreign-key cascade.
func (s *Store) CleanupWorkflowRuns(ctx context.Context, cutoff int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM workflow_runs
		WHERE status IN ('succeeded','failed','partial','canceled') AND created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CancelWorkflowRun cancels a queued/running run and its unfinished steps.
func (s *Store) CancelWorkflowRun(ctx context.Context, id string, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET status='canceled',error='canceled by administrator',finished_at=?,heartbeat_at=? WHERE id=? AND status IN ('queued','running')`, now, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workflow_step_runs SET status='canceled',finished_at=? WHERE run_id=? AND status IN ('pending','running')`, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateStepRun inserts a step run if it does not exist yet.
func (s *Store) CreateStepRun(ctx context.Context, step *WorkflowStepRun) error {
	if step.JobIDsJSON == "" {
		step.JobIDsJSON = "[]"
	}
	if step.OutputJSON == "" {
		step.OutputJSON = "{}"
	}
	if step.AvailableAt == 0 {
		step.AvailableAt = step.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO workflow_step_runs
		(id,run_id,step_id,type,name,status,attempts,job_ids_json,output_json,error,started_at,finished_at,available_at,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		step.ID, step.RunID, step.StepID, step.Type, step.Name, step.Status, step.Attempts,
		step.JobIDsJSON, step.OutputJSON, step.Error, step.StartedAt, step.FinishedAt, step.AvailableAt, step.CreatedAt)
	return err
}

// ListStepRuns returns step runs for a workflow run.
func (s *Store) ListStepRuns(ctx context.Context, runID string) ([]WorkflowStepRun, error) {
	rows, err := s.db.QueryContext(ctx, workflowStepCols+` WHERE run_id=? ORDER BY rowid ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkflowStepRun{}
	for rows.Next() {
		st, err := s.scanStepRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

// GetStepRun returns one step run by run and step ID.
func (s *Store) GetStepRun(ctx context.Context, runID, stepID string) (*WorkflowStepRun, error) {
	return s.scanStepRun(s.db.QueryRowContext(ctx, workflowStepCols+` WHERE run_id=? AND step_id=?`, runID, stepID))
}

// StartStepRun moves a pending step to running and increments attempts.
func (s *Store) StartStepRun(ctx context.Context, id string, now int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_step_runs
		SET status='running',attempts=attempts+1,started_at=?,available_at=0,error=NULL
		WHERE id=? AND status='pending' AND available_at<=?`, now, id, now)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	return nil
}

// RetryStepRun puts a running step back into pending with a backoff. The step
// keeps attempts and the last error so the UI can show why it is waiting.
func (s *Store) RetryStepRun(ctx context.Context, id string, availableAt int64, errMsg *string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE workflow_step_runs
		SET status='pending',started_at=NULL,finished_at=NULL,available_at=?,error=?
		WHERE id=? AND status='running'`, availableAt, errMsg, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	return nil
}

// SetStepJobs records the jobs that a step is waiting for.
func (s *Store) SetStepJobs(ctx context.Context, id, jobIDsJSON string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE workflow_step_runs SET job_ids_json=? WHERE id=? AND status='running'`, jobIDsJSON, id)
	return err
}

// SetStepOutput updates the step output while it is still running.
func (s *Store) SetStepOutput(ctx context.Context, id, outputJSON string) error {
	if outputJSON == "" {
		outputJSON = "{}"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE workflow_step_runs SET output_json=? WHERE id=? AND status='running'`, outputJSON, id)
	return err
}

// FinishStepRun stores a terminal step state.
func (s *Store) FinishStepRun(ctx context.Context, id, status, outputJSON string, errMsg *string, now int64) error {
	if outputJSON == "" {
		outputJSON = "{}"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE workflow_step_runs SET status=?,output_json=?,error=?,finished_at=?,available_at=0 WHERE id=? AND status IN ('pending','running')`, status, outputJSON, errMsg, now, id)
	return err
}

const workflowCols = `SELECT id,name,description,definition_json,enabled,trigger_type,schedule,created_at,updated_at FROM workflows`

func (s *Store) scanWorkflow(row interface{ Scan(...any) error }) (*Workflow, error) {
	var w Workflow
	var enabled int
	if err := row.Scan(&w.ID, &w.Name, &w.Description, &w.DefinitionJSON, &enabled, &w.TriggerType, &w.Schedule, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	w.Enabled = enabled != 0
	return &w, nil
}

const workflowRunCols = `SELECT id,workflow_id,status,trigger_type,definition_json,owner,error,started_at,finished_at,heartbeat_at,created_at FROM workflow_runs`

func (s *Store) scanWorkflowRun(row interface{ Scan(...any) error }) (*WorkflowRun, error) {
	var r WorkflowRun
	var errMsg sql.NullString
	var started, finished, heartbeat sql.NullInt64
	var owner sql.NullString
	if err := row.Scan(&r.ID, &r.WorkflowID, &r.Status, &r.TriggerType, &r.DefinitionJSON, &owner, &errMsg, &started, &finished, &heartbeat, &r.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	r.Owner = owner.String
	r.Error = nullPtr(errMsg)
	if started.Valid {
		r.StartedAt = &started.Int64
	}
	if finished.Valid {
		r.FinishedAt = &finished.Int64
	}
	if heartbeat.Valid {
		r.HeartbeatAt = &heartbeat.Int64
	}
	return &r, nil
}

const workflowStepCols = `SELECT id,run_id,step_id,type,name,status,attempts,job_ids_json,output_json,error,started_at,finished_at,available_at,created_at FROM workflow_step_runs`

func (s *Store) scanStepRun(row interface{ Scan(...any) error }) (*WorkflowStepRun, error) {
	var st WorkflowStepRun
	var errMsg sql.NullString
	var started, finished sql.NullInt64
	if err := row.Scan(&st.ID, &st.RunID, &st.StepID, &st.Type, &st.Name, &st.Status, &st.Attempts, &st.JobIDsJSON, &st.OutputJSON, &errMsg, &started, &finished, &st.AvailableAt, &st.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	st.Error = nullPtr(errMsg)
	if started.Valid {
		st.StartedAt = &started.Int64
	}
	if finished.Valid {
		st.FinishedAt = &finished.Int64
	}
	return &st, nil
}
