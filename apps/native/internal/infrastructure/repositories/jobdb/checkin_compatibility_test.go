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
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
	"modernc.org/sqlite"
)

type checkinCompatibilityInput struct {
	Name      string `json:"name"`
	Operation string `json:"operation"`
	Now       string `json:"now"`
	Date      string `json:"date"`
	Rejected  bool   `json:"rejected"`
	FailWrite bool   `json:"failWrite"`
	Duplicate bool   `json:"duplicate"`
	Unchanged bool   `json:"unchanged"`
}

var checkinFaultSequence atomic.Uint64

var checkinTableColumns = map[string]string{
	"profile":   "id,name,level,experience,stage,current_mood,created_at,updated_at",
	"settings":  "pet_id,desktop_enabled,always_on_top,muted,focus_mode_enabled,proactive_level,last_window_x,last_window_y,updated_at",
	"checkins":  "id,pet_id,check_in_date,streak_count,cycle_day,reward_lp,growth_value,reward_items_json,created_at",
	"events":    "id,pet_id,event_type,event_source,event_value,event_time,metadata",
	"economy":   "id,pet_id,entry_type,currency_key,amount,balance_after,source_type,source_key,metadata,created_at",
	"wallets":   "pet_id,currency_key,balance,lifetime_earned,lifetime_spent,updated_at",
	"inventory": "id,pet_id,item_key,item_type,slot,quantity,equipped,source,purchased_at,updated_at",
	"cosmetics": "id,pet_id,cosmetic_type,cosmetic_key,unlocked_at,equipped",
	"counters":  "pet_id,counter_key,counter_value,last_event_key,updated_at",
	"limits":    "pet_id,item_key,limit_date,free_claimed,updated_at",
}

func TestWriteRustCheckInMakeupMilestoneAndReopen(t *testing.T) {
	ctx := context.Background()
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if directory == "" {
		t.Fatal("run the Rust export_native_checkin_compatibility_fixture with LIBERTY_NATIVE_FIXTURE_DIR")
	}
	var fixture struct {
		ClockMode      string              `json:"clockMode"`
		StartDate      string              `json:"startDate"`
		ContinuousDays int                 `json:"continuousDays"`
		Columns        map[string][]string `json:"columns"`
		Queries        map[string]string   `json:"queries"`
		Initial        json.RawMessage     `json:"initial"`
		Steps          []struct {
			Input         checkinCompatibilityInput `json:"input"`
			Output        json.RawMessage           `json:"output"`
			Persisted     json.RawMessage           `json:"persisted"`
			ReopenedState json.RawMessage           `json:"reopenedState"`
			AfterRead     json.RawMessage           `json:"afterRead"`
			TriggerError  *string                   `json:"triggerError"`
		} `json:"steps"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "checkin-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.ClockMode != "thread-local-fixed" || fixture.StartDate != "2026-07-01" || fixture.ContinuousDays != 21 || len(fixture.Queries) != 10 || len(fixture.Columns) != 10 || len(fixture.Steps) != 83 {
		t.Fatal("incomplete or unexpectedly changed Rust check-in fixture")
	}
	for name, columns := range checkinTableColumns {
		if !reflect.DeepEqual(fixture.Columns[name], strings.Split(columns, ",")) || fixture.Queries[name] == "" {
			t.Fatalf("missing or changed %s table projection", name)
		}
	}
	var faultHits atomic.Int64
	faultFunction := fmt.Sprintf("liberty_checkin_fault_probe_%d", checkinFaultSequence.Add(1))
	if err := sqlite.RegisterScalarFunction(faultFunction, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		faultHits.Add(1)
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Parallel()
	baseline, err := os.ReadFile(filepath.Join(directory, "checkin-base.sqlite3"))
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
			store = nil
			t.Fatal(err)
		}
		store = nil
		store, err = openCompatibility(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
	}
	ids := newCheckinIDs()
	compareSnapshot := func(t *testing.T, label string, expected, actual any, baseline bool) {
		t.Helper()
		rust := decodeCheckinJSON(t, expected).(map[string]any)
		native := decodeCheckinJSON(t, actual).(map[string]any)
		if err := ids.bindSnapshot(rust, native, baseline); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		for side, snapshot := range []map[string]any{rust, native} {
			if err := ids.normalizeSnapshot(snapshot, side); err != nil {
				t.Fatalf("%s: %v", label, err)
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
	compareSnapshot(t, "initial", fixture.Initial, checkinPersistence(t, store, fixture.Queries), true)
	seen := map[string]bool{}
	rejected, faults, claims, makeups, duplicates := 0, 0, 0, 0, 0
	for _, step := range fixture.Steps {
		if store == nil {
			t.Fatal("cannot continue after reopening the isolated check-in database failed")
		}
		if seen[step.Input.Name] {
			t.Fatalf("duplicate fixture step %q", step.Input.Name)
		}
		seen[step.Input.Name] = true
		t.Run(step.Input.Name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339Nano, step.Input.Now)
			if err != nil {
				t.Fatal(err)
			}
			if date := now.Local().Format("2006-01-02"); date != step.Input.Date {
				t.Fatalf("Rust fixed local date=%s, Go input local date=%s", step.Input.Date, date)
			}
			faultHits.Store(0)
			if step.Input.FailWrite {
				trigger := fmt.Sprintf("CREATE TRIGGER synthetic_checkin_failure BEFORE INSERT ON pet_daily_check_ins BEGIN SELECT %s(); SELECT RAISE(ABORT,'synthetic-checkin-write-failure'); END;", faultFunction)
				if _, err := store.db.ExecContext(ctx, trigger); err != nil {
					t.Fatal(err)
				}
			}
			before := checkinPersistence(t, store, fixture.Queries)
			var output any
			var actionErr error
			switch step.Input.Operation {
			case "state":
				output, actionErr = store.PetCheckIn(ctx, step.Input.Now)
			case "claim":
				var result domain.PetDailyCheckInClaimResult
				result, actionErr = store.PetClaimCheckIn(ctx, step.Input.Now)
				output = result
				if actionErr == nil {
					if result.Duplicate != step.Input.Duplicate {
						t.Errorf("duplicate: expected=%v actual=%v", step.Input.Duplicate, result.Duplicate)
					}
					if result.Duplicate {
						duplicates++
					} else {
						claims++
					}
				}
			case "makeup":
				output, actionErr = store.PetMakeupCheckIn(ctx, step.Input.Now)
				if actionErr == nil {
					makeups++
				}
			default:
				t.Fatalf("unknown check-in operation %q", step.Input.Operation)
			}
			if (actionErr != nil) != step.Input.Rejected {
				t.Errorf("Rust rejected=%v, Go error=%v", step.Input.Rejected, actionErr)
			}
			if step.Input.FailWrite {
				faults++
				if !errors.Is(actionErr, ErrDatabase) || faultHits.Load() != 1 || step.TriggerError == nil || *step.TriggerError != "synthetic-checkin-write-failure" {
					t.Fatalf("synthetic trigger provenance: Go error=%v hits=%d Rust=%v", actionErr, faultHits.Load(), step.TriggerError)
				}
				output = map[string]any{"fault": "synthetic-checkin-write-failure"}
			} else {
				if faultHits.Load() != 0 || step.TriggerError != nil {
					t.Fatal("unexpected synthetic fault outside injected step")
				}
				if actionErr != nil {
					output = map[string]any{"error": actionErr.Error()}
				}
			}
			persisted := checkinPersistence(t, store, fixture.Queries)
			compareSnapshot(t, "persisted", step.Persisted, persisted, false)
			compareAPI(t, "output", step.Output, output)
			if step.Input.Rejected || step.Input.Unchanged {
				compareCheckinJSON(t, "rejected-or-duplicate-action-rollback", before, persisted)
			}
			if step.Input.Rejected {
				rejected++
			}
			reopen(t)
			reopened := checkinPersistence(t, store, fixture.Queries)
			compareSnapshot(t, "reopened", step.Persisted, reopened, false)
			compareCheckinJSON(t, "reopen-preserves-Go-writes", persisted, reopened)
			if step.Input.Rejected {
				compareCheckinJSON(t, "reopened-rejected-action-rollback", before, reopened)
			}
			if step.Input.FailWrite {
				if _, err := store.db.ExecContext(ctx, "DROP TRIGGER synthetic_checkin_failure"); err != nil {
					t.Fatal(err)
				}
			}
			for read := 1; read <= 2; read++ {
				state, err := store.PetCheckIn(ctx, step.Input.Now)
				if err != nil {
					t.Fatal(err)
				}
				afterRead := checkinPersistence(t, store, fixture.Queries)
				compareSnapshot(t, fmt.Sprintf("after-reopen-read-%d", read), step.AfterRead, afterRead, false)
				compareAPI(t, fmt.Sprintf("reopened-state-%d", read), step.ReopenedState, state)
				reopen(t)
				compareCheckinJSON(t, "repeated-reopen-preserves-IDs-and-JSON", afterRead, checkinPersistence(t, store, fixture.Queries))
			}
		})
	}
	for day := 1; day <= 21; day++ {
		for _, operation := range []string{"before", "claim", "duplicate"} {
			if !seen[fmt.Sprintf("%s-day-%02d", operation, day)] {
				t.Fatalf("missing day %d %s coverage", day, operation)
			}
		}
	}
	for _, name := range []string{"replay-day-01-after-day-02", "missed-one-day", "makeup-day-22", "makeup-day-24", "remaining-missed-day", "makeup-no-ticket-rejected", "makeup-seven-days-rejected", "claim-day-26-reset", "claim-day-34-reset"} {
		if !seen[name] {
			t.Fatalf("missing scenario %s", name)
		}
	}
	if rejected != 6 || faults != 2 || claims != 24 || makeups != 2 || duplicates != 25 {
		t.Fatalf("unexpected coverage: rejected=%d faults=%d claims=%d makeups=%d duplicates=%d", rejected, faults, claims, makeups, duplicates)
	}
	t.Log("83 steps, 21 consecutive days, 10 tables + complete APIs, 6 rejections, 2 trigger faults, 25 duplicates; every step reopened three times")
}

func checkinPersistence(t *testing.T, store *Store, queries map[string]string) map[string]any {
	t.Helper()
	snapshot := make(map[string]any, len(queries))
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
		if !reflect.DeepEqual(columns, strings.Split(checkinTableColumns[name], ",")) {
			rows.Close()
			t.Fatalf("actual query projection changed for %s: %v", name, columns)
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
			for column, field := range columns {
				if field == "metadata" || strings.HasSuffix(field, "_json") {
					row[column] = checkinEmbeddedJSON(row[column])
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

type checkinIDPair struct{ rust, native string }

type checkinIDs struct {
	identities map[string]checkinIDPair
	owners     [2]map[string]string
	active     [2]map[string]map[string]string
}

func newCheckinIDs() *checkinIDs {
	return &checkinIDs{identities: map[string]checkinIDPair{}, owners: [2]map[string]string{{}, {}}}
}

func (ids *checkinIDs) bind(category, identity, rust, native string, baseline bool) error {
	key := category + ":" + identity
	if rust == "" || native == "" {
		return fmt.Errorf("%s: empty ID", key)
	}
	pair := checkinIDPair{rust, native}
	if previous, exists := ids.identities[key]; exists && previous != pair {
		return fmt.Errorf("%s: existing ID changed from %+v to %+v", key, previous, pair)
	}
	opaque := category == "checkins" || category == "economy" || category == "events"
	if (baseline || !opaque) && rust != native {
		return fmt.Errorf("%s: existing or deterministic ID differs: Rust=%q Go=%q", key, rust, native)
	}
	for side, value := range []string{rust, native} {
		if owner, exists := ids.owners[side][value]; exists && owner != key {
			return fmt.Errorf("%s: side %d ID %q collides with %s", key, side, value, owner)
		}
	}
	ids.identities[key] = pair
	ids.owners[0][rust], ids.owners[1][native] = key, key
	return nil
}

func (ids *checkinIDs) bindSnapshot(rust, native map[string]any, baseline bool) error {
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
				if _, exists := rowsByKey[side][string(key)]; exists {
					return fmt.Errorf("%s: duplicate business identity %s", category, key)
				}
				if _, exists := ids.active[side][category][id]; exists {
					return fmt.Errorf("%s: duplicate primary ID %q", category, id)
				}
				rowsByKey[side][string(key)] = id
				ids.active[side][category][id] = string(key)
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

func (ids *checkinIDs) reference(category, id string, side int) (string, error) {
	identity, exists := ids.active[side][category][id]
	if !exists {
		return "", fmt.Errorf("%s: side %d dangling or wrong-entity ID %q", category, side, id)
	}
	return ids.identities[category+":"+identity].rust, nil
}

func checkinEmbeddedJSON(value any) any {
	if text, ok := value.(string); ok && json.Valid([]byte(text)) {
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var decoded any
		if decoder.Decode(&decoded) == nil {
			return map[string]any{"$checkinJSON": decoded}
		}
	}
	return value
}

func (ids *checkinIDs) normalizeSnapshot(snapshot map[string]any, side int) error {
	for name, columns := range checkinTableColumns {
		rows, ok := snapshot[name].([]any)
		if !ok {
			return fmt.Errorf("%s: missing table", name)
		}
		for _, value := range rows {
			row, ok := value.([]any)
			if !ok || len(row) != len(strings.Split(columns, ",")) {
				return fmt.Errorf("%s: malformed row", name)
			}
			for column, field := range strings.Split(columns, ",") {
				if field == "id" || field == "pet_id" {
					category := name
					if field == "pet_id" {
						category = "profile"
					}
					id, ok := row[column].(string)
					if !ok {
						return fmt.Errorf("%s.%s: invalid reference", name, field)
					}
					normalized, err := ids.reference(category, id, side)
					if err != nil {
						return err
					}
					row[column] = normalized
				} else if field == "metadata" || strings.HasSuffix(field, "_json") {
					row[column] = checkinEmbeddedJSON(row[column])
				}
			}
		}
	}
	return nil
}

func (ids *checkinIDs) normalizeAPI(value any, side int) error {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			if err := ids.normalizeAPI(item, side); err != nil {
				return err
			}
		}
	case map[string]any:
		category := ""
		switch {
		case value["checkInDate"] != nil && value["streakCount"] != nil:
			category = "checkins"
		case value["sourceType"] != nil && value["currencyKey"] != nil:
			category = "economy"
		case value["itemKey"] != nil && value["purchasedAt"] != nil:
			category = "inventory"
		case value["experience"] != nil && value["stage"] != nil:
			category = "profile"
		}
		for field, item := range value {
			if (field == "id" && category != "") || field == "petId" {
				target := category
				if field == "petId" {
					target = "profile"
				}
				id, ok := item.(string)
				if !ok {
					return fmt.Errorf("API %s.%s: invalid ID", category, field)
				}
				normalized, err := ids.reference(target, id, side)
				if err != nil {
					return err
				}
				value[field] = normalized
			} else {
				if field == "metadata" || strings.HasSuffix(field, "_json") {
					item = checkinEmbeddedJSON(item)
					value[field] = item
				}
				if err := ids.normalizeAPI(item, side); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func decodeCheckinJSON(t *testing.T, value any) any {
	t.Helper()
	data, raw := value.(json.RawMessage)
	if raw {
		if data == nil {
			return nil
		}
	} else {
		var err error
		data, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		t.Fatal("trailing check-in JSON")
	}
	return decoded
}

func checkinJSONDifferences(path string, rust, native any) []string {
	if reflect.DeepEqual(rust, native) {
		return nil
	}
	switch rust := rust.(type) {
	case json.Number:
		if native, ok := native.(json.Number); ok {
			left, leftOK := new(big.Rat).SetString(string(rust))
			right, rightOK := new(big.Rat).SetString(string(native))
			if leftOK && rightOK && left.Cmp(right) == 0 {
				return nil
			}
		}
	case map[string]any:
		if native, ok := native.(map[string]any); ok {
			keys := make([]string, 0, len(rust)+len(native))
			for key := range rust {
				keys = append(keys, key)
			}
			for key := range native {
				if _, exists := rust[key]; !exists {
					keys = append(keys, key)
				}
			}
			sort.Strings(keys)
			var differences []string
			for _, key := range keys {
				rustValue, rustExists := rust[key]
				nativeValue, nativeExists := native[key]
				if rustExists != nativeExists {
					differences = append(differences, fmt.Sprintf("%s.%s: Rust field exists=%v, Go field exists=%v", path, key, rustExists, nativeExists))
					continue
				}
				differences = append(differences, checkinJSONDifferences(path+"."+key, rustValue, nativeValue)...)
			}
			return differences
		}
	case []any:
		if native, ok := native.([]any); ok {
			var differences []string
			if len(rust) != len(native) {
				differences = append(differences, fmt.Sprintf("%s: Rust rows/items=%d Go rows/items=%d", path, len(rust), len(native)))
			}
			for index := 0; index < min(len(rust), len(native)); index++ {
				differences = append(differences, checkinJSONDifferences(fmt.Sprintf("%s[%d]", path, index), rust[index], native[index])...)
			}
			return differences
		}
	}
	return []string{fmt.Sprintf("%s: Rust=%#v Go=%#v", path, rust, native)}
}

func compareCheckinJSON(t *testing.T, label string, expected, actual any) {
	t.Helper()
	compareDecodedCheckinJSON(t, label, decodeCheckinJSON(t, expected), decodeCheckinJSON(t, actual))
}

func compareDecodedCheckinJSON(test *testing.T, label string, expected, actual any) {
	test.Helper()
	for _, difference := range checkinJSONDifferences(label, expected, actual) {
		test.Error(difference)
	}
}

func TestCheckinCompatibilityJSONIsExact(t *testing.T) {
	for _, scenario := range []struct {
		name, rust, native string
		equal              bool
	}{
		{"numeric-zero", `0`, `0.0`, true},
		{"numeric-exponent", `9223372036854775806`, `9.223372036854775806e18`, true},
		{"int64-precision", `9223372036854775806`, `9223372036854775807`, false},
		{"keys-and-numbers", `{"b":0.0,"a":1}`, `{"a":1.0,"b":0}`, true},
		{"missing-field", `{"a":null}`, `{}`, false},
		{"array-order", `[1,2]`, `[2,1]`, false},
		{"null-and-empty", `null`, `[]`, false},
		{"balance", `631`, `630`, false},
		{"date", `"2026-07-14"`, `"2026-07-15"`, false},
		{"growth", `130`, `125`, false},
		{"wording", `"补签完成"`, `"签到完成"`, false},
		{"state", `true`, `false`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			differences := checkinJSONDifferences("value", decodeCheckinJSON(t, json.RawMessage(scenario.rust)), decodeCheckinJSON(t, json.RawMessage(scenario.native)))
			if (len(differences) == 0) != scenario.equal {
				t.Fatalf("equal=%v differences=%v", scenario.equal, differences)
			}
		})
	}
	left := checkinEmbeddedJSON(`{"preciseInteger":9223372036854775806,"zero":0.0}`)
	right := checkinEmbeddedJSON(`{ "zero":0,"preciseInteger":9223372036854775806 }`)
	if differences := checkinJSONDifferences("metadata", left, right); len(differences) != 0 {
		t.Fatal(differences)
	}
	if checkinEmbeddedJSON("gift-box-tool") != "gift-box-tool" {
		t.Fatal("legacy plain-text metadata changed")
	}
	if len(checkinJSONDifferences("metadata", checkinEmbeddedJSON("gift-box-tool"), checkinEmbeddedJSON(`"gift-box-tool"`))) == 0 {
		t.Fatal("plain-text metadata must not equal a JSON string document")
	}
}

func TestCheckinCompatibilityDecodePreservesPrecisionAndIsolation(test *testing.T) {
	oracle := json.RawMessage(`{"rows":[["synthetic",9223372036854775807,"0.0",null]],"empty":[]}`)
	snapshot := map[string]any{
		"rows":  [][]any{{"synthetic", int64(9223372036854775807), "0.0", nil}},
		"empty": []any{},
	}
	rust := decodeCheckinJSON(test, oracle).(map[string]any)
	native := decodeCheckinJSON(test, snapshot).(map[string]any)
	compareDecodedCheckinJSON(test, "snapshot", rust, native)
	if number, ok := native["rows"].([]any)[0].([]any)[1].(json.Number); !ok || number.String() != "9223372036854775807" {
		test.Fatal("SQLite integer lost precision", number)
	}
	native["rows"].([]any)[0].([]any)[0] = "changed-ID"
	native["empty"] = nil
	if differences := checkinJSONDifferences("snapshot", rust, native); len(differences) != 2 {
		test.Fatal("ID drift or null/empty difference hidden", differences)
	}
	compareDecodedCheckinJSON(test, "source-retained", rust, decodeCheckinJSON(test, snapshot))
	rust["rows"].([]any)[0].([]any)[2] = "0"
	if differences := checkinJSONDifferences("oracle", decodeCheckinJSON(test, oracle), rust); len(differences) != 1 {
		test.Fatal("opaque JSON text changed or oracle mutated", differences)
	}
	if decodeCheckinJSON(test, json.RawMessage(nil)) != nil {
		test.Fatal("nil raw JSON must remain null")
	}
}

func TestCheckinCompatibilityIDMappingRejectsDrift(t *testing.T) {
	ids := newCheckinIDs()
	for _, binding := range []struct {
		category, identity, rust, native string
		baseline, rejected               bool
	}{
		{"events", "existing", "seed", "seed", true, false},
		{"events", "existing", "seed", "rewritten", false, true},
		{"events", "existing", "rewritten", "seed", false, true},
		{"checkins", "day-1", "rust-checkin", "go-checkin", false, false},
		{"checkins", "day-1", "rust-checkin", "go-checkin", false, false},
		{"checkins", "day-2", "rust-day-2", "go-checkin", false, true},
		{"checkins", "day-2", "rust-checkin", "go-day-2", false, true},
		{"economy", "reward", "rust-checkin", "go-economy", false, true},
		{"inventory", "item", "rust-inventory", "go-inventory", false, true},
		{"events", "missing", "", "go-event", false, true},
	} {
		err := ids.bind(binding.category, binding.identity, binding.rust, binding.native, binding.baseline)
		if (err != nil) != binding.rejected {
			t.Fatalf("%+v: %v", binding, err)
		}
	}
	ids.active = [2]map[string]map[string]string{
		{"checkins": {"rust-checkin": "day-1"}},
		{"checkins": {"go-checkin": "day-1"}},
	}
	valid := map[string]any{"id": "go-checkin", "checkInDate": "2026-07-01", "streakCount": json.Number("1")}
	if err := ids.normalizeAPI(valid, 1); err != nil || valid["id"] != "rust-checkin" {
		t.Fatalf("valid API reference: %v %v", valid, err)
	}
	for _, id := range []string{"dangling", "seed", "rust-checkin"} {
		if _, err := ids.reference("checkins", id, 1); err == nil {
			t.Fatalf("accepted dangling/wrong-entity/wrong-side reference %q", id)
		}
	}
}
