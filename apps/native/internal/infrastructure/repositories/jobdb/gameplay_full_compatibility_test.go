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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

type fullGameplayInput struct {
	gameplayCompatibilityInput
	Fault    string `json:"fault"`
	SetupSQL string `json:"setupSql"`
	Limit    int    `json:"limit"`
}

func fullGameplayPersistence(t *testing.T, store *Store, queries map[string]string) map[string]any {
	t.Helper()
	additional := map[string]string{
		"plots":    "id,plot_index,crop_key,status,stage_index,planted_at,last_watered_at,next_care_at,mature_at,updated_at",
		"tasks":    "id,game_key,slot_index,job_key,status,stage_index,started_at,last_cared_at,next_care_at,claimable_at,updated_at",
		"harvests": "id,plot_id,crop_key,rewards_json,lp_reward,created_at",
		"rewards":  "id,game_key,task_id,job_key,rewards_json,lp_reward,created_at",
	}
	snapshot := map[string]any{}
	for name, query := range queries {
		rows, err := store.db.QueryContext(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		expected := checkinTableColumns[name]
		if expected == "" {
			expected = additional[name]
		}
		if !reflect.DeepEqual(columns, strings.Split(expected, ",")) {
			rows.Close()
			t.Fatal("changed table projection", name, columns)
		}
		values := [][]any{}
		for rows.Next() {
			row := make([]any, len(columns))
			targets := make([]any, len(columns))
			for index := range row {
				targets[index] = &row[index]
			}
			if err := rows.Scan(targets...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			values = append(values, row)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		snapshot[name] = values
	}
	return snapshot
}

func fullGameplayView(t *testing.T, store *Store, now time.Time) any {
	t.Helper()
	ctx := context.Background()
	farm, err := store.FarmState(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	market, err := store.WorkMarketState(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	work := []any{}
	for _, key := range []string{"mine", "factory", "convenience-store"} {
		state, err := store.WorkGameState(ctx, key, now)
		if err != nil {
			t.Fatal(err)
		}
		work = append(work, state)
	}
	return map[string]any{"farm": farm, "market": market, "work": work}
}

func normalizeGameplaySnapshot(ids *checkinIDs, snapshot map[string]any, side int) error {
	if err := ids.normalizeSnapshot(snapshot, side); err != nil {
		return err
	}
	for _, name := range []string{"harvests", "rewards"} {
		rows, ok := snapshot[name].([]any)
		if !ok {
			return fmt.Errorf("%s: missing ledger", name)
		}
		for _, value := range rows {
			row, ok := value.([]any)
			column := 3
			if name == "rewards" {
				column = 4
			}
			if !ok || len(row) != column+3 {
				return fmt.Errorf("%s: malformed ledger", name)
			}
			row[column] = checkinEmbeddedJSON(row[column])
		}
	}
	return nil
}

func TestWriteRustAllFarmAndWorkActionsAndReopen(t *testing.T) {
	ctx := context.Background()
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if directory == "" || !filepath.IsAbs(directory) {
		t.Fatal("export the real Rust gameplay fixture to an isolated absolute LIBERTY_NATIVE_FIXTURE_DIR")
	}
	var fixture struct {
		ClockMode string            `json:"clockMode"`
		Queries   map[string]string `json:"queries"`
		Cases     []struct {
			Name       string `json:"name"`
			Definition struct {
				Game        string `json:"gameKey"`
				Config      string `json:"config"`
				Minimum     int64  `json:"minimum"`
				Maximum     int64  `json:"maximum"`
				Bonus       bool   `json:"bonus"`
				Application bool   `json:"application"`
			} `json:"definition"`
			Initial json.RawMessage `json:"initial"`
			Steps   []struct {
				Input         fullGameplayInput `json:"input"`
				Output        json.RawMessage   `json:"output"`
				RawError      *string           `json:"rawError"`
				Trigger       *string           `json:"trigger"`
				Persisted     json.RawMessage   `json:"persisted"`
				ReopenedState json.RawMessage   `json:"reopenedState"`
				AfterRead     json.RawMessage   `json:"afterRead"`
			} `json:"steps"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "gameplay-full-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		t.Fatal("trailing gameplay fixture JSON")
	}
	if fixture.ClockMode != "thread-local-fixed" || len(fixture.Queries) != 14 || len(fixture.Cases) != 18 {
		t.Fatal("incomplete real Rust gameplay oracle")
	}
	var hits atomic.Int64
	function := fmt.Sprintf("liberty_gameplay_fault_%d", checkinFaultSequence.Add(1))
	if err := sqlite.RegisterScalarFunction(function, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		hits.Add(1)
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Parallel()
	steps, rejections, faults := 0, 0, 0
	seenCases := map[string]bool{}
	for _, scenario := range fixture.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			if scenario.Name == "work-factory-basic-assembly-application" {
				if !scenario.Definition.Application {
					t.Fatal("application service oracle case is not explicitly marked")
				}
				for _, step := range scenario.Steps {
					if !step.Input.Application {
						t.Fatalf("application service case bypasses application layer at %s", step.Input.Name)
					}
				}
			}
			if seenCases[scenario.Name] {
				t.Fatal("duplicate case")
			}
			seenCases[scenario.Name] = true
			baseline, err := os.ReadFile(filepath.Join(directory, "gameplay-full-"+scenario.Name+"-base.sqlite3"))
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
					t.Fatal(err)
				}
				store = nil
				store, err = Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
			}
			ids := newCheckinIDs()
			persist := func(t *testing.T) any { t.Helper(); return fullGameplayPersistence(t, store, fixture.Queries) }
			compareSnapshot := func(t *testing.T, label string, expected, actual any, baseline bool) {
				t.Helper()
				rust := decodeCheckinJSON(t, expected).(map[string]any)
				native := decodeCheckinJSON(t, actual).(map[string]any)
				if err := bindPetStoreIDs(ids, rust, native, baseline); err != nil {
					t.Fatalf("%s: %v", label, err)
				}
				for side, snapshot := range []map[string]any{rust, native} {
					if err := normalizeGameplaySnapshot(ids, snapshot, side); err != nil {
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
						t.Fatal(err)
					}
				}
				compareDecodedCheckinJSON(t, label, values[0], values[1])
			}
			compareSnapshot(t, "initial", scenario.Initial, persist(t), true)
			seen := map[string]bool{}
			for _, step := range scenario.Steps {
				if !t.Run(step.Input.Name, func(t *testing.T) {
					if seen[step.Input.Name] {
						t.Fatal("duplicate action")
					}
					seen[step.Input.Name] = true
					steps++
					now, err := time.Parse(time.RFC3339Nano, step.Input.Now)
					if err != nil {
						t.Fatal(err)
					}
					if step.Input.SetupSQL != "" {
						if _, err := store.db.ExecContext(ctx, step.Input.SetupSQL); err != nil {
							t.Fatal(err)
						}
					}
					hits.Store(0)
					if step.Trigger != nil {
						if _, err := store.db.ExecContext(ctx, strings.ReplaceAll(*step.Trigger, "liberty_checkin_fault_probe", function)); err != nil {
							t.Fatal(err)
						}
					}
					before := persist(t)
					var output any
					var actionErr error
					switch step.Input.Operation {
					case "market":
						output, actionErr = store.WorkMarketState(ctx, now)
					case "harvest-ledger":
						output, actionErr = store.HarvestLedger(ctx, step.Input.Limit)
					default:
						output, actionErr = runGameplayCompatibilityAction(ctx, store, step.Input.gameplayCompatibilityInput)
					}
					if (actionErr != nil) != step.Input.Rejected {
						t.Errorf("Rust rejected=%v Go=%v", step.Input.Rejected, actionErr)
					}
					if step.Input.Fault != "" {
						faults++
						if hits.Load() != 1 || step.RawError == nil {
							t.Fatalf("fault provenance: hits=%d Rust=%v Go=%v", hits.Load(), step.RawError, actionErr)
						}
						if !strings.HasPrefix(step.Input.Fault, "seed") {
							if !errors.Is(actionErr, ErrDatabase) || *step.RawError != "synthetic-gameplay-write-failure" {
								t.Fatalf("wrong storage fault: Rust=%v Go=%v", step.RawError, actionErr)
							}
							output = map[string]any{"fault": "synthetic-gameplay-write-failure"}
						} else if actionErr != nil {
							output = map[string]any{"error": actionErr.Error()}
						}
					} else {
						if hits.Load() != 0 {
							t.Fatal("unexpected fault probe")
						}
						if actionErr != nil {
							output = map[string]any{"error": actionErr.Error()}
						}
					}
					if step.Trigger != nil {
						if _, err := store.db.ExecContext(ctx, "DROP TRIGGER synthetic_gameplay_failure"); err != nil {
							t.Fatal(err)
						}
					}
					compareSnapshot(t, "persisted", step.Persisted, persist(t), false)
					compareAPI(t, "output", step.Output, output)
					if step.Input.Rejected {
						rejections++
						compareCheckinJSON(t, "failed-action-rollback", before, persist(t))
					}
					reopen(t)
					compareSnapshot(t, "raw-reopen", step.Persisted, persist(t), false)
					for iteration := 0; iteration < 3; iteration++ {
						if iteration > 0 {
							reopen(t)
						}
						state := fullGameplayView(t, store, now)
						compareSnapshot(t, "after-read", step.AfterRead, persist(t), false)
						compareAPI(t, "reopened-state", step.ReopenedState, state)
					}
					if step.Input.Name == "first-claim" || step.Input.Name == "second-claim" {
						result := decodeCheckinJSON(t, output).(map[string]any)
						key := "harvest"
						if scenario.Definition.Game != "" {
							key = "reward"
						}
						reward := result[key].(map[string]any)
						expectedLP := scenario.Definition.Minimum
						count := 1
						if step.Input.Name == "second-claim" {
							expectedLP = scenario.Definition.Maximum
							if scenario.Definition.Bonus {
								count = 2
							}
						}
						compareCheckinJSON(t, "LP-boundary", json.Number(fmt.Sprint(expectedLP)), reward["lpReward"])
						if len(reward["rewards"].([]any)) != count {
							t.Fatal("bonus branch missing")
						}
					}
				}) {
					return
				}
			}
			for _, name := range []string{"first-begin", "second-begin", "first-claim", "second-claim", "first-clock-backwards", "first-stage-1-subsecond", "unknown-target", "unknown-config", "first-claim-repeat", "second-claim-repeat"} {
				if !seen[name] {
					t.Fatal("missing coverage", name)
				}
			}
		})
	}
	if !seenCases["work-factory-basic-assembly-application"] {
		t.Fatal("missing application-clock factory care/claim differential")
	}
	for _, crop := range []string{"wheat", "carrot", "tomato", "pumpkin", "corn", "strawberry", "blueberry", "potato"} {
		if !seenCases["farm-"+crop] {
			t.Fatal("missing crop", crop)
		}
	}
	t.Logf("%d real Rust farm/work steps, %d rejections, %d injected rollbacks; 8 crops, 9 jobs across 3 maps, LP/bonus boundaries, 14 complete tables and full APIs; every step reopened three times", steps, rejections, faults)
}
