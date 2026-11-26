//go:build compatibility

package jobdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
)

type gameplayCompatibilityInput struct {
	Name        string `json:"name"`
	Operation   string `json:"operation"`
	Now         string `json:"now"`
	Target      string `json:"target"`
	Config      string `json:"config"`
	GameKey     string `json:"gameKey"`
	Rejected    bool   `json:"rejected"`
	Application bool   `json:"application"`
}

func TestWriteRustGameplayCareRewardsAndReplay(t *testing.T) {
	ctx := context.Background()
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if directory == "" {
		t.Fatal("run pnpm native:compat")
	}
	var fixture struct {
		Queries map[string]string `json:"queries"`
		Initial any               `json:"initial"`
		Cases   []struct {
			Name  string `json:"name"`
			Steps []struct {
				Input     gameplayCompatibilityInput `json:"input"`
				Output    any                        `json:"output"`
				Persisted any                        `json:"persisted"`
			} `json:"steps"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "gameplay-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Queries) != 7 || len(fixture.Cases) != 2 {
		t.Fatal("incomplete Rust gameplay fixture")
	}
	baseline, err := os.ReadFile(filepath.Join(directory, "gameplay-base.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range fixture.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "liberty.sqlite3")
			if err := os.WriteFile(path, baseline, 0600); err != nil {
				t.Fatal(err)
			}
			store, err := openCompatibility(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if store != nil {
					store.Close()
				}
			})
			compareGameplayJSON(t, "initial", fixture.Initial, gameplayPersistence(t, store, fixture.Queries))
			if len(scenario.Steps) != 36 {
				t.Fatalf("expected three complete 12-step cycles, got %d", len(scenario.Steps))
			}
			for _, step := range scenario.Steps {
				t.Run(step.Input.Name, func(t *testing.T) {
					before := gameplayPersistence(t, store, fixture.Queries)
					output, actionErr := runGameplayCompatibilityAction(ctx, store, step.Input)
					if (actionErr != nil) != step.Input.Rejected {
						t.Errorf("Rust rejected=%v, Go error=%v", step.Input.Rejected, actionErr)
					}
					if actionErr != nil {
						output = map[string]any{"error": actionErr.Error()}
					}
					compareGameplayJSON(t, "output", step.Output, output)
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					store, err = openCompatibility(ctx, path)
					if err != nil {
						t.Fatal(err)
					}
					persisted := gameplayPersistence(t, store, fixture.Queries)
					compareGameplayJSON(t, "reopened", step.Persisted, persisted)
					if step.Input.Rejected {
						compareGameplayJSON(t, "rejected-action-rollback", before, persisted)
					}
				})
				if store == nil {
					t.Fatal("cannot continue gameplay scenario after reopen failure")
				}
			}
		})
	}
}

func runGameplayCompatibilityAction(ctx context.Context, store *Store, input gameplayCompatibilityInput) (any, error) {
	now, err := time.Parse(time.RFC3339Nano, input.Now)
	if err != nil {
		return nil, err
	}
	if input.Application {
		service := application.NewWork(store, func() time.Time { return now })
		switch input.Operation {
		case "market":
			return service.Market(ctx)
		case "work_state":
			return service.Game(ctx, input.GameKey)
		case "start":
			return service.Start(ctx, input.GameKey, input.Target, input.Config)
		case "care":
			return service.Care(ctx, input.GameKey, input.Target)
		case "claim":
			return service.Claim(ctx, input.GameKey, input.Target)
		default:
			return nil, fmt.Errorf("unsupported application work operation %q", input.Operation)
		}
	}
	switch input.Operation {
	case "farm_state":
		return store.FarmState(ctx, now)
	case "plant":
		return store.PlantCrop(ctx, input.Target, input.Config, now)
	case "water":
		return store.WaterPlot(ctx, input.Target, now)
	case "harvest":
		return store.HarvestPlot(ctx, input.Target, now)
	case "work_state":
		return store.WorkGameState(ctx, input.GameKey, now)
	case "start":
		return store.StartWorkTask(ctx, input.GameKey, input.Target, input.Config, now)
	case "care":
		return store.CareWorkTask(ctx, input.GameKey, input.Target, now)
	case "claim":
		return store.ClaimWorkTask(ctx, input.GameKey, input.Target, now)
	default:
		return nil, fmt.Errorf("unknown gameplay operation %q", input.Operation)
	}
}

func gameplayPersistence(t *testing.T, store *Store, queries map[string]string) map[string]any {
	t.Helper()
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
			for index, column := range columns {
				if column == "rewards_json" {
					text, ok := row[index].(string)
					if !ok {
						rows.Close()
						t.Fatalf("invalid %s rewards JSON: %T", name, row[index])
					}
					decoder := json.NewDecoder(bytes.NewBufferString(text))
					decoder.UseNumber()
					if err := decoder.Decode(&row[index]); err != nil {
						rows.Close()
						t.Fatal(err)
					}
				}
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

func compareGameplayJSON(t *testing.T, label string, expected, actual any) {
	t.Helper()
	compareCheckinJSON(t, label, expected, actual)
}
