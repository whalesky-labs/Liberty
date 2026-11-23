package domain

import "strings"

type MinutesDocument struct {
	Title            string               `json:"title"`
	Info             MinutesInfo          `json:"info"`
	Topics           string               `json:"topics"`
	ClosingSummary   []string             `json:"closingSummary"`
	FallbackOverview []string             `json:"fallbackOverview"`
	SpeechBlocks     []MinutesSpeechBlock `json:"speechBlocks"`
}

func BuildMinutesDocument(r Result, source SummaryExportSource, members []MeetingMember) MinutesDocument {
	s, p := source.Result, source.MinutesPayload
	parsed := ParseMinutesOverview(s.Overview)
	aiMetadata := parsed.Info
	if p.SchemaVersion >= 2 && p.MeetingInfoSource == "ai" {
		aiMetadata = p.MeetingInfo
	}
	metadata := ProjectMinutesMetadata(&p, aiMetadata)
	title := strings.TrimSpace(metadata.Info.MeetingName)
	if title == "" {
		title = strings.TrimSpace(s.Title)
	}
	if title == "" {
		title = r.Job.Title
	}
	d := MinutesDocument{Title: title, Info: metadata.Info, ClosingSummary: append([]string{}, p.GlobalSummary...), FallbackOverview: parsed.FallbackOverview, SpeechBlocks: []MinutesSpeechBlock{}}
	if !strings.HasSuffix(d.Title, "会议纪要") {
		d.Title += "会议纪要"
	}
	d.Info.MeetingName = title
	if len(p.Topics) > 0 {
		d.Topics = strings.Join(p.Topics, "；")
	} else {
		d.Topics = strings.Join(s.Topics, "；")
	}
	for _, report := range p.SpeakerReports {
		var member *MeetingMember
		if strings.TrimSpace(report.MemberID) != "" {
			for i := range members {
				if members[i].ID == report.MemberID {
					member = &members[i]
					break
				}
			}
		}
		if member == nil {
			member = memberForName(members, report.ResolvedName)
		}
		if member == nil {
			member = memberForName(members, report.SpeakerLabel)
		}
		name, department := strings.TrimSpace(report.ResolvedName), strings.TrimSpace(report.Department)
		if name == "" {
			name = strings.TrimSpace(report.SpeakerLabel)
		}
		if member != nil {
			name = strings.TrimSpace(member.Name)
			if strings.TrimSpace(member.Department) != "" {
				department = strings.TrimSpace(member.Department)
			}
		}
		d.SpeechBlocks = append(d.SpeechBlocks, MinutesSpeechBlock{Department: department, Name: name, WeeklySummary: append([]string{}, report.WeeklySummary...), NextWeekPlan: append([]string{}, report.NextWeekPlan...), Summary: append([]string{}, report.Summary...), OriginalIndex: report.OriginalIndex})
	}
	sortMinutesBlocks(d.SpeechBlocks, members)
	if len(d.SpeechBlocks) > 0 {
		names := []string{}
		for _, b := range d.SpeechBlocks {
			if name := strings.TrimSpace(b.Name); name != "" {
				names = append(names, name)
			}
		}
		d.Info.Attendees = strings.Join(names, "、")
	}
	hasContent := false
	for _, b := range d.SpeechBlocks {
		hasContent = hasContent || len(b.WeeklySummary) > 0 || len(b.NextWeekPlan) > 0 || len(b.Summary) > 0
	}
	if !hasContent {
		items := append([]string{}, d.ClosingSummary...)
		if len(items) == 0 {
			items = minutesFallbackContent(d.FallbackOverview)
		}
		if len(items) == 0 {
			for _, topic := range s.Topics {
				if topic = strings.TrimSpace(topic); topic != "" {
					items = append(items, topic)
				}
			}
		}
		items = uniqueItems([]string{}, items)
		if len(items) > 0 {
			if len(d.SpeechBlocks) == 0 {
				b := newSpeechBlock("摘要", 0)
				b.Department = "会议纪要"
				d.SpeechBlocks = append(d.SpeechBlocks, b)
			}
			d.SpeechBlocks[0].Summary = items
		}
	}
	return d
}

func minutesFallbackContent(lines []string) []string {
	items := []string{}
	for _, line := range lines {
		metadata := false
		for _, prefix := range []string{"会议名称", "会议时间", "会议地点", "记录人", "出席人员", "缺席人员", "主要议题", "会议主持人", "审阅"} {
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
				value = strings.TrimSpace(value)
				metadata = metadata || strings.HasPrefix(value, "：") || strings.HasPrefix(value, ":")
			}
		}
		label := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line), "：:"))
		_, _, header := ParseMinutesSpeakerHeader(line)
		if metadata || header || label == "发言内容" || label == "上周总结" || label == "本周计划" || label == "总结" {
			continue
		}
		text := strings.TrimSpace(strings.Trim(strings.Trim(strings.TrimSpace(trimMinutesNumber(line)), "【"), "】"))
		if text != "" {
			items = append(items, text)
		}
	}
	return uniqueItems([]string{}, items)
}

func trimMinutesNumber(value string) string {
	value = strings.TrimSpace(value)
	offset := 0
	for i, r := range value {
		if r >= '0' && r <= '9' {
			offset = i + 1
			continue
		}
		if offset > 0 && strings.ContainsRune("、.）)", r) {
			return strings.TrimSpace(value[i+len(string(r)):])
		}
		break
	}
	return strings.TrimSpace(value[offset:])
}
