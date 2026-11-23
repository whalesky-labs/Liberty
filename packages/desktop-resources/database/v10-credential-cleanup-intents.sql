CREATE TABLE IF NOT EXISTS credential_cleanup_intents (
               id TEXT PRIMARY KEY,
               model_id TEXT NOT NULL,
               credential_reference TEXT NOT NULL UNIQUE,
               operation TEXT NOT NULL CHECK (operation IN ('retire', 'rollback')),
               attempt INTEGER NOT NULL DEFAULT 0,
               last_error TEXT,
               created_at TEXT NOT NULL,
               updated_at TEXT NOT NULL
             );
             CREATE INDEX IF NOT EXISTS idx_credential_cleanup_intents_model
               ON credential_cleanup_intents(model_id, created_at);
