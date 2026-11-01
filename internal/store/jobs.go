package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// CreateJob inserts a queued job. If the idempotency key already exists the
// existing job is returned with created=false.
func (s *Store) CreateJob(ctx context.Context, j *Job) (bool, error) {
	if j.PayloadJSON == "" {
		j.PayloadJSON = "{}"
	}
	if j.AvailableAt == 0 {
		j.AvailableAt = j.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs
		(id,type,certificate_id,ca_account_id,status,attempts,max_attempts,payload_json,idempotency_key,error_code,error_message,log_path,created_at,started_at,finished_at,heartbeat_at,available_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.Type, j.CertificateID, j.CAAccountID, j.Status, j.Attempts, j.MaxAttempts, j.PayloadJSON, j.IdempotencyKey, j.ErrorCode, j.ErrorMessage, j.LogPath, j.CreatedAt, j.StartedAt, j.FinishedAt, j.HeartbeatAt, j.AvailableAt)
	if err == nil {
		return true, nil
	}
	if j.IdempotencyKey == nil {
		return false, err
	}
	existing, getErr := s.GetJobByIdempotencyKey(ctx, *j.IdempotencyKey)
	if getErr != nil {
		return false, fmt.Errorf("create job: %w", err)
	}
	*j = *existing
	return false, nil
}

// GetJob returns a job by id.
func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	return s.scanJob(s.db.QueryRowContext(ctx, jobCols+` WHERE id=?`, id))
}

// GetJobByIdempotencyKey finds a job by its idempotency key.
func (s *Store) GetJobByIdempotencyKey(ctx context.Context, key string) (*Job, error) {
	return s.scanJob(s.db.QueryRowContext(ctx, jobCols+` WHERE idempotency_key=?`, key))
}

// ListJobs returns jobs filtered by certificate and/or status.
func (s *Store) ListJobs(ctx context.Context, certID, status string, limit int) ([]Job, error) {
	q := jobCols + ` WHERE 1=1`
	var args []any
	if certID != "" {
		q += ` AND certificate_id=?`
		args = append(args, certID)
	}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := s.scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// ClaimNext atomically claims the oldest available queued job.
func (s *Store) ClaimNext(ctx context.Context, now int64) (*Job, error) {
	j, err := s.scanJob(s.db.QueryRowContext(ctx, `UPDATE jobs SET status='running', started_at=?, heartbeat_at=?, attempts=attempts+1
		WHERE id = (SELECT id FROM jobs WHERE status='queued' AND available_at<=? ORDER BY created_at ASC LIMIT 1)
		RETURNING id,type,certificate_id,ca_account_id,status,attempts,max_attempts,payload_json,idempotency_key,error_code,error_message,log_path,created_at,started_at,finished_at,heartbeat_at,available_at`, now, now, now))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return j, err
}

// FindActiveJob returns a queued/running job for the same certificate and type.
func (s *Store) FindActiveJob(ctx context.Context, jobType, certID string) (*Job, error) {
	return s.scanJob(s.db.QueryRowContext(ctx, jobCols+` WHERE type=? AND certificate_id=? AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, jobType, certID))
}

// FindActiveJobForCA returns a queued/running job for a CA account.
func (s *Store) FindActiveJobForCA(ctx context.Context, jobType, caID string) (*Job, error) {
	return s.scanJob(s.db.QueryRowContext(ctx, jobCols+` WHERE type=? AND ca_account_id=? AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, jobType, caID))
}

// SetJobLogPath records where the job log lives.
func (s *Store) SetJobLogPath(ctx context.Context, id, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET log_path=? WHERE id=?`, path, id)
	return err
}

// Heartbeat marks a running job as alive.
func (s *Store) Heartbeat(ctx context.Context, id string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET heartbeat_at=? WHERE id=? AND status='running'`, now, id)
	return err
}

// FinishJob stores a terminal state.
func (s *Store) FinishJob(ctx context.Context, id, status string, code, msg *string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?,error_code=?,error_message=?,finished_at=?,heartbeat_at=? WHERE id=?`, status, code, msg, now, now, id)
	return err
}

// RequeueJob schedules a retry with a backoff delay.
func (s *Store) RequeueJob(ctx context.Context, id string, availableAt int64, code, msg *string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='queued',available_at=?,error_code=?,error_message=?,started_at=?,finished_at=NULL,heartbeat_at=NULL WHERE id=?`, availableAt, code, msg, now, id)
	return err
}

// InterruptRunning marks every running job as interrupted after a crash.
func (s *Store) InterruptRunning(ctx context.Context, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='interrupted',error_code='E_INTERRUPTED',error_message='process restarted',finished_at=? WHERE status='running'`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RecentJobs returns the latest jobs for the dashboard.
func (s *Store) RecentJobs(ctx context.Context, limit int) ([]Job, error) {
	return s.ListJobs(ctx, "", "", limit)
}

// PurgeOldJobs removes job rows older than the cutoff. Jobs are kept for audit
// by default; this is only used by explicit maintenance commands.
func (s *Store) PurgeOldJobs(ctx context.Context, before int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE created_at < ? AND status NOT IN ('queued','running')`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const jobCols = `SELECT id,type,certificate_id,ca_account_id,status,attempts,max_attempts,payload_json,idempotency_key,error_code,error_message,log_path,created_at,started_at,finished_at,heartbeat_at,available_at FROM jobs`

func (s *Store) scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var certID, caID, key, code, msg, logPath sql.NullString
	var started, finished, heartbeat sql.NullInt64
	if err := row.Scan(&j.ID, &j.Type, &certID, &caID, &j.Status, &j.Attempts, &j.MaxAttempts, &j.PayloadJSON, &key, &code, &msg, &logPath, &j.CreatedAt, &started, &finished, &heartbeat, &j.AvailableAt); err != nil {
		return nil, notFound(err)
	}
	j.CertificateID, j.CAAccountID = nullPtr(certID), nullPtr(caID)
	j.IdempotencyKey, j.ErrorCode, j.ErrorMessage, j.LogPath = nullPtr(key), nullPtr(code), nullPtr(msg), nullPtr(logPath)
	if started.Valid {
		j.StartedAt = &started.Int64
	}
	if finished.Valid {
		j.FinishedAt = &finished.Int64
	}
	if heartbeat.Valid {
		j.HeartbeatAt = &heartbeat.Int64
	}
	return &j, nil
}

// JobStatuses summarizes job counts.
func (s *Store) JobStatuses(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// NormalizeStatuses is used by tests and the doctor command.
func NormalizeStatuses(list []string) string { return strings.Join(list, ",") }
