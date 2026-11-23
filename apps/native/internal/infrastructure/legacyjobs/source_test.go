package legacyjobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"liberty.local/liberty/internal/application"
)

const syntheticID = "job-1700000000000-1"
const syntheticJSON = `{"id":"job-1700000000000-1","title":"Synthetic legacy","durationMinutes":0,"createdAt":"fixture","lang":"zh","enableSpeaker":true,"summaryTemplate":"","uploadStatus":"uploaded","asrStatus":"queued","summaryStatus":"queued","overallStatus":"queued"}`

func TestLegacyJSONDefaultsNullsAndFieldTypes(t *testing.T) {
	job, err := decodeJob([]byte(syntheticJSON))
	if err != nil {
		t.Fatal(err)
	}
	if job.Result.Job.DiarizationStatus != "legacy_unverified" || job.Result.Job.ASRBackend != "unknown" || job.Result.Job.Source != "local" {
		t.Fatal("serde defaults differ")
	}
	for _, replacement := range []string{`"title":null`, `"title":7`, `"summary":null`, `"transcriptSegments":[{"id":"s","startMs":0,"endMs":1,"text":null}]`, `"transcriptSegments":[{"startMs":0,"endMs":1,"text":"missing id"}]`, `"sourceFiles":[{"id":"s","name":"file"}]`, `"warnings":[{"code":"missing message"}]`, `"durationMinutes":4294967296`, `"diarizationStatus":"future"`, `"asrBackend":null`, `"summary":{"topics":[null]}`, `"processingStartedAtMs":18446744073709551615`, `"progressPercent":1.5`} {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal([]byte(syntheticJSON), &fields)
		var changed map[string]json.RawMessage
		_ = json.Unmarshal([]byte("{"+replacement+"}"), &changed)
		for k, v := range changed {
			fields[k] = v
		}
		data, _ := json.Marshal(fields)
		if _, err := decodeJob(data); err == nil {
			t.Fatalf("invalid legacy field accepted: %s", replacement)
		}
	}
}
func TestLegacySourceOverlaysProgressAndResultWithinRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "jobs", syntheticID)
	if os.MkdirAll(root, 0700) != nil {
		t.Fatal("fixture")
	}
	files := map[string]string{"job.json": syntheticJSON, "progress.json": `{"stage":"failed","statusMessage":"Synthetic failure"}`, "result.json": `{"durationMinutes":2,"transcriptSegments":[{"id":"s","startMs":0,"endMs":10,"text":"synthetic"}],"failureReason":"result fallback"}`, "process.log": "  synthetic log \n"}
	for name, text := range files {
		if os.WriteFile(filepath.Join(root, name), []byte(text), 0600) != nil {
			t.Fatal("fixture")
		}
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got application.LegacyJob
	skipped, err := s.Each(context.Background(), func(j application.LegacyJob) error { got = j; return nil })
	if err != nil || skipped != 0 || got.Result.Job.OverallStatus != "failed" || *got.Result.Job.FailureReason != "Synthetic failure" || got.Result.Job.DurationMinutes != 2 || len(got.Result.Transcript) != 1 || *got.Result.ProcessLog != "synthetic log" {
		t.Fatal("legacy overlay differs", err)
	}
	// Bad optional results do not erase an otherwise valid saved job.
	if os.WriteFile(filepath.Join(root, "result.json"), []byte(`{"transcriptSegments":[{"id":"s","startMs":0,"endMs":1,"text":null}]}`), 0600) != nil {
		t.Fatal("fixture")
	}
	_, err = s.Each(context.Background(), func(j application.LegacyJob) error {
		if len(j.Result.Transcript) != 0 {
			t.Fatal("invalid result merged")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if os.WriteFile(filepath.Join(root, "job.json"), []byte(strings.Replace(syntheticJSON, syntheticID, "job-1700000000000-2", 1)), 0600) != nil {
		t.Fatal("fixture")
	}
	skipped, err = s.Each(context.Background(), func(application.LegacyJob) error { t.Fatal("cross-directory task accepted"); return nil })
	if err != nil || skipped != 1 {
		t.Fatal("invalid task not skipped", err)
	}
}
func TestLegacyLinkedSnapshotIsNotRead(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "jobs", syntheticID)
	if os.MkdirAll(root, 0700) != nil {
		t.Fatal("fixture")
	}
	outside := filepath.Join(t.TempDir(), "job.json")
	if os.WriteFile(outside, []byte(syntheticJSON), 0600) != nil {
		t.Fatal("fixture")
	}
	if err := os.Symlink(outside, filepath.Join(root, "job.json")); err != nil {
		t.Skip("symlink unavailable")
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	skipped, err := s.Each(context.Background(), func(application.LegacyJob) error { t.Fatal("symlink snapshot read"); return nil })
	if err != nil || skipped != 1 {
		t.Fatal("linked snapshot not rejected", err)
	}
}

func TestOversizedLegacyArtifactStopsImportWithoutCompletion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs", syntheticID)
	if os.MkdirAll(path, 0700) != nil {
		t.Fatal("fixture")
	}
	if os.WriteFile(filepath.Join(path, "job.json"), []byte(syntheticJSON), 0600) != nil {
		t.Fatal("fixture")
	}
	f, err := os.Create(filepath.Join(path, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxLog + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Each(context.Background(), func(application.LegacyJob) error { t.Fatal("oversized artifact silently dropped"); return nil })
	if !errors.Is(err, ErrLimit) {
		t.Fatal("size limit not reported", err)
	}
}
