package jobdb

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestPetHistoriesOrderEqualTimestampsByLatestInsertionAfterReopen(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "liberty.sqlite3")
	store, _, err := OpenPrepared(ctx, file, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if store != nil {
			store.Close()
		}
	}()
	const now = "2026-07-01T12:00:00Z"
	if _, err := store.PetStore(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := store.transaction(ctx, func(tx *sql.Tx) error {
		for sequence := range 27 {
			created := now
			if sequence == 0 {
				created = "2026-07-01T12:00:01Z"
			} else if sequence == 26 {
				created = "2026-07-01T11:59:59Z"
			}
			identity := fmt.Sprintf("synthetic-%03d", 100-sequence)
			if _, err := tx.ExecContext(ctx, `INSERT INTO pet_economy_ledger(id,pet_id,entry_type,currency_key,amount,balance_after,source_type,source_key,created_at) VALUES(?,'default-pet','earn','lp',1,?,'synthetic',?,?)`, identity, sequence+1, identity, created); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO pet_blind_box_draws(id,pet_id,draw_date,item_key,item_type,quantity,duplicate_compensation_lp,created_at) VALUES(?,'default-pet','2026-07-01','nothing','none',1,0,?)`, identity, created); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for restart := range 3 {
		for _, plan := range []string{"OFF", "ON"} {
			if _, err := store.db.ExecContext(ctx, "PRAGMA reverse_unordered_selects = "+plan); err != nil {
				t.Fatal(err)
			}
			state, err := store.PetBlindBox(ctx, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(state.History) != 20 || len(state.StoreState.Economy) != 20 {
				t.Fatal("history limits changed", len(state.History), len(state.StoreState.Economy))
			}
			for index := range 20 {
				sequence := 0
				if index > 0 {
					sequence = 26 - index
				}
				want := fmt.Sprintf("synthetic-%03d", 100-sequence)
				if entry := state.StoreState.Economy[index]; entry.ID != want || entry.BalanceAfter != int64(sequence+1) {
					t.Errorf("restart=%d plan=%s economy[%d]: ID=%s balance=%d want=%s/%d", restart, plan, index, entry.ID, entry.BalanceAfter, want, sequence+1)
				}
				if entry := state.History[index]; entry.ID != want {
					t.Errorf("restart=%d plan=%s draw[%d]: ID=%s want=%s", restart, plan, index, entry.ID, want)
				}
			}
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store, err = Open(ctx, file)
		if err != nil {
			t.Fatal(err)
		}
	}
}
