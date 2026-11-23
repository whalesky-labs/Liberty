//go:build compatibility

package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRustAI(t *testing.T) {
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if directory == "" {
		t.Fatal("run pnpm native:compat")
	}
	data, err := os.ReadFile(filepath.Join(directory, "ai-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Executions []SummaryExecution
		Prompts    []struct {
			Input  SummaryPromptInput
			Prompt SummaryPrompt
			Plan   SummaryPlan
		}
		Responses []struct {
			Raw      string
			Valid    bool
			Expected SummaryResult
		}
		Merge struct {
			Raw, Required []string
			Expected      SummaryResult
			Structured    json.RawMessage
		}
	}
	if json.Unmarshal(data, &fixture) != nil || len(fixture.Prompts) != 12 || len(fixture.Responses) != 6 {
		t.Fatal("invalid fixture")
	}
	if len(fixture.Executions) != 12 {
		t.Fatal("missing Rust execution snapshots")
	}
	for i, e := range fixture.Executions {
		if _, err := e.Snapshot(); err != nil {
			t.Fatalf("Rust execution snapshot %d rejected: %v", i, err)
		}
	}
	for i, v := range fixture.Prompts {
		got := BuildSummaryPrompt(v.Input)
		if got != v.Prompt {
			t.Fatalf("prompt %d differs from Rust", i)
		}
		plan, err := PlanSummary(got)
		if err != nil || !reflect.DeepEqual(plan, v.Plan) {
			t.Fatalf("plan %d differs: %v", i, err)
		}
	}
	for i, v := range fixture.Responses {
		got, err := ParseSummaryResponse(v.Raw, "回退标题")
		if (err == nil) != v.Valid || (v.Valid && !reflect.DeepEqual(got, v.Expected)) {
			t.Fatalf("response %d differs", i)
		}
	}
	raw, err := MergeSummaryResponses(fixture.Merge.Raw, "回退标题", fixture.Merge.Required)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ParseSummaryResponse(raw, "回退标题")
	if err != nil || !reflect.DeepEqual(result, fixture.Merge.Expected) {
		t.Fatal("merged result differs")
	}
	got, err := ParseSummaryStructured(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if json.Unmarshal(encoded, &a) != nil || json.Unmarshal(fixture.Merge.Structured, &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("merged structured response differs")
	}
}
