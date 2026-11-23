package domain

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
)

var ErrAIResponse = errors.New("ai_response_invalid")
var ErrAIStructured = errors.New("ai_structured_result_invalid")
var ErrAITruncated = errors.New("ai_response_incomplete")

const MaxAIResponseBytes = 4 << 20

var summaryFields = []string{"title", "overview", "topics", "decisions", "actionItems", "risks", "followUps", "speakerReports", "globalSummary"}

func ParseSummaryStructured(raw string) (map[string]any, error) {
	if len(raw) > MaxAIResponseBytes {
		return nil, ErrAIResponse
	}
	var envelope summaryCompletionResponse
	if json.Unmarshal([]byte(raw), &envelope) != nil || len(envelope.Choices) == 0 {
		return nil, ErrAIResponse
	}
	choice := envelope.Choices[0]
	if choice.FinishReason == nil || strings.TrimSpace(*choice.FinishReason) != "stop" {
		return nil, ErrAITruncated
	}
	if choice.Message == nil || choice.Message.Content == nil {
		return nil, ErrAIResponse
	}
	text := choice.Message.Content.Text
	var object map[string]any
	d := json.NewDecoder(strings.NewReader(strings.TrimSpace(text)))
	d.UseNumber()
	if d.Decode(&object) != nil || object == nil {
		return nil, ErrAIStructured
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, ErrAIStructured
	}
	for _, key := range summaryFields {
		if _, ok := object[key]; !ok {
			return nil, ErrAIStructured
		}
	}
	return object, nil
}

func ParseSummaryResponse(raw, fallbackTitle string) (SummaryResult, error) {
	v, err := ParseSummaryStructured(raw)
	if err != nil {
		return SummaryResult{}, err
	}
	r := SummaryResult{Title: summaryFallback(summaryValue(v["title"]), fallbackTitle), Overview: summaryValue(v["overview"]), Topics: summaryStrings(v["topics"]), Decisions: summaryStrings(v["decisions"]), ActionItems: []SummaryAction{}, Risks: summaryStrings(v["risks"]), FollowUps: summaryStrings(v["followUps"])}
	for _, value := range summaryArray(v["actionItems"]) {
		m, _ := value.(map[string]any)
		task := summaryValue(m["task"])
		if task != "" {
			r.ActionItems = append(r.ActionItems, SummaryAction{Task: task, Owner: summaryValue(m["owner"]), DueDate: summaryValue(m["dueDate"])})
		}
	}
	return r, nil
}

func MergeSummaryResponses(responses []string, title string, required []string) (string, error) {
	if len(responses) == 0 {
		return "", ErrAIStructured
	}
	merged := map[string]any{"title": title, "overview": ""}
	for _, key := range summaryFields[2:] {
		merged[key] = []any{}
	}
	for _, raw := range responses {
		source, err := ParseSummaryStructured(raw)
		if err != nil {
			return "", err
		}
		if t, ok := merged["title"].(string); !ok || t == "" {
			merged["title"] = source["title"]
		}
		if text := summaryValue(source["overview"]); text != "" {
			current, _ := merged["overview"].(string)
			found := false
			for _, line := range strings.Split(current, "\n") {
				found = found || strings.TrimSpace(line) == text
			}
			if current == "" {
				merged["overview"] = text
			} else if !found {
				merged["overview"] = current + "\n" + text
			}
		}
		for _, key := range []string{"topics", "decisions", "actionItems", "risks", "followUps", "globalSummary"} {
			appendSummaryArray(merged, source, key)
		}
		reports := summaryArray(merged["speakerReports"])
		for _, value := range summaryArray(source["speakerReports"]) {
			report, _ := value.(map[string]any)
			label := summarySpeakerLabel(report)
			if label == "" {
				continue
			}
			var target map[string]any
			for _, old := range reports {
				m, _ := old.(map[string]any)
				if summarySpeakerLabel(m) == label {
					target = m
					break
				}
			}
			if target == nil {
				reports = append(reports, normalizedSummaryReport(report, label))
				continue
			}
			for _, key := range []string{"weeklySummary", "nextWeekPlan", "summary"} {
				appendSummaryArray(target, report, key)
			}
			if department, ok := target["department"].(string); !ok || department == "" {
				if value, ok := report["department"]; ok {
					target["department"] = value
				} else {
					target["department"] = ""
				}
			}
		}
		merged["speakerReports"] = reports
	}
	reports := summaryArray(merged["speakerReports"])
	for _, label := range required {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		found := false
		for _, v := range reports {
			m, _ := v.(map[string]any)
			found = found || summarySpeakerLabel(m) == label
		}
		if !found {
			reports = append(reports, normalizedSummaryReport(map[string]any{"matchStatus": "missing_from_ai"}, label))
		}
	}
	merged["speakerReports"] = reports
	lines := []string{}
	for _, line := range strings.Split(summaryValue(merged["overview"]), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(reports) > 0 {
		lines = append(lines, "发言内容")
	}
	for _, v := range reports {
		m, _ := v.(map[string]any)
		label := summarySpeakerLabel(m)
		if label == "" {
			continue
		}
		department, _ := m["department"].(string)
		lines = append(lines, "【"+summaryFallback(department, "未匹配部门")+"】："+label)
		for _, section := range []struct{ label, key string }{{"上周总结", "weeklySummary"}, {"本周计划", "nextWeekPlan"}, {"个人总结", "summary"}} {
			lines = append(lines, section.label+"：")
			for _, item := range summaryArray(m[section.key]) {
				if text := summaryValue(item); text != "" {
					lines = append(lines, text)
				}
			}
		}
	}
	if values := summaryArray(merged["globalSummary"]); len(values) > 0 {
		lines = append(lines, "全局总结：")
		for _, item := range values {
			if text := summaryValue(item); text != "" {
				lines = append(lines, text)
			}
		}
	}
	merged["overview"] = strings.Join(lines, "\n")
	content, err := SummaryJSON(merged)
	if err != nil {
		return "", ErrAIStructured
	}
	encoded, err := SummaryJSON(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(content)}}}})
	if err != nil || len(encoded) > MaxAIResponseBytes {
		return "", ErrAIResponse
	}
	return string(encoded), nil
}

func summaryValue(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	case []any:
		parts := []string{}
		for _, v := range x {
			if s := summaryValue(v); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		keys := []string{}
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := []string{}
		for _, k := range keys {
			if s := summaryValue(x[k]); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}
func summaryArray(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return []any{}
}
func summaryStrings(v any) []string {
	values, ok := v.([]any)
	if !ok {
		values = []any{v}
	}
	result := []string{}
	for _, value := range values {
		if text := summaryValue(value); text != "" {
			result = append(result, text)
		}
	}
	return result
}
func appendSummaryArray(target, source map[string]any, key string) {
	items := summaryArray(target[key])
	for _, item := range summaryArray(source[key]) {
		found := false
		for _, existing := range items {
			found = found || reflect.DeepEqual(item, existing)
		}
		if !found {
			items = append(items, item)
		}
	}
	target[key] = items
}
func summarySpeakerLabel(v map[string]any) string {
	for _, key := range []string{"speakerLabel", "resolvedName", "name"} {
		if s := summaryValue(v[key]); s != "" {
			return s
		}
	}
	return ""
}
func normalizedSummaryReport(v map[string]any, label string) map[string]any {
	status, ok := v["matchStatus"]
	if !ok {
		status = "unmatched"
	}
	return map[string]any{"speakerLabel": label, "department": summaryValue(v["department"]), "weeklySummary": summaryArray(v["weeklySummary"]), "nextWeekPlan": summaryArray(v["nextWeekPlan"]), "summary": summaryArray(v["summary"]), "matchStatus": status}
}
