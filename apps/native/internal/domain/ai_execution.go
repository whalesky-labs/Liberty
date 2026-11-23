package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

var ErrSummaryExecution = errors.New("ai_execution_invalid")
var ErrSummaryLease = errors.New("ai_execution_stale")
var ErrSummaryRunning = errors.New("ai_execution_already_running")
var ErrSummaryClosed = errors.New("ai_service_closed")
var ErrSummaryEndpoint = errors.New("ai_model_endpoint_changed")

// SummaryRequest never enters persistence. Secret masks JSON and formatting.
type SummaryRequest struct {
	BaseURL, Model, SystemPrompt, UserPrompt string
	Credential                               Secret
}

// SummarySnapshotJob preserves the legacy flattened MeetingJob JSON contract.
type SummarySnapshotJob struct {
	JobSummary
	Transcript  []Segment    `json:"transcriptSegments"`
	Speakers    []Segment    `json:"speakerSegments"`
	Summary     Summary      `json:"summary"`
	SummaryRuns []SummaryRun `json:"summaryRuns"`
	ProcessLog  *string      `json:"processLog"`
}

func SnapshotSummaryJob(r Result) SummarySnapshotJob {
	j := r.Job
	j.SourceFiles = append([]SourceFile{}, j.SourceFiles...)
	j.Warnings = append([]RunnerWarning{}, j.Warnings...)
	j.Hotwords = append([]string{}, j.Hotwords...)
	j.ExportFormats = append([]string{}, j.ExportFormats...)
	return SummarySnapshotJob{JobSummary: j, Transcript: append([]Segment{}, r.Transcript...), Speakers: append([]Segment{}, r.Speakers...), Summary: r.Summary, SummaryRuns: append([]SummaryRun{}, r.SummaryRuns...), ProcessLog: r.ProcessLog}
}
func (j SummarySnapshotJob) Result() Result {
	return Result{Job: j.JobSummary, Transcript: j.Transcript, Speakers: j.Speakers, Summary: j.Summary, SummaryRuns: j.SummaryRuns, ProcessLog: j.ProcessLog}
}

type SummaryExecutionSnapshot struct {
	SchemaVersion    uint32             `json:"schemaVersion"`
	Job              SummarySnapshotJob `json:"job"`
	ModelBaseURL     string             `json:"modelBaseUrl"`
	ModelName        string             `json:"modelName"`
	TemplateID       string             `json:"templateId"`
	Members          []MeetingMember    `json:"members"`
	RequiredSpeakers []string           `json:"requiredSpeakers"`
	FallbackTitle    string             `json:"fallbackTitle"`
	SystemPrompt     string             `json:"systemPrompt"`
}
type SummaryLease struct {
	JobID, RunID     string
	AttemptID, Token int64
}

func (l SummaryLease) Validate() error {
	if ValidateJobID(l.JobID) != nil || l.RunID == "" || l.AttemptID < 1 || l.Token < 1 {
		return ErrSummaryLease
	}
	return nil
}

type SummaryChunk struct {
	Index                               int
	SHA256, UserPrompt, Raw, Structured string
	Completed                           bool
}
type SummaryExecution struct {
	Run                                                                SummaryRun
	Lease                                                              SummaryLease
	TranscriptRevision, TranscriptSHA256, TranscriptJSON, SnapshotJSON string
	ChunkCount                                                         int
}
type SummarySeed struct {
	Execution SummaryExecution
	Chunks    []SummaryChunk
}
type SummaryCompletion struct {
	Raw         string
	Result      SummaryResult
	Minutes     MinutesPayload
	Diagnostics string
	CompletedAt string
}

func SummaryHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func (e SummaryExecution) Snapshot() (SummaryExecutionSnapshot, error) {
	var s SummaryExecutionSnapshot
	if json.Unmarshal([]byte(e.SnapshotJSON), &s) != nil || s.SchemaVersion != 1 || s.Job.ID != e.Run.JobID || s.Job.Source != "local" || e.ChunkCount < 1 || s.TemplateID != e.Run.TemplateID || s.ModelBaseURL == "" || s.ModelName == "" || s.SystemPrompt == "" {
		return s, ErrSummaryExecution
	}
	transcript, err := SummaryJSON(s.Job.Result().PrimarySegments())
	if err != nil {
		return s, ErrSummaryExecution
	}
	hash := SummaryHash(transcript)
	if hash != e.TranscriptSHA256 || e.TranscriptRevision != "sha256:"+hash || SummaryHash([]byte(e.TranscriptJSON)) != hash {
		return s, ErrSummaryExecution
	}
	return s, nil
}
