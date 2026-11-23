package remote

import (
	"bytes"
	"encoding/json"
	"strings"

	"liberty.local/liberty/internal/domain"
)

func object(data []byte) (map[string]json.RawMessage, bool) {
	var value map[string]json.RawMessage
	err := json.Unmarshal(data, &value)
	return value, err == nil && value != nil
}
func requireFields(value map[string]json.RawMessage, fields ...string) bool {
	for _, key := range fields {
		raw, ok := value[key]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return false
		}
	}
	return true
}
func requireArray(value map[string]json.RawMessage, key string) bool {
	raw := bytes.TrimSpace(value[key])
	return len(raw) > 0 && raw[0] == '['
}
func requireString(value map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		var s string
		raw := bytes.TrimSpace(value[key])
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
			return false
		}
	}
	return true
}

// This mirrors Rust's serde required/default fields, including legacy speaker
// downgrade. Server-provided executable and media paths have no native DTO slot.
func parseJob(data []byte) (domain.Result, error) {
	obj, ok := object(data)
	if !ok || !requireString(obj, "id", "title", "createdAt", "overallStatus", "lang", "summaryTemplate", "uploadStatus", "asrStatus", "summaryStatus") || !requireFields(obj, "durationMinutes", "enableSpeaker") {
		return domain.Result{}, domain.ErrRemote
	}
	for _, key := range []string{"sourceFiles", "transcriptSegments", "speakerSegments", "summaryRuns"} {
		if !requireArray(obj, key) {
			return domain.Result{}, domain.ErrRemote
		}
	}
	if _, ok := object(obj["summary"]); !ok {
		return domain.Result{}, domain.ErrRemote
	}
	for _, key := range []string{"hotwords", "exportFormats", "warnings"} {
		if _, exists := obj[key]; exists && !requireArray(obj, key) {
			return domain.Result{}, domain.ErrRemote
		}
	}
	if _, exists := obj["asrBackend"]; !exists {
		obj["asrBackend"] = json.RawMessage(`"unknown"`)
	}
	status := "legacy_unverified"
	if requireString(obj, "diarizationStatus") {
		_ = json.Unmarshal(obj["diarizationStatus"], &status)
	}
	switch status {
	case "disabled", "pending", "processing", "completed", "unavailable", "failed", "legacy_unverified":
	default:
		return domain.Result{}, domain.ErrRemote
	}
	obj["diarizationStatus"], _ = json.Marshal(status)
	if _, exists := obj["warnings"]; !exists {
		obj["warnings"] = json.RawMessage(`[{"code":"remote_diarization_unverified","message":"远端旧版任务未声明版本化说话人状态，默认仅使用逐字稿。"}]`)
	}
	for _, key := range []string{"sourceFiles", "transcriptSegments", "speakerSegments", "summaryRuns", "warnings"} {
		var values []json.RawMessage
		if json.Unmarshal(obj[key], &values) != nil {
			return domain.Result{}, domain.ErrRemote
		}
		for _, raw := range values {
			item, ok := object(raw)
			if !ok {
				return domain.Result{}, domain.ErrRemote
			}
			valid := false
			switch key {
			case "sourceFiles":
				valid = requireString(item, "id", "name", "sizeLabel", "kind")
			case "transcriptSegments", "speakerSegments":
				valid = requireString(item, "id", "text") && requireFields(item, "startMs", "endMs")
			case "summaryRuns":
				valid = requireString(item, "id", "jobId", "status", "createdAt", "updatedAt") && requireFields(item, "includeSpeaker", "includeTimestamp")
			case "warnings":
				valid = requireString(item, "code", "message")
			}
			if !valid {
				return domain.Result{}, domain.ErrRemote
			}
		}
	}
	encoded, _ := json.Marshal(obj)
	var wire struct {
		domain.JobSummary
		Summary     domain.Summary      `json:"summary"`
		SummaryRuns []domain.SummaryRun `json:"summaryRuns"`
		ProcessLog  *string             `json:"processLog"`
		Transcript  []domain.Segment    `json:"transcriptSegments"`
		Speakers    []domain.Segment    `json:"speakerSegments"`
	}
	if !decodeWire(encoded, &wire) || strings.TrimSpace(wire.ID) == "" || strings.TrimSpace(wire.Title) == "" {
		return domain.Result{}, domain.ErrRemote
	}
	if wire.ASRBackend != "unknown" && wire.ASRBackend != "funasr" && wire.ASRBackend != "sherpa-onnx" {
		return domain.Result{}, domain.ErrRemote
	}
	if wire.ProgressPercent != nil && (*wire.ProgressPercent < 0 || *wire.ProgressPercent > float64(^uint32(0)) || *wire.ProgressPercent != float64(uint32(*wire.ProgressPercent))) {
		return domain.Result{}, domain.ErrRemote
	}
	wire.Source = "remote"
	var rawRuns []map[string]json.RawMessage
	_ = json.Unmarshal(obj["summaryRuns"], &rawRuns)
	for i := range wire.SummaryRuns {
		payload := wire.SummaryRuns[i].MinutesPayload
		if payload == nil {
			continue
		}
		fields, _ := object(rawRuns[i]["minutesPayload"])
		if _, exists := fields["schemaVersion"]; !exists {
			payload.SchemaVersion = 1
		}
		if _, exists := fields["meetingInfoSource"]; !exists {
			payload.MeetingInfoSource = "empty"
		}
		if payload.MeetingInfoSource != "empty" && payload.MeetingInfoSource != "user" && payload.MeetingInfoSource != "ai" {
			return domain.Result{}, domain.ErrRemote
		}
	}
	return domain.Result{Job: wire.JobSummary, Summary: wire.Summary, SummaryRuns: wire.SummaryRuns, ProcessLog: wire.ProcessLog, Transcript: wire.Transcript, Speakers: wire.Speakers}, nil
}
