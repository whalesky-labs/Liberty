package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func TestVoiceSettingsSaveMicrophoneAndTranslationForFutureSessions(t *testing.T) {
	liveCallbacks := make(chan func(), 8)
	liveService := &liveModelsSettingsService{options: application.LiveOptions{
		Devices: []domain.AudioDevice{{ID: "synthetic-microphone", Name: "Synthetic Microphone"}}, ModelReady: true, TranslationReady: true,
	}}
	live := client.NewLive(liveService, nil, func(callback func()) { liveCallbacks <- callback }, func() {})
	defer live.Close()
	live.EnsureOptions()
	select {
	case callback := <-liveCallbacks:
		callback()
	case <-time.After(time.Second):
		t.Fatal("microphone options did not load")
	}
	if !live.OptionsLoaded() || live.Busy || len(live.Options.Devices) != 1 {
		t.Fatal("microphone options did not settle before rendering", live.OptionsLoaded(), live.Busy, live.Options.Devices)
	}

	settingsService := settingsParityService{saved: make(chan domain.Settings, 4)}
	settingsCallbacks := make(chan func(), 8)
	settings := client.NewSettings(settingsService, func(callback func()) { settingsCallbacks <- callback }, func() {}, func(domain.UIPreferences) {})
	settings.Loaded = true
	settings.Draft = domain.DefaultSettings()
	defer settings.Close()
	applied := make(chan domain.Settings, 4)
	settings.OnSaved(func(value domain.Settings) {
		live.Request.DeviceID = value.LiveDeviceID
		live.Request.Translate = value.LiveTranslate
		applied <- value
	})

	view := &SettingsView{Client: settings, Runtime: &RuntimeView{Live: live}, Locale: "en-US", section: "voice"}
	tester := ui.NewTester(view.View, 1100, 1500)
	deviceControl, ok := tester.AccessByRole(ui.RolePopUpButton, view.msg("voiceInputDevice"))
	if !ok || deviceControl.Disabled || live.Busy || !live.OptionsLoaded() {
		t.Fatal("loaded microphone selector remained disabled", deviceControl, live.Busy, live.OptionsLoaded(), live.Error)
	}
	tester.ClickAt(deviceControl.Bounds.X+deviceControl.Bounds.W/2, deviceControl.Bounds.Y+deviceControl.Bounds.H/2)
	tester.Frame()
	option := "Synthetic Microphone (1)"
	if _, ok := tester.AccessByRole(ui.RoleOption, option); !ok {
		t.Fatal("enumerated microphone was not offered", tester.AccessAll())
	}
	if err := tester.Click(option); err != nil {
		t.Fatal(err)
	}
	flushView(t, settingsCallbacks, tester)
	waitSavedSettingsVoice(t, settingsService.saved, applied)
	if settings.Draft.LiveDeviceID != "synthetic-microphone" || live.Request.DeviceID != "synthetic-microphone" {
		t.Fatal("selected microphone was not saved and applied to future sessions", settings.Draft.LiveDeviceID, live.Request.DeviceID)
	}

	translationToggle, ok := tester.AccessByRole(ui.RoleCheckBox, view.msg("voiceTranslate"))
	if !ok || !translationToggle.Checked {
		t.Fatal("translation toggle did not retain its saved state", translationToggle)
	}
	tester.ClickAt(translationToggle.Bounds.X+translationToggle.Bounds.W/2, translationToggle.Bounds.Y+translationToggle.Bounds.H/2)
	flushView(t, settingsCallbacks, tester)
	waitSavedSettingsVoice(t, settingsService.saved, applied)
	if settings.Draft.LiveTranslate || live.Request.Translate {
		t.Fatal("translation preference was not saved and applied to future sessions", settings.Draft.LiveTranslate, live.Request.Translate)
	}
}

func waitSavedSettingsVoice(t *testing.T, saved <-chan domain.Settings, applied <-chan domain.Settings) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-saved:
	case <-timer.C:
		t.Fatal("voice setting was not persisted")
	}
	select {
	case <-applied:
	case <-timer.C:
		t.Fatal("persisted voice setting was not applied to the live controller")
	}
}
