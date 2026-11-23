package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../../../../packages/shared-types/fixtures/runner/v2", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGoldenResults(t *testing.T) {
	for _, status := range []string{"completed", "unavailable"} {
		r, err := ParseResult(fixture(t, "result-"+status), true, "unknown")
		if err != nil {
			t.Fatal(err)
		}
		if r.DiarizationStatus != status || r.ProtocolVersion != 2 || len(r.Transcript) != 1 || r.Duration(0) != 1 {
			t.Fatalf("wrong result: %+v", r)
		}
		if status == "completed" && (len(r.Speakers) != 1 || *r.Speakers[0].Speaker != "speaker-0") {
			t.Fatal("speaker lost")
		}
		if status == "unavailable" && (len(r.Speakers) != 0 || len(r.Warnings) != 1) {
			t.Fatal("unavailable semantics lost")
		}
	}
	p, err := ParseProgress(fixture(t, "progress-transcribing"))
	if err != nil || p.Revision == nil || *p.Revision != 1 || *p.ProgressPercent != 32 {
		t.Fatalf("progress: %+v %v", p, err)
	}
	e, err := ParseEvent(fixture(t, "event-progress"))
	if err != nil || e.Type != "progress" || e.Code != "asr_transcribing" {
		t.Fatalf("event: %+v %v", e, err)
	}
}

func TestInvalidV2Results(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown field", func(m map[string]any) { m["unexpected"] = true }},
		{"missing required", func(m map[string]any) { delete(m, "durationMinutes") }},
		{"null required", func(m map[string]any) { m["warnings"] = nil }},
		{"negative", func(m map[string]any) { m["durationMinutes"] = -1 }},
		{"overflow", func(m map[string]any) { m["durationMinutes"] = uint64(1) << 32 }},
		{"unsupported version", func(m map[string]any) { m["protocolVersion"] = 3 }},
		{"inconsistent request", func(m map[string]any) { m["diarizationRequested"] = false }},
		{"missing speakers", func(m map[string]any) { m["speakerSegments"] = []any{} }},
		{"empty transcript", func(m map[string]any) { m["transcriptSegments"] = []any{} }},
		{"default label", func(m map[string]any) { m["speakerSegments"].([]any)[0].(map[string]any)["speaker"] = "Speaker 1" }},
		{"blank label", func(m map[string]any) { m["speakerSegments"].([]any)[0].(map[string]any)["speaker"] = "  " }},
		{"segment overflow", func(m map[string]any) {
			m["transcriptSegments"].([]any)[0].(map[string]any)["endMs"] = json.Number("18446744073709551616")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(fixture(t, "result-completed"), &m); err != nil {
				t.Fatal(err)
			}
			tc.mutate(m)
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseResult(data, true, "funasr"); err == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
}

func TestV1Normalization(t *testing.T) {
	data := []byte(`{"durationMinutes":1,"transcriptSegments":[{"id":"1","startMs":0,"endMs":1,"text":"ok"}],"speakerSegments":[{"id":"1","startMs":0,"endMs":1,"speaker":"Speaker 1","text":"ok"}],"failureReason":null}`)
	r, err := ParseResult(data, true, "funasr")
	if err != nil || r.DiarizationStatus != "legacy_unverified" || len(r.Warnings) != 1 || len(r.Speakers) != 1 {
		t.Fatalf("legacy: %+v %v", r, err)
	}
	r, err = ParseResult(data, false, "sherpa-onnx")
	if err != nil || r.DiarizationStatus != "disabled" || len(r.Warnings) != 0 || len(r.Speakers) != 0 {
		t.Fatalf("disabled: %+v %v", r, err)
	}
	for _, bad := range []string{
		`{"transcriptSegments":[{}]}`, `{"transcriptSegments":[null]}`,
		strings.Replace(string(data), `"startMs":0`, `"startMs":null`, 1),
		strings.Replace(string(data), `"failureReason":null`, `"failureReason":"sensitive detail"`, 1),
	} {
		_, err := ParseResult([]byte(bad), true, "funasr")
		if err == nil || strings.Contains(err.Error(), "sensitive detail") {
			t.Fatal("invalid or leaking V1 error")
		}
	}
	p, err := ParseProgress([]byte(`{"stage":"","revision":"ignored","progressPercent":101}`))
	if err != nil || p.ProtocolVersion != 1 || p.Revision != nil {
		t.Fatal("V1 optional fields changed", err)
	}
	for _, version := range []string{`"2"`, `null`, `-1`, `2.0`} {
		legacy := strings.Replace(string(fixture(t, "result-completed")), `"protocolVersion": 2`, `"protocolVersion": `+version, 1)
		result, err := ParseResult([]byte(legacy), true, "funasr")
		if err != nil || result.ProtocolVersion != 1 || result.DiarizationStatus != "legacy_unverified" {
			t.Fatalf("legacy version fallback changed for %s: %v", version, err)
		}
	}
}

func TestProgressAndEventValidation(t *testing.T) {
	for _, pair := range []struct{ old, new string }{
		{`"revision": 1`, `"revision": -1`},
		{`"progressPercent": 32`, `"progressPercent": 101`},
		{`"stage": "transcribing"`, `"stage": "completed"`},
		{`"stage": "transcribing"`, `"stage": "failed"`},
	} {
		if _, err := ParseProgress([]byte(strings.Replace(string(fixture(t, "progress-transcribing")), pair.old, pair.new, 1))); err == nil {
			t.Fatal("invalid progress accepted")
		}
	}
	for _, data := range []string{`plain runner log`, `{}`, `{"protocolVersion":2,"type":"failure","level":"error","code":"bad","message":"oops","unexpected":true}`} {
		if _, err := ParseEvent([]byte(data)); err == nil {
			t.Fatal("non-event stdout accepted")
		}
	}
}

func TestProgressFencePerAttempt(t *testing.T) {
	var first, retry ProgressFence
	progress := func(revision uint64) Progress { return Progress{ProtocolVersion: 2, Revision: &revision} }
	for _, rev := range []uint64{1, 2, 2, 9} {
		if err := first.Accept(progress(rev)); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Accept(progress(8)); err != ErrStale {
		t.Fatal("late revision accepted")
	}
	if err := retry.Accept(progress(1)); err != nil {
		t.Fatal("retry inherited old fence")
	}
	if err := first.Accept(Progress{ProtocolVersion: 2}); err != ErrProtocol {
		t.Fatal("missing revision accepted")
	}
}
