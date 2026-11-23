package runner

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func TestV1ProgressRequiresExactStageAndIgnoresCaseAliases(t *testing.T) {
	for _, raw := range []string{`{"STAGE":"completed"}`, `{"ſtage":"completed"}`, `{"stage":null,"STAGE":"completed"}`} {
		if _, err := ParseProgress([]byte(raw)); !errors.Is(err, ErrProtocol) {
			t.Fatal("a case alias supplied a missing required stage")
		}
	}
	want := Progress{ProtocolVersion: 1, Stage: "transcribing"}
	for _, raw := range []string{
		`{"stage":"transcribing","STAGE":"completed"}`,
		`{"STAGE":"completed","stage":"transcribing"}`,
		`{"stage":"transcribing","STATUSMESSAGE":"alias","FAILUREREASON":"alias","PROGRESSPERCENT":100}`,
		`{"stage":"transcribing","statusMessage":null,"STATUSMESSAGE":"alias"}`,
	} {
		got, err := ParseProgress([]byte(raw))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("V1 stage or optional status changed through a case alias")
		}
	}
}

func TestExecuteV1ProgressUsesExactStageBeforeCancellation(t *testing.T) {
	spec := testSpec(t, "v1_progress_alias")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stop := errors.New("synthetic_progress_received")
	var observed *Progress
	_, err := Execute(ctx, spec, Hooks{Progress: func(progress Progress) error {
		observed = &progress
		return stop
	}})
	if !errors.Is(err, stop) || observed == nil || observed.Stage != "transcribing" || observed.StatusMessage == nil || *observed.StatusMessage != "Exact progress" || observed.ProgressPercent == nil || *observed.ProgressPercent != 12 {
		t.Fatal("real child progress was replaced by case aliases or failed to stop")
	}
}

func TestV1ResultCaseAliasesCannotChangeOutcomeOrTranscript(t *testing.T) {
	const segment = `{"id":"s1","startMs":0,"endMs":1000,"text":"Exact transcript"}`
	for name, suffix := range map[string]string{
		"failure-alias":  `,"FAILUREREASON":"alias failure"`,
		"duration-alias": `,"DURATIONMINUTES":99`,
		"array-alias":    `,"TRANSCRIPTSEGMENTS":[{"id":"other","startMs":0,"endMs":1,"text":"alias"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseResult([]byte(`{"transcriptSegments":[`+segment+`]`+suffix+`}`), false, "funasr")
			if err != nil || len(got.Transcript) != 1 || got.Transcript[0].Text != "Exact transcript" || got.DurationMinutes != nil {
				t.Fatal("case alias changed the V1 result outcome")
			}
		})
	}
	got, err := ParseResult([]byte(`{"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1000,"text":"Exact transcript","TEXT":"alias","SPEAKER":"alias"}]}`), true, "funasr")
	if err != nil || got.Transcript[0].Text != "Exact transcript" || got.Transcript[0].Speaker != nil || got.DiarizationStatus != "legacy_unverified" {
		t.Fatal("nested alias changed transcript or promoted unverified speakers")
	}
	if _, err := ParseResult([]byte(`{"transcriptSegments":[{"ID":"s1","STARTMS":0,"ENDMS":1,"TEXT":"alias"}]}`), false, "funasr"); !errors.Is(err, ErrProtocol) {
		t.Fatal("nested aliases supplied missing mandatory segment fields")
	}
	if _, err := ParseResult([]byte(`{"failureReason":"actual failure","FAILUREREASON":"","transcriptSegments":[`+segment+`]}`), false, "funasr"); !errors.Is(err, domain.ErrRunnerResult) {
		t.Fatal("case alias cleared an actual Runner failure")
	}
}

func TestV1DuplicateExactKeysFollowOriginalValueNormalization(t *testing.T) {
	// Unlike job.json import, the original Runner first reads serde_json::Value.
	// Duplicate exact keys therefore keep the last value before typed decoding.
	p, err := ParseProgress([]byte(`{"stage":42,"stage":"completed","statusMessage":{},"statusMessage":null}`))
	if err != nil || p.Stage != "completed" || p.StatusMessage != nil {
		t.Fatal("last exact V1 progress values were not normalized first")
	}
	r, err := ParseResult([]byte(`{"durationMinutes":"invalid","durationMinutes":3,"transcriptSegments":[{"id":null,"id":"s1","startMs":{},"startMs":9007199254740993,"endMs":18446744073709551615,"text":[],"text":"exact"}]}`), false, "funasr")
	if err != nil || r.DurationMinutes == nil || *r.DurationMinutes != 3 || len(r.Transcript) != 1 || r.Transcript[0].StartMS != 9007199254740993 || r.Transcript[0].EndMS != ^uint64(0) || r.Transcript[0].Text != "exact" {
		t.Fatal("last exact V1 result values or integer precision changed")
	}
	if _, err := ParseProgress([]byte(`{"stage":"completed","stage":null}`)); !errors.Is(err, ErrProtocol) {
		t.Fatal("invalid last exact progress value was accepted")
	}
}
