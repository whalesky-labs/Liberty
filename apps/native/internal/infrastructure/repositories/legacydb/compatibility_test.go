//go:build compatibility

package legacydb

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func TestRustDatabaseCompatibility(t *testing.T) {
	dir := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if dir == "" {
		t.Fatal("run pnpm native:compat to generate the Rust fixture")
	}
	var expected struct {
		Timestamps []struct {
			Nanos int64
			Text  string
		} `json:"timestamps"`
		Preferences domain.UIPreferences     `json:"preferences"`
		Jobs        []domain.JobSummary      `json:"jobs"`
		Results     []domain.Result          `json:"results"`
		Dashboard   domain.DashboardOverview `json:"dashboard"`
		Levels      []struct {
			Experience int64 `json:"experience"`
			Level      int64 `json:"level"`
			Current    int64 `json:"current"`
			Next       int64 `json:"next"`
			Percent    int64 `json:"percent"`
		} `json:"levels"`
	}
	bytes, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(bytes, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected.Jobs) != 14 || len(expected.Results) != 14 || len(expected.Timestamps) != 7 {
		t.Fatal("incomplete Rust fixture")
	}
	for _, stamp := range expected.Timestamps {
		if got := domain.PersistedUTC(time.Unix(0, stamp.Nanos)); got != stamp.Text {
			t.Fatalf("timestamp %d: got %q, Rust wrote %q", stamp.Nanos, got, stamp.Text)
		}
	}
	path := filepath.Join(dir, "liberty.sqlite3")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	preferences, err := r.UIPreferences(context.Background())
	if err != nil || preferences != expected.Preferences {
		t.Fatal("UI preferences differ from Rust")
	}
	jobs, err := r.ListJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jobs, expected.Jobs) {
		t.Fatal("queue differs from Rust repository")
	}
	for _, want := range expected.Results {
		got, err := r.ReadResult(context.Background(), want.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("result differs from Rust repository for synthetic id %s", want.Job.ID)
		}
	}
	dashboard, err := r.Dashboard(context.Background(), "all", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dashboard, expected.Dashboard) {
		t.Fatalf("dashboard differs from Rust repository:\nGo: %+v\nRust: %+v", dashboard, expected.Dashboard)
	}
	if len(expected.Levels) != 255*3 {
		t.Fatal("missing level boundaries")
	}
	for _, want := range expected.Levels {
		var got domain.DashboardCompanion
		got.SetExperience(want.Experience)
		if got.Level != want.Level || got.CurrentLevelExperience != want.Current || got.NextLevelExperience != want.Next || got.LevelProgressPercent != want.Percent {
			t.Fatalf("pet level differs for experience %d", want.Experience)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("compatibility read changed database")
	}
}
