package jobdb

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func workTestStore(t *testing.T) *Store {
	t.Helper()
	s, _, err := OpenPrepared(context.Background(), filepath.Join(t.TempDir(), "liberty.sqlite3"), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func workTestTime(value string) time.Time { t, _ := time.Parse(time.RFC3339Nano, value); return t }
func workTestSeed(t *testing.T, s *Store, key string, qty int64, now time.Time) {
	t.Helper()
	err := s.transaction(context.Background(), func(tx *sql.Tx) error {
		text := formatWorkTime(now)
		if err := ensurePet(context.Background(), tx, text); err != nil {
			return err
		}
		if err := ensurePetStore(context.Background(), tx, text); err != nil {
			return err
		}
		item, ok := domain.FindPetItem(key)
		if !ok {
			t.Fatalf("unknown fixture item %s", key)
		}
		return grantPetItem(context.Background(), tx, item, qty, "synthetic_test", text)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFarmFullCycleWaitsForEachCareAndConsumesSeedOnce(t *testing.T) {
	s := workTestStore(t)
	ctx := context.Background()
	now := workTestTime("2026-07-15T12:00:00+00:00")
	workTestSeed(t, s, "carrot-seed", 2, now)
	state, err := s.PlantCrop(ctx, "farm-plot-1", "carrot", now)
	if err != nil {
		t.Fatal(err)
	}
	if state.Plots[0].Status != domain.FarmPlanted || *state.Plots[0].NextCareAt != "2026-07-15T12:05:00+00:00" {
		t.Fatalf("bad first stage %+v", state.Plots[0])
	}
	if _, err = s.PlantCrop(ctx, "farm-plot-1", "carrot", now); err == nil {
		t.Fatal("duplicate planting accepted")
	}
	var seeds int64
	if err = s.db.QueryRowContext(ctx, `SELECT quantity FROM pet_inventory WHERE item_key='carrot-seed'`).Scan(&seeds); err != nil || seeds != 1 {
		t.Fatal("seed consumption not atomic", seeds, err)
	}
	if _, err = s.WaterPlot(ctx, "farm-plot-1", now.Add(4*time.Minute)); err == nil {
		t.Fatal("early care accepted")
	}
	state, err = s.FarmState(ctx, now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if state.Plots[0].Status != domain.FarmNeedsWater || state.Plots[0].StageIndex != 0 {
		t.Fatal("offline time skipped required care")
	}
	waterAt := now.Add(24 * time.Hour)
	state, err = s.WaterPlot(ctx, "farm-plot-1", waterAt)
	if err != nil {
		t.Fatal(err)
	}
	if state.Plots[0].MatureAt != nil || *state.Plots[0].NextCareAt != formatWorkTime(waterAt.Add(5*time.Minute)) {
		t.Fatal("late care interval not measured from action")
	}
	state, err = s.WaterPlot(ctx, "farm-plot-1", waterAt.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if state.Plots[0].MatureAt == nil || *state.Plots[0].MatureAt != formatWorkTime(waterAt.Add(10*time.Minute)) {
		t.Fatal("final growth interval missing")
	}
	if _, err = s.HarvestPlot(ctx, "farm-plot-1", waterAt.Add(9*time.Minute)); err == nil {
		t.Fatal("early harvest accepted")
	}
	result, err := s.HarvestPlot(ctx, "farm-plot-1", waterAt.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Plots[0].Status != domain.FarmEmpty || result.Harvest.LPReward < 3 || result.Harvest.LPReward > 6 {
		t.Fatal("harvest outcome differs")
	}
	ledger, err := s.HarvestLedger(ctx, 0)
	if err != nil || len(ledger) != 0 {
		t.Fatal("zero limit must return no harvests", ledger, err)
	}
	ledger, err = s.HarvestLedger(ctx, 1)
	if err != nil || len(ledger) != 1 || ledger[0].ID != result.Harvest.ID {
		t.Fatal("harvest ledger did not return the recorded harvest", err)
	}
	if _, err = s.HarvestPlot(ctx, "farm-plot-1", waterAt.Add(10*time.Minute)); err == nil {
		t.Fatal("cleared plot harvested twice")
	}
}

func TestPlantCropOnlyUpdatesConsumedSeedAndPlot(t *testing.T) {
	s := workTestStore(t)
	ctx := context.Background()
	seededAt := workTestTime("2026-07-17T00:08:00+00:00")
	workTestSeed(t, s, "carrot-seed", 2, seededAt)
	plantedAt := workTestTime("2026-07-18T23:58:00+00:00")
	state, err := s.PlantCrop(ctx, "farm-plot-1", "carrot", plantedAt)
	if err != nil {
		t.Fatal(err)
	}
	if state.Plots[0].Status != domain.FarmPlanted || state.Plots[0].PlantedAt == nil || *state.Plots[0].PlantedAt != formatWorkTime(plantedAt) {
		t.Fatal("planting was not persisted", state.Plots[0])
	}
	var defaultUpdatedAt, seedUpdatedAt string
	var seedQuantity int64
	if err := s.db.QueryRowContext(ctx, `SELECT updated_at FROM pet_inventory WHERE item_key='libby-default'`).Scan(&defaultUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if defaultUpdatedAt != formatWorkTime(seededAt) {
		t.Fatalf("planting changed unrelated inventory: got %s, want %s", defaultUpdatedAt, formatWorkTime(seededAt))
	}
	if err := s.db.QueryRowContext(ctx, `SELECT quantity,updated_at FROM pet_inventory WHERE item_key='carrot-seed'`).Scan(&seedQuantity, &seedUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if seedQuantity != 1 || seedUpdatedAt != formatWorkTime(plantedAt) {
		t.Fatal("seed consumption was not persisted", seedQuantity, seedUpdatedAt)
	}
}

func TestWorkClaimsPreserveExistingProfileWithoutRepairingSettings(t *testing.T) {
	for _, farm := range []bool{true, false} {
		t.Run(map[bool]string{true: "farm", false: "work"}[farm], func(t *testing.T) {
			store := workTestStore(t)
			ctx := context.Background()
			now := workTestTime("2026-07-15T12:00:00+00:00")
			workTestSeed(t, store, "wheat-seed", 1, now)
			if _, err := store.FarmState(ctx, now); err != nil {
				t.Fatal(err)
			}
			if _, err := store.WorkGameState(ctx, "factory", now); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.ExecContext(ctx, `DELETE FROM pet_settings WHERE pet_id='default-pet'`); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.ExecContext(ctx, `UPDATE pet_profile SET name='Synthetic preserved profile',level=37,experience=1,stage='first_meet',current_mood='calm',created_at='before',updated_at='after' WHERE id='default-pet'`); err != nil {
				t.Fatal(err)
			}
			query := `UPDATE farm_plots SET crop_key='wheat',status='mature',stage_index=1,planted_at='2026-07-15T10:00:00+00:00',next_care_at=NULL,mature_at='2026-07-15T11:00:00+00:00' WHERE id='farm-plot-1'`
			if !farm {
				query = `UPDATE work_game_tasks SET job_key='basic-assembly',status='claimable',stage_index=1,started_at='2026-07-15T10:00:00+00:00',next_care_at=NULL,claimable_at='2026-07-15T11:00:00+00:00' WHERE id='factory-slot-1'`
			}
			if _, err := store.db.ExecContext(ctx, query); err != nil {
				t.Fatal(err)
			}
			if farm {
				if _, err := store.HarvestPlot(ctx, "farm-plot-1", now); err != nil {
					t.Fatal(err)
				}
			} else if _, err := store.ClaimWorkTask(ctx, "factory", "factory-slot-1", now); err != nil {
				t.Fatal(err)
			}
			path := store.path
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			var settings int
			if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pet_settings WHERE pet_id='default-pet'`).Scan(&settings); err != nil || settings != 0 {
				t.Fatal("claim repaired unrelated settings", settings, err)
			}
			var name, stage, mood, created, updated string
			var level, experience int64
			if err := reopened.db.QueryRowContext(ctx, `SELECT name,level,experience,stage,current_mood,created_at,updated_at FROM pet_profile WHERE id='default-pet'`).Scan(&name, &level, &experience, &stage, &mood, &created, &updated); err != nil {
				t.Fatal(err)
			}
			if name != "Synthetic preserved profile" || level != 37 || experience != 1 || stage != "first_meet" || mood != "calm" || created != "before" || updated != "after" {
				t.Fatal("claim changed the existing profile", name, level, experience, stage, mood, created, updated)
			}
		})
	}
}

func TestWorkAllMapsRespectSlotAndCareIntervals(t *testing.T) {
	s := workTestStore(t)
	ctx := context.Background()
	now := workTestTime("2026-07-15T12:00:00+00:00")
	for _, game := range domain.WorkGameCatalog() {
		for _, job := range game.Jobs {
			id := game.Key + "-slot-" + string(rune('0'+job.SlotIndex))
			state, err := s.StartWorkTask(ctx, game.Key, id, job.JobKey, now)
			if err != nil {
				t.Fatal(err)
			}
			step := time.Duration(job.DurationSeconds/(job.CareRequired+1)) * time.Second
			task := state.Tasks[job.SlotIndex-1]
			if task.Job == nil || task.Job.GameKey != game.Key || *task.NextCareAt != formatWorkTime(now.Add(step)) {
				t.Fatalf("bad task projection %+v", task)
			}
			if _, err = s.CareWorkTask(ctx, game.Key, id, now); err == nil {
				t.Fatal("early care accepted")
			}
			for stage := int64(1); stage <= job.CareRequired; stage++ {
				state, err = s.CareWorkTask(ctx, game.Key, id, now.Add(step*time.Duration(stage)))
				if err != nil {
					t.Fatal(err)
				}
				if state.Tasks[job.SlotIndex-1].StageIndex != stage {
					t.Fatal("care stage not persisted")
				}
			}
			task = state.Tasks[job.SlotIndex-1]
			if task.NextCareAt != nil || task.ClaimableAt == nil {
				t.Fatal("final care did not select claim deadline")
			}
			claimAt := now.Add(step * time.Duration(job.CareRequired+1))
			result, err := s.ClaimWorkTask(ctx, game.Key, id, claimAt)
			if err != nil {
				t.Fatal(err)
			}
			if result.State.Tasks[job.SlotIndex-1].Status != domain.WorkIdle || result.Reward.LPReward < job.LPMin || result.Reward.LPReward > job.LPMax {
				t.Fatal("invalid claim state/reward")
			}
			if _, err = s.ClaimWorkTask(ctx, game.Key, id, claimAt); err == nil {
				t.Fatal("duplicate claim accepted")
			}
		}
	}
	if _, err := s.StartWorkTask(ctx, "mine", "factory-slot-1", "shallow-vein", now); err == nil {
		t.Fatal("cross-map task accepted")
	}
	if _, err := s.StartWorkTask(ctx, "mine", "mine-slot-1", "deep-vein", now); err == nil {
		t.Fatal("cross-slot job accepted")
	}
}

func TestWorkAndFarmReplayAndLedgerFailureAreAtomic(t *testing.T) {
	for _, farm := range []bool{true, false} {
		t.Run(map[bool]string{true: "farm", false: "work"}[farm], func(t *testing.T) {
			s := workTestStore(t)
			ctx := context.Background()
			now := workTestTime("2026-07-15T12:00:00+00:00")
			prepare := func(at string) {
				t.Helper()
				if farm {
					if _, e := s.FarmState(ctx, now); e != nil {
						t.Fatal(e)
					}
				} else {
					if _, e := s.WorkGameState(ctx, "factory", now); e != nil {
						t.Fatal(e)
					}
				}
				query := `UPDATE farm_plots SET crop_key='wheat',status='mature',stage_index=1,planted_at=?,next_care_at=NULL,mature_at=? WHERE id='farm-plot-1'`
				if !farm {
					query = `UPDATE work_game_tasks SET job_key='basic-assembly',status='claimable',stage_index=1,started_at=?,next_care_at=NULL,claimable_at=? WHERE id='factory-slot-1'`
				}
				if _, e := s.db.ExecContext(ctx, query, at, formatWorkTime(now)); e != nil {
					t.Fatal(e)
				}
			}
			claim := func() (string, error) {
				if farm {
					v, e := s.HarvestPlot(ctx, "farm-plot-1", now)
					return v.Harvest.ID, e
				}
				v, e := s.ClaimWorkTask(ctx, "factory", "factory-slot-1", now)
				return v.Reward.ID, e
			}
			table, item, statusQuery, expected := "farm_harvest_ledger", "wheat-harvest-food", `SELECT status FROM farm_plots WHERE id='farm-plot-1'`, domain.FarmMature
			if !farm {
				table, item, statusQuery, expected = "work_game_reward_ledger", "energy-capsule-tool", `SELECT status FROM work_game_tasks WHERE id='factory-slot-1'`, domain.WorkClaimable
			}
			prepare("2026-07-15T10:00:00+00:00")
			if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_work_ledger BEFORE INSERT ON `+table+` BEGIN SELECT RAISE(ABORT,'synthetic ledger failure');END`); err != nil {
				t.Fatal(err)
			}
			if _, err := claim(); err == nil {
				t.Fatal("injected failure passed")
			}
			var status string
			if err := s.db.QueryRowContext(ctx, statusQuery).Scan(&status); err != nil || status != expected {
				t.Fatal("failed ledger consumed state", status, err)
			}
			var amount int64
			if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(quantity),0) FROM pet_inventory WHERE item_key=?`, item).Scan(&amount); err != nil || amount != 0 {
				t.Fatal("failed ledger granted items", amount, err)
			}
			if _, err := s.db.ExecContext(ctx, `DROP TRIGGER reject_work_ledger`); err != nil {
				t.Fatal(err)
			}
			first := ""
			for i, at := range []string{"2026-07-15T10:00:00+00:00", "2026-07-15T11:00:00+00:00", "2026-07-15T10:00:00+00:00"} {
				prepare(at)
				id, err := claim()
				if err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					first = id
				}
				if i == 2 && id != first {
					t.Fatal("replay changed ledger identity")
				}
			}
			var entries, total, balance int64
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(lp_reward),0) FROM `+table).Scan(&entries, &total); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRowContext(ctx, `SELECT quantity FROM pet_inventory WHERE item_key=?`, item).Scan(&amount); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRowContext(ctx, `SELECT balance FROM pet_wallets WHERE pet_id='default-pet' AND currency_key='lp'`).Scan(&balance); err != nil {
				t.Fatal(err)
			}
			if entries != 2 || amount != 2 || balance != total {
				t.Fatalf("A/B/A duplicated reward entries=%d items=%d balance=%d ledger=%d", entries, amount, balance, total)
			}
		})
	}
}
