package jobdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

func TestPerformanceIndicatorsSurviveReopenAndStaleWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "profile.sqlite3")
	store, _, err := OpenPrepared(ctx, path, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	initial, err := store.Settings(ctx)
	if err != nil || initial.Snapshot.HidePerformanceIndicators {
		t.Fatal("indicators should default to visible", err)
	}
	current := initial
	current.Snapshot.HidePerformanceIndicators = true
	if err := store.PublishSettings(ctx, current, current.Reference, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishSettings(ctx, initial, initial.Reference, nil); !errors.Is(err, domain.ErrSettingsConflict) {
		t.Fatal("expected revision conflict", err)
	}
	reader, err := legacydb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	prefs, err := reader.UIPreferences(ctx)
	if err != nil || !prefs.HidePerformanceIndicators {
		t.Fatal("saved preference missing at startup", err)
	}
	current, err = store.Settings(ctx)
	if err != nil || !current.Snapshot.HidePerformanceIndicators {
		t.Fatal("saved preference missing in settings", err)
	}
	current.Snapshot.HidePerformanceIndicators = false
	if err := store.PublishSettings(ctx, current, current.Reference, nil); err != nil {
		t.Fatal(err)
	}
	prefs, err = reader.UIPreferences(ctx)
	if err != nil || prefs.HidePerformanceIndicators {
		t.Fatal("could not re-enable indicators", err)
	}
}
