package jobdb

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"liberty.local/liberty/internal/domain"
)

func TestSavePetNameRejectsEmptyWithoutChangingProfile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "liberty.sqlite3")
	store, _, err := OpenPrepared(ctx, path, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	const now = "2026-07-01T12:00:00+00:00"
	before, err := store.PetProfile(ctx, now)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	for _, name := range []string{"", " \t\n"} {
		_, err := store.SavePetName(ctx, name, "2026-07-02T12:00:00+00:00")
		if !errors.Is(err, domain.ErrPet) || err.Error() != "宠物名字不能为空。" {
			t.Errorf("empty name lost its safe pet validation error: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	after, err := store.PetProfile(ctx, now)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected names changed the persisted profile", after, err)
	}
}

func TestPetProfileReadDoesNotProvisionOrRefreshStore(t *testing.T) {
	ctx := context.Background()
	store, _, err := OpenPrepared(ctx, filepath.Join(t.TempDir(), "liberty.sqlite3"), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const now = "2026-07-01T12:00:00+00:00"
	if _, err := store.PetProfile(ctx, now); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"pet_inventory", "pet_wallets"} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("profile read provisioned store state", table, count, err)
		}
	}
	if err := store.EnsurePet(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PetProfile(ctx, "2026-07-02T12:00:00+00:00"); err != nil {
		t.Fatal(err)
	}
	var updated string
	if err := store.db.QueryRowContext(ctx, "SELECT updated_at FROM pet_inventory WHERE item_key='libby-default'").Scan(&updated); err != nil || updated != now {
		t.Fatal("profile read refreshed inventory", updated, err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE pet_profile SET stage='mature',level=1,experience=0"); err != nil {
		t.Fatal(err)
	}
	profile, err := store.PetProfile(ctx, now)
	if err != nil || profile.Stage != "deep_bond" || profile.LevelSnapshot.CurrentStage != profile.Stage || profile.LevelSnapshot.CurrentStageLabelZh != "深深羁绊" {
		t.Fatal("legacy stage and full profile snapshot differ", profile, err)
	}
}

func TestPetGrowthNormalizesMetadataAndKeepsClaimsAfterReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "liberty.sqlite3")
	store, _, err := OpenPrepared(ctx, path, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if store != nil {
			store.Close()
		}
	}()
	const now = "2026-07-01T12:00:00+00:00"
	metadata := " \t synthetic-core-job \n"
	profile, err := store.PetGrowth(ctx, "workflow", " JOB_CREATED ", &metadata, now)
	if err != nil || profile.Experience != 5 {
		t.Fatal(profile, err)
	}
	metadata = "synthetic-core-job"
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.PetGrowth(ctx, "workflow", "job_created", &metadata, now)
	if err != nil || !reflect.DeepEqual(profile, again) {
		t.Fatal("normalized duplicate changed profile after reopen", again, err)
	}
	action := " FEED "
	interaction, err := store.PetGrowth(ctx, "interaction", action, &action, now)
	if err != nil || interaction.Experience != 6 || interaction.CurrentMood != "proud" {
		t.Fatal(interaction, err)
	}
	events, err := store.PetEvents(ctx, 100)
	if err != nil || len(events) != 2 {
		t.Fatal("duplicate event was inserted", events, err)
	}
	for _, event := range events {
		var reward struct{ Source string }
		if event.Metadata == nil || json.Unmarshal([]byte(*event.Metadata), &reward) != nil {
			t.Fatal("missing reward metadata", event)
		}
		expected := "synthetic-core-job"
		if event.EventType == "interaction" {
			expected = ""
		}
		if reward.Source != expected {
			t.Fatal("event metadata differs from normalized command contract", event, reward)
		}
	}
	var balance, nonNullInteractionMetadata int
	if err := store.db.QueryRowContext(ctx, "SELECT balance FROM pet_wallets WHERE pet_id='default-pet'").Scan(&balance); err != nil || balance != 16 {
		t.Fatal("duplicate LP reward", balance, err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pet_economy_ledger WHERE source_key LIKE 'interaction:%' AND metadata IS NOT NULL").Scan(&nonNullInteractionMetadata); err != nil || nonNullInteractionMetadata != 0 {
		t.Fatal("interaction persisted command payload as metadata", nonNullInteractionMetadata, err)
	}
}

func TestSavePetSettingsFailureRollsBackWithoutLeakingSQLiteError(t *testing.T) {
	ctx := context.Background()
	store, _, err := OpenPrepared(ctx, filepath.Join(t.TempDir(), "liberty.sqlite3"), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const now = "2026-07-01T12:00:00+00:00"
	before, err := store.PetSettings(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "CREATE TRIGGER synthetic_core_settings_failure BEFORE UPDATE ON pet_settings BEGIN SELECT RAISE(ABORT,'synthetic-internal-database-detail'); END"); err != nil {
		t.Fatal(err)
	}
	value := before
	value.DesktopEnabled, value.Muted = false, true
	if _, err := store.SavePetSettings(ctx, value, "2026-07-02T12:00:00+00:00"); !errors.Is(err, ErrDatabase) || err.Error() != ErrDatabase.Error() {
		t.Fatal("settings save exposed its SQLite error", err)
	}
	after, err := store.PetSettings(ctx, now)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed save changed persisted settings", after, err)
	}
}
