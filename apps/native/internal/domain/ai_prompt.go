package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var ErrSummaryPrompt = errors.New("ai_summary_prompt_invalid")

const TranscriptStartMarker = "--- LIBERTY TRANSCRIPT START ---"
const TranscriptEndMarker = "--- LIBERTY TRANSCRIPT END ---"
const RequiredSpeakersPrefix = "Required transcript speakers (JSON): "

// Keep this contract in sync with local_ai/prompt.rs. Cross-language fixtures
// compare complete prompts, including template text, mapping order and clocks.
const structuredSummaryRequirements = `Return one complete JSON object with every field below, even when a field is empty:
{"title":"","overview":"","topics":[],"decisions":[],"actionItems":[],"risks":[],"followUps":[],"speakerReports":[],"globalSummary":[]}
Each speakerReports item must use {"speakerLabel":"","department":"","weeklySummary":[],"nextWeekPlan":[],"summary":[]}.
Keep transcript speaker labels verbatim. Include every required transcript speaker exactly once. Do not merge, rename, or omit speakers.
Use arrays of strings for all list fields. actionItems items must use {"task":"","owner":"","dueDate":""}. Output JSON only.`

type SummaryPromptInput struct {
	Job                                          Result
	Template                                     SummaryTemplate
	IncludeSpeaker, IncludeTimestamp, UseMembers bool
	Members                                      []MeetingMember
	Extra                                        string
}
type SummaryPrompt struct{ System, User string }
type SummaryPlan struct {
	FallbackTitle    string
	RequiredSpeakers []string
	UserPrompts      []string
}

func BuildSummaryPrompt(in SummaryPromptInput) SummaryPrompt {
	speakers, transcript := []string{}, []string{}
	for _, segment := range in.Job.PrimarySegments() {
		parts := []string{}
		if in.IncludeTimestamp {
			parts = append(parts, fmt.Sprintf("[%s - %s]", summaryClock(segment.StartMS), summaryClock(segment.EndMS)))
		}
		if in.IncludeSpeaker {
			name := ""
			if segment.Speaker != nil {
				name = strings.TrimSpace(*segment.Speaker)
			}
			if name != "" {
				speakers = uniqueItems(speakers, []string{name})
			}
			parts = append(parts, summaryFallback(name, "Unknown speaker")+":")
		}
		parts = append(parts, strings.TrimSpace(segment.Text))
		transcript = append(transcript, strings.Join(parts, " "))
	}
	words := "none"
	if len(in.Job.Job.Hotwords) > 0 {
		words = strings.Join(in.Job.Job.Hotwords, ", ")
	}
	encoded, _ := SummaryJSON(speakers)
	lines := []string{
		"Meeting title: " + in.Job.Job.Title,
		"Meeting language: " + in.Job.Job.Lang,
		"Hotwords: " + words,
		"Include speaker info: " + summaryYes(in.IncludeSpeaker),
		"Include timestamps: " + summaryYes(in.IncludeTimestamp),
		"Use member mapping: " + summaryYes(in.UseMembers),
		"Extra instructions: " + summaryFallback(in.Extra, "none"),
		RequiredSpeakersPrefix + string(encoded),
	}
	if in.UseMembers {
		lines = append(lines, "", "Member directory mapping:")
		members := append([]MeetingMember{}, in.Members...)
		sort.SliceStable(members, func(i, j int) bool { return members[i].SortOrder < members[j].SortOrder })
		if len(members) == 0 {
			lines = append(lines, "- No member directory records available.")
		}
		for _, member := range members {
			lines = append(lines, fmt.Sprintf("- %s | department=%s | sortOrder=%d | recorder=%s", member.Name, summaryFallback(member.Department, "未设置"), member.SortOrder, summaryYes(member.IsRecorder)))
		}
		lines = append(lines, "", "When the transcript already contains speaker names, keep those names exactly as they appear and use the member directory only to补充部门、排序相关上下文，不要改写姓名。")
	}
	text := strings.Join(transcript, "\n")
	if strings.TrimSpace(text) == "" {
		text = "Transcript is missing."
	}
	lines = append(lines, "", "Please output JSON based on the following meeting content:", TranscriptStartMarker, text, TranscriptEndMarker)
	return SummaryPrompt{System: strings.TrimSpace(strings.TrimSpace(in.Template.Prompt) + "\n\n" + structuredSummaryRequirements), User: strings.Join(lines, "\n")}
}

func summaryClock(ms uint64) string {
	seconds := ms / 1000 % 86400
	return fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds%3600/60, seconds%60)
}
func summaryYes(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
func summaryFallback(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

// SummaryJSON avoids HTML escaping in the persisted prompt/protocol. It is not
// a browser document. Like serde_json it preserves Unicode line separators.
func SummaryJSON(value any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(value); err != nil {
		return nil, err
	}
	data := bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
	// Only replace active escapes (an escaped backslash is a separate token).
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == '\\' && i+1 < len(data) {
			if i+6 <= len(data) && (string(data[i:i+6]) == `\u2028` || string(data[i:i+6]) == `\u2029`) {
				r := rune(0x2028)
				if data[i+5] == '9' {
					r = 0x2029
				}
				out = append(out, string(r)...)
				i += 5
				continue
			}
			out = append(out, data[i], data[i+1])
			i++
			continue
		}
		out = append(out, data[i])
	}
	return out, nil
}

func PlanSummary(prompt SummaryPrompt) (SummaryPlan, error) {
	prefix, rest, ok := strings.Cut(prompt.User, TranscriptStartMarker)
	if !ok {
		return SummaryPlan{}, ErrSummaryPrompt
	}
	transcript, suffix, ok := strings.Cut(rest, TranscriptEndMarker)
	if !ok {
		return SummaryPlan{}, ErrSummaryPrompt
	}
	plan := SummaryPlan{FallbackTitle: "会议总结", RequiredSpeakers: []string{}, UserPrompts: []string{}}
	titleFound, speakersFound := false, false
	for _, line := range strings.Split(prefix, "\n") {
		line = strings.TrimSpace(line)
		if !titleFound && strings.HasPrefix(line, "Meeting title: ") {
			plan.FallbackTitle = strings.TrimSpace(strings.TrimPrefix(line, "Meeting title: "))
			titleFound = true
		}
		if !speakersFound && strings.HasPrefix(line, RequiredSpeakersPrefix) {
			var labels []string
			var values []json.RawMessage
			text := strings.TrimPrefix(line, RequiredSpeakersPrefix)
			valid := json.Unmarshal([]byte(text), &values) == nil && values != nil
			for _, v := range values {
				var label string
				if string(v) == "null" || json.Unmarshal(v, &label) != nil {
					valid = false
					break
				}
				labels = append(labels, label)
			}
			if valid {
				plan.RequiredSpeakers = append([]string{}, labels...)
			}
			speakersFound = true
		}
	}
	budget := max(1000, 12000-estimateSummaryTokens(prompt.System)-estimateSummaryTokens(prefix)-estimateSummaryTokens(suffix)-512)
	chunks := chunkSummaryTranscript(strings.TrimSpace(transcript), budget)
	for i, chunk := range chunks {
		plan.UserPrompts = append(plan.UserPrompts, fmt.Sprintf("%s\n%s\nSummary chunk %d/%d. Summarize only this chunk; keep all required JSON fields.\n%s\n%s%s", strings.TrimRightFunc(prefix, func(r rune) bool { return strings.TrimSpace(string(r)) == "" }), TranscriptStartMarker, i+1, len(chunks), chunk, TranscriptEndMarker, suffix))
	}
	return plan, nil
}
func estimateSummaryTokens(value string) int {
	ascii, other := 0, 0
	for _, r := range value {
		if r < 128 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+3)/4 + other
}
func chunkSummaryTranscript(transcript string, budget int) []string {
	if strings.TrimSpace(transcript) == "" {
		return []string{"Transcript is missing."}
	}
	chunks, current := []string{}, []string{}
	tokens := 0
	for _, line := range strings.Split(transcript, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cost := max(1, estimateSummaryTokens(line))
		if len(current) > 0 && tokens+cost > budget {
			chunks = append(chunks, strings.Join(current, "\n"))
			current = nil
			tokens = 0
		}
		current = append(current, line)
		tokens += cost
	}
	if len(current) > 0 {
		chunks = append(chunks, strings.Join(current, "\n"))
	}
	return chunks
}
