package domain

import (
	"errors"
	"sort"
	"strings"
)

var (
	ErrSummaryMissing    = errors.New("summary_export_missing")
	ErrSummaryIncomplete = errors.New("summary_export_incomplete")
	ErrMinutesSource     = errors.New("minutes_summary_source_mismatch")
	ErrMinutesCoverage   = errors.New("minutes_speaker_coverage_incomplete")
)

func memberForName(members []MeetingMember, name string) *MeetingMember {
	name = NormalizeMemberName(name)
	for i := range members {
		if NormalizeMemberName(members[i].Name) == name {
			return &members[i]
		}
	}
	return nil
}
func uniqueItems(target, items []string) []string {
	for _, item := range items {
		found := false
		for _, existing := range target {
			found = found || existing == item
		}
		if !found {
			target = append(target, item)
		}
	}
	return target
}
func collectMinutesSpeakers(r Result) []string {
	names := []string{}
	for _, s := range r.PrimarySegments() {
		if s.Speaker != nil {
			if name := NormalizeMemberName(*s.Speaker); name != "" {
				names = uniqueItems(names, []string{name})
			}
		}
	}
	return names
}
func sortMinutesBlocks(blocks []MinutesSpeechBlock, members []MeetingMember) {
	for i := range blocks {
		if m := memberForName(members, blocks[i].Name); m != nil {
			if strings.TrimSpace(m.Department) != "" {
				blocks[i].Department = strings.TrimSpace(m.Department)
			}
			blocks[i].Name = strings.TrimSpace(m.Name)
		}
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		a, b := memberForName(members, blocks[i].Name), memberForName(members, blocks[j].Name)
		if a != nil && b == nil {
			return true
		}
		if a == nil && b != nil {
			return false
		}
		if a != nil && b != nil && a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		return blocks[i].OriginalIndex < blocks[j].OriginalIndex
	})
}
func blockParticipant(block MinutesSpeechBlock, members []MeetingMember) MinutesParticipant {
	p := MinutesParticipant{SpeakerLabel: strings.TrimSpace(block.Name), ResolvedName: strings.TrimSpace(block.Name), Department: strings.TrimSpace(block.Department), OriginalIndex: block.OriginalIndex, MatchStatus: "unmatched"}
	if m := memberForName(members, block.Name); m != nil {
		p.MemberID, p.ResolvedName, p.SortOrder, p.MatchStatus = m.ID, strings.TrimSpace(m.Name), m.SortOrder, "matched"
		if strings.TrimSpace(m.Department) != "" {
			p.Department = strings.TrimSpace(m.Department)
		}
	}
	return p
}
func ReconstructMinutes(r Result, s SummaryResult, members []MeetingMember, template string, run *string) (MinutesPayload, error) {
	parsed := ParseMinutesOverview(s.Overview)
	metadata := ProjectMinutesMetadata(nil, parsed.Info)
	blocks := []MinutesSpeechBlock{}
	byName := map[string]int{}
	for _, block := range parsed.SpeechBlocks {
		name := NormalizeMemberName(block.Name)
		if name == "" {
			continue
		}
		if i, ok := byName[name]; ok {
			b := &blocks[i]
			b.WeeklySummary = uniqueItems(b.WeeklySummary, block.WeeklySummary)
			b.NextWeekPlan = uniqueItems(b.NextWeekPlan, block.NextWeekPlan)
			b.Summary = uniqueItems(b.Summary, block.Summary)
			if strings.TrimSpace(b.Department) == "" {
				b.Department = strings.TrimSpace(block.Department)
			}
		} else {
			block.Name = name
			block.OriginalIndex = uint64(len(blocks))
			byName[name] = len(blocks)
			blocks = append(blocks, block)
		}
	}
	names := collectMinutesSpeakers(r)
	if len(blocks) == 0 {
		for i, name := range names {
			blocks = append(blocks, newSpeechBlock(name, uint64(i)))
		}
	}
	sortMinutesBlocks(blocks, members)
	p := MinutesPayload{SchemaVersion: 2, MeetingInfoSource: metadata.Source, TemplateID: strings.TrimSpace(template), SourceSummaryRunID: run, MeetingInfo: metadata.Info, Participants: []MinutesParticipant{}, SpeakerReports: []MinutesReport{}, Topics: append([]string{}, s.Topics...), GlobalSummary: parsed.ClosingSummary}
	if len(p.Topics) == 0 && !missingMinutesValue(parsed.Topics) {
		for _, topic := range strings.FieldsFunc(parsed.Topics, func(r rune) bool { return strings.ContainsRune("、；;", r) }) {
			if topic = strings.TrimSpace(topic); topic != "" {
				p.Topics = append(p.Topics, topic)
			}
		}
	}
	var firstRecovered uint64
	for _, block := range blocks {
		participant := blockParticipant(block, members)
		p.Participants = append(p.Participants, participant)
		p.SpeakerReports = append(p.SpeakerReports, MinutesReport{MinutesParticipant: participant, WeeklySummary: block.WeeklySummary, NextWeekPlan: block.NextWeekPlan, Summary: block.Summary})
		firstRecovered = max(firstRecovered, block.OriginalIndex+1)
	}
	for i, name := range names {
		hasParticipant, hasReport := false, false
		for _, v := range p.Participants {
			hasParticipant = hasParticipant || matchesMinutesSpeaker(v, name)
		}
		for _, v := range p.SpeakerReports {
			hasReport = hasReport || matchesMinutesSpeaker(v.MinutesParticipant, name)
		}
		if hasParticipant && hasReport {
			continue
		}
		block := newSpeechBlock(name, firstRecovered+uint64(i))
		participant := blockParticipant(block, members)
		participant.MatchStatus = "missing_from_ai"
		if !hasParticipant {
			p.Participants = append(p.Participants, participant)
		}
		if !hasReport {
			p.SpeakerReports = append(p.SpeakerReports, MinutesReport{MinutesParticipant: participant, WeeklySummary: []string{}, NextWeekPlan: []string{}, Summary: []string{}})
		}
	}
	markMissingMinutesSpeakers(r, &p)
	hasSpeechContent := false
	for _, report := range p.SpeakerReports {
		hasSpeechContent = hasSpeechContent || len(report.WeeklySummary) > 0 || len(report.NextWeekPlan) > 0 || len(report.Summary) > 0
	}
	if !hasSpeechContent && len(p.GlobalSummary) == 0 {
		p.GlobalSummary = minutesFallbackContent(parsed.FallbackOverview)
	}
	return p, ValidateMinutesCoverage(r, p)
}
func matchesMinutesSpeaker(p MinutesParticipant, name string) bool {
	return NormalizeMemberName(p.SpeakerLabel) == name || NormalizeMemberName(p.ResolvedName) == name
}
func ValidateMinutesCoverage(r Result, p MinutesPayload) error {
	if r.Job.DiarizationStatus != "completed" {
		return nil
	}
	for _, s := range r.Speakers {
		if s.Speaker == nil {
			continue
		}
		name := NormalizeMemberName(*s.Speaker)
		if name == "" {
			continue
		}
		participant, report := false, false
		for _, v := range p.Participants {
			participant = participant || matchesMinutesSpeaker(v, name)
		}
		for _, v := range p.SpeakerReports {
			report = report || matchesMinutesSpeaker(v.MinutesParticipant, name)
		}
		if !participant || !report {
			return ErrMinutesCoverage
		}
	}
	return nil
}
func markMissingMinutesSpeakers(r Result, p *MinutesPayload) {
	if r.Job.DiarizationStatus != "completed" {
		return
	}
	names := map[string]bool{}
	for _, s := range r.Speakers {
		if s.Speaker != nil {
			if name := NormalizeMemberName(*s.Speaker); name != "" {
				names[name] = true
			}
		}
	}
	for i := range p.SpeakerReports {
		report := &p.SpeakerReports[i]
		name := report.SpeakerLabel
		if strings.TrimSpace(name) == "" {
			name = report.ResolvedName
		}
		name = NormalizeMemberName(name)
		if names[name] && len(report.WeeklySummary) == 0 && len(report.NextWeekPlan) == 0 && len(report.Summary) == 0 {
			report.MatchStatus = "missing_from_ai"
			for j := range p.Participants {
				if matchesMinutesSpeaker(p.Participants[j], name) {
					p.Participants[j].MatchStatus = "missing_from_ai"
					break
				}
			}
		}
	}
}

type SummaryExportSource struct {
	Result               SummaryResult  `json:"result"`
	MinutesPayload       MinutesPayload `json:"minutesPayload"`
	RunID                *string        `json:"runId"`
	ReconstructedPayload bool           `json:"reconstructedPayload"`
}

func HasSummaryContent(s Summary) bool {
	return strings.TrimSpace(s.Overview) != "" || len(s.Topics) > 0 || len(s.Decisions) > 0 || len(s.ActionItems) > 0 || len(s.Risks) > 0 || len(s.FollowUps) > 0
}
func LegacySummaryResult(r Result) SummaryResult {
	s := r.Summary
	value := SummaryResult{Title: r.Job.Title, Overview: s.Overview, Topics: append([]string{}, s.Topics...), Decisions: append([]string{}, s.Decisions...), ActionItems: []SummaryAction{}, Risks: append([]string{}, s.Risks...), FollowUps: append([]string{}, s.FollowUps...)}
	for _, item := range s.ActionItems {
		value.ActionItems = append(value.ActionItems, SummaryAction{Task: item})
	}
	return value
}
func ResolveSummaryExport(r Result, members []MeetingMember, requested string) (SummaryExportSource, error) {
	id := strings.TrimSpace(requested)
	if id == "" && r.Job.ActiveSummaryRunID != nil {
		id = *r.Job.ActiveSummaryRunID
	}
	if id == "" {
		if !HasSummaryContent(r.Summary) {
			return SummaryExportSource{}, ErrSummaryMissing
		}
		result := LegacySummaryResult(r)
		p, err := ReconstructMinutes(r, result, members, "", nil)
		if err != nil {
			return SummaryExportSource{}, err
		}
		makeMinutesAuthoritative(&result)
		return SummaryExportSource{Result: result, MinutesPayload: p}, nil
	}
	for _, run := range r.SummaryRuns {
		if run.ID != id {
			continue
		}
		if run.Status != "completed" {
			return SummaryExportSource{}, ErrSummaryIncomplete
		}
		if run.Result == nil {
			return SummaryExportSource{}, ErrSummaryMissing
		}
		source := SummaryExportSource{Result: *run.Result, RunID: &id, ReconstructedPayload: run.MinutesPayload == nil}
		if run.MinutesPayload != nil {
			source.MinutesPayload = cloneMinutes(*run.MinutesPayload)
			if ref := source.MinutesPayload.SourceSummaryRunID; ref != nil && strings.TrimSpace(*ref) != "" && strings.TrimSpace(*ref) != id {
				return SummaryExportSource{}, ErrMinutesSource
			}
			if err := ValidateMinutesCoverage(r, source.MinutesPayload); err != nil {
				return SummaryExportSource{}, err
			}
		} else {
			var err error
			source.MinutesPayload, err = ReconstructMinutes(r, source.Result, members, run.TemplateID, &id)
			if err != nil {
				return SummaryExportSource{}, err
			}
		}
		markMissingMinutesSpeakers(r, &source.MinutesPayload)
		makeMinutesAuthoritative(&source.Result)
		return source, nil
	}
	return SummaryExportSource{}, ErrSummaryMissing
}

// Clone returns owned collections, serializing absent collections as [] like
// Rust Vec rather than JSON null (which the original reader rejects).
func (p MinutesPayload) Clone() MinutesPayload { return cloneMinutes(p) }

func cloneMinutes(p MinutesPayload) MinutesPayload {
	p.Participants = append([]MinutesParticipant{}, p.Participants...)
	p.SpeakerReports = append([]MinutesReport{}, p.SpeakerReports...)
	for i := range p.SpeakerReports {
		v := &p.SpeakerReports[i]
		v.WeeklySummary = append([]string{}, v.WeeklySummary...)
		v.NextWeekPlan = append([]string{}, v.NextWeekPlan...)
		v.Summary = append([]string{}, v.Summary...)
	}
	p.Topics = append([]string{}, p.Topics...)
	p.GlobalSummary = append([]string{}, p.GlobalSummary...)
	return p
}
func makeMinutesAuthoritative(s *SummaryResult) {
	lines := []string{}
	appendSection := func(heading string, items []string) {
		nonempty := []string{}
		for _, v := range items {
			if v = strings.TrimSpace(v); v != "" {
				nonempty = append(nonempty, v)
			}
		}
		if len(nonempty) > 0 {
			lines = append(lines, heading+"：")
			lines = append(lines, nonempty...)
		}
	}
	appendSection("决策", s.Decisions)
	actions := []string{}
	for _, v := range s.ActionItems {
		if strings.TrimSpace(v.Task) != "" {
			owner, due := strings.TrimSpace(v.Owner), strings.TrimSpace(v.DueDate)
			if owner == "" {
				owner = "待定"
			}
			if due == "" {
				due = "待定"
			}
			actions = append(actions, "任务："+strings.TrimSpace(v.Task)+"；负责人："+owner+"；截止日期："+due)
		}
	}
	appendSection("行动项", actions)
	appendSection("风险", s.Risks)
	appendSection("跟进事项", s.FollowUps)
	s.Overview = strings.Join(lines, "\n")
}
