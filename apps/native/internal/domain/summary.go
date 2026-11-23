package domain

import "strings"

type Summary struct {
	Overview    string   `json:"overview"`
	Topics      []string `json:"topics"`
	Decisions   []string `json:"decisions"`
	ActionItems []string `json:"actionItems"`
	Risks       []string `json:"risks"`
	FollowUps   []string `json:"followUps"`
}
type SummaryAction struct {
	Task    string `json:"task"`
	Owner   string `json:"owner"`
	DueDate string `json:"dueDate"`
}
type SummaryResult struct {
	Title       string          `json:"title"`
	Overview    string          `json:"overview"`
	Topics      []string        `json:"topics"`
	Decisions   []string        `json:"decisions"`
	ActionItems []SummaryAction `json:"actionItems"`
	Risks       []string        `json:"risks"`
	FollowUps   []string        `json:"followUps"`
}
type SummaryRun struct {
	ID                string          `json:"id"`
	JobID             string          `json:"jobId"`
	ModelConfigID     string          `json:"modelConfigId"`
	TemplateID        string          `json:"templateId"`
	IncludeSpeaker    bool            `json:"includeSpeaker"`
	IncludeTimestamp  bool            `json:"includeTimestamp"`
	ExtraInstructions string          `json:"extraInstructions"`
	Status            string          `json:"status"`
	ErrorMessage      *string         `json:"errorMessage"`
	PromptPreview     *string         `json:"promptPreview"`
	RawResponse       *string         `json:"rawResponse"`
	Result            *SummaryResult  `json:"result"`
	MinutesPayload    *MinutesPayload `json:"minutesPayload"`
	CreatedAt         string          `json:"createdAt"`
	UpdatedAt         string          `json:"updatedAt"`
}
type MinutesInfo struct {
	MeetingName     string `json:"meetingName"`
	MeetingTime     string `json:"meetingTime"`
	MeetingLocation string `json:"meetingLocation"`
	Recorder        string `json:"recorder"`
	Attendees       string `json:"attendees"`
	Absentees       string `json:"absentees"`
	Host            string `json:"host"`
	Reviewer        string `json:"reviewer"`
}
type MinutesParticipant struct {
	SpeakerLabel  string `json:"speakerLabel"`
	MemberID      string `json:"memberId"`
	ResolvedName  string `json:"resolvedName"`
	Department    string `json:"department"`
	SortOrder     int64  `json:"sortOrder"`
	OriginalIndex uint64 `json:"originalIndex"`
	MatchStatus   string `json:"matchStatus"`
}
type MinutesReport struct {
	MinutesParticipant
	WeeklySummary []string `json:"weeklySummary"`
	NextWeekPlan  []string `json:"nextWeekPlan"`
	Summary       []string `json:"summary"`
}
type MinutesPayload struct {
	SchemaVersion      uint32               `json:"schemaVersion"`
	MeetingInfoSource  string               `json:"meetingInfoSource"`
	TemplateID         string               `json:"templateId"`
	SourceSummaryRunID *string              `json:"sourceSummaryRunId"`
	MeetingInfo        MinutesInfo          `json:"meetingInfo"`
	Participants       []MinutesParticipant `json:"participants"`
	SpeakerReports     []MinutesReport      `json:"speakerReports"`
	Topics             []string             `json:"topics"`
	GlobalSummary      []string             `json:"globalSummary"`
}

func EmptySummary() Summary {
	return Summary{Topics: []string{}, Decisions: []string{}, ActionItems: []string{}, Risks: []string{}, FollowUps: []string{}}
}

// HydrateSummary mirrors the existing load_job projection. A selected failed or
// corrupt run cannot hide a valid completed result. With no completed run, the
// most recently updated run is selected (including its result, if present).
func (r *Result) HydrateSummary() {
	var active *SummaryRun
	for i := range r.SummaryRuns {
		run := &r.SummaryRuns[i]
		if run.Status == "completed" && run.Result != nil && r.Job.ActiveSummaryRunID != nil && run.ID == *r.Job.ActiveSummaryRunID {
			active = run
			break
		}
	}
	if active == nil {
		for i := range r.SummaryRuns {
			run := &r.SummaryRuns[i]
			if run.Status == "completed" && run.Result != nil {
				active = run
				break
			}
		}
	}
	if active == nil {
		for i := range r.SummaryRuns {
			run := &r.SummaryRuns[i]
			if active == nil || run.UpdatedAt >= active.UpdatedAt {
				active = run
			}
		}
	}
	r.Job.ActiveSummaryRunID = nil
	r.Summary = EmptySummary()
	if active != nil {
		id := active.ID
		r.Job.ActiveSummaryRunID = &id
		if s := active.Result; s != nil {
			r.Summary = Summary{Overview: s.Overview, Topics: s.Topics, Decisions: s.Decisions, Risks: s.Risks, FollowUps: s.FollowUps, ActionItems: []string{}}
			for _, item := range s.ActionItems {
				suffix := []string{}
				for _, part := range []string{item.Owner, item.DueDate} {
					if strings.TrimSpace(part) != "" {
						suffix = append(suffix, part)
					}
				}
				task := item.Task
				if len(suffix) > 0 {
					task += "（" + strings.Join(suffix, " / ") + "）"
				}
				r.Summary.ActionItems = append(r.Summary.ActionItems, task)
			}
		}
	}
	if len(r.SummaryRuns) == 0 && r.Job.SummaryStatus == "queued" {
		r.Job.SummaryStatus = "idle"
	}
}
