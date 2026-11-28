package schema

import (
	"context"
	"reflect"
	"testing"
)

func TestLiveV11MigrationIsAtomicRetryableAndPreservesExistingData(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('synthetic','Synthetic retained member','now','now'); DROP TABLE meeting_drafts;DROP TABLE live_segments;DROP TABLE live_session_events;DROP TABLE live_sessions;UPDATE app_meta SET value='10' WHERE key='schema_version';CREATE TABLE live_segments(collision TEXT)`)
	if _, err := Apply(ctx, db, path, nil); err == nil {
		t.Fatal("injected DDL collision did not fail")
	}
	if scalar(t, db, "SELECT value FROM app_meta WHERE key='schema_version'") != "10" || scalar(t, db, "SELECT count(*) FROM sqlite_master WHERE name='live_sessions'") != "0" {
		t.Fatal("partial migration escaped rollback")
	}
	exec(t, db, "DROP TABLE live_segments")
	r, err := Apply(ctx, db, path, nil)
	if err != nil || r.FromVersion != 10 || r.Version != 13 || r.Backup == "" {
		t.Fatal("v10 retry failed", r, err)
	}
	if scalar(t, db, "SELECT name FROM meeting_members WHERE id='synthetic'") != "Synthetic retained member" {
		t.Fatal("existing member lost")
	}
	exec(t, db, `INSERT INTO live_sessions(id,state,source_language,target_language,translate_enabled,created_at,updated_at) VALUES('synthetic-live','completed','zh','en',0,'now','now')`)
	before := snapshot(t, db)
	if _, err := Apply(ctx, db, path, nil); err != nil || !reflect.DeepEqual(before, snapshot(t, db)) {
		t.Fatal("repeat startup changed session data", err)
	}
}

func TestLiveV12AutoLanguageMigrationPreservesDirectionAndRollsBack(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO live_sessions(id,state,source_language,target_language,translate_enabled,created_at,updated_at) VALUES('old-live','completed','zh','en',1,'now','now');
INSERT INTO live_segments(session_id,segment_id,revision,start_ms,end_ms,original_text,translated_text,translation_state) VALUES('old-live',1,1,0,1000,'合成测试','Synthetic test','completed');
ALTER TABLE live_sessions DROP COLUMN auto_detect_language;
DROP TABLE meeting_drafts;
UPDATE app_meta SET value='11' WHERE key='schema_version'`)
	before := snapshot(t, db)
	// A conflicting pre-existing column must fail atomically, never silently
	// accept a different schema or lose existing text during retry.
	exec(t, db, `ALTER TABLE live_sessions ADD COLUMN auto_detect_language TEXT`)
	if _, err := Apply(ctx, db, path, nil); err == nil {
		t.Fatal("DDL collision accepted")
	}
	if scalar(t, db, "SELECT value FROM app_meta WHERE key='schema_version'") != "11" {
		t.Fatal("failed migration advanced version")
	}
	exec(t, db, `ALTER TABLE live_sessions DROP COLUMN auto_detect_language`)
	if !reflect.DeepEqual(before, snapshot(t, db)) {
		t.Fatal("failed migration changed old data")
	}
	result, err := Apply(ctx, db, path, nil)
	if err != nil || result.FromVersion != 11 || result.Version != 13 || result.Backup == "" {
		t.Fatal("v11 upgrade", result, err)
	}
	if scalar(t, db, "SELECT auto_detect_language FROM live_sessions WHERE id='old-live'") != "0" || scalar(t, db, "SELECT translated_text FROM live_segments WHERE session_id='old-live'") != "Synthetic test" {
		t.Fatal("historical direction or text changed")
	}
	exec(t, db, "UPDATE live_sessions SET auto_detect_language=1 WHERE id='old-live'")
	after := snapshot(t, db)
	if _, err := Apply(ctx, db, path, nil); err != nil || !reflect.DeepEqual(after, snapshot(t, db)) {
		t.Fatal("reopen changed automatic mode", err)
	}
}
