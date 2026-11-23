package domain

import "strings"

type MinutesSpeechBlock struct {
	Department    string   `json:"department"`
	Name          string   `json:"name"`
	WeeklySummary []string `json:"weeklySummary"`
	NextWeekPlan  []string `json:"nextWeekPlan"`
	Summary       []string `json:"summary"`
	OriginalIndex uint64   `json:"originalIndex"`
}
type ParsedMinutes struct {
	Info             MinutesInfo          `json:"info"`
	Topics           string               `json:"topics"`
	ClosingSummary   []string             `json:"closingSummary"`
	FallbackOverview []string             `json:"fallbackOverview"`
	SpeechBlocks     []MinutesSpeechBlock `json:"speechBlocks"`
}

func newSpeechBlock(name string, index uint64) MinutesSpeechBlock {
	return MinutesSpeechBlock{Name: name, OriginalIndex: index, WeeklySummary: []string{}, NextWeekPlan: []string{}, Summary: []string{}}
}
func numberedMinutesItem(s string) bool {
	seen := false
	for _, r := range strings.TrimSpace(s) {
		if r >= '0' && r <= '9' {
			seen = true
			continue
		}
		return seen && strings.ContainsRune("、.）)", r)
	}
	return false
}
func ParseMinutesSpeakerHeader(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if numberedMinutesItem(line) {
		return "", "", false
	}
	left, right, ok := strings.Cut(strings.ReplaceAll(line, "：", ":"), ":")
	if !ok {
		return "", "", false
	}
	raw := strings.TrimSpace(left)
	department := strings.TrimSpace(strings.TrimRight(strings.TrimLeft(raw, "【"), "】"))
	name := strings.TrimSpace(strings.TrimRight(strings.TrimLeft(strings.TrimSpace(right), "【"), "】"))
	if department == "" || name == "" {
		return "", "", false
	}
	known := strings.HasPrefix(raw, "【") && strings.HasSuffix(raw, "】")
	for _, word := range []string{"部", "办", "室", "中心", "组", "科", "处", "局", "院", "园", "厅", "店", "前台", "客房", "温泉", "营销", "财务", "行政", "人事", "工程", "保安", "餐饮"} {
		known = known || strings.Contains(department, word)
	}
	return department, name, known
}
func ParseMinutesOverview(overview string) ParsedMinutes {
	p := ParsedMinutes{ClosingSummary: []string{}, FallbackOverview: []string{}, SpeechBlocks: []MinutesSpeechBlock{}}
	for _, line := range strings.Split(overview, "\n") {
		if value := strings.TrimSpace(line); value != "" {
			p.FallbackOverview = append(p.FallbackOverview, value)
		}
	}
	var current *MinutesSpeechBlock
	section := ""
	speech, closing := false, false
	flush := func() {
		if current != nil {
			p.SpeechBlocks = append(p.SpeechBlocks, *current)
			current = nil
		}
	}
	for index, line := range p.FallbackOverview {
		if line == "发言内容" {
			speech, closing = true, false
			flush()
			section = ""
			continue
		}
		if !speech {
			for _, field := range []struct {
				label string
				value *string
			}{{"会议名称", &p.Info.MeetingName}, {"会议时间", &p.Info.MeetingTime}, {"会议地点", &p.Info.MeetingLocation}, {"记录人", &p.Info.Recorder}, {"出席人员", &p.Info.Attendees}, {"缺席人员", &p.Info.Absentees}, {"主要议题", &p.Topics}, {"会议主持人", &p.Info.Host}, {"审阅", &p.Info.Reviewer}} {
				if value, ok := strings.CutPrefix(line, field.label); ok {
					value = strings.TrimSpace(value)
					if strings.HasPrefix(value, "：") || strings.HasPrefix(value, ":") {
						if strings.HasPrefix(value, "：") {
							value = strings.TrimPrefix(value, "：")
						} else {
							value = strings.TrimPrefix(value, ":")
						}
						*field.value = strings.TrimSpace(value)
						break
					}
				}
			}
			continue
		}
		if department, name, ok := ParseMinutesSpeakerHeader(line); ok {
			flush()
			b := newSpeechBlock(NormalizeMemberName(name), uint64(len(p.SpeechBlocks)))
			b.Department = department
			current = &b
			closing = false
			section = ""
			continue
		}
		switch strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line), "：:")) {
		case "上周总结":
			section = "weekly"
			continue
		case "本周计划":
			section = "next"
			continue
		case "个人总结":
			section = "summary"
			closing = false
			continue
		case "全局总结":
			flush()
			section = ""
			closing = true
			continue
		case "总结":
			future := false
			for _, candidate := range p.FallbackOverview[index+1:] {
				_, _, ok := ParseMinutesSpeakerHeader(candidate)
				future = future || ok
			}
			if current != nil && (future || len(p.SpeechBlocks) == 0) {
				section = "summary"
				closing = false
			} else {
				flush()
				section = ""
				closing = true
			}
			continue
		}
		if closing {
			p.ClosingSummary = append(p.ClosingSummary, line)
			continue
		}
		if current != nil {
			switch section {
			case "weekly":
				current.WeeklySummary = append(current.WeeklySummary, line)
			case "next":
				current.NextWeekPlan = append(current.NextWeekPlan, line)
			case "summary":
				current.Summary = append(current.Summary, line)
			}
		}
	}
	flush()
	return p
}
