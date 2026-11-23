ALTER TABLE jobs ADD COLUMN runner_protocol_version INTEGER;
             ALTER TABLE jobs ADD COLUMN asr_backend TEXT NOT NULL DEFAULT 'unknown';
             ALTER TABLE jobs ADD COLUMN diarization_status TEXT NOT NULL DEFAULT 'pending';
             ALTER TABLE jobs ADD COLUMN warnings_json TEXT NOT NULL DEFAULT '[]';
             UPDATE jobs
             SET diarization_status = CASE
               WHEN enable_speaker = 0 THEN 'disabled'
               WHEN overall_status = 'failed' OR asr_status = 'failed' THEN 'failed'
               WHEN overall_status = 'completed' OR asr_status = 'completed'
                 THEN 'legacy_unverified'
               ELSE 'pending'
             END;
