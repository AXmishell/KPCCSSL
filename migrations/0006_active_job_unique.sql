-- +goose Up
-- Prevent duplicate active jobs for the same certificate/type when serve and
-- worker run as separate processes. Deploy jobs are intentionally excluded:
-- multiple targets can be deployed concurrently.
UPDATE jobs
SET status = 'canceled',
    finished_at = COALESCE(finished_at, strftime('%s', 'now')),
    error_code = COALESCE(error_code, 'E_DUPLICATE'),
    error_message = COALESCE(error_message, 'deduplicated while creating the active-job index')
WHERE status IN ('queued', 'running')
  AND certificate_id IS NOT NULL
  AND type <> 'deploy'
  AND id NOT IN (
    SELECT id FROM (
      SELECT id,
             ROW_NUMBER() OVER (
               PARTITION BY type, certificate_id
               ORDER BY created_at ASC, id ASC
             ) AS rn
      FROM jobs
      WHERE status IN ('queued', 'running')
        AND certificate_id IS NOT NULL
        AND type <> 'deploy'
    ) ranked
    WHERE ranked.rn = 1
  );

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_active_cert_type
  ON jobs(type, certificate_id)
  WHERE status IN ('queued', 'running')
    AND certificate_id IS NOT NULL
    AND type <> 'deploy';

-- +goose Down
DROP INDEX IF EXISTS idx_jobs_active_cert_type;
