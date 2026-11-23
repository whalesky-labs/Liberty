package legacydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestReadRowsRetainIndependentNullableAndJSONValues(test *testing.T) {
	path := fixture(test, SchemaVersion)
	uri := fixtureURI(path)
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = database.Close() })
	for index := range 3 {
		id := fmt.Sprintf("independent-%d", index)
		var reason, active any
		if index != 1 {
			reason, active = fmt.Sprintf("reason-%d", index), fmt.Sprintf("summary-%d", index)
		}
		_, err := database.Exec(`INSERT INTO jobs (id,title,created_at,overall_status,diarization_status,duration_minutes,
			processing_started_at_ms,failure_reason,active_summary_run_id,hotwords_json,warnings_json,export_formats_json)
			VALUES (?,?,?,'failed','failed',1,?,?,?,?,?,?)`, id, id, fmt.Sprintf("2026-11-%02d", 20+index),
			int64(index+1), reason, active, fmt.Sprintf(`["term-%d"]`, index),
			fmt.Sprintf(`[{"code":"warning-%d","message":"message-%d"}]`, index, index), fmt.Sprintf(`["format-%d"]`, index))
		if err != nil {
			test.Fatal(err)
		}
		if _, err := database.Exec("INSERT INTO job_source_files VALUES (?,?,?,?,?)", "source-"+id, id, "file-"+id, "audio", "1 MB"); err != nil {
			test.Fatal(err)
		}
	}
	for index, speaker := range []any{"speaker-one", "speaker-two", nil, "", "speaker-three"} {
		if _, err := database.Exec(`INSERT INTO transcript_segments VALUES (?,?, 'speaker',?,?,?,?,?)`,
			fmt.Sprintf("speaker-segment-%d", index), "independent-0", index*1000, (index+1)*1000, speaker, fmt.Sprintf("text-%d", index), index); err != nil {
			test.Fatal(err)
		}
	}
	reader, err := Open(context.Background(), path)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = reader.Close() })
	jobs, err := reader.ListJobs(context.Background())
	if err != nil || len(jobs) != 4 {
		test.Fatal("read job rows", len(jobs), err)
	}
	for position := range 3 {
		index := 2 - position
		job := jobs[position]
		id := fmt.Sprintf("independent-%d", index)
		if job.ID != id || job.ProcessingStartedAtMS == nil || *job.ProcessingStartedAtMS != uint64(index+1) ||
			len(job.SourceFiles) != 1 || job.SourceFiles[0].Name != "file-"+id || len(job.Hotwords) != 1 || job.Hotwords[0] != fmt.Sprintf("term-%d", index) ||
			len(job.Warnings) != 1 || job.Warnings[0].Code != fmt.Sprintf("warning-%d", index) || len(job.ExportFormats) != 1 || job.ExportFormats[0] != fmt.Sprintf("format-%d", index) {
			test.Fatal("job rows reused prior storage", position, job)
		}
		if index == 1 {
			if job.FailureReason != nil || job.ActiveSummaryRunID != nil {
				test.Fatal("SQL NULL retained an earlier nullable value")
			}
		} else if job.FailureReason == nil || *job.FailureReason != fmt.Sprintf("reason-%d", index) || job.ActiveSummaryRunID == nil || *job.ActiveSummaryRunID != fmt.Sprintf("summary-%d", index) {
			test.Fatal("nullable job fields shared storage", position)
		}
	}
	result, err := reader.ReadResult(context.Background(), "independent-0")
	if err != nil || len(result.Speakers) != 5 {
		test.Fatal("read segment rows", err)
	}
	for index, expected := range []*string{rowString("speaker-one"), rowString("speaker-two"), nil, rowString(""), rowString("speaker-three")} {
		segment := result.Speakers[index]
		if segment.ID != fmt.Sprintf("speaker-segment-%d", index) || segment.Text != fmt.Sprintf("text-%d", index) || segment.StartMS != uint64(index*1000) {
			test.Fatal("segment rows shared storage", index)
		}
		if expected == nil {
			if segment.Speaker != nil {
				test.Fatal("SQL NULL retained an earlier speaker")
			}
		} else if segment.Speaker == nil || *segment.Speaker != *expected {
			test.Fatal("nullable speakers shared storage", index)
		}
	}
}

func rowString(value string) *string { return &value }

func TestReadStringArraysPreserveRustNullAndTypeSemantics(test *testing.T) {
	fallback := []string{"fallback"}
	for _, scenario := range []struct {
		input string
		want  []string
	}{
		{input: `[]`, want: []string{}},
		{input: ` [ ] `, want: []string{}},
		{input: `["会议", "", "null", "quote\"", "line\nfeed"]`, want: []string{"会议", "", "null", "quote\"", "line\nfeed"}},
		{input: `null`, want: fallback},
		{input: `[null]`, want: fallback},
		{input: `["term", null]`, want: fallback},
		{input: `["term", 1]`, want: fallback},
		{input: `[true]`, want: fallback},
		{input: `[[]]`, want: fallback},
		{input: `[{}]`, want: fallback},
		{input: `{}`, want: fallback},
		{input: `"term"`, want: fallback},
		{input: `["term"] trailing`, want: fallback},
		{input: `["unfinished"`, want: fallback},
		{input: ``, want: fallback},
	} {
		test.Run(scenario.input, func(test *testing.T) {
			if got := readStrings(scenario.input, fallback); !reflect.DeepEqual(got, scenario.want) {
				test.Fatalf("string array = %#v, want %#v", got, scenario.want)
			}
		})
	}
}

func FuzzReadStringArraysPreserveStrictJSON(fuzz *testing.F) {
	for _, input := range []string{`[]`, ` [ ] `, `null`, `[null]`, `["term",null]`, `["null"]`, `["null\\",null]`, `["quote\"null"]`, `["\\\"",null]`, `["\u006eull"]`, `[true]`, `[1]`, `[[]]`, `[{}]`, `"null"`, `{"null":1}`, `["会议", "line\nfeed", ""]`, `["term"] trailing`, `["unfinished"`, string([]byte{'[', '"', 0xff, '"', ']'})} {
		fuzz.Add(input)
	}
	fuzz.Fuzz(func(test *testing.T, input string) {
		fallback := []string{"synthetic-fallback"}
		want := fallback
		var raw []json.RawMessage
		if json.Unmarshal([]byte(input), &raw) == nil && raw != nil {
			strict := make([]string, 0, len(raw))
			valid := true
			for _, value := range raw {
				var text string
				if string(value) == "null" || json.Unmarshal(value, &text) != nil {
					valid = false
					break
				}
				strict = append(strict, text)
			}
			if valid {
				want = strict
			}
		}
		if got := readStrings(input, fallback); !reflect.DeepEqual(got, want) {
			test.Fatalf("strict JSON result mismatch: got=%#v want=%#v", got, want)
		}
	})
}

func TestLongTranscriptPreservesAllRowsAndProjectionOrder(test *testing.T) {
	path := fixture(test, SchemaVersion)
	uri := fixtureURI(path)
	database, err := sql.Open("sqlite", uri.String())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = database.Close() })
	_, err = database.Exec(`INSERT INTO jobs (id,title,created_at,overall_status,diarization_status,duration_minutes)
		VALUES ('long-transcript','Synthetic long transcript','2026-11-19','completed','completed',1);
		WITH RECURSIVE synthetic(position) AS (
			VALUES(0) UNION ALL SELECT position + 1 FROM synthetic WHERE position < 65536
		)
		INSERT INTO transcript_segments
		SELECT printf('long-%d',position),'long-transcript','transcript',position,position+1,NULL,'synthetic',65536-position FROM synthetic;
		INSERT INTO transcript_segments VALUES ('long-speaker','long-transcript','speaker',1,2,'speaker-one','synthetic speaker',0);`)
	if err != nil {
		test.Fatal(err)
	}
	reader, err := Open(context.Background(), path)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = reader.Close() })
	result, err := reader.ReadResult(context.Background(), "long-transcript")
	if err != nil || len(result.Transcript) != 65537 || len(result.Speakers) != 1 {
		test.Fatal("long projection lost or mixed rows", len(result.Transcript), len(result.Speakers), err)
	}
	for index, segment := range result.Transcript {
		position := 65536 - index
		if segment.ID != fmt.Sprintf("long-%d", position) || segment.StartMS != uint64(position) || segment.EndMS != uint64(position+1) || segment.Speaker != nil || segment.Text != "synthetic" {
			test.Fatal("long projection changed row ordering or storage", index)
		}
	}
	if result.Speakers[0].ID != "long-speaker" || result.Speakers[0].Speaker == nil || *result.Speakers[0].Speaker != "speaker-one" {
		test.Fatal("speaker projection lost its independent row")
	}
	other, err := reader.ReadResult(context.Background(), "visible")
	if err != nil || len(other.Transcript) != 1 || other.Transcript[0].ID != "t" || len(other.Speakers) != 0 {
		test.Fatal("long read changed another job projection", err)
	}
}
