// Package domain contains desktop-independent Liberty business policies.
package domain

import "strings"

type Segment struct {
	ID      string  `json:"id"`
	StartMS uint64  `json:"startMs"`
	EndMS   uint64  `json:"endMs"`
	Speaker *string `json:"speaker"`
	Text    string  `json:"text"`
}

// JobSummary is a persisted queue projection, not a fully hydrated MeetingJob DTO.
// Runner progress files and active summary selection belong to separate use cases.
type JobSummary struct {
	ID                        string          `json:"id"`
	Source                    string          `json:"source"`
	Title                     string          `json:"title"`
	CreatedAt                 string          `json:"createdAt"`
	OverallStatus             string          `json:"overallStatus"`
	DiarizationStatus         string          `json:"diarizationStatus"`
	DurationMinutes           uint32          `json:"durationMinutes"`
	SourceFiles               []SourceFile    `json:"sourceFiles"`
	Lang                      string          `json:"lang"`
	EnableSpeaker             bool            `json:"enableSpeaker"`
	RunnerProtocolVersion     *uint32         `json:"runnerProtocolVersion"`
	ASRBackend                string          `json:"asrBackend"`
	Warnings                  []RunnerWarning `json:"warnings"`
	SummaryTemplate           string          `json:"summaryTemplate"`
	UploadStatus              string          `json:"uploadStatus"`
	ASRStatus                 string          `json:"asrStatus"`
	SummaryStatus             string          `json:"summaryStatus"`
	ProcessingStartedAtMS     *uint64         `json:"processingStartedAtMs"`
	ProcessingFinishedAtMS    *uint64         `json:"processingFinishedAtMs"`
	ProcessingDurationSeconds *uint32         `json:"processingDurationSeconds"`
	ProgressPercent           *float64        `json:"progressPercent"`
	ProgressMessage           *string         `json:"progressMessage"`
	FailureReason             *string         `json:"failureReason"`
	ActiveSummaryRunID        *string         `json:"activeSummaryRunId"`
	LastExportedAt            *string         `json:"lastExportedAt"`
	Hotwords                  []string        `json:"hotwords"`
	ExportFormats             []string        `json:"exportFormats"`
}

type SourceFile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeLabel string `json:"sizeLabel"`
}

type Result struct {
	// Native-only evidence state, not part of remote or historical DTOs.
	SummarySourceChanged bool          `json:"-"`
	Draft                *MeetingDraft `json:"-"`
	Summary              Summary       `json:"summary"`
	SummaryRuns          []SummaryRun  `json:"summaryRuns"`
	ProcessLog           *string       `json:"processLog"`
	Job                  JobSummary    `json:"job"`
	Transcript           []Segment     `json:"transcriptSegments"`
	Speakers             []Segment     `json:"speakerSegments"`
}

// PrimarySegments is the authoritative projection used for summaries and exports.
// Keep aligned with shared/services/meeting/transcript.ts.
func (r Result) PrimarySegments() []Segment {
	if r.Job.DiarizationStatus == "completed" && len(r.Speakers) > 0 {
		return r.Speakers
	}
	return r.Transcript
}

// DisplaySegments preserves the existing, display-only legacy speaker fallback.
// A legacy speaker projection never becomes verified merely because it is shown.
func (r Result) DisplaySegments() []Segment {
	if r.Job.DiarizationStatus == "completed" && len(r.Speakers) > 0 {
		return r.Speakers
	}
	if r.Job.DiarizationStatus != "legacy_unverified" || len(r.Speakers) == 0 || len(r.Speakers) != len(r.Transcript) {
		return r.Transcript
	}
	for i, speaker := range r.Speakers {
		text := r.Transcript[i]
		if speaker.Speaker == nil || strings.TrimSpace(*speaker.Speaker) == "" ||
			speaker.StartMS != text.StartMS || speaker.EndMS != text.EndMS || speaker.Text != text.Text {
			return r.Transcript
		}
	}
	return r.Speakers
}
