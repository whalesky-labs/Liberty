package profiletransfer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

type testVault struct {
	values                    map[string]domain.Secret
	failedWrite, failedDelete bool
	failedDeleteReference     string
	writes, deletes           []string
}

func (v *testVault) Get(_ context.Context, key string) (domain.Secret, error) {
	return v.values[key], nil
}
func (v *testVault) Set(_ context.Context, key string, secret domain.Secret) error {
	v.values[key] = secret
	v.writes = append(v.writes, key)
	if v.failedWrite {
		return domain.ErrCredentialStore
	}
	return nil
}
func (v *testVault) Delete(_ context.Context, key string) error {
	v.deletes = append(v.deletes, key)
	if v.failedDelete || key == v.failedDeleteReference {
		return domain.ErrCredentialStore
	}
	delete(v.values, key)
	return nil
}

func syntheticSource(t *testing.T) (string, *sql.DB, *testVault) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "legacy")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(directory, profile.DatabaseName)
	store, _, err := jobdb.OpenPrepared(t.Context(), database, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// This connection intentionally bypasses the new profile lock, like an
	// already distributed legacy client. Keep WAL open throughout import.
	db, err := sql.Open("sqlite", schema.URI(database, url.Values{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;
INSERT INTO meeting_members(id,name,sort_order,created_at,updated_at) VALUES('before','Synthetic committed WAL',1,'now','now');
INSERT INTO app_settings(id,theme_mode,liquid_glass_style,accent_color,locale,backend_url,api_token,api_token_ref,default_hotwords,summary_template,python_path,runner_script_path)
VALUES(1,'auto','default','blue','en-US','','','legacy-token','','','','');
INSERT INTO credential_cleanup_intents(id,model_id,credential_reference,operation,attempt,created_at,updated_at) VALUES('old-intent','','legacy-retired','retire',0,'now','now');`); err != nil {
		t.Fatal(err)
	}
	vault := &testVault{values: map[string]domain.Secret{"legacy-token": domain.NewSecret("synthetic-token-value"), "legacy-retired": domain.NewSecret("synthetic-retained-value")}}
	return directory, db, vault
}

func readString(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(query).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func openDatabase(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", schema.URI(name, url.Values{"mode": {"rw"}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestImportConsistentWALSnapshotSeparatesActiveDataAndCredentialOwnership(t *testing.T) {
	source, legacy, vault := syntheticSource(t)
	target := filepath.Join(t.TempDir(), "native-active")
	mainBefore, err := os.ReadFile(filepath.Join(source, profile.DatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Import(t.Context(), source, target, vault)
	if err != nil || first.AlreadyImported || first.Version != 13 || first.CredentialCount != 1 {
		t.Fatal("snapshot import failed", err)
	}
	mainAfter, err := os.ReadFile(filepath.Join(source, profile.DatabaseName))
	if err != nil || !bytes.Equal(mainBefore, mainAfter) {
		t.Fatal("import modified source main database", err)
	}
	native := openDatabase(t, filepath.Join(target, profile.DatabaseName))
	if readString(t, native, "SELECT name FROM meeting_members WHERE id='before'") != "Synthetic committed WAL" {
		t.Fatal("committed source WAL was omitted")
	}
	newReference := readString(t, native, "SELECT api_token_ref FROM app_settings WHERE id=1")
	if newReference == "legacy-token" || !strings.HasPrefix(newReference, "liberty:native-import:") || vault.values[newReference].Reveal() != vault.values["legacy-token"].Reveal() {
		t.Fatal("source credential account was reused")
	}
	if readString(t, native, "SELECT COUNT(*) FROM credential_cleanup_intents") != "0" || readString(t, legacy, "SELECT api_token_ref FROM app_settings WHERE id=1") != "legacy-token" || readString(t, legacy, "SELECT COUNT(*) FROM credential_cleanup_intents") != "1" {
		t.Fatal("source cleanup ownership crossed profiles")
	}
	if _, err := legacy.Exec("INSERT INTO meeting_members(id,name,sort_order,created_at,updated_at) VALUES('old-later','Old client later',2,'now','now')"); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec("INSERT INTO meeting_members(id,name,sort_order,created_at,updated_at) VALUES('new-later','Native later',3,'now','now')"); err != nil {
		t.Fatal(err)
	}
	second, err := Import(t.Context(), source, target, vault)
	if err != nil || !second.AlreadyImported || len(vault.writes) != 1 {
		t.Fatal("repeat import reset live data or copied secrets twice", err)
	}
	if readString(t, native, "SELECT COUNT(*) FROM meeting_members WHERE id='old-later'") != "0" || readString(t, legacy, "SELECT COUNT(*) FROM meeting_members WHERE id='new-later'") != "0" || readString(t, native, "SELECT COUNT(*) FROM meeting_members WHERE id='new-later'") != "1" {
		t.Fatal("unlocked legacy writes reached Native active database")
	}
	for _, reference := range vault.deletes {
		if strings.HasPrefix(reference, "legacy-") {
			t.Fatal("import deleted a source credential")
		}
	}
	stateBytes, err := os.ReadFile(filepath.Join(target, stateName))
	if err != nil || bytes.Contains(stateBytes, []byte(source)) || bytes.Contains(stateBytes, []byte("synthetic-token-value")) || bytes.Contains(stateBytes, []byte("legacy-token")) {
		t.Fatal("import evidence contains source data or secrets", err)
	}
}

func TestImportAmbiguousVaultFailureRetainsIntentAndRetriesWithoutPublishing(t *testing.T) {
	source, legacy, vault := syntheticSource(t)
	target := filepath.Join(t.TempDir(), "native-active")
	vault.failedWrite, vault.failedDelete = true, true
	if _, err := Import(t.Context(), source, target, vault); !errors.Is(err, ErrImport) {
		t.Fatal("ambiguous write unexpectedly published", err)
	}
	if _, err := os.Stat(filepath.Join(target, profile.DatabaseName)); !os.IsNotExist(err) {
		t.Fatal("failed import published active database", err)
	}
	if len(vault.writes) != 1 {
		t.Fatal("missing synthetic ambiguous credential write")
	}
	owned := vault.writes[0]
	data, err := os.ReadFile(filepath.Join(target, stateName))
	if err != nil || !bytes.Contains(data, []byte(owned)) {
		t.Fatal("failed compensation lost durable ownership evidence", err)
	}
	vault.failedWrite, vault.failedDelete = false, false
	result, err := Import(t.Context(), source, target, vault)
	if err != nil || result.AlreadyImported {
		t.Fatal("failed import could not retry", err)
	}
	if _, exists := vault.values[owned]; exists || !slices.Contains(vault.deletes, owned) {
		t.Fatal("ambiguous write was not compensated")
	}
	if vault.values["legacy-token"].Empty() || vault.values["legacy-retired"].Empty() || readString(t, legacy, "SELECT COUNT(*) FROM meeting_members") != "1" {
		t.Fatal("failure or retry changed source data/credentials")
	}
}

func TestImportReadyUnpublishedCompensationFailureCanRetryWithoutTouchingSourceReferences(t *testing.T) {
	source, legacy, vault := syntheticSource(t)
	if _, err := legacy.Exec(`INSERT INTO ai_model_configs(id,name,base_url,model,api_key,api_key_ref,created_at,updated_at)
VALUES('synthetic-model','Synthetic','https://synthetic.invalid','synthetic','','legacy-model','fixture','fixture')`); err != nil {
		t.Fatal(err)
	}
	vault.values["legacy-model"] = domain.NewSecret("synthetic-model-value")
	sourceCredentials := map[string]domain.Secret{
		"legacy-token":   vault.values["legacy-token"],
		"legacy-model":   vault.values["legacy-model"],
		"legacy-retired": vault.values["legacy-retired"],
	}
	target := filepath.Join(t.TempDir(), "native-active")
	if _, err := Import(t.Context(), source, target, vault); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, stateName))
	var state importState
	if err != nil || json.Unmarshal(data, &state) != nil || len(state.References) != 2 || len(state.Live) != 2 {
		t.Fatal("missing completed import fixture", err)
	}
	oldReferences := slices.Clone(state.References)
	// Reconstruct the durable boundary after ready is persisted but before the
	// publication link exists, as after a crash or an unsuccessful os.Link.
	database := filepath.Join(target, profile.DatabaseName)
	stage := filepath.Join(target, stageName)
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(database, filepath.Join(stage, profile.DatabaseName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(database); err != nil {
		t.Fatal(err)
	}
	state.Phase = "ready"
	if err := writeState(target, state); err != nil {
		t.Fatal(err)
	}
	vault.failedDeleteReference = oldReferences[1]
	if _, err := Import(t.Context(), source, target, vault); !errors.Is(err, ErrImport) {
		t.Fatal("partial compensation unexpectedly published", err)
	}
	if _, err := os.Stat(database); !os.IsNotExist(err) {
		t.Fatal("partial compensation published an active database", err)
	}
	if !vault.values[oldReferences[0]].Empty() || vault.values[oldReferences[1]].Empty() {
		t.Fatal("fixture did not stop after one successful credential deletion")
	}
	interrupted, err := readState(target, state.Source)
	if err != nil || !slices.Equal(interrupted.References, oldReferences[1:]) || !slices.Equal(interrupted.Live, oldReferences[1:]) {
		t.Fatal("partial compensation persisted an unreadable live credential set", err)
	}
	vault.failedDeleteReference = ""
	result, err := Import(t.Context(), source, target, vault)
	if err != nil || result.AlreadyImported || result.CredentialCount != 2 || schema.VerifyTransferredProfile(t.Context(), database, vault) != nil {
		t.Fatal("ready import could not recover from interrupted compensation", err)
	}
	completed, err := readState(target, state.Source)
	if err != nil || completed.Phase != "complete" || len(completed.Live) != 2 {
		t.Fatal("retry did not complete its durable state", err)
	}
	for _, reference := range oldReferences {
		if !vault.values[reference].Empty() || slices.Contains(completed.References, reference) || slices.Contains(completed.Live, reference) {
			t.Fatal("retry retained a compensated credential")
		}
	}
	for reference, secret := range sourceCredentials {
		if vault.values[reference].Reveal() != secret.Reveal() || slices.Contains(vault.deletes, reference) {
			t.Fatal("compensation touched a source credential")
		}
	}
	if readString(t, legacy, "SELECT api_token_ref FROM app_settings WHERE id=1") != "legacy-token" || readString(t, legacy, "SELECT api_key_ref FROM ai_model_configs WHERE id='synthetic-model'") != "legacy-model" || readString(t, legacy, "SELECT COUNT(*) FROM credential_cleanup_intents") != "1" {
		t.Fatal("retry changed source credential ownership")
	}
}

func TestImportRejectsExistingForeignLinkedAndOverlappingTargets(t *testing.T) {
	source, _, vault := syntheticSource(t)
	root := t.TempDir()
	foreign := filepath.Join(root, "foreign")
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{source, filepath.Join(source, "inside"), filepath.Dir(source), foreign} {
		if _, err := Import(t.Context(), source, target, vault); err == nil {
			t.Fatal("unsafe target accepted")
		}
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(source, linked); err == nil {
		if _, err := Import(t.Context(), source, linked, vault); err == nil {
			t.Fatal("source alias accepted")
		}
	}
	if len(vault.writes) != 0 || len(vault.deletes) != 0 {
		t.Fatal("preflight rejection accessed credentials")
	}
}

func TestImportedProfileRestoreRetainsNativeChangesWithoutTouchingLegacy(t *testing.T) {
	source, legacy, vault := syntheticSource(t)
	target := filepath.Join(t.TempDir(), "native-active")
	if _, err := Import(t.Context(), source, target, vault); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(target, profile.DatabaseName)
	store, handover, err := jobdb.OpenHandover(t.Context(), database, vault, false)
	if err != nil {
		t.Fatal(err)
	}
	resources := application.NewResources(store, vault)
	settings, err := resources.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	oldNativeReference := vault.writes[0]
	if _, _, err := resources.SaveSettings(t.Context(), settings, domain.CredentialUpdate{Action: domain.SetCredential, Value: domain.NewSecret("synthetic-native-rotated-token")}); err != nil {
		t.Fatal(err)
	}
	if vault.values[oldNativeReference].Empty() || vault.values["legacy-token"].Empty() || vault.values["legacy-retired"].Empty() {
		t.Fatal("rotation reclaimed source or snapshot-retained Native references")
	}
	if err := store.SaveMember(t.Context(), domain.MeetingMember{ID: "after", Name: "Native after import", SortOrder: 2}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	owner, err := profile.Acquire(target)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	result, err := schema.RestoreLocked(t.Context(), database, filepath.Base(handover.Backup), vault)
	if err != nil || result.SafetyBackup == "" {
		t.Fatal("imported restore failed", err)
	}
	native := openDatabase(t, database)
	if readString(t, native, "SELECT COUNT(*) FROM meeting_members WHERE id='after'") != "0" {
		t.Fatal("restore did not select imported recovery point")
	}
	if readString(t, native, "SELECT api_token_ref FROM app_settings WHERE id=1") != oldNativeReference {
		t.Fatal("restore lost the imported credential recovery point")
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := schema.RestoreLocked(t.Context(), database, filepath.Base(result.SafetyBackup), vault); err != nil {
		t.Fatal(err)
	}
	native = openDatabase(t, database)
	if readString(t, native, "SELECT COUNT(*) FROM meeting_members WHERE id='after'") != "1" || readString(t, legacy, "SELECT COUNT(*) FROM meeting_members WHERE id='after'") != "0" {
		t.Fatal("recovery lost Native changes or modified legacy source")
	}
	newReference := readString(t, native, "SELECT api_token_ref FROM app_settings WHERE id=1")
	if newReference == oldNativeReference || vault.values[newReference].Reveal() != "synthetic-native-rotated-token" || vault.values["legacy-token"].Empty() {
		t.Fatal("safety restore lost rotated Native credentials or changed legacy ownership")
	}
}

func TestImportRejectsActiveLegacyOwnershipBeforeCredentialWritesAndCanRetry(t *testing.T) {
	for _, activity := range []string{
		"INSERT INTO job_runs(job_id,status,pid,process_identity) VALUES('synthetic-running','running',123,'synthetic-identity')",
		"INSERT INTO job_deletion_ops(operation_id,job_id,trash_name,phase,created_at_ms,updated_at_ms) VALUES('synthetic-delete','synthetic-job','synthetic-trash','prepared',1,1)",
		"INSERT INTO runtime_component_state(platform_id,component,source,operation_kind,updated_at) VALUES('synthetic-platform','python','managed','installing','now')",
		"INSERT INTO live_sessions(id,state,source_language,target_language,translate_enabled,created_at,updated_at) VALUES('synthetic-live','paused','zh','en',0,'now','now')",
	} {
		t.Run(strings.Fields(activity)[2], func(t *testing.T) {
			source, legacy, vault := syntheticSource(t)
			target := filepath.Join(t.TempDir(), "native-active")
			if _, err := legacy.Exec(activity); err != nil {
				t.Fatal(err)
			}
			if _, err := Import(t.Context(), source, target, vault); !errors.Is(err, schema.ErrProfileTransferBusy) {
				t.Fatal("active legacy work was imported", err)
			}
			if len(vault.writes) != 0 || len(vault.deletes) != 0 {
				t.Fatal("active snapshot touched system credentials")
			}
			if _, err := os.Stat(filepath.Join(target, profile.DatabaseName)); !os.IsNotExist(err) {
				t.Fatal("active snapshot became a Native database", err)
			}
			if _, err := legacy.Exec("DELETE FROM job_runs; DELETE FROM job_deletion_ops; DELETE FROM runtime_component_state; DELETE FROM live_sessions"); err != nil {
				t.Fatal(err)
			}
			if _, err := Import(t.Context(), source, target, vault); err != nil {
				t.Fatal("quiescent retry failed", err)
			}
		})
	}
}

func TestImportAllowsIdleAndFailedRuntimeOperationsWithoutChangingSource(t *testing.T) {
	for _, kind := range []string{"idle", "failed"} {
		t.Run(kind, func(t *testing.T) {
			source, legacy, vault := syntheticSource(t)
			if (domain.RuntimeOperation{Kind: kind}).Active() {
				t.Fatal("fixture must represent a terminal runtime operation")
			}
			if _, err := legacy.Exec(`INSERT INTO runtime_component_state(platform_id,component,source,operation_kind,updated_at)
VALUES('synthetic-platform','python','managed',?,'now')`, kind); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "native-active")
			result, err := Import(t.Context(), source, target, vault)
			if err != nil || result.AlreadyImported || result.CredentialCount != 1 {
				t.Fatal("terminal runtime state blocked a quiescent import", err)
			}
			native := openDatabase(t, filepath.Join(target, profile.DatabaseName))
			query := "SELECT operation_kind FROM runtime_component_state WHERE platform_id='synthetic-platform'"
			if readString(t, native, query) != kind || readString(t, legacy, query) != kind {
				t.Fatal("import changed a terminal runtime operation")
			}
			if vault.values["legacy-token"].Empty() || vault.values["legacy-retired"].Empty() || len(vault.deletes) != 0 {
				t.Fatal("terminal runtime import changed source credential ownership")
			}
		})
	}
}

func TestImportRejectsEveryActiveAndUnknownRuntimeOperationBeforeCredentialWrites(t *testing.T) {
	for _, test := range []struct {
		kind string
		err  error
	}{
		{"detecting", schema.ErrProfileTransferBusy},
		{"waiting_for_python", schema.ErrProfileTransferBusy},
		{"downloading", schema.ErrProfileTransferBusy},
		{"installing", schema.ErrProfileTransferBusy},
		{"validating", schema.ErrProfileTransferBusy},
		{"unknown-operation", ErrImport},
		{"", ErrImport},
	} {
		t.Run(test.kind, func(t *testing.T) {
			source, legacy, vault := syntheticSource(t)
			if (domain.RuntimeOperation{Kind: test.kind}).Active() != errors.Is(test.err, schema.ErrProfileTransferBusy) {
				t.Fatal("snapshot activity classification differs from the domain")
			}
			if _, err := legacy.Exec(`INSERT INTO runtime_component_state(platform_id,component,source,operation_kind,updated_at)
VALUES('synthetic-platform','python','managed',?,'now')`, test.kind); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "native-active")
			if _, err := Import(t.Context(), source, target, vault); !errors.Is(err, test.err) {
				t.Fatal("runtime operation was not rejected with its activity classification", err)
			}
			if len(vault.writes) != 0 || len(vault.deletes) != 0 {
				t.Fatal("runtime preflight rejection touched credentials")
			}
			if _, err := os.Stat(filepath.Join(target, profile.DatabaseName)); !os.IsNotExist(err) {
				t.Fatal("rejected runtime state published a Native database", err)
			}
		})
	}
}
