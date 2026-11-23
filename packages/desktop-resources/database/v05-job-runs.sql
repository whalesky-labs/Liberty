CREATE TABLE IF NOT EXISTS job_runs (
               job_id TEXT PRIMARY KEY,
               attempt_id INTEGER NOT NULL DEFAULT 0 CHECK (attempt_id >= 0),
               lease_token INTEGER NOT NULL DEFAULT 0 CHECK (lease_token >= 0),
               status TEXT NOT NULL CHECK (
                 status IN ('queued', 'running', 'completed', 'failed', 'cancelled', 'fenced')
               ),
               pid INTEGER,
               process_identity TEXT,
               heartbeat_at_ms INTEGER,
               started_at_ms INTEGER,
               finished_at_ms INTEGER,
               output_dir TEXT,
               last_error TEXT,
               FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
             );
             CREATE INDEX IF NOT EXISTS idx_job_runs_status_heartbeat
               ON job_runs(status, heartbeat_at_ms);

-- next block

INSERT INTO job_runs (
               job_id, attempt_id, lease_token, status, started_at_ms, output_dir
             )
             SELECT id,
                    CASE WHEN overall_status = 'queued' THEN 0 ELSE 1 END,
                    CASE WHEN overall_status = 'queued' THEN 0 ELSE 1 END,
                    CASE WHEN overall_status = 'queued' THEN 'queued' ELSE 'running' END,
                    processing_started_at_ms,
                    CASE
                      WHEN overall_status = 'queued' THEN NULL
                      ELSE 'attempts/attempt-1-1'
                    END
             FROM jobs
             WHERE overall_status IN ('queued', 'transcribing', 'speaker_processing')
             ON CONFLICT(job_id) DO NOTHING
