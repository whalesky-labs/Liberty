UPDATE app_settings
             SET python_runtime_source = CASE
                   WHEN TRIM(python_path) <> '' THEN 'system'
                   ELSE 'managed'
                 END,
                 ffmpeg_runtime_source = CASE
                   WHEN TRIM(ffmpeg_path) <> '' THEN 'system'
                   ELSE 'managed'
                 END
             WHERE id = 1

-- next block

INSERT INTO app_meta(key, value)
             VALUES('runtime_sources_migrated', '2026-07-15')
