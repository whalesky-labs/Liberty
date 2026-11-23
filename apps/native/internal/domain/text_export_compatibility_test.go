//go:build compatibility

package domain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRustTextExports(t *testing.T) {
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if directory == "" {
		t.Fatal("run pnpm native:compat")
	}
	data, err := os.ReadFile(filepath.Join(directory, "text-exports.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Locale   string
		Kind     TextExportKind
		Labels   TextExportLabels
		Result   Result
		Expected string
	}
	if json.Unmarshal(data, &cases) != nil || len(cases) != 36 {
		t.Fatal("invalid Rust export fixtures")
	}
	for _, c := range cases {
		text, err := RenderTextExport(c.Result, c.Kind, c.Labels)
		if err != nil || text != c.Expected {
			t.Fatalf("Rust export mismatch for %s/%s/%s", c.Locale, c.Kind, c.Result.Job.DiarizationStatus)
		}
	}
}
