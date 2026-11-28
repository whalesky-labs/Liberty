package domain

import "testing"

func TestLiveSettingsDefaultAndNormalizeDeviceID(t *testing.T) {
	settings := DefaultSettings()
	if !settings.LiveTranslate || settings.LiveDeviceID != "" {
		t.Fatal("live translation should default to enabled with no selected microphone", settings.LiveTranslate, settings.LiveDeviceID)
	}

	settings.LiveDeviceID = " \x00invalid\n "
	if got := settings.Normalized(); got.LiveDeviceID != "" {
		t.Fatal("invalid live device identity was retained", got.LiveDeviceID)
	}

	settings.LiveDeviceID = " 1:device "
	if got := settings.Normalized(); got.LiveDeviceID != "1:device" {
		t.Fatal("live device identity was not trimmed", got.LiveDeviceID)
	}
}
