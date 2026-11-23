CREATE TABLE IF NOT EXISTS job_deletion_ops (
               operation_id TEXT PRIMARY KEY,
               job_id TEXT NOT NULL UNIQUE,
               trash_name TEXT NOT NULL UNIQUE,
               phase TEXT NOT NULL CHECK (
                 phase IN ('prepared', 'fenced', 'trashed', 'database_deleted')
               ),
               runner_pid INTEGER,
               runner_process_identity TEXT,
               created_at_ms INTEGER NOT NULL,
               updated_at_ms INTEGER NOT NULL,
               last_error TEXT
             );
             CREATE INDEX IF NOT EXISTS idx_job_deletion_ops_phase
               ON job_deletion_ops(phase, created_at_ms);
