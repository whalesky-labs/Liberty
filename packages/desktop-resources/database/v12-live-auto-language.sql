ALTER TABLE live_sessions ADD COLUMN auto_detect_language INTEGER NOT NULL DEFAULT 0 CHECK (auto_detect_language IN (0,1));
