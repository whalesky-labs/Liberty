package schema

import (
	"context"
	"reflect"
	"testing"
)

func TestMeetingDraftMigrationFromV12RollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `DROP TABLE meeting_drafts; UPDATE app_meta SET value='12' WHERE key='schema_version'; CREATE TABLE meeting_drafts(collision TEXT); INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('retained','Synthetic retained member','now','now')`)
	if _, err := Apply(ctx, db, path, nil); err == nil {
		t.Fatal("collision accepted")
	}
	if scalar(t, db, `SELECT value FROM app_meta WHERE key='schema_version'`) != "12" {
		t.Fatal("failed migration advanced version")
	}
	exec(t, db, `DROP TABLE meeting_drafts`)
	result, err := Apply(ctx, db, path, nil)
	if err != nil || result.FromVersion != 12 || result.Version != CurrentVersion || result.Backup == "" {
		t.Fatal(result, err)
	}
	if scalar(t, db, `SELECT name FROM meeting_members WHERE id='retained'`) != "Synthetic retained member" {
		t.Fatal("existing data changed")
	}
	before := snapshot(t, db)
	if _, err := Apply(ctx, db, path, nil); err != nil || !reflect.DeepEqual(before, snapshot(t, db)) {
		t.Fatal("repeat startup changed data", err)
	}
}
