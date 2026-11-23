ALTER TABLE jobs ADD COLUMN active_summary_run_id TEXT

-- next block

ALTER TABLE jobs ADD COLUMN processing_started_at_ms INTEGER

-- next block

ALTER TABLE jobs ADD COLUMN processing_finished_at_ms INTEGER

-- next block

ALTER TABLE jobs ADD COLUMN processing_duration_seconds INTEGER

-- next block

ALTER TABLE app_settings ADD COLUMN local_asr_device TEXT NOT NULL DEFAULT 'auto'

-- next block

ALTER TABLE app_settings ADD COLUMN local_asr_threads INTEGER NOT NULL DEFAULT 0

-- next block

ALTER TABLE app_settings ADD COLUMN local_asr_batch_size_seconds INTEGER NOT NULL DEFAULT 300

-- next block

ALTER TABLE app_settings ADD COLUMN runtime_download_source TEXT NOT NULL DEFAULT ''

-- next block

ALTER TABLE app_settings ADD COLUMN ffmpeg_path TEXT NOT NULL DEFAULT ''

-- next block

ALTER TABLE app_settings ADD COLUMN python_runtime_source TEXT NOT NULL DEFAULT 'managed'

-- next block

ALTER TABLE app_settings ADD COLUMN ffmpeg_runtime_source TEXT NOT NULL DEFAULT 'managed'

-- next block

ALTER TABLE app_settings ADD COLUMN api_token_ref TEXT NOT NULL DEFAULT ''

-- next block

ALTER TABLE app_settings ADD COLUMN processing_mode TEXT NOT NULL DEFAULT 'local'

-- next block

ALTER TABLE ai_model_configs ADD COLUMN api_key_ref TEXT NOT NULL DEFAULT ''

-- next block

ALTER TABLE ai_summary_runs ADD COLUMN minutes_payload_json TEXT

-- next block

UPDATE app_settings
             SET processing_mode = CASE
                   WHEN TRIM(backend_url) <> '' THEN 'remote'
                   ELSE 'local'
                 END
             WHERE id = 1
