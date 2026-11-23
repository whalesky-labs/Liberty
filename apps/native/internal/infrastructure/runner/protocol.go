// Package runner implements the unchanged Python JSON protocol and isolated
// process execution. Runtime installation and job scheduling are separate ports.
package runner

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"liberty.local/liberty/internal/contracts"
	"liberty.local/liberty/internal/domain"
)

var (
	ErrProtocol = errors.New("runner_protocol_invalid")
	ErrVersion  = errors.New("runner_protocol_unsupported")
	ErrStale    = errors.New("runner_progress_stale")
)

const schemaRoot = "https://liberty.local/schemas/runner/v2/"

func validate(kind string, data []byte) error {
	v, err := contracts.Shared()
	if err != nil || v.Validate(schemaRoot+kind+".schema.json", data) != nil {
		return ErrProtocol
	}
	return nil
}

func version(data []byte) (uint32, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return 0, ErrProtocol
	}
	raw, ok := object["protocolVersion"]
	if !ok {
		return 1, nil
	}
	// serde_json::Value::as_u64 used by the existing reader treats non-u64
	// values as the legacy path. Preserve it, including the unverified label.
	var v *uint64
	if json.Unmarshal(raw, &v) != nil || v == nil {
		return 1, nil
	}
	if *v != 2 {
		return 0, ErrVersion
	}
	return 2, nil
}

func ParseResult(data []byte, requested bool, legacyBackend string) (domain.RunnerResult, error) {
	v, err := version(data)
	if err != nil {
		return domain.RunnerResult{}, err
	}
	var result domain.RunnerResult
	if v == 2 {
		if err := validate("result", data); err != nil {
			return result, err
		}
		if json.Unmarshal(data, &result) != nil {
			return result, ErrProtocol
		}
	} else {
		var legacy struct {
			DurationMinutes *uint32
			Transcript      []json.RawMessage
			Speakers        []json.RawMessage
			FailureReason   *string
		}
		if !decodeV1Fields(data, map[string]any{
			"durationMinutes": &legacy.DurationMinutes, "transcriptSegments": &legacy.Transcript,
			"speakerSegments": &legacy.Speakers, "failureReason": &legacy.FailureReason,
		}) {
			return result, ErrProtocol
		}
		if legacy.FailureReason != nil && strings.TrimSpace(*legacy.FailureReason) != "" {
			return result, domain.ErrRunnerResult
		}
		transcript, err := legacySegments(legacy.Transcript)
		if err != nil {
			return result, err
		}
		speakers, err := legacySegments(legacy.Speakers)
		if err != nil {
			return result, err
		}
		result = domain.RunnerResult{ProtocolVersion: 1, ASRBackend: legacyBackend, DiarizationStatus: "disabled", Warnings: []domain.RunnerWarning{}, DurationMinutes: legacy.DurationMinutes, Transcript: transcript, Speakers: []domain.Segment{}}
		if requested {
			result.DiarizationStatus = "legacy_unverified"
			result.Speakers = speakers
			result.Warnings = []domain.RunnerWarning{{Code: "legacy_diarization_unverified", Message: "旧版 Runner 说话人结果无法验证，默认仅使用逐字稿。"}}
		}
	}
	if err := result.Validate(); err != nil {
		return domain.RunnerResult{}, err
	}
	return result, nil
}

func legacySegments(input []json.RawMessage) ([]domain.Segment, error) {
	output := make([]domain.Segment, 0, len(input))
	for _, raw := range input {
		// Pointers distinguish required zero/empty values from missing/null.
		var id, text, speaker *string
		var start, end *uint64
		if !decodeV1Fields(raw, map[string]any{
			"id": &id, "startMs": &start, "endMs": &end, "text": &text, "speaker": &speaker,
		}) || id == nil || start == nil || end == nil || text == nil {
			return nil, ErrProtocol
		}
		output = append(output, domain.Segment{ID: *id, StartMS: *start, EndMS: *end, Text: *text, Speaker: speaker})
	}
	return output, nil
}

type Progress struct {
	ProtocolVersion uint32  `json:"protocolVersion"`
	Revision        *uint64 `json:"revision"`
	Stage           string  `json:"stage"`
	StatusMessage   *string `json:"statusMessage"`
	FailureReason   *string `json:"failureReason"`
	ProgressPercent *uint32 `json:"progressPercent"`
}

func ParseProgress(data []byte) (Progress, error) {
	v, err := version(data)
	if err != nil {
		return Progress{}, err
	}
	if v == 1 {
		var legacy struct {
			Stage           *string
			StatusMessage   *string
			FailureReason   *string
			ProgressPercent *uint32
		}
		if !decodeV1Fields(data, map[string]any{
			"stage": &legacy.Stage, "statusMessage": &legacy.StatusMessage,
			"failureReason": &legacy.FailureReason, "progressPercent": &legacy.ProgressPercent,
		}) || legacy.Stage == nil {
			return Progress{}, ErrProtocol
		}
		return Progress{ProtocolVersion: 1, Stage: *legacy.Stage, StatusMessage: legacy.StatusMessage, FailureReason: legacy.FailureReason, ProgressPercent: legacy.ProgressPercent}, nil
	}
	if err := validate("progress", data); err != nil {
		return Progress{}, err
	}
	var result Progress
	if json.Unmarshal(data, &result) != nil || result.Stage == "" {
		return result, ErrProtocol
	}
	result.ProtocolVersion = v
	return result, nil
}

// The original V1 reader first builds serde_json::Value: exact duplicate keys
// keep their final value, then typed fields match case-sensitively. Decode only
// named scalar/array slots here; nested segments use the same boundary above.
// Unknown extensions, including case aliases, never enter typed decoding.
func decodeV1Fields(data []byte, destinations map[string]any) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return false
	}
	for name, destination := range destinations {
		if raw, exists := fields[name]; exists && json.Unmarshal(raw, destination) != nil {
			return false
		}
	}
	return true
}

type Event struct {
	Type      string  `json:"type"`
	Level     string  `json:"level"`
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	Revision  *uint64 `json:"revision"`
	Timestamp *string `json:"timestamp"`
}

func ParseEvent(line []byte) (Event, error) {
	if err := validate("event", line); err != nil {
		return Event{}, err
	}
	var event Event
	if json.Unmarshal(line, &event) != nil {
		return event, ErrProtocol
	}
	return event, nil
}

// ProgressFence is owned by one attempt. A retry creates a new fence, avoiding
// a process-global cache of paths and preserving monotonic revisions per run.
type ProgressFence struct {
	mu       sync.Mutex
	revision uint64
	seen     bool
}

func (f *ProgressFence) Accept(progress Progress) error {
	if progress.ProtocolVersion != 2 {
		return nil
	}
	if progress.Revision == nil {
		return ErrProtocol
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen && *progress.Revision < f.revision {
		return ErrStale
	}
	f.revision, f.seen = *progress.Revision, true
	return nil
}
