CREATE TABLE meeting_drafts (
    job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL CHECK (revision > 0),
    summary_json TEXT NOT NULL,
    source_run_id TEXT NOT NULL DEFAULT '',
    transcript_sha256 TEXT NOT NULL,
	 sources_json TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL
);
