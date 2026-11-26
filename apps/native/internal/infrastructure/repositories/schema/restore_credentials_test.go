package schema

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func restoreCredentialRecords(t *testing.T, path string) []restoreCredentialRecord {
	t.Helper()
	records, err := readRestoreCredentialJournals(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func TestRestoreCredentialReferenceBoundsMatchSystemVault(t *testing.T) {
	for _, test := range []struct {
		name      string
		reference string
		valid     bool
	}{
		{name: "ordinary", reference: "synthetic-reference", valid: true},
		{name: "unicode", reference: "synthetic-资料", valid: true},
		{name: "max-bytes", reference: strings.Repeat("a", 2047), valid: true},
		{name: "max-unicode-bytes", reference: strings.Repeat("资", 682) + "a", valid: true},
		{name: "empty"},
		{name: "nul", reference: "synthetic\x00reference"},
		{name: "cr", reference: "synthetic\rreference"},
		{name: "lf", reference: "synthetic\nreference"},
		{name: "too-long", reference: strings.Repeat("a", 2048)},
		{name: "unicode-too-long", reference: strings.Repeat("资", 683)},
		{name: "invalid-utf8", reference: "synthetic\xffreference"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if validRestoreCredentialReference(test.reference) != test.valid {
				t.Fatal("retirement reference validation differs from the system-vault boundary")
			}
			path := filepath.Join(t.TempDir(), "synthetic.sqlite3")
			err := writeRestoreCredentialJournal(context.Background(), path, []string{test.reference})
			if test.valid && err != nil {
				t.Fatal("valid reference could not be privately published")
			}
			if !test.valid && !errors.Is(err, ErrRestore) {
				t.Fatal("invalid reference was published")
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal("synthetic journal directory could not be inspected")
			}
			if !test.valid && len(entries) != 0 {
				t.Fatal("rejected reference left a temporary or final journal")
			}
		})
	}
}

func TestRestoreCredentialCapturePreservesLivePendingAndMigrationReferences(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "unpublished", true: "published"}[published], func(t *testing.T) {
			ctx := context.Background()
			db, path := database(t)
			if _, err := Apply(ctx, db, path, nil); err != nil {
				t.Fatal(err)
			}
			exec(t, db, `INSERT INTO ai_model_configs(id,name,base_url,model,api_key,api_key_ref,created_at,updated_at) VALUES('synthetic','Synthetic','https://synthetic.invalid','synthetic','','live-model','fixture','fixture');
 INSERT INTO app_settings(id,theme_mode,liquid_glass_style,accent_color,locale,backend_url,api_token,api_token_ref,default_hotwords,summary_template,python_path,runner_script_path)
 VALUES(1,'auto','transparent','#2f6dff','zh-CN','','','live-token','','','','');
 INSERT INTO credential_cleanup_intents VALUES('pending','synthetic','pending-reference','rollback',7,'synthetic','before','after');`)
			encoded, err := json.Marshal(credentialJournal{Published: published, Entries: []credentialEntry{{Reference: "migration-staged", Previous: "migration-previous"}, {Reference: "live-model"}}})
			if err != nil {
				t.Fatal(err)
			}
			exec(t, db, "INSERT INTO app_meta(key,value) VALUES(?,?)", journalKey, string(encoded))
			connection, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			references, err := restoreCredentialReferences(ctx, connection, 10)
			connection.Close()
			want := []string{"live-model", "live-token", "migration-previous", "migration-staged", "pending-reference"}
			if err != nil || !reflect.DeepEqual(references, want) {
				t.Fatal("restore capture lost a live or pending reference", references, err)
			}
			for range 2 {
				if err := writeRestoreCredentialJournal(ctx, path, references); err != nil {
					t.Fatal(err)
				}
			}
			records := restoreCredentialRecords(t, path)
			if len(records) != 2 || records[0].path == records[1].path {
				t.Fatal("multiple recovery attempts replaced an earlier journal")
			}
			for _, record := range records {
				if !reflect.DeepEqual(record.journal.References, want) || record.journal.Database != filepath.Base(path) || record.journal.Version != 1 || (runtime.GOOS != "windows" && record.info.Mode().Perm() != 0600) {
					t.Fatal("unexpected private journal contents or permissions")
				}
			}
			if err := RecoverRestoreCredentials(ctx, db, path); err != nil {
				t.Fatal(err)
			}
			if len(restoreCredentialRecords(t, path)) != 0 || scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents") != "3" || scalar(t, db, "SELECT operation||'|'||attempt||'|'||last_error||'|'||created_at||'|'||updated_at FROM credential_cleanup_intents WHERE credential_reference='pending-reference'") != "rollback|7|synthetic|before|after" {
				t.Fatal("import retired a live reference or overwrote an existing intent")
			}
			if scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents WHERE credential_reference IN ('migration-staged','migration-previous') AND operation='retire' AND attempt=0 AND last_error IS NULL AND created_at=updated_at") != "2" {
				t.Fatal("migration cleanup references were not imported precisely")
			}
		})
	}
}

func TestRestoreCredentialCaptureAcceptsCompatibleEmptyMigrationJournal(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	for _, entries := range []string{"null", "[]"} {
		exec(t, db, "INSERT INTO app_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", journalKey, `{"published":false,"entries":`+entries+`}`)
		connection, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		references, err := restoreCredentialReferences(ctx, connection, 10)
		connection.Close()
		if err != nil || references == nil || len(references) != 0 {
			t.Fatal("compatible empty migration journal prevented retirement capture", err)
		}
	}
}

func TestRestoreCredentialImportFailureIsAtomicAndRetryIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"first", "second"} {
		if err := writeRestoreCredentialJournal(ctx, path, []string{reference}); err != nil {
			t.Fatal(err)
		}
	}
	records := restoreCredentialRecords(t, path)
	exec(t, db, `CREATE TRIGGER reject_restore_intent BEFORE INSERT ON credential_cleanup_intents WHEN NEW.credential_reference='second' BEGIN SELECT RAISE(ABORT,'synthetic import failure'); END;`)
	if err := RecoverRestoreCredentials(ctx, db, path); !errors.Is(err, ErrRestore) {
		t.Fatal("import error was not reported", err)
	}
	if scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents") != "0" || len(restoreCredentialRecords(t, path)) != 2 {
		t.Fatal("failed transaction partially imported or deleted its recovery journals")
	}
	exec(t, db, "DROP TRIGGER reject_restore_intent")
	if err := RecoverRestoreCredentials(ctx, db, path); err != nil {
		t.Fatal(err)
	}
	if len(restoreCredentialRecords(t, path)) != 0 || scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents") != "2" {
		t.Fatal("retry did not persist all references before removing journals")
	}
	exec(t, db, "UPDATE credential_cleanup_intents SET attempt=9,last_error='synthetic',created_at='original',updated_at='later'")
	for _, record := range records {
		if err := os.WriteFile(record.path, record.data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := RecoverRestoreCredentials(ctx, db, path); err != nil {
			t.Fatal(err)
		}
	}
	if scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents WHERE attempt=9 AND last_error='synthetic' AND created_at='original' AND updated_at='later'") != "2" {
		t.Fatal("crash replay overwrote existing cleanup intent metadata")
	}
}

func TestRestoreCredentialReplayCheckpointsExistingNormalWALIntent(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, "PRAGMA wal_autocheckpoint=0; PRAGMA busy_timeout=0; PRAGMA synchronous=NORMAL")
	exec(t, db, "PRAGMA wal_checkpoint(TRUNCATE)")
	reader, err := sql.Open("sqlite", URI(path, url.Values{"mode": {"ro"}}))
	if err != nil {
		t.Fatal("could not open the synthetic WAL reader")
	}
	defer reader.Close()
	snapshot, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal("could not pin the synthetic WAL snapshot")
	}
	defer snapshot.Rollback()
	const countIntents = "SELECT COUNT(*) FROM credential_cleanup_intents"
	var count int
	if snapshot.QueryRowContext(ctx, countIntents).Scan(&count) != nil || count != 0 {
		t.Fatal("reader did not pin a snapshot before the intent was inserted")
	}
	const reference = "synthetic-normal-wal-reference"
	exec(t, db, `INSERT INTO credential_cleanup_intents VALUES('kept','owner',?,'rollback',7,'synthetic','before','after')`, reference)
	const metadata = "SELECT id||'|'||model_id||'|'||operation||'|'||attempt||'|'||last_error||'|'||created_at||'|'||updated_at FROM credential_cleanup_intents WHERE credential_reference=?"
	const want = "kept|owner|rollback|7|synthetic|before|after"
	readMainOnly := func(query string, args ...any) string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("could not read synthetic main database")
		}
		copyPath := filepath.Join(t.TempDir(), "main.sqlite3")
		if os.WriteFile(copyPath, data, 0600) != nil {
			t.Fatal("could not create synthetic main-only copy")
		}
		copyDB, err := sql.Open("sqlite", URI(copyPath, url.Values{"mode": {"ro"}, "immutable": {"1"}}))
		if err != nil {
			t.Fatal("could not open synthetic main-only copy")
		}
		defer copyDB.Close()
		return scalar(t, copyDB, query, args...)
	}
	if readMainOnly(countIntents) != "0" || scalar(t, db, metadata, reference) != want {
		t.Fatal("existing intent was not isolated to committed NORMAL WAL")
	}
	if err := writeRestoreCredentialJournal(ctx, path, []string{reference}); err != nil {
		t.Fatal(err)
	}
	changes := scalar(t, db, "SELECT total_changes()")
	if err := RecoverRestoreCredentials(ctx, db, path); !errors.Is(err, ErrRestore) {
		t.Fatal("a busy checkpoint must defer journal removal", err)
	}
	if len(restoreCredentialRecords(t, path)) != 1 || scalar(t, db, metadata, reference) != want || scalar(t, db, "SELECT total_changes()") != changes {
		t.Fatal("busy checkpoint changed an existing intent or removed its recovery journal")
	}
	if err := snapshot.Rollback(); err != nil {
		t.Fatal("could not release the synthetic WAL pin")
	}
	if err := RecoverRestoreCredentials(ctx, db, path); err != nil {
		t.Fatal(err)
	}
	if len(restoreCredentialRecords(t, path)) != 0 || readMainOnly(metadata, reference) != want || scalar(t, db, "SELECT total_changes()") != changes {
		t.Fatal("no-op replay did not place the unmodified intent in the main database before removing the journal")
	}
}

func TestRestoreCredentialJournalSurvivesPostRestoreImportFailure(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `CREATE TRIGGER reject_restore_intent BEFORE INSERT ON credential_cleanup_intents BEGIN SELECT RAISE(ABORT,'synthetic restore import failure'); END;`)
	result, err := ApplyHandover(ctx, db, path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, `DROP TRIGGER reject_restore_intent;
 INSERT INTO ai_model_configs(id,name,base_url,model,api_key,api_key_ref,created_at,updated_at) VALUES('synthetic','Synthetic','https://synthetic.invalid','synthetic','','new-model','fixture','fixture');
 INSERT INTO credential_cleanup_intents VALUES('pending','synthetic','pending-reference','retire',0,NULL,'before','before');
 INSERT INTO app_meta(key,value) VALUES('native_credential_migration_v3','{"published":true,"entries":[{"reference":"migration-staged","previous":"migration-previous"}]}');`)
	restored, err := RestoreLocked(ctx, path, filepath.Base(result.Backup), nil)
	if !errors.Is(err, ErrRestore) || restored.SafetyBackup == "" {
		t.Fatal("post-restore import failure did not retain the safety backup", err)
	}
	records := restoreCredentialRecords(t, path)
	want := []string{"migration-previous", "migration-staged", "new-model", "pending-reference"}
	if len(records) != 1 || !reflect.DeepEqual(records[0].journal.References, want) || scalar(t, db, "SELECT COUNT(*) FROM ai_model_configs") != "0" || scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents") != "0" {
		t.Fatal("failed import lost pre-restore references or partially published cleanup intents")
	}
	if err := os.Remove(restored.SafetyBackup); err != nil {
		t.Fatal(err)
	}
	exec(t, db, "DROP TRIGGER reject_restore_intent")
	if err := RecoverRestoreCredentials(ctx, db, path); err != nil {
		t.Fatal(err)
	}
	if scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents") != "4" || len(restoreCredentialRecords(t, path)) != 0 {
		t.Fatal("deleted safety backup caused a retirement reference to be lost on retry")
	}
}

func TestRestoreCredentialPublicationFailurePrecedesRestoreAndCanRetry(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-publication", true: "uncertain-publication"}[published], func(t *testing.T) {
			ctx := context.Background()
			db, path := database(t)
			if _, err := Apply(ctx, db, path, nil); err != nil {
				t.Fatal(err)
			}
			handover, err := ApplyHandover(ctx, db, path, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			exec(t, db, `INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('current','Keep until publication','fixture','fixture');
 INSERT INTO ai_model_configs(id,name,base_url,model,api_key,api_key_ref,created_at,updated_at) VALUES('synthetic','Synthetic','https://synthetic.invalid','synthetic','','post-snapshot-model','fixture','fixture')`)
			called := false
			result, err := restoreLocked(ctx, path, filepath.Base(handover.Backup), nil, func(ctx context.Context, database string, references []string) error {
				called = true
				if !reflect.DeepEqual(references, []string{"post-snapshot-model"}) {
					t.Fatal("publication did not capture the current live reference")
				}
				entries, err := os.ReadDir(filepath.Dir(database))
				if err != nil {
					t.Fatal("synthetic profile directory could not be inspected")
				}
				verified := false
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), filepath.Base(database)+".pre-restore-") && strings.HasSuffix(entry.Name(), ".bak") {
						if verifyBackup(ctx, filepath.Join(filepath.Dir(database), entry.Name()), CurrentVersion) != nil {
							t.Fatal("journal publication preceded a verified safety backup")
						}
						verified = true
					}
				}
				if !verified {
					t.Fatal("journal publication preceded safety backup creation")
				}
				if published {
					if err := writeRestoreCredentialJournal(ctx, database, references); err != nil {
						t.Fatal(err)
					}
				}
				return ErrRestore
			})
			if !called || !errors.Is(err, ErrRestore) || result.SafetyBackup == "" || scalar(t, db, "SELECT name FROM meeting_members WHERE id='current'") != "Keep until publication" || scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents") != "0" {
				t.Fatal("uncertain journal durability did not stop before database restoration")
			}
			wantRecords := 0
			if published {
				wantRecords = 1
			}
			if len(restoreCredentialRecords(t, path)) != wantRecords {
				t.Fatal("uncertain publication discarded an independently published journal")
			}
			if _, err := RestoreLocked(ctx, path, filepath.Base(handover.Backup), nil); err != nil {
				t.Fatal(err)
			}
			if scalar(t, db, "SELECT COUNT(*) FROM meeting_members WHERE id='current'") != "0" || scalar(t, db, "SELECT credential_reference FROM credential_cleanup_intents") != "post-snapshot-model" || len(restoreCredentialRecords(t, path)) != 0 {
				t.Fatal("publication retry lost an intent or failed to restore the selected snapshot")
			}
		})
	}
}

func TestRestoreCredentialJournalRemovalFailureRetainsCommittedIntentsForRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permission failure scenario")
	}
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeRestoreCredentialJournal(ctx, path, []string{"retirement-reference"}); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(path)
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal("synthetic profile permissions could not be changed")
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0700) })
	err := RecoverRestoreCredentials(ctx, db, path)
	if chmodErr := os.Chmod(directory, 0700); chmodErr != nil {
		t.Fatal("synthetic profile permissions could not be restored")
	}
	if !errors.Is(err, ErrRestore) || len(restoreCredentialRecords(t, path)) != 1 || scalar(t, db, "SELECT credential_reference FROM credential_cleanup_intents") != "retirement-reference" {
		t.Fatal("journal removal failure reverted the committed intent or discarded the side journal")
	}
	if err := RecoverRestoreCredentials(ctx, db, path); err != nil {
		t.Fatal(err)
	}
	if len(restoreCredentialRecords(t, path)) != 0 || scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents") != "1" {
		t.Fatal("journal removal retry did not preserve a single committed intent")
	}
}

func TestRestoreCredentialJournalSurvivesRestoreCancellation(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	handover, err := ApplyHandover(ctx, db, path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO ai_model_configs(id,name,base_url,model,api_key,api_key_ref,created_at,updated_at) VALUES('synthetic','Synthetic','https://synthetic.invalid','synthetic','','post-snapshot-model','fixture','fixture')`)
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	result, err := restoreLocked(canceled, path, filepath.Base(handover.Backup), nil, func(ctx context.Context, database string, references []string) error {
		if err := writeRestoreCredentialJournal(ctx, database, references); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, ErrRestore) || result.SafetyBackup == "" || len(restoreCredentialRecords(t, path)) != 1 {
		t.Fatal("canceled restore discarded its journal or safety backup")
	}
	if verifyBackup(ctx, result.SafetyBackup, CurrentVersion) != nil {
		t.Fatal("canceled restore did not retain a verified safety backup")
	}
	if _, err := RestoreLocked(ctx, path, filepath.Base(handover.Backup), nil); err != nil {
		t.Fatal(err)
	}
	if scalar(t, db, "SELECT credential_reference FROM credential_cleanup_intents") != "post-snapshot-model" || len(restoreCredentialRecords(t, path)) != 0 {
		t.Fatal("restore cancellation retry lost the post-snapshot retirement intent")
	}
}

func TestRestoreCredentialInvalidJournalBlocksRestoreBeforeSafetyBackup(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	handover, err := ApplyHandover(ctx, db, path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('current','Keep unchanged','fixture','fixture')`)
	journal := path + ".restore-credentials-" + strings.Repeat("b", 32) + ".json"
	if err := os.WriteFile(journal, []byte("invalid synthetic journal"), 0600); err != nil {
		t.Fatal("synthetic invalid journal could not be written")
	}
	result, err := RestoreLocked(ctx, path, filepath.Base(handover.Backup), nil)
	if !errors.Is(err, ErrRestore) || result.SafetyBackup != "" || scalar(t, db, "SELECT name FROM meeting_members WHERE id='current'") != "Keep unchanged" {
		t.Fatal("invalid side journal did not stop restore during preflight")
	}
}

func TestRestoreCredentialJournalDefersImportUntilOldSchemaIsUpgraded(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	v2(t, db)
	exec(t, db, "DELETE FROM ai_model_configs; UPDATE app_settings SET api_token='',api_token_ref=''")
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := backup(ctx, connection, path, 2, "pre-migration")
	connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO ai_model_configs(id,name,base_url,model,api_key,api_key_ref,created_at,updated_at) VALUES('synthetic','Synthetic','https://synthetic.invalid','synthetic','','new-model','fixture','fixture')`)
	restored, err := RestoreLocked(ctx, path, filepath.Base(snapshot), nil)
	if err != nil || restored.RestoredVersion != 2 || len(restoreCredentialRecords(t, path)) != 1 {
		t.Fatal("old-schema restore did not retain the side journal", err)
	}
	if err := RecoverRestoreCredentials(ctx, db, path); err != nil || len(restoreCredentialRecords(t, path)) != 1 {
		t.Fatal("old schema attempted unsupported intent writes", err)
	}
	if err := os.Remove(restored.SafetyBackup); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	if err := RecoverRestoreCredentials(ctx, db, path); err != nil {
		t.Fatal(err)
	}
	if scalar(t, db, "SELECT credential_reference||'|'||operation FROM credential_cleanup_intents") != "new-model|retire" || len(restoreCredentialRecords(t, path)) != 0 {
		t.Fatal("upgrade lost the pre-restore retirement reference")
	}
}

func TestRestoreCredentialJournalsRejectMalformedForeignAndLinkedRecords(t *testing.T) {
	cases := map[string]string{
		"truncated":      `{"version":1`,
		"version":        `{"version":2,"database":"DATABASE","references":["reference"]}`,
		"wrong-database": `{"version":1,"database":"foreign.sqlite3","references":["reference"]}`,
		"traversal":      `{"version":1,"database":"../DATABASE","references":["reference"]}`,
		"unknown":        `{"version":1,"database":"DATABASE","references":["reference"],"secret":"forbidden"}`,
		"duplicate":      `{"version":1,"database":"foreign","database":"DATABASE","references":["reference"]}`,
		"null":           `{"version":1,"database":"DATABASE","references":null}`,
		"null-entry":     `{"version":1,"database":"DATABASE","references":[null]}`,
		"duplicate-ref":  `{"version":1,"database":"DATABASE","references":["reference","reference"]}`,
		"unsorted":       `{"version":1,"database":"DATABASE","references":["second","first"]}`,
		"trailing":       `{"version":1,"database":"DATABASE","references":["reference"]}{}`,
		"invalid-utf8":   "{\"version\":1,\"database\":\"DATABASE\",\"references\":[\"\xff\"]}",
		"cr-reference":   `{"version":1,"database":"DATABASE","references":["synthetic\rreference"]}`,
		"lf-reference":   `{"version":1,"database":"DATABASE","references":["synthetic\nreference"]}`,
		"long-reference": `{"version":1,"database":"DATABASE","references":["` + strings.Repeat("a", 2048) + `"]}`,
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			db, path := database(t)
			if _, err := Apply(context.Background(), db, path, nil); err != nil {
				t.Fatal(err)
			}
			if err := writeRestoreCredentialJournal(context.Background(), path, []string{"valid-reference"}); err != nil {
				t.Fatal(err)
			}
			record := restoreCredentialRecords(t, path)[0]
			bad := strings.ReplaceAll(contents, "DATABASE", filepath.Base(path))
			if err := os.WriteFile(record.path, []byte(bad), 0600); err != nil {
				t.Fatal(err)
			}
			if err := RecoverRestoreCredentials(context.Background(), db, path); !errors.Is(err, ErrRestore) || scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents") != "0" {
				t.Fatal("invalid journal was accepted or imported", err)
			}
			if data, err := os.ReadFile(record.path); err != nil || string(data) != bad {
				t.Fatal("invalid journal was discarded", err)
			}
		})
	}
	t.Run("link", func(t *testing.T) {
		db, path := database(t)
		if _, err := Apply(context.Background(), db, path, nil); err != nil {
			t.Fatal(err)
		}
		if err := writeRestoreCredentialJournal(context.Background(), path, []string{"reference"}); err != nil {
			t.Fatal(err)
		}
		record := restoreCredentialRecords(t, path)[0]
		outside := filepath.Join(t.TempDir(), "outside.json")
		if err := os.Rename(record.path, outside); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, record.path); err != nil {
			if runtime.GOOS == "windows" {
				t.Skip("symlink privilege unavailable")
			}
			t.Fatal(err)
		}
		if err := RecoverRestoreCredentials(context.Background(), db, path); !errors.Is(err, ErrRestore) || scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents") != "0" {
			t.Fatal("linked journal was imported", err)
		}
	})
}

func TestRestoreCredentialJournalWriteFailureAndForeignScope(t *testing.T) {
	ctx := context.Background()
	db, path := database(t)
	if _, err := Apply(ctx, db, path, nil); err != nil {
		t.Fatal(err)
	}
	otherDB, otherPath := database(t)
	if _, err := Apply(ctx, otherDB, otherPath, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeRestoreCredentialJournal(ctx, path, []string{"reference"}); err != nil {
		t.Fatal(err)
	}
	if err := RecoverRestoreCredentials(ctx, otherDB, path); !errors.Is(err, ErrRestore) || len(restoreCredentialRecords(t, path)) != 1 {
		t.Fatal("a journal was imported into the wrong open database", err)
	}
	foreign := filepath.Join(filepath.Dir(path), "foreign.sqlite3.restore-credentials-"+strings.Repeat("a", 32)+".json")
	if err := os.WriteFile(foreign, []byte("not our journal"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverRestoreCredentials(ctx, db, path); err != nil {
		t.Fatal("another database's side record entered the profile scope", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("another database's side record was removed", err)
	}
	if runtime.GOOS != "windows" {
		directory := filepath.Dir(path)
		if err := os.Chmod(directory, 0500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(directory, 0700) })
		err := writeRestoreCredentialJournal(ctx, path, []string{"another-reference"})
		if chmodErr := os.Chmod(directory, 0700); chmodErr != nil {
			t.Fatal(chmodErr)
		}
		if !errors.Is(err, ErrRestore) || len(restoreCredentialRecords(t, path)) != 0 || scalar(t, db, "SELECT COUNT(*) FROM credential_cleanup_intents") != "1" {
			t.Fatal("journal write failure modified retirement state", err)
		}
		if err := writeRestoreCredentialJournal(ctx, path, []string{"another-reference"}); err != nil {
			t.Fatal("journal write could not be retried", err)
		}
	}
}
