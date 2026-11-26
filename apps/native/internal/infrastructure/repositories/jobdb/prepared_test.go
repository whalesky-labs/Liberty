package jobdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/legacydb"
)

func TestPreparedProfileInitializesOriginalServicesAndRetainsOwnership(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "liberty.sqlite3")
	owner, err := profile.Acquire(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = OpenPrepared(ctx, path, nil, true); !errors.Is(err, profile.ErrInUse) {
		t.Fatal("profile ownership bypassed", err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("database created without ownership")
	}
	owner.Close()
	if _, _, err = OpenPrepared(ctx, path, nil, false); err == nil {
		t.Fatal("implicit creation accepted")
	}
	store, result, err := OpenPrepared(ctx, path, nil, true)
	if err != nil || result.Version != 12 {
		t.Fatal(result, err)
	}
	defer store.Close()
	reader, err := legacydb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.UIPreferences(ctx); err != nil {
		t.Fatal(err)
	}
	if jobs, err := reader.ListJobs(ctx); err != nil || len(jobs) != 0 {
		t.Fatal("fresh job projection", err)
	}
	if _, err := reader.Dashboard(ctx, "all", time.Now()); err != nil {
		t.Fatal(err)
	}
	resources := application.NewResources(store, nil)
	settings, err := resources.Settings(ctx)
	if err != nil || settings.Locale != "zh-CN" {
		t.Fatal("default settings unavailable", err)
	}
	if templates, err := resources.Templates(ctx); err != nil || len(templates) != 5 {
		t.Fatal("builtin templates unavailable", err)
	}
	if _, err := resources.SaveMember(ctx, domain.MeetingMember{Name: "Synthetic initialized member"}); err != nil {
		t.Fatal(err)
	}
	resources.Close()
	// Force a replacement connection. Safety pragmas must survive pooling.
	store.db.SetConnMaxLifetime(time.Nanosecond)
	var foreignKeys int
	if store.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys) != nil || foreignKeys != 1 {
		t.Fatal("replacement connection lost foreign keys")
	}
	if _, err := profile.Acquire(directory); !errors.Is(err, profile.ErrInUse) {
		t.Fatal("writer did not retain ownership")
	}
	store.Close()
	store, _, err = OpenPrepared(ctx, path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	values, err := store.Members(ctx)
	if err != nil || len(values) != 1 || values[0].Name != "Synthetic initialized member" {
		t.Fatal("reopen lost persisted data", err)
	}
}
