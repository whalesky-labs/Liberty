package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummaryJSONPreservesUnicodeAndLiteralEscapes(t *testing.T) {
	value := []string{"<&>", "\u2028", `\u2028`, "\u2029", `\u2029`}
	data, err := SummaryJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip []string
	if json.Unmarshal(data, &roundtrip) != nil {
		t.Fatal("invalid JSON")
	}
	for i := range value {
		if value[i] != roundtrip[i] {
			t.Fatal("escaped data changed")
		}
	}
	if strings.Contains(string(data), `\u003c`) {
		t.Fatal("HTML escaping changed prompt")
	}
}
func TestSummaryRejectsIncompleteResponses(t *testing.T) {
	for _, raw := range []string{`{}`, `{"choices":[]}`, `{"choices":[{"message":{"content":"{}"}}]}`, `{"choices":[{"finish_reason":"length","message":{"content":"{}"}}]}`, `{"choices":[{"finish_reason":"stop","message":{"content":"{}"}}]}`, `{"choices":[{"finish_reason":"stop","message":{"content":null}}]}`} {
		if _, err := ParseSummaryStructured(raw); err == nil {
			t.Fatal("incomplete accepted")
		}
	}
}
func TestSummaryChunkBoundariesPreserveLines(t *testing.T) {
	line := "甲: " + strings.Repeat("内容", 40)
	transcript := strings.Repeat(line+"\n", 40)
	chunks := chunkSummaryTranscript(transcript, 180)
	if len(chunks) < 2 || strings.Join(chunks, "\n") != strings.TrimSpace(transcript) {
		t.Fatal("lost/split transcript")
	}
}
