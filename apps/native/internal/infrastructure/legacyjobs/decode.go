package legacyjobs

import (
	"encoding/json"
	"math"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/runner"
)

type legacyFile struct {
	domain.SourceFile
	Path *string `json:"path"`
}
type legacySnapshot struct {
	domain.JobSummary
	Files       []legacyFile        `json:"sourceFiles"`
	Transcript  []domain.Segment    `json:"transcriptSegments"`
	Speakers    []domain.Segment    `json:"speakerSegments"`
	Summary     domain.Summary      `json:"summary"`
	SummaryRuns []domain.SummaryRun `json:"summaryRuns"`
	ProcessLog  *string             `json:"processLog"`
	PythonPath  *string             `json:"pythonPath"`
	RunnerPath  *string             `json:"runnerScriptPath"`
}

func requiredFields(data []byte, keys ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return false
	}
	for _, key := range keys {
		if value, ok := fields[key]; !ok || string(value) == "null" {
			return false
		}
	}
	return true
}
func validSegments(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return false
	}
	for _, item := range items {
		if !requiredFields(item, "id", "startMs", "endMs", "text") {
			return false
		}
	}
	return true
}
func decodeJob(data []byte) (application.LegacyJob, error) {
	var out application.LegacyJob
	if !requiredFields(data, "id", "title", "durationMinutes", "createdAt", "lang", "enableSpeaker", "summaryTemplate", "uploadStatus", "asrStatus", "summaryStatus", "overallStatus") {
		return out, errInvalid
	}
	value := legacySnapshot{JobSummary: domain.JobSummary{Source: "local", ASRBackend: "unknown", DiarizationStatus: "legacy_unverified"}, Summary: domain.EmptySummary()}
	if !decodeLegacy(data, &value) || domain.ValidateJobID(value.ID) != nil {
		return out, errInvalid
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	if !validSegments(fields["transcriptSegments"]) || !validSegments(fields["speakerSegments"]) {
		return out, errInvalid
	}
	for _, field := range []string{"sourceFiles", "warnings", "summaryRuns"} {
		var items []json.RawMessage
		if raw, ok := fields[field]; ok {
			if json.Unmarshal(raw, &items) != nil {
				return out, errInvalid
			}
		}
		for _, item := range items {
			keys := []string{"id", "name", "sizeLabel", "kind"}
			if field == "warnings" {
				keys = []string{"code", "message"}
			}
			if field == "summaryRuns" {
				keys = []string{"id", "jobId", "includeSpeaker", "includeTimestamp", "status", "createdAt", "updatedAt"}
			}
			if !requiredFields(item, keys...) {
				return out, errInvalid
			}
		}
	}
	if !known(value.ASRBackend, "unknown", "funasr", "sherpa-onnx") || !known(value.DiarizationStatus, "disabled", "pending", "processing", "completed", "unavailable", "failed", "legacy_unverified") {
		return out, errInvalid
	}
	for _, stamp := range []*uint64{value.ProcessingStartedAtMS, value.ProcessingFinishedAtMS} {
		if stamp != nil && *stamp > math.MaxInt64 {
			return out, errInvalid
		}
	}
	for _, segments := range [][]domain.Segment{value.Transcript, value.Speakers} {
		for _, s := range segments {
			if s.StartMS > math.MaxInt64 || s.EndMS > math.MaxInt64 {
				return out, errInvalid
			}
		}
	}
	if value.ProgressPercent != nil && (*value.ProgressPercent < 0 || *value.ProgressPercent > math.MaxUint32 || math.Trunc(*value.ProgressPercent) != *value.ProgressPercent) {
		return out, errInvalid
	}
	value.JobSummary.SourceFiles = []domain.SourceFile{}
	for _, file := range value.Files {
		value.JobSummary.SourceFiles = append(value.JobSummary.SourceFiles, file.SourceFile)
		out.Sources = append(out.Sources, application.LegacySourceFile{Metadata: file.SourceFile, Path: file.Path})
	}
	out.Result = domain.Result{Job: value.JobSummary, Transcript: value.Transcript, Speakers: value.Speakers, Summary: value.Summary, ProcessLog: value.ProcessLog}
	out.PythonPath, out.RunnerPath = value.PythonPath, value.RunnerPath
	return out, nil
}
func known(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
func applyProgress(job *application.LegacyJob, p runner.Progress) {
	j := &job.Result.Job
	switch p.Stage {
	case "queued", "transcribing", "speaker_processing":
		j.UploadStatus, j.ASRStatus, j.SummaryStatus, j.OverallStatus = "uploaded", p.Stage, "idle", p.Stage
	case "completed":
		j.UploadStatus, j.ASRStatus, j.OverallStatus = "uploaded", "completed", "completed"
		if j.SummaryStatus == "queued" {
			j.SummaryStatus = "idle"
		}
	case "failed":
		j.ASRStatus, j.OverallStatus = "failed", "failed"
	}
	if p.Stage == "failed" {
		if p.FailureReason != nil {
			j.FailureReason = p.FailureReason
		} else if p.StatusMessage != nil {
			j.FailureReason = p.StatusMessage
		}
	}
}
func applyResult(job *application.LegacyJob, data []byte) {
	var value struct {
		DurationMinutes *uint32           `json:"durationMinutes"`
		Transcript      *[]domain.Segment `json:"transcriptSegments"`
		Speakers        *[]domain.Segment `json:"speakerSegments"`
		FailureReason   *string           `json:"failureReason"`
	}
	if !decodeLegacy(data, &value) {
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return
	}
	for _, key := range []string{"transcriptSegments", "speakerSegments"} {
		if raw, ok := fields[key]; ok && string(raw) != "null" && !validSegments(raw) {
			return
		}
	}
	for _, segments := range []*[]domain.Segment{value.Transcript, value.Speakers} {
		if segments != nil {
			for _, s := range *segments {
				if s.StartMS > math.MaxInt64 || s.EndMS > math.MaxInt64 {
					return
				}
			}
		}
	}
	if len(job.Result.Transcript) == 0 && value.Transcript != nil {
		job.Result.Transcript = *value.Transcript
	}
	if len(job.Result.Speakers) == 0 && value.Speakers != nil {
		job.Result.Speakers = *value.Speakers
	}
	if job.Result.Job.DurationMinutes == 0 && value.DurationMinutes != nil {
		job.Result.Job.DurationMinutes = *value.DurationMinutes
	}
	if job.Result.Job.FailureReason == nil {
		job.Result.Job.FailureReason = value.FailureReason
	}
}
