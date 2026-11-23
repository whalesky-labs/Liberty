CREATE TABLE IF NOT EXISTS ai_summary_runs (
               id TEXT PRIMARY KEY,
               job_id TEXT NOT NULL,
               model_config_id TEXT,
               template_id TEXT,
               include_speaker INTEGER NOT NULL DEFAULT 1,
               include_timestamp INTEGER NOT NULL DEFAULT 1,
               extra_instructions TEXT NOT NULL DEFAULT '',
               status TEXT NOT NULL,
               error_message TEXT,
               prompt_preview TEXT,
               raw_response TEXT,
               result_json TEXT,
               minutes_payload_json TEXT,
               created_at TEXT NOT NULL,
               updated_at TEXT NOT NULL,
               FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE
             );

-- next block

ALTER TABLE ai_summary_runs ADD COLUMN transcript_revision TEXT NOT NULL DEFAULT ''

-- next block

ALTER TABLE ai_summary_runs ADD COLUMN transcript_sha256 TEXT NOT NULL DEFAULT ''

-- next block

ALTER TABLE ai_summary_runs ADD COLUMN transcript_snapshot_json TEXT NOT NULL DEFAULT ''

-- next block

ALTER TABLE ai_summary_runs ADD COLUMN execution_snapshot_json TEXT NOT NULL DEFAULT ''

-- next block

ALTER TABLE ai_summary_runs ADD COLUMN chunk_count INTEGER NOT NULL DEFAULT 0 CHECK (chunk_count >= 0)

-- next block

ALTER TABLE ai_summary_runs ADD COLUMN attempt_id INTEGER NOT NULL DEFAULT 0 CHECK (attempt_id >= 0)

-- next block

ALTER TABLE ai_summary_runs ADD COLUMN lease_token INTEGER NOT NULL DEFAULT 0 CHECK (lease_token >= 0)

-- next block

ALTER TABLE ai_summary_runs ADD COLUMN diagnostics_json TEXT

-- next block

ALTER TABLE ai_summary_runs ADD COLUMN completed_at TEXT

-- next block

CREATE TABLE IF NOT EXISTS ai_summary_run_chunks (
               run_id TEXT NOT NULL,
               chunk_index INTEGER NOT NULL CHECK (chunk_index >= 0),
               chunk_sha256 TEXT NOT NULL,
               user_prompt TEXT NOT NULL,
               status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed')),
               raw_response TEXT,
               structured_result_json TEXT,
               attempt_id INTEGER NOT NULL DEFAULT 0 CHECK (attempt_id >= 0),
               lease_token INTEGER NOT NULL DEFAULT 0 CHECK (lease_token >= 0),
               created_at TEXT NOT NULL,
               updated_at TEXT NOT NULL,
               completed_at TEXT,
               PRIMARY KEY(run_id, chunk_index),
               FOREIGN KEY(run_id) REFERENCES ai_summary_runs(id) ON DELETE CASCADE
             );
             CREATE INDEX IF NOT EXISTS idx_ai_summary_runs_completed
               ON ai_summary_runs(job_id, status, completed_at DESC, updated_at DESC, id DESC);
             CREATE INDEX IF NOT EXISTS idx_ai_summary_run_chunks_pending
               ON ai_summary_run_chunks(run_id, status, chunk_index);

-- next block

UPDATE ai_summary_runs
             SET completed_at = COALESCE(completed_at, updated_at)
             WHERE status = 'completed' AND completed_at IS NULL

-- next block

UPDATE ai_summary_runs
             SET status = 'failed',
                 error_message = COALESCE(
                   error_message,
                   '旧版本未完成的 AI 总结无法恢复，请重新生成。'
                 )
             WHERE status = 'running' AND execution_snapshot_json = ''

-- next block

UPDATE ai_summary_runs AS stale
             SET status = 'failed',
                 error_message = '检测到同一任务存在重复运行，已由较新的运行接管。'
             WHERE stale.status = 'running'
               AND EXISTS (
                 SELECT 1 FROM ai_summary_runs AS newer
                 WHERE newer.job_id = stale.job_id
                   AND newer.status = 'running'
                   AND (
                     newer.updated_at > stale.updated_at
                     OR (newer.updated_at = stale.updated_at AND newer.id > stale.id)
                   )
               )

-- next block

CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_summary_runs_one_running_per_job
               ON ai_summary_runs(job_id) WHERE status = 'running';

-- next block

UPDATE jobs
             SET summary_status = CASE
               WHEN EXISTS (
                 SELECT 1 FROM ai_summary_runs
                 WHERE job_id = jobs.id AND status = 'running'
               ) THEN 'summarizing'
               WHEN EXISTS (
                 SELECT 1 FROM ai_summary_runs
                 WHERE job_id = jobs.id AND status = 'completed' AND result_json IS NOT NULL
               ) THEN 'completed'
               WHEN EXISTS (
                 SELECT 1 FROM ai_summary_runs
                 WHERE job_id = jobs.id AND status = 'failed'
               ) THEN 'failed'
               ELSE 'idle'
             END
             WHERE EXISTS (
               SELECT 1 FROM ai_summary_runs WHERE job_id = jobs.id
             )
