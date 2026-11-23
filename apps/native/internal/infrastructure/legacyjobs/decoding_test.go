package legacyjobs

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func withLegacyFields(fields string) []byte {
	return []byte(strings.TrimSuffix(syntheticJSON, "}") + "," + fields + "}")
}

func TestLegacyExactDecodePreservesDefaultsPrivatePathsAndIntegerBounds(t *testing.T) {
	job, err := decodeJob(withLegacyFields(`"sourceFiles":[{"id":"f1","name":"synthetic.wav","sizeLabel":"1 B","kind":"audio","path":"/synthetic/exact.wav","PATH":42}],"pythonPath":null,"transcriptSegments":[{"id":"s1","startMs":9007199254740993,"endMs":9223372036854775807,"text":"exact"}],"future":{},"future":null,"SUMMARY":42`))
	if err != nil || len(job.Sources) != 1 || job.Sources[0].Path == nil || *job.Sources[0].Path != "/synthetic/exact.wav" || len(job.Result.Job.SourceFiles) != 1 || job.PythonPath != nil {
		t.Fatal("exact private source path or nullable pointer changed")
	}
	if job.Result.Transcript[0].StartMS != 9007199254740993 || job.Result.Transcript[0].EndMS != math.MaxInt64 || job.Result.Summary.Topics == nil || job.Result.Job.Source != "local" {
		t.Fatal("numeric precision or preseeded defaults changed")
	}
	for _, fields := range []string{`"transcriptSegments":[{"id":"s1","startMs":0,"endMs":9223372036854775808,"text":"exact"}]`, `"summary":{"overview":null}`, `"sourceFiles":null`} {
		if _, err := decodeJob(withLegacyFields(fields)); err == nil {
			t.Fatal("numeric or nonoptional null boundary weakened")
		}
	}
}

func TestLegacyCaseAliasesCannotReplaceSavedValues(t *testing.T) {
	for _, fields := range []string{
		`"TITLE":"alias"`,
		`"PYTHONPATH":"alias","RUNNERSCRIPTPATH":"alias"`,
		`"summary":{"OVERVIEW":"alias"}`,
		`"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1,"text":"exact","TEXT":"alias","SPEAKER":"alias"}]`,
		`"sourceFiles":[{"id":"f1","name":"synthetic.wav","sizeLabel":"1 B","kind":"audio","PATH":"alias"}]`,
		`"HOTWORDS":["alias"]`,
		`"failureReaſon":"alias"`,
	} {
		job, err := decodeJob(withLegacyFields(fields))
		if err != nil {
			t.Fatal("unknown extension should be ignored")
		}
		data, _ := json.Marshal(job)
		if strings.Contains(string(data), "alias") {
			t.Fatal("case-folded extension changed saved job values")
		}
		if job.Result.Job.Title != "Synthetic legacy" || job.Result.Job.ASRBackend != "unknown" || job.Result.Job.DiarizationStatus != "legacy_unverified" {
			t.Fatal("saved fields or legacy defaults changed")
		}
	}
}

func TestLegacyDuplicateKnownFieldsRejectSnapshot(t *testing.T) {
	for name, fields := range map[string]string{
		"top-level":     `"title":"second"`,
		"escaped-name":  `"\u0074itle":"second"`,
		"null-pointer":  `"pythonPath":null,"pythonPath":"synthetic"`,
		"segment":       `"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1,"text":"first","text":"second"}]`,
		"summary":       `"summary":{"overview":"first","overview":"second"}`,
		"source-path":   `"sourceFiles":[{"id":"f1","name":"synthetic.wav","sizeLabel":"1 B","kind":"audio","path":"first","path":"second"}]`,
		"discarded-run": `"summaryRuns":[{"id":"r1","jobId":"j1","includeSpeaker":false,"includeTimestamp":false,"status":"completed","createdAt":"date","updatedAt":"date","result":{"overview":"first","overview":"second"}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeJob(withLegacyFields(fields)); err == nil {
				t.Fatal("duplicate known field accepted where original Serde rejects it")
			}
		})
	}
}

func TestLegacyOptionalResultIgnoresAliasesAndRejectsDuplicatesAsAWhole(t *testing.T) {
	for name, result := range map[string]string{
		"alias-only":        `{"DURATIONMINUTES":7,"TRANSCRIPTSEGMENTS":[{"id":"s1","startMs":0,"endMs":1,"text":"alias"}],"FAILUREREASON":"alias"}`,
		"duplicate-scalar":  `{"durationMinutes":7,"durationMinutes":8,"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1,"text":"exact"}]}`,
		"duplicate-segment": `{"durationMinutes":7,"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1,"text":"first","text":"second"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			job, err := decodeJob([]byte(syntheticJSON))
			if err != nil {
				t.Fatal(err)
			}
			before := job
			applyResult(&job, []byte(result))
			if !reflect.DeepEqual(before, job) {
				t.Fatal("invalid or alias-only optional result changed saved snapshot")
			}
		})
	}
	job, err := decodeJob([]byte(syntheticJSON))
	if err != nil {
		t.Fatal(err)
	}
	applyResult(&job, []byte(`{"durationMinutes":2,"transcriptSegments":[{"id":"s1","startMs":0,"endMs":1,"text":"exact","TEXT":"alias"}]}`))
	if len(job.Result.Transcript) != 1 || job.Result.Transcript[0].Text != "exact" || job.Result.Job.DurationMinutes != 2 {
		t.Fatal("known result fields were replaced by case aliases")
	}
}
