package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type summaryWireCase struct {
	name, raw string
	wantErr   error
}

func summaryWireCases() []summaryWireCase {
	content := `{"title":"Exact title","overview":"Exact overview","topics":[],"decisions":[],"actionItems":[],"risks":[],"followUps":[],"speakerReports":[],"globalSummary":[]}`
	quoted, _ := json.Marshal(content)
	message := `{"content":` + string(quoted) + `}`
	choice := `{"finish_reason":"stop","message":` + message + `}`
	envelope := func(c string) string { return `{"choices":[` + c + `]}` }
	return []summaryWireCase{
		{"ordinary text", envelope(choice), nil},
		{"whitespace stop", envelope(strings.Replace(choice, `"stop"`, `" stop "`, 1)), nil},
		{"escaped exact names", `{"choi\u0063es":[{"finish_rea\u0073on":"stop","message":{"con\u0074ent":` + string(quoted) + `}}]}`, nil},
		{"upper choices ignored", envelope(choice)[:len(envelope(choice))-1] + `,"CHOICES":[]}`, nil},
		{"only upper choices", `{"CHOICES":[` + choice + `]}`, ErrAIResponse},
		{"upper reason cannot complete truncation", envelope(`{"finish_reason":"length","FINISH_REASON":"stop","message":` + message + `}`), ErrAITruncated},
		{"unicode reason cannot complete truncation", envelope(`{"finish_reason":"length","finiſh_reaſon":"stop","message":` + message + `}`), ErrAITruncated},
		{"only upper reason", envelope(`{"FINISH_REASON":"stop","message":` + message + `}`), ErrAITruncated},
		{"unknown reason extension", envelope(`{"finish_reason":"stop","FINISH_REASON":{"extension":true},"message":` + message + `}`), nil},
		{"unknown message extension", envelope(`{"finish_reason":"stop","message":` + message + `,"MESSAGE":false}`), nil},
		{"only upper message", envelope(`{"finish_reason":"stop","MESSAGE":` + message + `}`), ErrAIResponse},
		{"unknown content extension", envelope(`{"finish_reason":"stop","message":{"content":` + string(quoted) + `,"CONTENT":false}}`), nil},
		{"only upper content", envelope(`{"finish_reason":"stop","message":{"CONTENT":` + string(quoted) + `}}`), ErrAIResponse},
		{"duplicate choices", `{"choices":[],"choices":[` + choice + `]}`, ErrAIResponse},
		{"duplicate null choices", `{"choices":null,"choices":[` + choice + `]}`, ErrAIResponse},
		{"duplicate reason", envelope(`{"finish_reason":"length","finish_reason":"stop","message":` + message + `}`), ErrAIResponse},
		{"duplicate null reason", envelope(`{"finish_reason":null,"finish_reason":"stop","message":` + message + `}`), ErrAIResponse},
		{"duplicate escaped reason", envelope(`{"finish_reason":"length","finish_rea\u0073on":"stop","message":` + message + `}`), ErrAIResponse},
		{"duplicate message", envelope(`{"finish_reason":"stop","message":null,"message":` + message + `}`), ErrAIResponse},
		{"duplicate content", envelope(`{"finish_reason":"stop","message":{"content":null,"content":` + string(quoted) + `}}`), ErrAIResponse},
		{"duplicate unknown extension", envelope(`{"extension":1,"extension":2,"finish_reason":"stop","message":` + message + `}`), nil},
		{"parts", envelope(`{"finish_reason":"stop","message":{"content":[{"type":"text","text":` + string(quoted) + `},{"text":null},{"extension":true}]}}`), nil},
		{"parts ignore upper text", envelope(`{"finish_reason":"stop","message":{"content":[{"text":` + string(quoted) + `,"TEXT":false}]}}`), nil},
		{"parts only upper text", envelope(`{"finish_reason":"stop","message":{"content":[{"TEXT":` + string(quoted) + `}]}}`), ErrAIStructured},
		{"duplicate part text", envelope(`{"finish_reason":"stop","message":{"content":[{"text":null,"text":` + string(quoted) + `}]}}`), ErrAIResponse},
		{"null part", envelope(`{"finish_reason":"stop","message":{"content":[{"text":` + string(quoted) + `},null]}}`), ErrAIResponse},
		{"null extra choice", envelope(choice + `,null`), ErrAIResponse},
		{"invalid extra choice content", envelope(choice + `,{"message":{"content":false}}`), ErrAIResponse},
		{"invalid extra choice part", envelope(choice + `,{"message":{"content":[{"text":42}]}}`), ErrAIResponse},
		{"duplicate extra choice reason", envelope(choice + `,{"finish_reason":null,"finish_reason":null}`), ErrAIResponse},
		{"valid unused extra choice", envelope(choice + `,{"finish_reason":"length","message":{"content":null}}`), nil},
		{"null message", envelope(`{"finish_reason":"stop","message":null}`), ErrAIResponse},
		{"null content", envelope(`{"finish_reason":"stop","message":{"content":null}}`), ErrAIResponse},
		{"empty content array", envelope(`{"finish_reason":"stop","message":{"content":[]}}`), ErrAIStructured},
		{"non-string reason", envelope(`{"finish_reason":true,"message":` + message + `}`), ErrAIResponse},
		{"trailing value", envelope(choice) + `{}`, ErrAIResponse},
		// The structured content is a JSON Value in Rust: duplicate object keys
		// there keep the last value, unlike the typed completion envelope.
		{"structured duplicates keep last", strings.Replace(envelope(choice), `\"title\":`, `\"title\":\"earlier\",\"title\":`, 1), nil},
	}
}

func TestSummaryWireMatchesTypedCompletionBoundary(t *testing.T) {
	for _, tc := range summaryWireCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSummaryStructured(tc.raw)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
			if err == nil && (got["title"] != "Exact title" || got["overview"] != "Exact overview") {
				t.Fatal("extension or duplicate field changed the accepted content")
			}
			if err != nil {
				if got != nil {
					t.Fatal("invalid response returned partial content")
				}
				if _, err := ParseSummaryResponse(tc.raw, "Fallback"); err == nil {
					t.Fatal("invalid response produced a display result")
				}
				if _, err := MergeSummaryResponses([]string{tc.raw}, "Fallback", nil); err == nil {
					t.Fatal("invalid response entered the merged summary")
				}
			}
		})
	}
}
