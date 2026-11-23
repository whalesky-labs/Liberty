package jobdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestPetGrowthAndEventClaimsAreTransactionalAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s, _, err := OpenPrepared(ctx, filepath.Join(t.TempDir(), "liberty.sqlite3"), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now, metadata := "2026-11-18T12:00:00.000Z", "job-1"
	first, err := s.PetGrowth(ctx, "workflow", "job_created", &metadata, now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.PetGrowth(ctx, "workflow", "job_created", &metadata, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Experience != 5 || again.Experience != first.Experience {
		t.Fatalf("duplicate changed growth: %+v %+v", first, again)
	}
	var balance, count int64
	if err := s.db.QueryRowContext(ctx, `SELECT balance FROM pet_wallets WHERE pet_id='default-pet' AND currency_key='lp'`).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT counter_value FROM pet_milestone_counters WHERE counter_key='tasks_created'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if balance != 15 || count != 1 {
		t.Fatalf("reward duplicated: balance=%d count=%d", balance, count)
	}
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := ensurePet(ctx, tx, now); err != nil {
			return err
		}
		if err := ensurePetStore(ctx, tx, now); err != nil {
			return err
		}
		return domain.ErrPet
	}); err == nil {
		t.Fatal("expected injected rollback")
	}
	var inventory int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM pet_inventory WHERE pet_id='default-pet'`).Scan(&inventory); err != nil {
		t.Fatal(err)
	}
	if inventory == 0 {
		t.Fatal("rollback removed previously committed inventory")
	}
}

func TestDesktopPetDragAndSettingsDoNotOverwriteEachOther(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "liberty.sqlite3")
	store, _, err := OpenPrepared(ctx, file, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const now = "2026-11-18T09:00:00Z"
	original, err := store.PetSettings(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SavePetPosition(ctx, -800, 120, now); err != nil {
		t.Fatal(err)
	}
	original.DesktopEnabled = false
	saved, err := store.SavePetSettings(ctx, original, now)
	if err != nil || saved.LastWindowX == nil || *saved.LastWindowX != -800 {
		t.Fatal("settings overwrote drag", err)
	}
	if err := store.SavePetPosition(ctx, 100, 200, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	settings, err := reopened.PetSettings(ctx, now)
	if err != nil || settings.DesktopEnabled || settings.LastWindowX == nil || *settings.LastWindowX != 100 || settings.LastWindowY == nil || *settings.LastWindowY != 200 {
		t.Fatal("position or visibility lost on restart", err)
	}
}
