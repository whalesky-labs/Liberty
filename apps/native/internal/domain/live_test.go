package domain

import "testing"

func TestLiveAutomaticTranslationDirection(t *testing.T) {
	for _, tc := range []struct{ text, source, target string }{
		{"会议明天下午三点开始。", "zh", "en"},
		{"The meeting starts at three tomorrow.", "en", "zh"},
		{"API Key 不需要配置。", "zh", "en"},
		{"Hello，世界。", "zh", "en"},
		{"2026-11-26, 3:00!", "", ""},
		{"😊 …", "", ""},
		{"こんにちは", "", ""},
	} {
		source, target := LiveTranslationDirection(tc.text)
		if source != tc.source || target != tc.target {
			t.Errorf("direction %q: %s → %s", tc.text, source, target)
		}
	}
	if (LiveRequest{DeviceID: "synthetic", SourceLanguage: "en", TargetLanguage: "zh", Translate: true, AutoDetect: true}).Validate() != nil {
		t.Fatal("automatic bilingual request rejected")
	}
}
