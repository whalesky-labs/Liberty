package domain

import (
	"errors"
	"strings"
)

var ErrRunnerResult = errors.New("runner_result_invalid")

type RunnerWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RunnerResult struct {
	ProtocolVersion   uint32          `json:"protocolVersion"`
	ASRBackend        string          `json:"asrBackend"`
	DiarizationStatus string          `json:"diarizationStatus"`
	Warnings          []RunnerWarning `json:"warnings"`
	DurationMinutes   *uint32         `json:"durationMinutes"`
	Transcript        []Segment       `json:"transcriptSegments"`
	Speakers          []Segment       `json:"speakerSegments"`
}

func (r RunnerResult) Validate() error {
	if len(r.Transcript) == 0 {
		return ErrRunnerResult
	}
	switch r.DiarizationStatus {
	case "completed":
		if len(r.Speakers) == 0 {
			return ErrRunnerResult
		}
		for _, segment := range r.Speakers {
			if segment.Speaker == nil || strings.TrimSpace(*segment.Speaker) == "" || *segment.Speaker == "Speaker 1" {
				return ErrRunnerResult
			}
		}
	case "disabled", "unavailable", "failed":
		if len(r.Speakers) != 0 {
			return ErrRunnerResult
		}
	case "legacy_unverified":
	default:
		return ErrRunnerResult
	}
	return nil
}

func (r RunnerResult) Duration(fallback uint32) uint32 {
	if r.DurationMinutes != nil && *r.DurationMinutes > 0 {
		return *r.DurationMinutes
	}
	var end uint64
	for _, segment := range r.Transcript {
		end = max(end, segment.EndMS)
	}
	for _, segment := range r.Speakers {
		end = max(end, segment.EndMS)
	}
	if end == 0 {
		return fallback
	}
	minutes := end / 60000
	if end%60000 > 0 {
		minutes++
	}
	return uint32(min(minutes, uint64(^uint32(0))))
}
