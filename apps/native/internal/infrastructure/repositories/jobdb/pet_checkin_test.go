package jobdb

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func TestCheckInMilestonesDoNotRepeatAfterDay14(t *testing.T) {
	store := workTestStore(t)
	ctx := context.Background()
	start := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.Local)
	for day := int64(1); day <= 21; day++ {
		now := start.AddDate(0, 0, int(day-1)).Format(time.RFC3339Nano)
		result, err := store.PetClaimCheckIn(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		if result.Duplicate || result.Entry.StreakCount != day || result.Entry.CycleDay != day {
			t.Fatalf("day %d: check-in track restarted: %+v", day, result.Entry)
		}
		if result.State.StoreState.Profile.Experience != day*5 || result.State.StoreState.Profile.CurrentMood != "cheerful" {
			t.Fatalf("day %d: returned stale profile: %+v", day, result.State.StoreState.Profile)
		}
		if day > 14 && (result.Entry.RewardLP != 20 || len(result.Entry.RewardItems) != 0 || result.State.TodayReward.CycleDay != day) {
			t.Fatalf("day %d: repeated a one-time milestone: %+v", day, result.Entry)
		}
		again, err := store.PetClaimCheckIn(ctx, now)
		if err != nil || !again.Duplicate || again.Entry.ID != result.Entry.ID || again.State.StoreState.Profile.Experience != day*5 {
			t.Fatalf("day %d: duplicate claim changed result: %+v %v", day, again, err)
		}
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pet_event_ledger WHERE event_type='daily_check_in' AND event_value=5`).Scan(&count); err != nil || count != 21 {
		t.Fatal("check-in events must record growth, not LP", count, err)
	}
	var metadata string
	if err := store.db.QueryRowContext(ctx, `SELECT metadata FROM pet_event_ledger WHERE event_type='daily_check_in' ORDER BY event_time DESC LIMIT 1`).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Source               string
		Makeup               bool
		StreakCount, FinalLP int64
		Zh, En               string
	}
	if err := json.Unmarshal([]byte(metadata), &event); err != nil || event.Makeup || event.Source != "daily_check_in" || event.StreakCount != 21 || event.FinalLP != 20 || event.Zh == "" || event.En == "" {
		t.Fatal("check-in event lost its user-facing metadata", event, err)
	}
}

func TestMakeupCheckInPreservesStreakAndReturnsUpdatedProfile(t *testing.T) {
	store := workTestStore(t)
	ctx := context.Background()
	start := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.Local)
	if _, err := store.PetClaimCheckIn(ctx, start.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	now := start.AddDate(0, 0, 3)
	workTestSeed(t, store, domain.PetMakeupTicket, 2, now)
	state, err := store.PetCheckIn(ctx, now.Format(time.RFC3339Nano))
	if err != nil || state.CurrentStreak != 1 || state.NextCycleDay != 2 || state.MissedDays != 2 || !state.MakeupAvailable {
		t.Fatal("makeup preview did not preserve history", state, err)
	}
	for day := int64(2); day <= 3; day++ {
		result, err := store.PetMakeupCheckIn(ctx, now.Format(time.RFC3339Nano))
		if err != nil || result.Entry.StreakCount != day || result.State.StoreState.Profile.Experience != day*5 || result.TicketQuantityAfter != 3-day {
			t.Fatal("makeup result was stale or consumed the wrong ticket quantity", result, err)
		}
		if result.State.NextCycleDay != day+1 {
			t.Fatal("makeup preview skipped the next day", result.State.NextCycleDay)
		}
	}
	if _, err := store.PetClaimCheckIn(ctx, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PetMakeupCheckIn(ctx, now.Format(time.RFC3339Nano)); err == nil || err.Error() != "今日已经签到过了。" {
		t.Fatal("makeup after today's claim did not preserve its reason", err)
	}
}
