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
