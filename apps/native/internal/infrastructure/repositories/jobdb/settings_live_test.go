package jobdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestLiveSettingsPersistAcrossReopenAndFenceStaleWrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profile.sqlite3")
	store, _, err := OpenPrepared(ctx, path, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.Settings(ctx)
	if err != nil || !initial.Snapshot.LiveTranslate || initial.Snapshot.LiveDeviceID != "" {
		t.Fatal("live settings did not use their defaults", initial.Snapshot.LiveTranslate, initial.Snapshot.LiveDeviceID, err)
	}

	updated := initial
	updated.Snapshot.LiveTranslate = false
	updated.Snapshot.LiveDeviceID = "2:synthetic-device"
	if err := store.PublishSettings(ctx, updated, updated.Reference, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishSettings(ctx, initial, initial.Reference, nil); !errors.Is(err, domain.ErrSettingsConflict) {
		t.Fatal("stale settings snapshot was accepted", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, _, err := OpenPrepared(ctx, path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, err := reopened.Settings(ctx)
	if err != nil || saved.Snapshot.LiveTranslate || saved.Snapshot.LiveDeviceID != "2:synthetic-device" {
		t.Fatal("live settings did not survive reopening", saved.Snapshot.LiveTranslate, saved.Snapshot.LiveDeviceID, err)
	}
}
