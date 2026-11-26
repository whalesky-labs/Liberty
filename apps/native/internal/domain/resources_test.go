package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSecretNeverAppearsInMetadataOrFormatting(t *testing.T) {
	value := NewSecret("synthetic-sensitive-value")
	for _, text := range []string{fmt.Sprint(value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value), fmt.Sprintf("%+v", CredentialUpdate{Action: SetCredential, Value: value})} {
		if strings.Contains(text, value.Reveal()) {
			t.Fatal("secret formatted")
		}
	}
	encoded, err := json.Marshal(CredentialUpdate{Action: SetCredential, Value: value})
	if err != nil || strings.Contains(string(encoded), value.Reveal()) {
		t.Fatal("secret serialized")
	}
	encoded, _ = json.Marshal(AIModel{})
	var fields map[string]any
	if json.Unmarshal(encoded, &fields) != nil || len(fields) != 9 {
		t.Fatal("model public contract differs")
	}
}
func TestSettingsNormalizationMatchesOriginal(t *testing.T) {
	value := Settings{UIPreferences: UIPreferences{ThemeMode: "invalid", LiquidGlassStyle: "invalid", AccentColor: " #ABC123 ", Locale: "other"}, BackendURL: " https://example.test ", ProcessingMode: " remote ", Concurrency: 99, Threads: 99, BatchSeconds: 1, Device: "invalid"}.Normalized()
	if value.ThemeMode != "auto" || value.LiquidGlassStyle != "transparent" || value.Locale != "zh-CN" || value.AccentColor != "#abc123" || value.BackendURL != "https://example.test" || value.ProcessingMode != "remote" || value.Concurrency != 8 || value.Threads != 32 || value.BatchSeconds != 30 || value.Device != "auto" {
		t.Fatal("normalization differs")
	}
}

func TestMemberImportValidatesOnceAndOwnsItsData(t *testing.T) {
	for _, values := range [][]MeetingMember{
		nil, {{ID: "a", Name: " "}}, {{Name: "A"}},
		{{ID: "a", Name: "A"}, {ID: "b", Name: " A "}},
		{{ID: "a", Name: "A", IsRecorder: true}, {ID: "b", Name: "B", IsRecorder: true}},
	} {
		if _, err := NewMemberImport(values); err != ErrResourceInvalid {
			t.Fatal("invalid import accepted", err)
		}
	}
	values := []MeetingMember{{ID: "a", Name: " A ", Department: " Team ", IsRecorder: true}}
	batch, err := NewMemberImport(values)
	if err != nil {
		t.Fatal(err)
	}
	values[0].Name = "changed input"
	first := batch.Members()
	if first[0].Name != "A" || first[0].Department != "Team" {
		t.Fatal("batch not normalized or owned")
	}
	first[0].Name = "changed output"
	if batch.Members()[0].Name != "A" {
		t.Fatal("adapter mutated validated batch")
	}
}
