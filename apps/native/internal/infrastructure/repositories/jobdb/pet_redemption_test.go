package jobdb

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func redemptionStore(t *testing.T) *Store {
	t.Helper()
	s, _, err := OpenPrepared(context.Background(), filepath.Join(t.TempDir(), "synthetic.sqlite3"), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func redemptionPayload() domain.PetRedemptionPayload {
	return domain.PetRedemptionPayload{Version: 1, CampaignID: "synthetic-campaign", Nonce: "synthetic-nonce", Rewards: domain.PetRedeemKeyRewards{LP: 40, GrowthValue: 31, Items: []domain.PetRedeemKeyRewardItem{{ItemKey: "wheat-seed", Quantity: 2}}}}
}
func TestPetRedemptionTransactionDuplicateHistoryAndReopen(t *testing.T) {
	s := redemptionStore(t)
	ctx := context.Background()
	now := "2026-11-18T12:00:00Z"
	code := "LIB1.synthetic-test.signature"
	var calls atomic.Int32
	verifier := func(value string, at time.Time) (domain.PetRedemptionPayload, error) {
		calls.Add(1)
		if value != code || at.Format(time.RFC3339) != now {
			t.Error("verification input mismatch")
		}
		return redemptionPayload(), nil
	}
	first, err := s.redeemKey(ctx, " \t"+code+"\n", now, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if first.Duplicate || first.State.Wallet.Balance != 40 || first.State.Profile.Experience != 31 || first.Redemption.Status != "redeemed" {
		t.Fatal("reward was not persisted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := s.redeemKey(ctx, code, now, verifier)
			if err != nil || !v.Duplicate || v.State.Wallet.Balance != 40 || v.State.Profile.Experience != 31 {
				t.Error("duplicate reward changed state", err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate reverified or issued rewards")
	}
	history, err := s.PetRedemptions(ctx, 1000)
	if err != nil || len(history) != 1 || history[0].ID != first.Redemption.ID {
		t.Fatal("history mismatch", err)
	}
	if strings.Contains(history[0].RewardJson, code) || (history[0].Metadata != nil && strings.Contains(*history[0].Metadata, code)) {
		t.Fatal("raw code leaked into persisted record")
	}
	var qty, count int64
	if s.db.QueryRow(`SELECT quantity FROM pet_inventory WHERE item_key='wheat-seed'`).Scan(&qty) != nil || qty != 2 {
		t.Fatal("item reward duplicated")
	}
	if s.db.QueryRow(`SELECT COUNT(*) FROM pet_event_ledger WHERE event_type='redeem_key'`).Scan(&count) != nil || count != 1 {
		t.Fatal("event duplicated")
	}
	// A previously redeemed code stays idempotent even if current verification fails.
	again, err := s.redeemKey(ctx, code, "2030-01-01T00:00:00Z", func(string, time.Time) (domain.PetRedemptionPayload, error) {
		return domain.PetRedemptionPayload{}, domain.ErrPetRedemptionExpired
	})
	if err != nil || !again.Duplicate {
		t.Fatal("existing expired reward not returned", err)
	}
	path := filepath.Join(s.owner.Directory(), "synthetic.sqlite3")
	s.Close()
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	history, err = reopened.PetRedemptions(ctx, 1)
	if err != nil || len(history) != 1 || history[0].KeyHash != domain.PetRedemptionHash(code) {
		t.Fatal("reopen lost history", err)
	}
}

func TestPetRedemptionReissuedIdentityDoesNotGrantRewardsAgain(t *testing.T) {
	s := redemptionStore(t)
	ctx := context.Background()
	now := "2026-11-18T12:00:00Z"
	payload := redemptionPayload()
	original, err := s.redeemKey(ctx, "legacy-short-code", now, func(string, time.Time) (domain.PetRedemptionPayload, error) {
		return payload, nil
	})
	if err != nil || original.Duplicate {
		t.Fatal("original code did not redeem", err)
	}
	reissued, err := s.redeemKey(ctx, "LIB1.signed-reissue", now, func(string, time.Time) (domain.PetRedemptionPayload, error) {
		return payload, nil
	})
	if err != nil || !reissued.Duplicate || reissued.Redemption.ID != original.Redemption.ID {
		t.Fatal("reissued code changed existing redemption", err)
	}
	if reissued.State.Wallet.Balance != 40 || reissued.State.Profile.Experience != 31 {
		t.Fatal("reissued code granted rewards twice")
	}
}

func TestPetRedemptionFailuresRollBackAllRewards(t *testing.T) {
	ctx := context.Background()
	now := "2026-11-18T12:00:00Z"
	for _, scenario := range []string{"invalid signature", "ledger failure", "wallet overflow", "growth overflow"} {
		t.Run(scenario, func(t *testing.T) {
			s := redemptionStore(t)
			if err := s.EnsurePet(ctx, now); err != nil {
				t.Fatal(err)
			}
			verifier := func(string, time.Time) (domain.PetRedemptionPayload, error) { return redemptionPayload(), nil }
			switch scenario {
			case "invalid signature":
				verifier = func(string, time.Time) (domain.PetRedemptionPayload, error) {
					return domain.PetRedemptionPayload{}, domain.ErrPetRedemption
				}
			case "ledger failure":
				if _, err := s.db.Exec(`CREATE TRIGGER fail_redemption BEFORE INSERT ON pet_redeem_key_redemptions BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "wallet overflow":
				if _, err := s.db.Exec(`UPDATE pet_wallets SET balance=?,lifetime_earned=?`, int64(math.MaxInt64), int64(math.MaxInt64)); err != nil {
					t.Fatal(err)
				}
			case "growth overflow":
				if _, err := s.db.Exec(`UPDATE pet_profile SET experience=?`, int64(math.MaxInt64)); err != nil {
					t.Fatal(err)
				}
			}
			var before int64
			if err := s.db.QueryRow(`SELECT balance FROM pet_wallets`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := s.redeemKey(ctx, "LIB1.synthetic.failure", now, verifier); err == nil {
				t.Fatal("expected failure")
			}
			var balance, history, events, items int64
			if s.db.QueryRow(`SELECT balance FROM pet_wallets`).Scan(&balance) != nil || balance != before {
				t.Fatal("partial LP grant escaped rollback")
			}
			if s.db.QueryRow(`SELECT COUNT(*) FROM pet_redeem_key_redemptions`).Scan(&history) != nil || history != 0 {
				t.Fatal("partial redemption escaped rollback")
			}
			if s.db.QueryRow(`SELECT COUNT(*) FROM pet_event_ledger WHERE event_type='redeem_key'`).Scan(&events) != nil || events != 0 {
				t.Fatal("partial event escaped rollback")
			}
			if s.db.QueryRow(`SELECT COUNT(*) FROM pet_inventory WHERE item_key='wheat-seed'`).Scan(&items) != nil || items != 0 {
				t.Fatal("partial items escaped rollback")
			}
		})
	}
	s := redemptionStore(t)
	if _, err := s.RedeemKey(ctx, "TEST-short", now); !errors.Is(err, domain.ErrPetRedemptionUnsupported) {
		t.Fatal("public entry bypassed verifier", err)
	}
	var row int
	if err := s.db.QueryRow(`SELECT 1 FROM pet_profile LIMIT 1`).Scan(&row); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed code initialized partial state", err)
	}
}

func TestPetRedemptionInputBoundsFailBeforeVerificationAndLeaveNoState(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []struct {
		name, code string
		calls      int
	}{
		{"normalized boundary", strings.Repeat("a", 32*1024), 1},
		{"normalized excess", strings.Repeat("a", 32*1024+1), 0},
		{"raw boundary", strings.Repeat(" ", 64*1024-1) + "a", 1},
		{"raw excess", strings.Repeat(" ", 64*1024) + "a", 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store := redemptionStore(t)
			calls := 0
			_, err := store.redeemKey(ctx, scenario.code, "2026-11-18T12:00:00Z", func(string, time.Time) (domain.PetRedemptionPayload, error) {
				calls++
				return domain.PetRedemptionPayload{}, domain.ErrPetRedemption
			})
			if !errors.Is(err, domain.ErrPetRedemption) || calls != scenario.calls {
				t.Fatal("redemption input limit changed", calls, err)
			}
			var count int
			if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pet_profile").Scan(&count); err != nil || count != 0 {
				t.Fatal("rejected input initialized pet state", count, err)
			}
		})
	}
}
