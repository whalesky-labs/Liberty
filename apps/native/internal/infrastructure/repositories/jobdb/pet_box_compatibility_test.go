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
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

type boxIDs struct {
	entities   *checkinIDs
	draws      *checkinIDs
	keys       *checkinIDs
	activeKeys [2]map[string]string
}

var boxGeneratedGiftID = regexp.MustCompile(`^gift-box-([0-9]+-[0-9]+|[0-9a-f]{32})$`)

func (ids *boxIDs) bind(rust, native map[string]any, baseline bool) error {
	ids.activeKeys = [2]map[string]string{{}, {}}
	keyRows := [2]map[string]string{{}, {}}
	for side, snapshot := range []map[string]any{rust, native} {
		occurrences := map[string]int{}
		for _, value := range snapshot["economy"].([]any) {
			row := value.([]any)
			if row[6] != "gift_box_duplicate" {
				continue
			}
			item, ok := row[8].(string)
			if !ok {
				return errors.New("gift compensation metadata must retain its item key")
			}
			key, ok := row[7].(string)
			prefix := "gift-box:" + item + ":"
			if !ok || !strings.HasPrefix(key, prefix) || !boxGeneratedGiftID.MatchString(strings.TrimPrefix(key, prefix)) {
				return fmt.Errorf("invalid gift compensation source key %v", row[7])
			}
			identityBytes, err := json.Marshal([]any{row[1], row[3], row[6], row[8], row[9]})
			if err != nil {
				return err
			}
			identity := string(identityBytes)
			occurrences[identity]++
			identity = fmt.Sprintf("%s#%d", identity, occurrences[identity])
			if _, exists := ids.activeKeys[side][key]; exists {
				return errors.New("gift source key reused")
			}
			keyRows[side][identity] = key
			ids.activeKeys[side][key] = identity
		}
	}
	if len(keyRows[0]) != len(keyRows[1]) {
		return errors.New("gift compensation key count differs")
	}
	for identity, rustKey := range keyRows[0] {
		if err := ids.keys.bind("economy", identity, rustKey, keyRows[1][identity], baseline); err != nil {
			return err
		}
	}
	for side, snapshot := range []map[string]any{rust, native} {
		for _, value := range snapshot["economy"].([]any) {
			row := value.([]any)
			if row[6] == "gift_box_duplicate" {
				row[7] = ids.keys.identities["economy:"+ids.activeKeys[side][row[7].(string)]].rust
			}
		}
	}
	if err := bindPetStoreIDs(ids.entities, rust, native, baseline); err != nil {
		return err
	}
	ids.draws.active = [2]map[string]map[string]string{{"events": {}}, {"events": {}}}
	drawRows := [2]map[string]string{{}, {}}
	for side, snapshot := range []map[string]any{rust, native} {
		occurrences := map[string]int{}
		for _, value := range snapshot["draws"].([]any) {
			row := value.([]any)
			identityBytes, err := json.Marshal([]any{row[1], row[2], row[7]})
			if err != nil {
				return err
			}
			identity := string(identityBytes)
			occurrences[identity]++
			identity = fmt.Sprintf("%s#%d", identity, occurrences[identity])
			id, ok := row[0].(string)
			if !ok || id == "" {
				return errors.New("invalid draw ID")
			}
			if _, exists := ids.draws.active[side]["events"][id]; exists {
				return errors.New("draw ID collision")
			}
			drawRows[side][identity] = id
			ids.draws.active[side]["events"][id] = identity
		}
	}
	if len(drawRows[0]) != len(drawRows[1]) {
		return errors.New("draw count differs")
	}
	for identity, rustID := range drawRows[0] {
		if err := ids.draws.bind("events", identity, rustID, drawRows[1][identity], baseline); err != nil {
			return err
		}
	}
	return nil
}

func (ids *boxIDs) normalizeSnapshot(snapshot map[string]any, side int) error {
	if err := ids.entities.normalizeSnapshot(snapshot, side); err != nil {
		return err
	}
	for _, value := range snapshot["draws"].([]any) {
		row := value.([]any)
		id, err := ids.draws.reference("events", row[0].(string), side)
		if err != nil {
			return err
		}
		row[0] = id
		pet, err := ids.entities.reference("profile", row[1].(string), side)
		if err != nil {
			return err
		}
		row[1] = pet
	}
	return nil
}

func (ids *boxIDs) normalizeAPI(value any, side int) error {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			if err := ids.normalizeAPI(child, side); err != nil {
				return err
			}
		}
	case map[string]any:
		if value["drawDate"] != nil && value["itemKey"] != nil && value["createdAt"] != nil {
			id, ok := value["id"].(string)
			if !ok {
				return errors.New("invalid API draw ID")
			}
			normalized, err := ids.draws.reference("events", id, side)
			if err != nil {
				return err
			}
			value["id"] = normalized
		}
		if value["sourceType"] == "gift_box_duplicate" {
			key, ok := value["sourceKey"].(string)
			if !ok {
				return errors.New("invalid API source key")
			}
			identity, ok := ids.activeKeys[side][key]
			if !ok {
				return errors.New("API source key not in persisted compensation entity")
			}
			value["sourceKey"] = ids.keys.identities["economy:"+identity].rust
		}
		for _, child := range value {
			if err := ids.normalizeAPI(child, side); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestWriteRustPetBoxActionsAndReopen(t *testing.T) {
	ctx := context.Background()
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if !filepath.IsAbs(directory) {
		t.Fatal("explicit isolated LIBERTY_NATIVE_FIXTURE_DIR required")
	}
	var fixture struct {
		ClockMode string            `json:"clockMode"`
		Queries   map[string]string `json:"queries"`
		Cases     []struct {
			Name    string          `json:"name"`
			Blind   bool            `json:"blind"`
			Initial json.RawMessage `json:"initial"`
			Pool    [][2]any        `json:"pool"`
			Steps   []struct {
				Input struct {
					Name, Operation, Now, Date, ExpectedPrize, Fault string
					Rejected                                         bool
				} `json:"input"`
				Output        json.RawMessage `json:"output"`
				RawError      *string         `json:"rawError"`
				Trigger       *string         `json:"trigger"`
				Persisted     json.RawMessage `json:"persisted"`
				ReopenedState json.RawMessage `json:"reopenedState"`
				AfterRead     json.RawMessage `json:"afterRead"`
			} `json:"steps"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "pet-box-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		t.Fatal("trailing box fixture JSON")
	}
	if fixture.ClockMode != "thread-local-fixed-milliseconds" || len(fixture.Cases) != 5 || len(fixture.Queries) != 11 {
		t.Fatal("incomplete box oracle")
	}
	var hits atomic.Int64
	function := fmt.Sprintf("liberty_pet_box_fault_%d", checkinFaultSequence.Add(1))
	if err := sqlite.RegisterScalarFunction(function, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		hits.Add(1)
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Parallel()
	steps, rejected, faults, boundaries := 0, 0, 0, 0
	seenCases := map[string]bool{}
	for _, scenario := range fixture.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			if seenCases[scenario.Name] || !strings.Contains("|gift-default|gift-unlocked|gift-missing|gift-zero|blind|", "|"+scenario.Name+"|") {
				t.Fatal("duplicate or unknown box scenario", scenario.Name)
			}
			seenCases[scenario.Name] = true
			if scenario.Blind != (scenario.Name == "blind") {
				t.Fatal("box scenario type changed")
			}
			seenSteps := map[string]bool{}
			baseline, err := os.ReadFile(filepath.Join(directory, "pet-box-"+scenario.Name+"-base.sqlite3"))
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
			}
			persist := func(t *testing.T) map[string]any {
				t.Helper()
				out := map[string]any{}
				for name, query := range fixture.Queries {
					rows, err := store.db.QueryContext(ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					expected := checkinTableColumns[name]
					if name == "draws" {
						expected = "id,pet_id,draw_date,item_key,item_type,quantity,duplicate_compensation_lp,created_at"
					}
					if !reflect.DeepEqual(columns, strings.Split(expected, ",")) {
						t.Fatal("box table projection changed", name, columns)
					}
					values := [][]any{}
					for rows.Next() {
						row := make([]any, len(columns))
						targets := make([]any, len(columns))
						for index := range row {
							targets[index] = &row[index]
						}
						if err := rows.Scan(targets...); err != nil {
							t.Fatal(err)
						}
						values = append(values, row)
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					out[name] = values
				}
				return out
			}
			ids := &boxIDs{entities: newCheckinIDs(), draws: newCheckinIDs(), keys: newCheckinIDs()}
			compareSnapshot := func(t *testing.T, label string, expected, actual any, baseline bool) {
				t.Helper()
				rust := decodeCheckinJSON(t, expected).(map[string]any)
				native := decodeCheckinJSON(t, actual).(map[string]any)
				if err := ids.bind(rust, native, baseline); err != nil {
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
				if !t.Run(step.Input.Name, func(t *testing.T) {
					if seenSteps[step.Input.Name] {
						t.Fatal("duplicate box action", step.Input.Name)
					}
					seenSteps[step.Input.Name] = true
					steps++
					if strings.HasPrefix(step.Input.Name, "boundary-") {
						boundaries++
					}
					now, err := time.Parse(time.RFC3339Nano, step.Input.Now)
					if err != nil || now.Local().Format("2006-01-02") != step.Input.Date {
						t.Fatal("fixed local dates differ", err)
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
					case "gift-state":
						output, actionErr = store.PetStore(ctx, step.Input.Now)
					case "blind-state":
						output, actionErr = store.PetBlindBox(ctx, step.Input.Now)
					case "gift":
						output, actionErr = store.OpenPetGiftBox(ctx, step.Input.Now)
					case "use-gift":
						output, actionErr = store.UsePetItem(ctx, "gift-box-tool", 1, step.Input.Now)
					case "blind":
						output, actionErr = store.PetDrawBlindBox(ctx, step.Input.Now)
					default:
						t.Fatal("unknown box action")
					}
					if (actionErr != nil) != step.Input.Rejected {
						t.Errorf("Rust rejected=%v Go error=%v", step.Input.Rejected, actionErr)
					}
					if step.Input.Fault != "" {
						faults++
						if !errors.Is(actionErr, ErrDatabase) || hits.Load() != 1 || step.RawError == nil || *step.RawError != "synthetic-pet-box-write-failure" {
							t.Fatalf("fault provenance: error=%v hits=%d Rust=%v", actionErr, hits.Load(), step.RawError)
						}
						output = map[string]any{"fault": "synthetic-pet-box-write-failure"}
					} else {
						if hits.Load() != 0 {
							t.Fatal("unexpected fault")
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
					compareCheckinJSON(t, "reopen-preserves-Go-IDs-and-source-keys", persisted, persist(t))
					if step.Trigger != nil {
						if _, err := store.db.ExecContext(ctx, "DROP TRIGGER synthetic_pet_box_failure"); err != nil {
							t.Fatal(err)
						}
					}
					for read := 0; read < 2; read++ {
						var state any
						var err error
						if scenario.Blind {
							state, err = store.PetBlindBox(ctx, step.Input.Now)
						} else {
							state, err = store.PetStore(ctx, step.Input.Now)
						}
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
			for _, entry := range scenario.Pool {
				key, ok := entry[0].(string)
				weight, number := entry[1].(json.Number)
				if !ok || !number {
					t.Fatal("invalid weighted oracle pool")
				}
				if value, err := weight.Int64(); err != nil || value <= 0 {
					t.Fatal("invalid weight", weight)
				}
				for _, boundary := range []string{"start", "end"} {
					if !seenSteps["boundary-"+key+"-"+boundary] {
						t.Fatal("missing weighted endpoint", key, boundary)
					}
				}
			}
			required := []string{"initial-state"}
			if scenario.Name == "gift-missing" || scenario.Name == "gift-zero" {
				required = append(required, "open-unavailable-gift", "use-unavailable-gift")
			} else {
				required = append(required, "same-clock-first", "same-clock-replay", "before-epoch-clock", "rollback-inventory", "rollback-wallet", "rollback-economy")
				if scenario.Blind {
					required = append(required, "boundary-nothing-start", "boundary-nothing-end", "rollback-blind-event", "rollback-profile", "rollback-draw", "daily-exhausted-0", "next-day-1")
				} else {
					required = append(required, "rollback-gift-consume", "rollback-gift-event", "use-gift-production-entry")
				}
			}
			for _, name := range required {
				if !seenSteps[name] {
					t.Fatal("missing box coverage", name)
				}
			}
		})
	}
	if !t.Failed() {
		if steps != 362 || boundaries != 296 || rejected != 35 || faults != 16 {
			t.Fatalf("incomplete box oracle coverage: steps=%d endpoints=%d rejections=%d faults=%d", steps, boundaries, rejected, faults)
		}
		t.Logf("%d real Rust box steps, %d weighted interval endpoints, %d rejections, %d fault rollbacks; 11 complete tables and full APIs, every step reopened three times", steps, boundaries, rejected, faults)
	}
}

func TestPetBoxCompatibilityBijection(t *testing.T) {
	snapshot := func(native bool) map[string]any {
		prefix, source := "rust", "gift-box:clover-bow:gift-box-1780000000000-2"
		if native {
			prefix, source = "go", "gift-box:clover-bow:gift-box-0123456789abcdef0123456789abcdef"
		}
		return map[string]any{
			"profile":  []any{[]any{"default-pet", "Libby", json.Number("1"), json.Number("0"), "first_meet", "idle", "before", "after"}},
			"checkins": []any{}, "inventory": []any{}, "cosmetics": []any{}, "settings": []any{}, "wallets": []any{}, "counters": []any{}, "limits": []any{}, "events": []any{},
			"economy": []any{[]any{prefix + "-ledger", "default-pet", "earn", "lp", json.Number("30"), json.Number("30"), "gift_box_duplicate", source, "clover-bow", "2026-07-01T12:00:00Z"}},
			"draws":   []any{[]any{prefix + "-draw-1", "default-pet", "2026-07-01", "nothing", "none", json.Number("1"), json.Number("0"), "2026-07-01T12:00:00Z"}, []any{prefix + "-draw-2", "default-pet", "2026-07-01", "clover-bow", "cosmetic", json.Number("1"), json.Number("30"), "2026-07-01T12:00:00Z"}},
		}
	}
	newIDs := func() *boxIDs {
		return &boxIDs{entities: newCheckinIDs(), draws: newCheckinIDs(), keys: newCheckinIDs()}
	}
	ids := newIDs()
	if err := ids.bind(snapshot(false), snapshot(true), false); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"source-key-ID-drift", func(value map[string]any) {
			value["economy"].([]any)[0].([]any)[7] = "gift-box:clover-bow:gift-box-fedcba9876543210fedcba9876543210"
		}},
		{"source-key-wrong-item", func(value map[string]any) {
			value["economy"].([]any)[0].([]any)[7] = "gift-box:bell-accessory:gift-box-0123456789abcdef0123456789abcdef"
		}},
		{"source-key-not-generated", func(value map[string]any) {
			value["economy"].([]any)[0].([]any)[7] = "gift-box:clover-bow:gift-box-anything"
		}},
		{"draw-ID-drift", func(value map[string]any) { value["draws"].([]any)[0].([]any)[0] = "go-new-draw" }},
		{"draw-ID-collision", func(value map[string]any) { value["draws"].([]any)[1].([]any)[0] = "go-draw-1" }},
		{"draw-ID-swapped", func(value map[string]any) {
			rows := value["draws"].([]any)
			rows[0].([]any)[0], rows[1].([]any)[0] = rows[1].([]any)[0], rows[0].([]any)[0]
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			native := snapshot(true)
			scenario.mutate(native)
			if err := ids.bind(snapshot(false), native, false); err == nil {
				t.Fatal("entity ID difference was hidden")
			}
		})
	}
	if err := newIDs().bind(snapshot(false), snapshot(true), true); err == nil {
		t.Fatal("baseline IDs must not be remapped")
	}
	for _, scenario := range []struct {
		name, table string
		column      int
	}{
		{"compensation", "economy", 4}, {"draw-item", "draws", 3}, {"draw-quantity", "draws", 5}, {"draw-compensation", "draws", 6},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			rust, native := snapshot(false), snapshot(true)
			native[scenario.table].([]any)[0].([]any)[scenario.column] = "synthetic-changed-value"
			ids := newIDs()
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
}
