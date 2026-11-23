package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
)

func TestPreparedProfileStartsEmptyAndReleasesOwnershipAfterShutdown(t *testing.T) {
	directory := t.TempDir()
	database := filepath.Join(directory, "liberty.sqlite3")
	ctx := context.Background()
	p, result, err := OpenPrepared(ctx, database, true)
	if err != nil || result.Version != 10 {
		t.Fatal(result, err)
	}
	defer p.Close()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.Acquire(directory); !errors.Is(err, profile.ErrInUse) {
		t.Fatal("host lost profile ownership")
	}
	values, err := p.Resources.Templates(ctx)
	if err != nil || len(values) != 5 {
		t.Fatal("fresh host missing builtins", err)
	}
	settings, err := p.Resources.Settings(ctx)
	if err != nil || settings.ProcessingMode != "local" {
		t.Fatal("fresh host settings", err)
	}
	p.Close()
	p, _, err = OpenPrepared(ctx, database, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Start(ctx); err != nil {
		t.Fatal("restart recovery failed", err)
	}
	events, err := p.Pet.Events(ctx, 100)
	if err != nil || len(events) != 1 || events[0].EventSource != "daily_open" {
		t.Fatal("expected one daily-open reward across restart", err)
	}
}

func TestHandoverRetainsOwnershipAndCreatesRecoveryPoint(t *testing.T) {
	directory := t.TempDir()
	database := filepath.Join(directory, "liberty.sqlite3")
	ctx := context.Background()
	p, _, err := OpenPrepared(ctx, database, true)
	if err != nil {
		t.Fatal(err)
	}
	if other, _, err := OpenHandover(ctx, database, false); !errors.Is(err, profile.ErrInUse) {
		if other != nil {
			other.Close()
		}
		p.Close()
		t.Fatal("handover bypassed active owner", err)
	}
	p.Close()
	p, result, err := OpenHandover(ctx, database, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if result.Backup == "" {
		t.Fatal("existing profile has no recovery point")
	}
	if info, err := os.Stat(result.Backup); err != nil || info.Size() == 0 {
		t.Fatal("invalid handover backup", err)
	}
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	firstBackup := result.Backup
	p.Close()
	p, repeated, err := OpenHandover(ctx, database, false)
	if err != nil || repeated.Backup != firstBackup {
		t.Fatal("restarted handover replaced its original recovery point", err)
	}
	defer p.Close()
	if owner, err := profile.Acquire(directory); !errors.Is(err, profile.ErrInUse) {
		if owner != nil {
			owner.Close()
		}
		t.Fatal("handover released ownership before shutdown", err)
	}
}

func TestRestoreRequiresOwnershipAndReturnsToRecordedSnapshot(t *testing.T) {
	directory := t.TempDir()
	database := filepath.Join(directory, "liberty.sqlite3")
	ctx := context.Background()
	p, _, err := OpenPrepared(ctx, database, true)
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	p, result, err := OpenHandover(ctx, database, false)
	if err != nil || result.Backup == "" {
		t.Fatal("handover backup missing", err)
	}
	if _, err := RestoreProfile(ctx, database, filepath.Base(result.Backup)); !errors.Is(err, profile.ErrInUse) {
		p.Close()
		t.Fatal("restore bypassed active profile ownership", err)
	}
	if _, err := p.Resources.SaveMember(ctx, domain.MeetingMember{ID: "after-handover", Name: "After handover", Department: "Synthetic", SortOrder: 8}); err != nil {
		p.Close()
		t.Fatal(err)
	}
	p.Close()
	restored, err := RestoreProfile(ctx, database, filepath.Base(result.Backup))
	if err != nil || restored.RestoredVersion != 10 || restored.SafetyBackup == "" {
		t.Fatal("profile restore failed", restored, err, database, result.Backup)
	}
	reopened, err := Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	members, err := reopened.Resources.Members(ctx)
	if err != nil || len(members) != 0 {
		t.Fatal("restore retained post-snapshot member", members, err)
	}
}
