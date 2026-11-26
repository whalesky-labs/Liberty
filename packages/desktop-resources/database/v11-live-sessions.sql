CREATE TABLE live_sessions (
    id TEXT PRIMARY KEY NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('preparing','listening','paused','stopping','completed','failed','interrupted')),
    source_language TEXT NOT NULL CHECK (source_language IN ('zh','en')),
    target_language TEXT NOT NULL CHECK (target_language IN ('zh','en')),
    translate_enabled INTEGER NOT NULL CHECK (translate_enabled IN (0,1)),
    model_config_id TEXT NOT NULL DEFAULT '',
    glossary TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE live_segments (
    session_id TEXT NOT NULL REFERENCES live_sessions(id) ON DELETE CASCADE,
    segment_id INTEGER NOT NULL CHECK (segment_id > 0),
    revision INTEGER NOT NULL CHECK (revision > 0),
    start_ms INTEGER NOT NULL CHECK (start_ms >= 0),
    end_ms INTEGER NOT NULL CHECK (end_ms >= start_ms),
    original_text TEXT NOT NULL,
    translated_text TEXT NOT NULL DEFAULT '',
    translation_state TEXT NOT NULL CHECK (translation_state IN ('disabled','pending','completed','failed')),
    translation_ms INTEGER NOT NULL DEFAULT 0 CHECK (translation_ms >= 0),
    PRIMARY KEY (session_id, segment_id)
);
CREATE TABLE live_session_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES live_sessions(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX live_sessions_created ON live_sessions(created_at DESC, id DESC);
CREATE INDEX live_session_events_session ON live_session_events(session_id, id);
