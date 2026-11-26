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
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
	"modernc.org/sqlite"
)

type petCoreInput struct {
	Name, Operation, Source, Now, Date, Fault, SetupSQL string
	Metadata                                            *string
	Settings                                            domain.PetSettings
	Limit                                               int
	Rejected                                            bool
}

type petCoreIDs struct {
	entities *checkinIDs
	keys     *checkinIDs
	active   [2]map[string]string
}

var petCoreGeneratedInteraction = regexp.MustCompile(`^interaction:.*:interaction-([0-9]+-[0-9]+|[0-9a-f]{32})$`)

func newPetCoreIDs() *petCoreIDs {
	return &petCoreIDs{entities: newCheckinIDs(), keys: newCheckinIDs()}
}

func (ids *petCoreIDs) bind(rust, native map[string]any, baseline bool) error {
	ids.active = [2]map[string]string{{}, {}}
	claims := [2]map[string]string{{}, {}}
	for side, snapshot := range []map[string]any{rust, native} {
		occurrences := map[string]int{}
		for _, value := range snapshot["economy"].([]any) {
			row := value.([]any)
			key, ok := row[7].(string)
			if !ok || !strings.HasPrefix(key, "interaction:") || row[6] != "pet_growth" || strings.HasPrefix(key, "interaction:daily_open:") {
				continue
			}
			if !petCoreGeneratedInteraction.MatchString(key) {
				return fmt.Errorf("invalid generated interaction entity key %q", key)
			}
			if _, exists := ids.active[side][key]; exists {
				return errors.New("interaction key reused by multiple claims")
			}
			prefix := key[:strings.LastIndex(key, ":interaction-")]
			identityBytes, err := json.Marshal([]any{row[1], prefix, row[9]})
			if err != nil {
				return err
			}
			identity := string(identityBytes)
			occurrences[identity]++
			identity = fmt.Sprintf("%s#%d", identity, occurrences[identity])
			ids.active[side][key] = identity
			claims[side][identity] = key
		}
	}
	if len(claims[0]) != len(claims[1]) {
		return errors.New("interaction claim counts differ")
	}
	for identity, rustKey := range claims[0] {
		if err := ids.keys.bind("economy", identity, rustKey, claims[1][identity], baseline); err != nil {
			return err
		}
	}
	for side, snapshot := range []map[string]any{rust, native} {
		for _, value := range snapshot["economy"].([]any) {
			row := value.([]any)
			if identity, ok := ids.active[side][row[7].(string)]; ok {
				row[7] = ids.keys.identities["economy:"+identity].rust
			}
		}
		for _, value := range snapshot["counters"].([]any) {
			row := value.([]any)
			if identity, ok := ids.active[side][row[3].(string)]; ok {
				row[3] = ids.keys.identities["economy:"+identity].rust
			}
		}
	}
	return bindPetStoreIDs(ids.entities, rust, native, baseline)
}

func (ids *petCoreIDs) normalizeAPI(value any, side int) error {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			if err := ids.normalizeAPI(child, side); err != nil {
				return err
			}
		}
	case map[string]any:
		category := ""
		if value["eventType"] != nil && value["eventTime"] != nil {
			category = "events"
		}
		if value["cosmeticKey"] != nil && value["unlockedAt"] != nil {
			category = "cosmetics"
		}
		if category != "" {
			id, ok := value["id"].(string)
			if !ok {
				return errors.New("missing core API entity ID")
			}
			normalized, err := ids.entities.reference(category, id, side)
			if err != nil {
				return err
			}
			value["id"] = normalized
		}
		for _, child := range value {
			if err := ids.normalizeAPI(child, side); err != nil {
				return err
			}
		}
	}
	return nil
}

func (ids *petCoreIDs) normalizeSnapshot(snapshot map[string]any, side int) error {
	rows := snapshot["economy"].([]any)
	metadata := make([]any, len(rows))
	for index, value := range rows {
		metadata[index] = value.([]any)[8]
	}
	if err := ids.entities.normalizeSnapshot(snapshot, side); err != nil {
		return err
	}
	for index, value := range rows {
		value.([]any)[8] = metadata[index]
	}
	return nil
}

func petCoreAction(ctx context.Context, store *Store, input petCoreInput) (any, error) {
	switch input.Operation {
	case "profile":
		return store.PetProfile(ctx, input.Now)
	case "settings":
		return store.PetSettings(ctx, input.Now)
	case "save-settings":
		return store.SavePetSettings(ctx, input.Settings, input.Now)
	case "name":
		return store.SavePetName(ctx, input.Source, input.Now)
	case "events":
		return store.PetEvents(ctx, input.Limit)
	case "cosmetics":
		return store.PetCosmetics(ctx)
	case "interaction", "workflow":
		return store.PetGrowth(ctx, input.Operation, input.Source, input.Metadata, input.Now)
	default:
		return nil, errors.New("unknown pet core operation")
	}
}

func petCoreView(t *testing.T, store *Store, now string) any {
	t.Helper()
	ctx := context.Background()
	profile, err := store.PetProfile(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.PetSettings(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.PetEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	cosmetics, err := store.PetCosmetics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"profile": profile, "settings": settings, "events": events, "cosmetics": cosmetics}
}

func TestWriteRustPetCoreActionsAndReopen(t *testing.T) {
	ctx := context.Background()
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if !filepath.IsAbs(directory) {
		t.Fatal("explicit isolated LIBERTY_NATIVE_FIXTURE_DIR required")
	}
	var fixture struct {
		ClockMode string
		Queries   map[string]string
		Cases     []struct {
			Name    string
			Initial json.RawMessage
			Steps   []struct {
				Input         petCoreInput
				Output        json.RawMessage
				RawError      *string
				Trigger       *string
				Persisted     json.RawMessage
				ReopenedState json.RawMessage
				AfterRead     json.RawMessage
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "pet-core-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if decoder.Decode(new(any)) != io.EOF || fixture.ClockMode != "thread-local-fixed" || len(fixture.Cases) != 6 || len(fixture.Queries) != 10 {
		t.Fatal("incomplete pet core oracle")
	}
	expectedSteps := map[string]int{"defaults": 5, "workflow": 67, "interaction": 102, "settings": 8, "profile": 16, "faults": 18}
	seenCases := map[string]bool{}
	for _, scenario := range fixture.Cases {
		if seenCases[scenario.Name] || expectedSteps[scenario.Name] != len(scenario.Steps) {
			t.Fatal("duplicate, unknown or incomplete pet core scenario", scenario.Name, len(scenario.Steps))
		}
		seenCases[scenario.Name] = true
		seenSteps := map[string]bool{}
		for _, step := range scenario.Steps {
			if step.Input.Name == "" || seenSteps[step.Input.Name] {
				t.Fatal("missing or duplicate pet core step", scenario.Name, step.Input.Name)
			}
			seenSteps[step.Input.Name] = true
		}
	}
	var hits atomic.Int64
	function := fmt.Sprintf("liberty_pet_core_fault_%d", checkinFaultSequence.Add(1))
	if err := sqlite.RegisterScalarFunction(function, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		hits.Add(1)
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Parallel()
	cases, steps, rejected, faults, reopens := 0, 0, 0, 0, 0
	for _, scenario := range fixture.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			cases++
			baseline, err := os.ReadFile(filepath.Join(directory, "pet-core-"+scenario.Name+"-base.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
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
			reopen := func(t *testing.T) {
				t.Helper()
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				store = nil
				store, err = openCompatibility(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				reopens++
			}
			persist := func(t *testing.T) map[string]any {
				return fullGameplayPersistence(t, store, fixture.Queries)
			}
			ids := newPetCoreIDs()
			compareSnapshot := func(t *testing.T, label string, expected, actual any, baseline bool) {
				t.Helper()
				rust := decodeCheckinJSON(t, expected).(map[string]any)
				native := decodeCheckinJSON(t, actual).(map[string]any)
				if err := ids.bind(rust, native, baseline); err != nil {
					t.Fatalf("%s: %v", label, err)
				}
				for side, value := range []map[string]any{rust, native} {
					if err := ids.normalizeSnapshot(value, side); err != nil {
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
					if err := ids.entities.normalizeAPI(value, side); err != nil {
						t.Fatal(err)
					}
				}
				compareDecodedCheckinJSON(t, label, values[0], values[1])
			}
			compareSnapshot(t, "initial", scenario.Initial, persist(t), true)
			for _, step := range scenario.Steps {
				t.Run(step.Input.Name, func(t *testing.T) {
					steps++
					now, err := time.Parse(time.RFC3339Nano, step.Input.Now)
					if err != nil || now.Local().Format(time.DateOnly) != step.Input.Date {
						t.Fatal("fixed local dates differ", err)
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
					output, actionErr := petCoreAction(ctx, store, step.Input)
					if (actionErr != nil) != step.Input.Rejected {
						t.Errorf("Rust rejected=%v Go error=%v", step.Input.Rejected, actionErr)
					}
					if step.Trigger != nil {
						faults++
						if !errors.Is(actionErr, ErrDatabase) || hits.Load() != 1 || step.RawError == nil || *step.RawError != "synthetic-pet-core-write-failure" {
							t.Fatalf("fault provenance: error=%v hits=%d Rust=%v", actionErr, hits.Load(), step.RawError)
						}
						output = map[string]any{"fault": "synthetic-pet-core-write-failure"}
					} else {
						if hits.Load() != 0 {
							t.Fatal("unexpected fault probe")
						}
						if actionErr != nil {
							output = map[string]any{"error": actionErr.Error()}
						}
					}
					persisted := persist(t)
					compareSnapshot(t, "persisted", step.Persisted, persisted, false)
					compareAPI(t, "output", step.Output, output)
					if step.Input.Rejected {
						rejected++
						compareCheckinJSON(t, "rollback", before, persisted)
					}
					reopen(t)
					compareSnapshot(t, "reopened", step.Persisted, persist(t), false)
					compareCheckinJSON(t, "reopen-preserves-Go-IDs-and-keys", persisted, persist(t))
					if step.Trigger != nil {
						if _, err := store.db.ExecContext(ctx, "DROP TRIGGER synthetic_pet_core_failure"); err != nil {
							t.Fatal(err)
						}
					}
					for read := 0; read < 2; read++ {
						state := petCoreView(t, store, step.Input.Now)
						compareSnapshot(t, "after-read", step.AfterRead, persist(t), false)
						compareAPI(t, "reopened-state", step.ReopenedState, state)
						reopen(t)
						compareSnapshot(t, "reopened-after-read", step.AfterRead, persist(t), false)
					}
				})
			}
		})
	}
	t.Logf("pet core differential: cases=%d steps=%d rejected=%d faults=%d tables=%d reopens=%d", cases, steps, rejected, faults, len(fixture.Queries), reopens)
}

func TestPetCoreEntityBijectionRejectsDriftAndBusinessDifferences(t *testing.T) {
	snapshot := func(native bool) map[string]any {
		prefix, key := "rust", "interaction:tap:interaction-1000-7"
		if native {
			prefix, key = "go", "interaction:tap:interaction-0123456789abcdef0123456789abcdef"
		}
		return map[string]any{
			"profile":  []any{[]any{"default-pet", "Libby", json.Number("1"), json.Number("1"), "first_meet", "cheerful", "before", "after"}},
			"checkins": []any{}, "inventory": []any{}, "cosmetics": []any{}, "settings": []any{}, "wallets": []any{}, "counters": []any{}, "limits": []any{},
			"events": []any{[]any{prefix + "-event", "default-pet", "interaction", "tap", json.Number("1"), "after", `{"source":"","finalGrowth":1}`}},
			"economy": []any{
				[]any{prefix + "-claim", "default-pet", "event", "event_key", json.Number("0"), json.Number("0"), "pet_growth", key, nil, "after"},
				[]any{prefix + "-reward", "default-pet", "earn", "lp", json.Number("1"), json.Number("1"), "workflow_reward", key, nil, "after"},
			},
		}
	}
	ids := newPetCoreIDs()
	if err := ids.bind(snapshot(false), snapshot(true), false); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"claim-key-drift", func(value map[string]any) {
			for _, row := range value["economy"].([]any) {
				row.([]any)[7] = "interaction:tap:interaction-fedcba9876543210fedcba9876543210"
			}
		}},
		{"reward-key-wrong-entity", func(value map[string]any) {
			value["economy"].([]any)[1].([]any)[7] = "interaction:tap:interaction-fedcba9876543210fedcba9876543210"
		}},
		{"claim-key-not-generated", func(value map[string]any) {
			value["economy"].([]any)[0].([]any)[7] = "interaction:tap:interaction-anything"
		}},
		{"event-ID-drift", func(value map[string]any) { value["events"].([]any)[0].([]any)[0] = "go-new-event" }},
		{"ledger-ID-collision", func(value map[string]any) { value["economy"].([]any)[1].([]any)[0] = "go-claim" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			native := snapshot(true)
			scenario.mutate(native)
			if err := ids.bind(snapshot(false), native, false); err == nil {
				t.Fatal("entity identity difference hidden")
			}
		})
	}
	if err := newPetCoreIDs().bind(snapshot(false), snapshot(true), true); err == nil {
		t.Fatal("baseline IDs must remain exact")
	}
	for _, scenario := range []struct {
		name, table string
		row, column int
	}{
		{"experience", "profile", 0, 3}, {"mood", "profile", 0, 5}, {"growth", "events", 0, 4},
		{"metadata", "events", 0, 6}, {"opaque-source-metadata", "economy", 1, 8}, {"LP", "economy", 1, 4}, {"balance", "economy", 1, 5},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			rust, native := snapshot(false), snapshot(true)
			native[scenario.table].([]any)[scenario.row].([]any)[scenario.column] = "synthetic-changed-value"
			ids := newPetCoreIDs()
			if err := ids.bind(rust, native, false); err != nil {
				t.Fatal(err)
			}
			for side, value := range []map[string]any{rust, native} {
				if err := ids.normalizeSnapshot(value, side); err != nil {
					t.Fatal(err)
				}
			}
			if len(checkinJSONDifferences("snapshot", rust, native)) == 0 {
				t.Fatal("business difference hidden by ID bijection")
			}
		})
	}
	t.Run("opaque-metadata-retains-JSON-text", func(t *testing.T) {
		rust, native := snapshot(false), snapshot(true)
		rust["economy"].([]any)[1].([]any)[8] = "0"
		native["economy"].([]any)[1].([]any)[8] = "0.0"
		ids := newPetCoreIDs()
		if err := ids.bind(rust, native, false); err != nil {
			t.Fatal(err)
		}
		for side, value := range []map[string]any{rust, native} {
			if err := ids.normalizeSnapshot(value, side); err != nil {
				t.Fatal(err)
			}
		}
		if len(checkinJSONDifferences("snapshot", rust, native)) == 0 {
			t.Fatal("opaque source metadata text was weakened to JSON numeric equality")
		}
	})
}
