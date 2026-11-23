//go:build compatibility

package jobdb

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

type petStoreCompatibilityInput struct {
	Name          string `json:"name"`
	Operation     string `json:"operation"`
	Key           string `json:"key"`
	Quantity      int64  `json:"quantity"`
	Now           string `json:"now"`
	Date          string `json:"date"`
	Rejected      bool   `json:"rejected"`
	Fault         string `json:"fault"`
	DatabaseError bool   `json:"databaseError"`
}

func bindPetStoreIDs(ids *checkinIDs, rust, native map[string]any, baseline bool) error {
	identities := map[string][]int{
		"profile": {0}, "checkins": {1, 2}, "events": {1, 2, 3, 5},
		"economy": {1, 3, 6, 7}, "inventory": {1, 2}, "cosmetics": {1, 2, 3},
	}
	ids.active = [2]map[string]map[string]string{{}, {}}
	for category, indices := range identities {
		rowsByKey := [2]map[string]string{{}, {}}
		for side, snapshot := range []map[string]any{rust, native} {
			rows, ok := snapshot[category].([]any)
			if !ok {
				return fmt.Errorf("%s: missing table", category)
			}
			ids.active[side][category] = map[string]string{}
			occurrences := map[string]int{}
			for _, value := range rows {
				row, ok := value.([]any)
				if !ok || len(row) != len(strings.Split(checkinTableColumns[category], ",")) {
					return fmt.Errorf("%s: malformed row", category)
				}
				id, ok := row[0].(string)
				if !ok || id == "" {
					return fmt.Errorf("%s: invalid ID", category)
				}
				keyValues := make([]any, len(indices))
				for index, column := range indices {
					keyValues[index] = row[column]
				}
				key, err := json.Marshal(keyValues)
				if err != nil {
					return err
				}
				identity := string(key)
				if category == "events" {
					occurrences[identity]++
					identity = fmt.Sprintf("%s#%d", identity, occurrences[identity])
				}
				if _, exists := rowsByKey[side][identity]; exists {
					return fmt.Errorf("%s: duplicate business identity %s", category, identity)
				}
				if _, exists := ids.active[side][category][id]; exists {
					return fmt.Errorf("%s: duplicate primary ID %q", category, id)
				}
				rowsByKey[side][identity] = id
				ids.active[side][category][id] = identity
			}
		}
		if len(rowsByKey[0]) != len(rowsByKey[1]) {
			return fmt.Errorf("%s: row count differs: Rust=%d Go=%d", category, len(rowsByKey[0]), len(rowsByKey[1]))
		}
		for identity, rustID := range rowsByKey[0] {
			if err := ids.bind(category, identity, rustID, rowsByKey[1][identity], baseline); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestWriteRustPetStoreActionsAndReopen(t *testing.T) {
	ctx := context.Background()
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if directory == "" || !filepath.IsAbs(directory) {
		t.Fatal("export the Rust pet store fixture into an explicit LIBERTY_NATIVE_FIXTURE_DIR")
	}
	var fixture struct {
		ClockMode string            `json:"clockMode"`
		Queries   map[string]string `json:"queries"`
		Excluded  []string          `json:"excluded"`
		Cases     []struct {
			Name    string `json:"name"`
			Initial any    `json:"initial"`
			Steps   []struct {
				Input         petStoreCompatibilityInput `json:"input"`
				Output        any                        `json:"output"`
				RawError      *string                    `json:"rawError"`
				Trigger       *string                    `json:"trigger"`
				Persisted     any                        `json:"persisted"`
				ReopenedState any                        `json:"reopenedState"`
				AfterRead     any                        `json:"afterRead"`
			} `json:"steps"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "pet-store-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		t.Fatal("trailing pet store fixture JSON")
	}
	if fixture.ClockMode != "thread-local-fixed" || len(fixture.Queries) != 10 || len(fixture.Cases) != 2 || len(fixture.Excluded) != 2 || fixture.Excluded[0] != "random gift-box opening" || fixture.Excluded[1] != "blind-box drawing" {
		t.Fatal("incomplete Rust pet store oracle")
	}
	var faultHits atomic.Int64
	faultFunction := fmt.Sprintf("liberty_pet_store_fault_%d", checkinFaultSequence.Add(1))
	if err := sqlite.RegisterScalarFunction(faultFunction, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		faultHits.Add(1)
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Parallel()
	steps, rejections, faults, duplicates := 0, 0, 0, 0
	seenCases := map[string]bool{}
	for _, scenario := range fixture.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			if seenCases[scenario.Name] || (scenario.Name != "exact-balance" && scenario.Name != "funded") {
				t.Fatal("duplicate or unknown scenario", scenario.Name)
			}
			seenCases[scenario.Name] = true
			expectedSteps := 22
			if scenario.Name == "funded" {
				expectedSteps = 61
			}
			if len(scenario.Steps) != expectedSteps {
				t.Fatalf("missing action coverage: %s has %d steps, expected %d", scenario.Name, len(scenario.Steps), expectedSteps)
			}
			baseline, err := os.ReadFile(filepath.Join(directory, "pet-store-"+scenario.Name+"-base.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "liberty.sqlite3")
			if err := os.WriteFile(path, baseline, 0600); err != nil {
				t.Fatal(err)
			}
			store, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if store != nil {
					store.Close()
				}
			})
			reopen := func(t *testing.T) {
				t.Helper()
				if err := store.Close(); err != nil {
					store = nil
					t.Fatal(err)
				}
				store = nil
				store, err = Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
			}
			ids := newCheckinIDs()
			compareSnapshot := func(t *testing.T, label string, expected, actual any, baseline bool) {
				t.Helper()
				rust := decodeCheckinJSON(t, expected).(map[string]any)
				native := decodeCheckinJSON(t, actual).(map[string]any)
				if err := bindPetStoreIDs(ids, rust, native, baseline); err != nil {
					t.Fatalf("%s: %v", label, err)
				}
				for side, snapshot := range []map[string]any{rust, native} {
					if err := ids.normalizeSnapshot(snapshot, side); err != nil {
						t.Fatal(err)
					}
				}
				compareDecodedCheckinJSON(t, label, rust, native)
			}
			compareAPI := func(t *testing.T, label string, expected, actual any) {
				t.Helper()
				values := []any{decodeCheckinJSON(t, expected), decodeCheckinJSON(t, actual)}
				for side, value := range values {
					if err := ids.normalizeAPI(value, side); err != nil {
						t.Fatalf("%s: %v", label, err)
					}
				}
				compareDecodedCheckinJSON(t, label, values[0], values[1])
			}
			persist := func(t *testing.T) map[string]any { return checkinPersistence(t, store, fixture.Queries) }
			compareSnapshot(t, "initial", scenario.Initial, persist(t), true)
			seen := map[string]bool{}
			for _, step := range scenario.Steps {
				if !t.Run(step.Input.Name, func(t *testing.T) {
					if seen[step.Input.Name] {
						t.Fatal("duplicate step name")
					}
					seen[step.Input.Name] = true
					steps++
					now, err := time.Parse(time.RFC3339Nano, step.Input.Now)
					if err != nil || now.Local().Format("2006-01-02") != step.Input.Date {
						t.Fatal("Go and Rust fixed local dates disagree", err)
					}
					faultHits.Store(0)
					if step.Trigger != nil {
						if _, err := store.db.ExecContext(ctx, strings.ReplaceAll(*step.Trigger, "liberty_checkin_fault_probe", faultFunction)); err != nil {
							t.Fatal(err)
						}
					}
					before := persist(t)
					var output any
					var actionErr error
					switch step.Input.Operation {
					case "state":
						output, actionErr = store.PetStore(ctx, step.Input.Now)
					case "purchase":
						output, actionErr = store.PurchasePet(ctx, step.Input.Key, step.Input.Quantity, step.Input.Now)
					case "use":
						output, actionErr = store.UsePetItem(ctx, step.Input.Key, step.Input.Quantity, step.Input.Now)
					case "equip":
						output, actionErr = store.EquipPetItem(ctx, step.Input.Key, step.Input.Now)
					case "unequip":
						output, actionErr = store.UnequipPetSlot(ctx, step.Input.Key, step.Input.Now)
					default:
						t.Fatal("unknown store operation", step.Input.Operation)
					}
					if (actionErr != nil) != step.Input.Rejected {
						t.Errorf("Rust rejected=%v, Go error=%v", step.Input.Rejected, actionErr)
					}
					if step.Input.Fault != "" {
						faults++
						if !errors.Is(actionErr, ErrDatabase) || faultHits.Load() != 1 || step.RawError == nil || *step.RawError != "synthetic-pet-store-write-failure" {
							t.Fatalf("trigger provenance: error=%v hits=%d Rust=%v", actionErr, faultHits.Load(), step.RawError)
						}
						output = map[string]any{"fault": "synthetic-pet-store-write-failure"}
					} else if step.Input.DatabaseError {
						duplicates++
						if !errors.Is(actionErr, ErrDatabase) || step.RawError == nil || *step.RawError != "UNIQUE constraint failed: pet_economy_ledger.pet_id, pet_economy_ledger.currency_key, pet_economy_ledger.source_type, pet_economy_ledger.source_key" {
							t.Fatalf("duplicate purchase must hit the ledger constraint: %v", actionErr)
						}
						output = map[string]any{"databaseError": "duplicate-purchase-key"}
					} else if actionErr != nil {
						output = map[string]any{"error": actionErr.Error()}
					}
					if step.Input.Fault == "" && faultHits.Load() != 0 {
						t.Fatal("unexpected fault probe")
					}
					persisted := persist(t)
					compareSnapshot(t, "persisted", step.Persisted, persisted, false)
					compareAPI(t, "output", step.Output, output)
					if step.Input.Rejected {
						rejections++
						compareCheckinJSON(t, "rejected-action-rollback", before, persisted)
					}
					reopen(t)
					compareSnapshot(t, "reopened", step.Persisted, persist(t), false)
					compareCheckinJSON(t, "reopen-preserves-Go-writes", persisted, persist(t))
					if step.Trigger != nil {
						if _, err := store.db.ExecContext(ctx, "DROP TRIGGER synthetic_pet_store_failure"); err != nil {
							t.Fatal(err)
						}
					}
					for read := 0; read < 2; read++ {
						state, err := store.PetStore(ctx, step.Input.Now)
						if err != nil {
							t.Fatal(err)
						}
						afterRead := persist(t)
						compareSnapshot(t, "after-read", step.AfterRead, afterRead, false)
						compareAPI(t, "reopened-state", step.ReopenedState, state)
						reopen(t)
						compareCheckinJSON(t, "repeated-reopen-preserves-IDs-and-JSON", afterRead, persist(t))
					}
				}) {
					t.FailNow()
				}
			}
			required := []string{"unknown-purchase", "badge-purchase", "level-gate", "cosmetic-multiple", "unknown-use", "unknown-equip", "pet-unequip", "non-consumable-use", "claim-over-daily-cap", "daily-exhausted", "next-day-state", "claim-zero", "claim-negative", "claim-over-remaining", "claim-inventory-rollback", "claim-last-replay"}
			if scenario.Name == "funded" {
				required = append(required, "buy-accessory", "rebuy-accessory", "equip-accessory", "reequip-accessory", "unequip-accessory", "equip-badge", "unequip-badge", "unequip-empty-badge", "equip-pet", "equip-scene", "equip-new-accessory", "stage-gate", "buy-zero", "buy-negative", "buy-over-99", "identical-purchase-replay", "use-zero", "use-negative", "use-over-99", "use-insufficient", "use-exact", "use-empty", "use-tool", "use-drink", "use-meal", "purchase-wallet-rollback", "purchase-economy-rollback", "purchase-inventory-rollback", "food-profile-rollback", "food-event-rollback", "consume-delete-rollback", "equip-event-rollback", "unequip-inventory-rollback")
			} else {
				required = append(required, "balance-insufficient", "buy-exact-balance", "balance-exhausted", "use-last-food")
			}
			for _, name := range required {
				if !seen[name] {
					t.Fatal("missing store scenario", name)
				}
			}
		})
	}
	if !t.Failed() {
		if steps != 83 || rejections != 40 || faults != 10 || duplicates != 1 {
			t.Fatalf("incomplete store coverage: steps=%d rejections=%d faults=%d duplicate=%d", steps, rejections, faults, duplicates)
		}
		t.Logf("%d Rust production action steps, %d rejections, %d trigger faults, %d duplicate purchase, 10 complete tables and full store API; every step reopened three times", steps, rejections, faults, duplicates)
	}
}

func TestPetStoreCompatibilityEventBijection(t *testing.T) {
	snapshot := func(firstID, secondID string) map[string]any {
		return map[string]any{
			"profile":  []any{[]any{"default-pet", "Libby", json.Number("1"), json.Number("0"), "first_meet", "idle", "before", "after"}},
			"checkins": []any{}, "economy": []any{}, "inventory": []any{}, "cosmetics": []any{},
			"settings": []any{}, "wallets": []any{}, "counters": []any{}, "limits": []any{},
			"events": []any{
				[]any{firstID, "default-pet", "store_equip", "clover-bow", json.Number("1"), "2026-07-01T12:00:00Z", `{"zh":"first"}`},
				[]any{secondID, "default-pet", "store_equip", "clover-bow", json.Number("1"), "2026-07-01T12:00:00Z", `{"zh":"second"}`},
			},
		}
	}
	ids := newCheckinIDs()
	if err := bindPetStoreIDs(ids, snapshot("rust-1", "rust-2"), snapshot("go-1", "go-2"), false); err != nil {
		t.Fatal("independent same-time event IDs should have a bijection", err)
	}
	for _, scenario := range []struct {
		name, firstID, secondID string
	}{
		{"existing-ID-drift", "go-3", "go-2"},
		{"swapped-IDs", "go-2", "go-1"},
		{"ID-collision", "go-1", "go-1"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if err := bindPetStoreIDs(ids, snapshot("rust-1", "rust-2"), snapshot(scenario.firstID, scenario.secondID), false); err == nil {
				t.Fatal("event identity difference was hidden")
			}
		})
	}
	if err := bindPetStoreIDs(newCheckinIDs(), snapshot("rust-1", "rust-2"), snapshot("go-1", "go-2"), true); err == nil {
		t.Fatal("baseline event IDs must not change")
	}
	for _, column := range []int{4, 6} {
		rust, native := snapshot("rust-1", "rust-2"), snapshot("go-1", "go-2")
		native["events"].([]any)[0].([]any)[column] = "synthetic-difference"
		ids := newCheckinIDs()
		if err := bindPetStoreIDs(ids, rust, native, false); err != nil {
			t.Fatal(err)
		}
		for side, value := range []map[string]any{rust, native} {
			if err := ids.normalizeSnapshot(value, side); err != nil {
				t.Fatal(err)
			}
		}
		if len(checkinJSONDifferences("snapshot", rust, native)) == 0 {
			t.Fatal("event quantity or metadata difference was hidden")
		}
	}
}
