package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// The Rust completion envelope deserializes directly into typed structs.
// Field names are exact and duplicate known fields (even null) are invalid.
// Decode every choice's shape before selecting the first, as Serde does.
type summaryCompletionResponse struct{ Choices []summaryCompletionChoice }
type summaryCompletionChoice struct {
	FinishReason *string
	Message      *summaryCompletionMessage
}
type summaryCompletionMessage struct{ Content *summaryCompletionContent }
type summaryCompletionContent struct{ Text string }
type summaryCompletionPart struct{ Text *string }

func (v *summaryCompletionResponse) UnmarshalJSON(raw []byte) error {
	return decodeSummaryFields(raw, map[string]any{"choices": &v.Choices})
}

func (v *summaryCompletionChoice) UnmarshalJSON(raw []byte) error {
	return decodeSummaryFields(raw, map[string]any{"finish_reason": &v.FinishReason, "message": &v.Message})
}

func (v *summaryCompletionMessage) UnmarshalJSON(raw []byte) error {
	return decodeSummaryFields(raw, map[string]any{"content": &v.Content})
}

func (v *summaryCompletionPart) UnmarshalJSON(raw []byte) error {
	return decodeSummaryFields(raw, map[string]any{"text": &v.Text})
}

func (v *summaryCompletionContent) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		return json.Unmarshal(raw, &v.Text)
	}
	var parts []summaryCompletionPart
	if json.Unmarshal(raw, &parts) != nil || parts == nil {
		return ErrAIResponse
	}
	var text strings.Builder
	for _, part := range parts {
		if part.Text != nil {
			text.WriteString(*part.Text)
		}
	}
	v.Text = text.String()
	return nil
}

func decodeSummaryFields(raw []byte, fields map[string]any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return ErrAIResponse
	}
	seen := make(map[string]bool, len(fields))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return ErrAIResponse
		}
		name, ok := token.(string)
		var value json.RawMessage
		if !ok || decoder.Decode(&value) != nil {
			return ErrAIResponse
		}
		if destination, known := fields[name]; known {
			if seen[name] || json.Unmarshal(value, destination) != nil {
				return ErrAIResponse
			}
			seen[name] = true
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return ErrAIResponse
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrAIResponse
	}
	return nil
}
