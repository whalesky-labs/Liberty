package domain

// ManualMinutesDocument preserves all six user-edited sections in the existing
// formal template. Historical AI exports explicitly selected by run stay intact.
func ManualMinutesDocument(r Result) MinutesDocument {
	s := r.Summary
	info := MinutesInfo{MeetingName: r.Job.Title}
	if r.Draft != nil && r.Draft.SourceRunID != "" {
		for _, run := range r.SummaryRuns {
			if run.ID == r.Draft.SourceRunID && run.Result != nil {
				parsed := ParseMinutesOverview(run.Result.Overview)
				info = ProjectMinutesMetadata(run.MinutesPayload, parsed.Info).Info
				if info.MeetingName == "" {
					info.MeetingName = r.Job.Title
				}
				break
			}
		}
	}
	blocks := []MinutesSpeechBlock{}
	for _, section := range []struct {
		name  string
		items []string
	}{
		{"会议概述 / Overview", []string{s.Overview}},
		{"主要议题 / Topics", s.Topics},
		{"会议决议 / Decisions", s.Decisions},
		{"待办事项 / Action items", s.ActionItems},
		{"风险 / Risks", s.Risks},
		{"后续跟进 / Follow-ups", s.FollowUps},
	} {
		items := []string{}
		for _, item := range section.items {
			if item != "" {
				items = append(items, item)
			}
		}
		if len(items) > 0 {
			blocks = append(blocks, MinutesSpeechBlock{Name: section.name, Summary: items, WeeklySummary: []string{}, NextWeekPlan: []string{}, OriginalIndex: uint64(len(blocks))})
		}
	}
	return MinutesDocument{Title: info.MeetingName, Info: info, SpeechBlocks: blocks, ClosingSummary: []string{}, FallbackOverview: []string{}}
}
