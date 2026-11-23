//go:build compatibility

package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRustMinutes(t *testing.T) {
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if directory == "" {
		t.Fatal("run pnpm native:compat")
	}
	data, err := os.ReadFile(filepath.Join(directory, "minutes-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Job                           Result
		Result                        SummaryResult
		Members                       []MeetingMember
		Template, Run, SourceOverview string
		Expected                      MinutesPayload
		Document                      MinutesDocument
	}
	if err := json.Unmarshal(data, &cases); err != nil || len(cases) != 18 {
		t.Fatal("minutes fixture", err)
	}
	for i, c := range cases {
		actual, err := ReconstructMinutes(c.Job, c.Result, c.Members, c.Template, &c.Run)
		if err != nil || !reflect.DeepEqual(actual, c.Expected) {
			a, _ := json.Marshal(actual)
			b, _ := json.Marshal(c.Expected)
			t.Fatalf("case %d minutes mismatch: %v\n%s\n%s", i, err, a, b)
		}
		c.Job.Job.ActiveSummaryRunID = &c.Run
		c.Job.SummaryRuns = []SummaryRun{{ID: c.Run, Status: "completed", Result: &c.Result, TemplateID: c.Template}}
		source, err := ResolveSummaryExport(c.Job, c.Members, "")
		if err != nil || !reflect.DeepEqual(source.MinutesPayload, c.Expected) || source.Result.Overview != c.SourceOverview || !source.ReconstructedPayload {
			t.Fatalf("case %d source differs: %v", i, err)
		}
		if actual := BuildMinutesDocument(c.Job, source, c.Members); !reflect.DeepEqual(actual, c.Document) {
			a, _ := json.Marshal(actual)
			b, _ := json.Marshal(c.Document)
			t.Fatalf("case %d document differs\n%s\n%s", i, a, b)
		}
	}
}
