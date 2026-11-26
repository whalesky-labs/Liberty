package legacydb

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestPagedQueueMatchesFullProjection(t *testing.T) {
	ctx := context.Background()
	path := fixture(t, SchemaVersion)
	uri := fixtureURI(path)
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statuses := []string{"completed", "queued", "transcribing", "speaker_processing", "summarizing", "failed", "unknown"}
	for i := range 37 {
		id := fmt.Sprintf("synthetic-%02d", i)
		_, err := db.Exec(`INSERT INTO jobs(id,title,created_at,overall_status,diarization_status,duration_minutes) VALUES(?, ?, ?, ?, 'disabled', 1)`, id, fmt.Sprintf("ÉCOLE 会议 %d%%_", i), fmt.Sprintf("2026-11-%02d", 1+i/2), statuses[i%len(statuses)])
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`INSERT INTO job_source_files VALUES(?, ?, 'ÜBUNG.wav', 'audio', '1 MB')`, id, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	jobs, err := r.ListJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"local", "remote", ""} {
		for _, filter := range []string{"all", "processing", "completed", "failed", "unknown"} {
			for _, search := range []string{"", " école ", "übung", "会议", "%_", "' OR 1=1 --", "missing"} {
				for _, n := range []int{-1, 1, 2, 4, 99} {
					q := domain.QueueQuery{Source: source, Filter: filter, Search: search, Page: n}
					got, err := r.QueryJobs(ctx, q)
					want := domain.QueryJobs(jobs, q)
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Fatalf("query %+v differs: got %+v want %+v err %v", q, got, want, err)
					}
				}
			}
		}
	}
}

func TestReadRevisionTracksCommitsAndConnectionReplacement(t *testing.T) {
	ctx := context.Background()
	path := fixture(t, SchemaVersion)
	r, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	uri := fixtureURI(path)
	writer, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	read := func() uint64 {
		t.Helper()
		revision, err := r.ReadRevision(ctx)
		if err != nil || revision == 0 {
			t.Fatal("missing revision", err)
		}
		return revision
	}
	before := read()
	if read() != before {
		t.Fatal("idle read advanced revision")
	}
	for _, statement := range []string{
		`UPDATE transcript_segments SET speaker='changed' WHERE job_id='visible'`,
		`UPDATE jobs SET summary_status='completed' WHERE id='visible'`,
		`UPDATE job_source_files SET name='renamed.wav' WHERE job_id='visible'`,
		`INSERT INTO job_deletion_ops VALUES ('visible')`,
	} {
		if _, err := writer.Exec(statement); err != nil {
			t.Fatal(err)
		}
		after := read()
		if after <= before {
			t.Fatal("committed change was not observed")
		}
		before = after
	}
	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE jobs SET title='rolled back'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if read() != before {
		t.Fatal("rollback invalidated snapshot")
	}
	r.db.SetMaxIdleConns(0)
	r.db.SetMaxIdleConns(1)
	if read() <= before {
		t.Fatal("replacement connection reused an old revision")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadRevision(ctx); err == nil {
		t.Fatal("closed reader returned a valid revision")
	}
}
