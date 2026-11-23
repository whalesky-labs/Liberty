package application

import (
	"context"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type petUseCaseCall struct {
	method, first, second, now string
	quantity                   int64
}

type petUseCaseRepository struct {
	PetRepository
	calls []petUseCaseCall
}

func (repository *petUseCaseRepository) PurchasePet(_ context.Context, key string, quantity int64, now string) (domain.PetStoreState, error) {
	repository.calls = append(repository.calls, petUseCaseCall{method: "purchase", first: key, quantity: quantity, now: now})
	return domain.PetStoreState{}, nil
}

func (repository *petUseCaseRepository) OpenPetGiftBox(_ context.Context, now string) (domain.PetGiftBoxOpenResult, error) {
	repository.calls = append(repository.calls, petUseCaseCall{method: "gift", now: now})
	return domain.PetGiftBoxOpenResult{}, nil
}

func (repository *petUseCaseRepository) PetClaimCheckIn(_ context.Context, now string) (domain.PetDailyCheckInClaimResult, error) {
	repository.calls = append(repository.calls, petUseCaseCall{method: "check-in", now: now})
	return domain.PetDailyCheckInClaimResult{}, nil
}

func (repository *petUseCaseRepository) PetMakeupCheckIn(_ context.Context, now string) (domain.PetDailyCheckInMakeupResult, error) {
	repository.calls = append(repository.calls, petUseCaseCall{method: "makeup", now: now})
	return domain.PetDailyCheckInMakeupResult{}, nil
}

func (repository *petUseCaseRepository) PetDrawBlindBox(_ context.Context, now string) (domain.PetBlindBoxDrawResult, error) {
	repository.calls = append(repository.calls, petUseCaseCall{method: "draw", now: now})
	return domain.PetBlindBoxDrawResult{}, nil
}

func (repository *petUseCaseRepository) RedeemKey(_ context.Context, key, now string) (domain.PetRedeemKeyResult, error) {
	repository.calls = append(repository.calls, petUseCaseCall{method: "redeem", first: key, now: now})
	return domain.PetRedeemKeyResult{}, nil
}

func (repository *petUseCaseRepository) PetGrowth(_ context.Context, mode, source string, key *string, now string) (domain.PetProfile, error) {
	value := ""
	if key != nil {
		value = *key
	}
	repository.calls = append(repository.calls, petUseCaseCall{method: mode, first: source, second: value, now: now})
	return domain.PetProfile{}, nil
}

func TestPetUseCasesPreserveRewardOperationAndWorkflowSources(t *testing.T) {
	const timestamp = "2026-11-20T12:34:56.000000078Z"
	repository := &petUseCaseRepository{}
	service := NewPet(repository)
	service.now = func() string { return timestamp }
	ctx := context.Background()

	if _, err := service.Purchase(ctx, "synthetic-food", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GiftBox(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClaimCheckIn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.MakeupCheckIn(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DrawBlindBox(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Redeem(ctx, "LIB1.synthetic-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Workflow(ctx, "transcription_completed", "synthetic-job"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Interact(ctx, "feed"); err != nil {
		t.Fatal(err)
	}

	want := []petUseCaseCall{
		{method: "purchase", first: "synthetic-food", quantity: 3, now: timestamp},
		{method: "gift", now: timestamp},
		{method: "check-in", now: timestamp},
		{method: "makeup", now: timestamp},
		{method: "draw", now: timestamp},
		{method: "redeem", first: "LIB1.synthetic-key", now: timestamp},
		{method: "workflow", first: "transcription_completed", second: "synthetic-job", now: timestamp},
		{method: "interaction", first: "feed", second: "feed", now: timestamp},
	}
	if len(repository.calls) != len(want) {
		t.Fatalf("repository call count = %d, want %d", len(repository.calls), len(want))
	}
	for index := range want {
		if repository.calls[index] != want[index] {
			t.Errorf("call %d = %+v, want %+v", index, repository.calls[index], want[index])
		}
	}
}

type workUseCaseCall struct {
	method, game, task, job string
	now                     time.Time
}

type workUseCaseRepository struct {
	WorkRepository
	calls []workUseCaseCall
}

func (repository *workUseCaseRepository) CareWorkTask(_ context.Context, game, task string, now time.Time) (domain.WorkGameState, error) {
	repository.calls = append(repository.calls, workUseCaseCall{method: "care", game: game, task: task, now: now})
	return domain.WorkGameState{GameKey: game}, nil
}

func (repository *workUseCaseRepository) ClaimWorkTask(_ context.Context, game, task string, now time.Time) (domain.WorkGameClaimResult, error) {
	repository.calls = append(repository.calls, workUseCaseCall{method: "claim", game: game, task: task, now: now})
	return domain.WorkGameClaimResult{State: domain.WorkGameState{GameKey: game}, Reward: domain.WorkGameRewardLedgerEntry{TaskID: task}}, nil
}

func TestWorkCareAndClaimRemainDistinctTimedOperations(t *testing.T) {
	instant := time.Date(2026, 11, 20, 12, 34, 56, 78, time.FixedZone("synthetic", 5*3600))
	repository := &workUseCaseRepository{}
	service := NewWork(repository, func() time.Time { return instant })
	ctx := context.Background()

	state, err := service.Care(ctx, "factory", "factory-slot-1")
	if err != nil || state.GameKey != "factory" {
		t.Fatalf("care result = %+v, %v", state, err)
	}
	claim, err := service.Claim(ctx, "factory", "factory-slot-1")
	if err != nil || claim.State.GameKey != "factory" || claim.Reward.TaskID != "factory-slot-1" {
		t.Fatalf("claim result = %+v, %v", claim, err)
	}
	want := []workUseCaseCall{
		{method: "care", game: "factory", task: "factory-slot-1", now: instant},
		{method: "claim", game: "factory", task: "factory-slot-1", now: instant},
	}
	if len(repository.calls) != len(want) {
		t.Fatalf("repository call count = %d, want %d", len(repository.calls), len(want))
	}
	for index := range want {
		if repository.calls[index] != want[index] {
			t.Errorf("call %d = %+v, want %+v", index, repository.calls[index], want[index])
		}
	}
}
