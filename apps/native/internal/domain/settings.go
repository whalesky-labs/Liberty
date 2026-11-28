package domain

import (
	"regexp"
	"strings"
)

// Settings is the main-window snapshot. Runtime executable paths and sources
// are managed separately, so public saves cannot overwrite a detector result.
type Settings struct {
	UIPreferences
	BackendURL      string `json:"backendUrl"`
	TokenConfigured bool   `json:"apiTokenConfigured"`
	ProcessingMode  string `json:"processingMode"`
	DefaultHotwords string `json:"defaultHotwords"`
	SummaryTemplate string `json:"summaryTemplate"`
	Concurrency     uint32 `json:"concurrency"`
	RunnerPath      string `json:"runnerScriptPath"`
	Device          string `json:"localAsrDevice"`
	Threads         uint32 `json:"localAsrThreads"`
	BatchSeconds    uint32 `json:"localAsrBatchSizeSeconds"`
	LiveDeviceID    string `json:"liveDeviceId"`
	LiveTranslate   bool   `json:"liveTranslate"`
	DownloadSource  string `json:"runtimeDownloadSource"`
	Revision        int64  `json:"settingsRevision"`
}

func DefaultSettings() Settings {
	return Settings{UIPreferences: DefaultUIPreferences(), ProcessingMode: "local", DefaultHotwords: "SeACo-Paraformer, FunASR, 会议纪要", SummaryTemplate: "表格版会议纪要", Concurrency: 2, Device: "auto", BatchSeconds: 300, LiveTranslate: true}
}

var hexAccent = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s Settings) Normalized() Settings {
	if s.ThemeMode != "light" && s.ThemeMode != "dark" {
		s.ThemeMode = "auto"
	}
	if s.LiquidGlassStyle != "tinted" {
		s.LiquidGlassStyle = "transparent"
	}
	if s.Locale != "en-US" {
		s.Locale = "zh-CN"
	}
	s.AccentColor = strings.TrimSpace(s.AccentColor)
	if !hexAccent.MatchString(s.AccentColor) {
		s.AccentColor = "#2f6dff"
	}
	s.AccentColor = strings.ToLower(s.AccentColor)
	s.BackendURL = strings.TrimSpace(s.BackendURL)
	if strings.TrimSpace(s.ProcessingMode) == "remote" {
		s.ProcessingMode = "remote"
	} else {
		s.ProcessingMode = "local"
	}
	s.DefaultHotwords = strings.TrimSpace(s.DefaultHotwords)
	s.SummaryTemplate = strings.TrimSpace(s.SummaryTemplate)
	s.RunnerPath = strings.TrimSpace(s.RunnerPath)
	s.LiveDeviceID = strings.TrimSpace(s.LiveDeviceID)
	if len(s.LiveDeviceID) > 512 || strings.ContainsAny(s.LiveDeviceID, "\x00\r\n") {
		s.LiveDeviceID = ""
	}
	s.DownloadSource = strings.TrimSpace(s.DownloadSource)
	if s.Device != "cpu" && s.Device != "mps" && s.Device != "cuda" {
		s.Device = "auto"
	}
	s.Concurrency = max(1, min(8, s.Concurrency))
	s.Threads = min(32, s.Threads)
	s.BatchSeconds = max(30, min(1200, s.BatchSeconds))
	return s
}

type SettingsConflict struct{ Current Settings }

func (e *SettingsConflict) Error() string { return ErrSettingsConflict.Error() }
func (e *SettingsConflict) Unwrap() error { return ErrSettingsConflict }
