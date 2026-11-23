package remote

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

func TestCapabilityCaseAliasesCannotAuthorizeMutations(t *testing.T) {
	for _, mode := range []string{"alias-only", "alias-after", "alias-before", "unicode-fold"} {
		t.Run(mode, func(t *testing.T) {
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/capabilities" {
					operations := `"OPERATIONS":["jobs.delete"]`
					switch mode {
					case "alias-after":
						operations = `"operations":["jobs.read"],` + operations
					case "alias-before":
						operations += `,"operations":["jobs.read"]`
					case "unicode-fold":
						operations = `"operationſ":["jobs.delete"]`
					}
					_, _ = w.Write([]byte(`{"protocol":"liberty-meeting","protocolVersion":1,"jobSchemaVersion":1,"serviceVersion":"1",` + operations + `}`))
					return
				}
				writes.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			connection := application.RemoteConnection{URL: server.URL, Token: domain.NewSecret("synthetic")}
			if err := (Client{}).Delete(context.Background(), connection, "fixture"); !errors.Is(err, domain.ErrRemote) || writes.Load() != 0 {
				t.Fatal("non-protocol capability field authorized a remote mutation")
			}
		})
	}
}

func TestJobUnknownCaseAliasesCannotChangeProjection(t *testing.T) {
	for _, mutate := range []struct {
		name  string
		apply func(map[string]any)
	}{
		{"optional-progress", func(job map[string]any) { job["PROGRESSMESSAGE"] = "alias" }},
		{"optional-log", func(job map[string]any) { job["PROCESSLOG"] = "alias" }},
		{"summary-overview", func(job map[string]any) { job["summary"] = map[string]any{"OVERVIEW": "alias"} }},
		{"nested-optional-speaker", func(job map[string]any) { job["transcriptSegments"].([]any)[0].(map[string]any)["SPEAKER"] = "alias" }},
		{"summary-run-default", func(job map[string]any) { job["summaryRuns"].([]any)[0].(map[string]any)["MODELCONFIGID"] = "alias" }},
		{"minutes-embedded-field", func(job map[string]any) {
			job["summaryRuns"].([]any)[0].(map[string]any)["minutesPayload"] = map[string]any{"speakerReports": []any{map[string]any{"RESOLVEDNAME": "alias"}}}
		}},
		{"top-level-array-alias", func(job map[string]any) { job["HOTWORDS"] = []string{"alias"} }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			job := syntheticJob("fixture")
			job["summaryRuns"] = []any{map[string]any{"id": "run", "jobId": "fixture", "includeSpeaker": false, "includeTimestamp": false, "status": "completed", "createdAt": "date", "updatedAt": "date"}}
			mutate.apply(job)
			data, _ := json.Marshal(job)
			result, err := parseJob(data)
			if err != nil {
				t.Fatal("unknown case alias should be ignored like an unknown extension")
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "alias") {
				t.Fatal("unknown field changed a protocol projection")
			}
		})
	}
}

func TestNestedExactFieldsWinIndependentlyOfOrder(t *testing.T) {
	for _, suffix := range []bool{false, true} {
		job := syntheticJob("fixture")
		data, _ := json.Marshal(job)
		baseline, err := parseJob(data)
		if err != nil {
			t.Fatal(err)
		}
		segment := `{"id":"s1","startMs":0,"endMs":1000,"text":"Synthetic transcript","TEXT":"alias"}`
		if !suffix {
			segment = `{"TEXT":"alias","id":"s1","startMs":0,"endMs":1000,"text":"Synthetic transcript"}`
		}
		job["transcriptSegments"] = json.RawMessage("[" + segment + "]")
		data, _ = json.Marshal(job)
		result, err := parseJob(data)
		if err != nil || !reflect.DeepEqual(result, baseline) {
			t.Fatal("nested field order changed authoritative transcript")
		}
	}
}

func TestExactDecodeRetainsNumbersDefaultsAndNullBoundaries(t *testing.T) {
	job := syntheticJob("fixture")
	job["processingStartedAtMs"] = uint64(math.MaxUint64)
	job["progressMessage"] = nil
	job["hotwords"] = []string{}
	job["futureExtension"] = map[string]any{"any": nil}
	job["PROGRESSMESSAGE"] = map[string]any{"ignored": true}
	job["transcriptSegments"] = json.RawMessage(`[{"id":"s1","startMs":9007199254740993,"endMs":18446744073709551615,"text":"exact","SPEAKER":42,"TEXT":null}]`)
	job["summaryRuns"] = []any{map[string]any{
		"id": "run", "jobId": "fixture", "includeSpeaker": true, "includeTimestamp": false, "status": "completed", "createdAt": "date", "updatedAt": "date",
		"minutesPayload": map[string]any{"participants": []any{map[string]any{"sortOrder": int64(math.MinInt64), "originalIndex": uint64(math.MaxUint64)}}},
	}}
	data, _ := json.Marshal(job)
	result, err := parseJob(data)
	if err != nil || result.Job.ProcessingStartedAtMS == nil || *result.Job.ProcessingStartedAtMS != math.MaxUint64 || result.Job.ProgressMessage != nil || result.Job.Hotwords == nil || len(result.Job.Hotwords) != 0 {
		t.Fatal("exact decoding changed optional values, empty arrays or integer precision")
	}
	segment := result.Transcript[0]
	if segment.StartMS != 9007199254740993 || segment.EndMS != math.MaxUint64 || segment.Text != "exact" || segment.Speaker != nil {
		t.Fatal("segment scalar types or ignored aliases changed")
	}
	payload := result.SummaryRuns[0].MinutesPayload
	if payload.SchemaVersion != 1 || payload.MeetingInfoSource != "empty" || payload.Participants[0].SortOrder != math.MinInt64 || payload.Participants[0].OriginalIndex != math.MaxUint64 {
		t.Fatal("nested defaults or signed and unsigned precision changed")
	}
	for _, raw := range []string{
		`{"overview":null}`, `{"topics":[null]}`, `{"topics":{}}`, `{"overview":42}`,
	} {
		job["summary"] = json.RawMessage(raw)
		data, _ = json.Marshal(job)
		if _, err := parseJob(data); !errors.Is(err, domain.ErrRemote) {
			t.Fatal("known field accepted an invalid scalar, null or collection")
		}
	}
	var caps domain.RemoteCapabilities
	if !decodeWire([]byte(`{"\u0070rotocol":"liberty-meeting","protocolVersion":1,"jobSchemaVersion":1,"serviceVersion":"1","operations":[],"jobCreate":null,"exports":null}`), &caps) || caps.Normalize() != nil {
		t.Fatal("escaped exact key or nullable capability object was rejected")
	}
	before := caps
	if decodeWire([]byte(`{"protocol":"partial","operations":[null]}`), &caps) || !reflect.DeepEqual(caps, before) {
		t.Fatal("rejected response changed the destination")
	}
}

func TestRemoteExactTranscriptReachesTextExport(t *testing.T) {
	job := syntheticJob("fixture")
	job["transcriptSegments"] = json.RawMessage(`[{"id":"s1","startMs":0,"endMs":1000,"text":"Authoritative synthetic transcript","TEXT":"Alias must never be exported","SPEAKER":"Alias speaker"}]`)
	job["summary"] = json.RawMessage(`{"overview":"Authoritative summary","OVERVIEW":"Alias summary"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/capabilities":
			_ = json.NewEncoder(w).Encode(syntheticCapabilities())
		case "/api/jobs":
			_ = json.NewEncoder(w).Encode([]any{job})
		case "/api/jobs/fixture", "/api/jobs/fixture/result":
			_ = json.NewEncoder(w).Encode(job)
		default:
			t.Error("unexpected route")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	connection := application.RemoteConnection{URL: server.URL, Token: domain.NewSecret("synthetic")}
	ctx := context.Background()
	list, err := (Client{}).List(ctx, connection)
	if err != nil || len(list) != 1 {
		t.Fatal("remote list unavailable")
	}
	results := append([]domain.Result{}, list...)
	for _, readResult := range []bool{false, true} {
		result, err := (Client{}).Read(ctx, connection, "fixture", readResult)
		if err != nil {
			t.Fatal("remote detail or result unavailable")
		}
		results = append(results, result)
	}
	for _, result := range results {
		text, err := domain.RenderTextExport(result, domain.ExportBundle, domain.TextExportLabels{UnknownSpeaker: "Unknown"})
		if err != nil || !strings.Contains(text, "Authoritative synthetic transcript") || !strings.Contains(text, "Authoritative summary") || strings.Contains(text, "Alias") {
			t.Fatal("HTTP field alias changed user-visible export contents")
		}
	}
}
