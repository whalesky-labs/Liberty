//go:build compatibility

package jobdb

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql/driver"
	"encoding/base64"
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

	"liberty.local/liberty/internal/domain"
	"modernc.org/sqlite"
)

type redemptionInput struct {
	Name, Operation, Code, Now, Fault, SetupSQL string
	Limit                                       int
	Rejected                                    bool
}

type redemptionFixture struct {
	ClockMode, PublicKey string
	Queries              map[string]string
	Cases                []struct {
		Name    string
		Initial json.RawMessage
		Steps   []struct {
			Input         redemptionInput
			Output        json.RawMessage
			RawError      *string
			Trigger       *string
			Persisted     json.RawMessage
			ReopenedState json.RawMessage
			AfterRead     json.RawMessage
		}
	}
	LegacyBoundary struct {
		Code                              string
		Before, RustOutput, RustPersisted json.RawMessage
	}
}

type redemptionIDs struct{ entities *checkinIDs }

var generatedRedemptionID = regexp.MustCompile(`^pet-redeem-([0-9]+-[0-9]+|[0-9a-f]{32})$`)

func TestPetRedemptionExpiredSignedReissueReturnsExistingIdentity(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("synthetic key generation failed")
	}
	expires := "2026-11-18"
	value := domain.PetRedemptionPayload{Version: 1, CampaignID: "synthetic-campaign", Nonce: "synthetic-nonce", ExpiresAt: &expires, Rewards: domain.PetRedeemKeyRewards{LP: 40, GrowthValue: 7, Items: []domain.PetRedeemKeyRewardItem{}}}
	sign := func() string {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		encoded := base64.RawURLEncoding.EncodeToString(data)
		return "LIB1." + encoded + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(encoded)))
	}
	verify := func(code string, _ time.Time) (domain.PetRedemptionPayload, error) {
		return domain.VerifyPetRedemptionContentForCompatibility(code, public)
	}
	store := redemptionStore(t)
	ctx := context.Background()
	first, err := store.redeemKey(ctx, sign(), "2026-11-18T12:00:00Z", verify)
	if err != nil || first.Duplicate {
		t.Fatal("first real signed redemption failed", err)
	}
	path := filepath.Join(store.owner.Directory(), "synthetic.sqlite3")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = openCompatibility(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	issued := "2026-11-17"
	value.IssuedAt = &issued
	second, err := store.redeemKey(ctx, sign(), "2030-01-01T00:00:00Z", verify)
	if err != nil || !second.Duplicate || !reflect.DeepEqual(first.Redemption, second.Redemption) {
		t.Fatal("expired signed reissue lost persisted identity", err)
	}
	if !reflect.DeepEqual(first.State.Wallet, second.State.Wallet) || !reflect.DeepEqual(first.State.Profile, second.State.Profile) {
		t.Fatal("expired signed reissue granted rewards again")
	}
}

func TestPetRedemptionSignedUnicodeRejectionRollsBackAndReopens(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("synthetic key generation failed")
	}
	queries := map[string]string{}
	for name, table := range map[string]string{"profile": "pet_profile", "settings": "pet_settings", "checkins": "pet_daily_check_ins", "events": "pet_event_ledger", "economy": "pet_economy_ledger", "wallets": "pet_wallets", "inventory": "pet_inventory", "cosmetics": "pet_cosmetic_unlocks", "counters": "pet_milestone_counters", "limits": "pet_store_daily_limits"} {
		queries[name] = "SELECT " + checkinTableColumns[name] + " FROM " + table + " ORDER BY rowid"
	}
	queries["redemptions"] = "SELECT " + redemptionColumns + " FROM pet_redeem_key_redemptions ORDER BY rowid"
	for _, scenario := range []struct {
		name, payload string
		rejected      bool
	}{
		{"campaign-high", `{"version":1,"campaignId":"\ud800","nonce":"a","rewards":{"lp":1}}`, true},
		{"campaign-low", `{"version":1,"campaignId":"\udc00","nonce":"a","rewards":{"lp":1}}`, true},
		{"nonce-high", `{"version":1,"campaignId":"synthetic","nonce":"\ud800","rewards":{"lp":1}}`, true},
		{"issued-high", `{"version":1,"campaignId":"synthetic","nonce":"a","issuedAt":"\ud800","rewards":{"lp":1}}`, true},
		{"expires-low", `{"version":1,"campaignId":"synthetic","nonce":"a","expiresAt":"\udc00","rewards":{"lp":1}}`, true},
		{"item-high", `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1,"items":[{"itemKey":"\ud800"}]}}`, true},
		{"root-name", `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1},"\ud800":1}`, true},
		{"reward-name", `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1,"\udc00":1}}`, true},
		{"item-name", `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1,"items":[{"itemKey":"wheat-seed","\ud800":1}]}}`, true},
		{"valid-pair", `{"version":1,"campaignId":"\ud83d\ude00","nonce":"a","rewards":{"lp":1}}`, false},
		{"replacement-literal", `{"version":1,"campaignId":"�","nonce":"a","rewards":{"lp":1}}`, false},
		{"escaped-backslash", `{"version":1,"campaignId":"\\ud800","nonce":"a","rewards":{"lp":1}}`, false},
		{"ignored-nested-value", `{"version":1,"campaignId":"synthetic","nonce":"a","rewards":{"lp":1},"unknown":{"\ud800":["\udc00"]}}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			store := redemptionStore(t)
			now := "2026-11-18T12:00:00Z"
			if _, err := store.PetStore(ctx, now); err != nil {
				t.Fatal(err)
			}
			before := redemptionPersistence(t, store, queries)
			encoded := base64.RawURLEncoding.EncodeToString([]byte(scenario.payload))
			code := "LIB1." + encoded + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, []byte(encoded)))
			result, err := store.redeemKey(ctx, code, now, func(code string, _ time.Time) (domain.PetRedemptionPayload, error) {
				return domain.VerifyPetRedemptionContentForCompatibility(code, public)
			})
			if scenario.rejected {
				if !errors.Is(err, domain.ErrPetRedemption) || err.Error() != "兑换 Key 内容无法解析。" {
					t.Errorf("malformed signed Unicode reached reward chain: error=%v balance=%d", err, result.State.Wallet.Balance)
				}
			} else if err != nil || result.Duplicate || result.State.Wallet.Balance != 1 {
				t.Fatal("valid or ignored Unicode control did not redeem", err)
			}
			after := redemptionPersistence(t, store, queries)
			if scenario.rejected {
				compareCheckinJSON(t, "unicode-rejection-all-eleven-tables", before, after)
			}
			path := filepath.Join(store.owner.Directory(), "synthetic.sqlite3")
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = openCompatibility(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			compareCheckinJSON(t, "unicode-result-reopen", after, redemptionPersistence(t, store, queries))
		})
	}
}

func (ids *redemptionIDs) bind(rust, native map[string]any, baseline bool) error {
	if err := bindPetStoreIDs(ids.entities, rust, native, baseline); err != nil {
		return err
	}
	rows := [2]map[string]string{{}, {}}
	for side, snapshot := range []map[string]any{rust, native} {
		ids.entities.active[side]["redemptions"] = map[string]string{}
		for _, value := range snapshot["redemptions"].([]any) {
			row := value.([]any)
			if len(row) != 9 {
				return errors.New("malformed redemption row")
			}
			id, validID := row[0].(string)
			hash, validHash := row[2].(string)
			if !validID || !validHash || !generatedRedemptionID.MatchString(id) || len(hash) != 64 {
				return errors.New("invalid redemption entity identity")
			}
			if rows[side][hash] != "" || ids.entities.active[side]["redemptions"][id] != "" {
				return errors.New("duplicate redemption entity identity")
			}
			rows[side][hash] = id
			ids.entities.active[side]["redemptions"][id] = hash
		}
	}
	if len(rows[0]) != len(rows[1]) {
		return errors.New("redemption row count differs")
	}
	for hash, rustID := range rows[0] {
		nativeID := rows[1][hash]
		identity := "redemptions:" + hash
		pair := checkinIDPair{rust: rustID, native: nativeID}
		if nativeID == "" || (baseline && rustID != nativeID) {
			return errors.New("existing redemption identity differs")
		}
		if previous, exists := ids.entities.identities[identity]; exists && previous != pair {
			return errors.New("persisted redemption ID drift")
		}
		for side, id := range []string{rustID, nativeID} {
			if owner, exists := ids.entities.owners[side][id]; exists && owner != identity {
				return errors.New("redemption ID collides with another entity")
			}
			ids.entities.owners[side][id] = identity
		}
		ids.entities.identities[identity] = pair
	}
	return nil
}

func (ids *redemptionIDs) normalizeSnapshot(snapshot map[string]any, side int) error {
	columns := map[string]string{"redemptions": redemptionColumns}
	for table, fields := range checkinTableColumns {
		columns[table] = fields
	}
	for table, fields := range columns {
		for _, value := range snapshot[table].([]any) {
			row := value.([]any)
			for column, field := range strings.Split(fields, ",") {
				if field != "id" && field != "pet_id" {
					continue
				}
				category := table
				if field == "pet_id" {
					category = "profile"
				}
				id, ok := row[column].(string)
				if !ok {
					return errors.New("invalid persisted entity reference")
				}
				normalized, err := ids.entities.reference(category, id, side)
				if err != nil {
					return err
				}
				row[column] = normalized
			}
		}
	}
	return nil
}

func (ids *redemptionIDs) normalizeAPI(value any, side int) error {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			if err := ids.normalizeAPI(child, side); err != nil {
				return err
			}
		}
	case map[string]any:
		category := ""
		switch {
		case value["keyHash"] != nil && value["rewardJson"] != nil:
			category = "redemptions"
		case value["eventType"] != nil && value["eventTime"] != nil:
			category = "events"
		case value["cosmeticKey"] != nil && value["unlockedAt"] != nil:
			category = "cosmetics"
		case value["sourceType"] != nil && value["currencyKey"] != nil:
			category = "economy"
		case value["itemKey"] != nil && value["purchasedAt"] != nil:
			category = "inventory"
		case value["experience"] != nil && value["stage"] != nil:
			category = "profile"
		}
		for field, child := range value {
			if (field == "id" && category != "") || field == "petId" {
				target := category
				if field == "petId" {
					target = "profile"
				}
				id, ok := child.(string)
				if !ok {
					return errors.New("invalid API entity reference")
				}
				normalized, err := ids.entities.reference(target, id, side)
				if err != nil {
					return err
				}
				value[field] = normalized
			} else if err := ids.normalizeAPI(child, side); err != nil {
				return err
			}
		}
	}
	return nil
}

func redemptionView(t *testing.T, store *Store, now string) any {
	t.Helper()
	ctx := context.Background()
	state, err := store.PetStore(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.PetRedemptions(ctx, 100)
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
	return map[string]any{"state": state, "history": history, "events": events, "cosmetics": cosmetics}
}

func redemptionPersistence(t *testing.T, store *Store, queries map[string]string) map[string]any {
	t.Helper()
	base := map[string]string{}
	for table, query := range queries {
		if table != "redemptions" {
			base[table] = query
		}
	}
	snapshot := fullGameplayPersistence(t, store, base)
	rows, err := store.db.QueryContext(context.Background(), queries["redemptions"])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, strings.Split(redemptionColumns, ",")) {
		t.Fatal("changed redemption table projection", err)
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
	snapshot["redemptions"] = values
	return snapshot
}

func TestWriteRustPetRedemptionActionsAndReopen(t *testing.T) {
	ctx := context.Background()
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if !filepath.IsAbs(directory) {
		t.Fatal("explicit isolated LIBERTY_NATIVE_FIXTURE_DIR required")
	}
	data, err := os.ReadFile(filepath.Join(directory, "pet-redemption-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture redemptionFixture
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if decoder.Decode(new(any)) != io.EOF || fixture.ClockMode != "thread-local-fixed" || len(fixture.Queries) != 11 || len(fixture.Cases) != 8 {
		t.Fatal("incomplete Rust redemption oracle")
	}
	public, err := base64.RawURLEncoding.Strict().DecodeString(fixture.PublicKey)
	if err != nil || len(public) != 32 {
		t.Fatal("invalid synthetic public key")
	}
	verifier := func(code string, _ time.Time) (domain.PetRedemptionPayload, error) {
		return domain.VerifyPetRedemptionContentForCompatibility(code, public)
	}
	var hits atomic.Int64
	function := fmt.Sprintf("liberty_pet_redemption_fault_%d", checkinFaultSequence.Add(1))
	if err := sqlite.RegisterScalarFunction(function, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		hits.Add(1)
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Parallel()
	cases, steps, rejected, faults, reopens := 0, 0, 0, 0, 0
	expectedSteps := map[string]int{"signed": 22, "parser": 30, "unicode": 59, "compact": 14, "identity": 11, "legacy": 4, "faults": 18, "history": 32}
	seenCases := map[string]bool{}
	for _, scenario := range fixture.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			if seenCases[scenario.Name] || expectedSteps[scenario.Name] != len(scenario.Steps) || len(scenario.Steps) == 0 {
				t.Fatal("duplicate or empty redemption scenario")
			}
			seenCases[scenario.Name] = true
			cases++
			baseline, err := os.ReadFile(filepath.Join(directory, "pet-redemption-"+scenario.Name+"-base.sqlite3"))
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
			persist := func(t *testing.T) map[string]any { return redemptionPersistence(t, store, fixture.Queries) }
			ids := redemptionIDs{entities: newCheckinIDs()}
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
				}
				compareDecodedCheckinJSON(t, label, values[0], values[1])
			}
			compareSnapshot(t, "initial", scenario.Initial, persist(t), true)
			seenSteps := map[string]bool{}
			for _, step := range scenario.Steps {
				t.Run(step.Input.Name, func(t *testing.T) {
					if step.Input.Name == "" || seenSteps[step.Input.Name] {
						t.Fatal("duplicate or unnamed redemption step")
					}
					seenSteps[step.Input.Name] = true
					steps++
					if _, err := time.Parse(time.RFC3339Nano, step.Input.Now); err != nil {
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
					if step.Input.Operation == "history" {
						output, actionErr = store.PetRedemptions(ctx, step.Input.Limit)
					} else {
						output, actionErr = store.redeemKey(ctx, step.Input.Code, step.Input.Now, verifier)
					}
					if (actionErr != nil) != step.Input.Rejected {
						t.Errorf("Rust rejected=%v Go error=%v", step.Input.Rejected, actionErr)
					}
					if step.Trigger != nil {
						faults++
						if !errors.Is(actionErr, ErrDatabase) || hits.Load() != 1 || step.RawError == nil || *step.RawError != "synthetic-pet-redemption-write-failure" {
							t.Fatalf("fault provenance error=%v hits=%d", actionErr, hits.Load())
						}
						output = map[string]any{"fault": "synthetic-pet-redemption-write-failure"}
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
					compareCheckinJSON(t, "reopen-retains-Go-IDs", persisted, persist(t))
					if step.Trigger != nil {
						if _, err := store.db.ExecContext(ctx, "DROP TRIGGER synthetic_pet_redemption_failure"); err != nil {
							t.Fatal(err)
						}
					}
					for read := 0; read < 2; read++ {
						state := redemptionView(t, store, step.Input.Now)
						compareSnapshot(t, "after-read", step.AfterRead, persist(t), false)
						compareAPI(t, "reopened-state", step.ReopenedState, state)
						reopen(t)
						compareSnapshot(t, "reopened-after-read", step.AfterRead, persist(t), false)
					}
				})
			}
		})
	}
	t.Run("fresh-legacy-short-is-intentionally-unsupported", func(t *testing.T) {
		baseline, err := os.ReadFile(filepath.Join(directory, "pet-redemption-legacy-unredeemed-base.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "synthetic.sqlite3")
		if err := os.WriteFile(path, baseline, 0600); err != nil {
			t.Fatal(err)
		}
		store, err := openCompatibility(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		rust := decodeCheckinJSON(t, fixture.LegacyBoundary.RustOutput).(map[string]any)
		if rust["duplicate"] != false || reflect.DeepEqual(decodeCheckinJSON(t, fixture.LegacyBoundary.Before), decodeCheckinJSON(t, fixture.LegacyBoundary.RustPersisted)) {
			t.Fatal("old Rust did not redeem the real synthetic legacy code")
		}
		before := redemptionPersistence(t, store, fixture.Queries)
		compareCheckinJSON(t, "fresh-legacy-baseline", fixture.LegacyBoundary.Before, before)
		if _, err := store.RedeemKey(ctx, fixture.LegacyBoundary.Code, "2026-11-18T12:00:00+00:00"); !errors.Is(err, domain.ErrPetRedemptionUnsupported) || err.Error() != domain.ErrPetRedemptionUnsupported.Error() {
			t.Fatal("new Go accepted legacy MAC code", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store, err = openCompatibility(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		compareCheckinJSON(t, "fresh-legacy-rejection-reopen", before, redemptionPersistence(t, store, fixture.Queries))
	})
	t.Run("synthetic-signer-does-not-replace-production-trust", func(t *testing.T) {
		baseline, err := os.ReadFile(filepath.Join(directory, "pet-redemption-signed-base.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "synthetic.sqlite3")
		if err := os.WriteFile(path, baseline, 0600); err != nil {
			t.Fatal(err)
		}
		store, err := openCompatibility(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		before := redemptionPersistence(t, store, fixture.Queries)
		code := ""
		for _, scenario := range fixture.Cases {
			if scenario.Name == "signed" {
				code = scenario.Steps[0].Input.Code
			}
		}
		if _, err := store.RedeemKey(ctx, code, "2026-11-18T12:00:00+00:00"); !errors.Is(err, domain.ErrPetRedemption) {
			t.Fatal("synthetic signer entered production trust", err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store, err = openCompatibility(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		compareCheckinJSON(t, "production-key-rejection-reopen", before, redemptionPersistence(t, store, fixture.Queries))
	})
	t.Logf("pet redemption differential: cases=%d steps=%d rejected=%d faults=%d tables=%d reopens=%d legacy-boundary=1", cases, steps, rejected, faults, len(fixture.Queries), reopens)
}

func TestPetRedemptionEntityBijectionRejectsDriftAndBusinessDifferences(t *testing.T) {
	snapshot := func(native bool) map[string]any {
		value := map[string]any{}
		for table := range checkinTableColumns {
			value[table] = []any{}
		}
		value["profile"] = []any{[]any{"default-pet", "Synthetic", 1, 0, "first_meet", "idle", "before", "after"}}
		id := "pet-redeem-1000-1"
		if native {
			id = "pet-redeem-" + strings.Repeat("a", 32)
		}
		value["redemptions"] = []any{[]any{id, "default-pet", strings.Repeat("0", 64), "LIB1-synthetic", "synthetic-campaign", `{"lp":1,"growthValue":0,"items":[]}`, "redeemed", "before", `{"nonce":"synthetic"}`}}
		return value
	}
	for _, field := range []struct {
		name        string
		column      int
		replacement any
	}{
		{"reward", 5, `{"lp":2,"growthValue":0,"items":[]}`},
		{"reward-json-text", 5, `{"lp":1.0,"growthValue":0,"items":[]}`},
		{"metadata", 8, `{"nonce":"other"}`},
		{"campaign", 4, "other-campaign"},
		{"status", 6, "other-status"},
		{"time", 7, "after"},
	} {
		t.Run(field.name, func(t *testing.T) {
			rust, native := snapshot(false), snapshot(true)
			native["redemptions"].([]any)[0].([]any)[field.column] = field.replacement
			ids := redemptionIDs{entities: newCheckinIDs()}
			if err := ids.bind(rust, native, false); err != nil {
				t.Fatal(err)
			}
			if err := ids.normalizeSnapshot(rust, 0); err != nil {
				t.Fatal(err)
			}
			if err := ids.normalizeSnapshot(native, 1); err != nil {
				t.Fatal(err)
			}
			if reflect.DeepEqual(rust, native) {
				t.Fatal("entity normalization hid business difference")
			}
		})
	}
	t.Run("persistent-ID-drift", func(t *testing.T) {
		rust, native := snapshot(false), snapshot(true)
		ids := redemptionIDs{entities: newCheckinIDs()}
		if err := ids.bind(rust, native, false); err != nil {
			t.Fatal(err)
		}
		native["redemptions"].([]any)[0].([]any)[0] = "pet-redeem-" + strings.Repeat("b", 32)
		if ids.bind(rust, native, false) == nil {
			t.Fatal("persisted redemption ID was remapped")
		}
	})
	t.Run("baseline-ID-cannot-remap", func(t *testing.T) {
		ids := redemptionIDs{entities: newCheckinIDs()}
		if ids.bind(snapshot(false), snapshot(true), true) == nil {
			t.Fatal("baseline ID remapped")
		}
	})
	t.Run("dangling-API-ID", func(t *testing.T) {
		ids := redemptionIDs{entities: newCheckinIDs()}
		if err := ids.bind(snapshot(false), snapshot(true), false); err != nil {
			t.Fatal(err)
		}
		if ids.normalizeAPI(map[string]any{"id": "pet-redeem-" + strings.Repeat("b", 32), "keyHash": strings.Repeat("0", 64), "rewardJson": "{}"}, 1) == nil {
			t.Fatal("unpersisted API identity accepted")
		}
	})
	t.Run("duplicate-entity-ID", func(t *testing.T) {
		rust, native := snapshot(false), snapshot(true)
		for side, value := range []map[string]any{rust, native} {
			row := append([]any(nil), value["redemptions"].([]any)[0].([]any)...)
			row[2] = strings.Repeat("1", 64)
			if side == 0 {
				row[0] = "pet-redeem-1000-2"
			}
			value["redemptions"] = append(value["redemptions"].([]any), row)
		}
		ids := redemptionIDs{entities: newCheckinIDs()}
		if ids.bind(rust, native, false) == nil {
			t.Fatal("duplicate entity ID accepted")
		}
	})
	t.Run("row-order-retained", func(t *testing.T) {
		rust, native := snapshot(false), snapshot(true)
		for side, value := range []map[string]any{rust, native} {
			row := append([]any(nil), value["redemptions"].([]any)[0].([]any)...)
			row[2] = strings.Repeat("1", 64)
			row[0] = "pet-redeem-1000-2"
			if side == 1 {
				row[0] = "pet-redeem-" + strings.Repeat("b", 32)
			}
			value["redemptions"] = append(value["redemptions"].([]any), row)
		}
		native["redemptions"].([]any)[0], native["redemptions"].([]any)[1] = native["redemptions"].([]any)[1], native["redemptions"].([]any)[0]
		ids := redemptionIDs{entities: newCheckinIDs()}
		if err := ids.bind(rust, native, false); err != nil {
			t.Fatal(err)
		}
		if err := ids.normalizeSnapshot(rust, 0); err != nil {
			t.Fatal(err)
		}
		if err := ids.normalizeSnapshot(native, 1); err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(rust, native) {
			t.Fatal("row order was hidden")
		}
	})
}
