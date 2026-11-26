//go:build darwin

package schema

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/runner"
)

func restoreCrashDB(t *testing.T, path, mode string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", URI(path, url.Values{"mode": {mode}, "_pragma": {"busy_timeout(2000)"}}))
	if err != nil {
		t.Fatal("synthetic crash database could not be opened")
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func restoreCrashSnapshot(t *testing.T, path string) map[string][]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	db := restoreCrashDB(t, path, "ro")
	defer db.Close()
	transaction, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal("synthetic crash snapshot transaction failed")
	}
	defer transaction.Rollback()
	var integrity string
	if transaction.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity) != nil || integrity != "ok" {
		t.Fatal("synthetic crash snapshot failed its integrity check")
	}
	violations, err := transaction.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal("synthetic crash snapshot relationship check failed")
	}
	invalid := violations.Next()
	readErr := violations.Err()
	if violations.Close() != nil || readErr != nil || invalid {
		t.Fatal("synthetic crash snapshot contains an invalid relationship")
	}
	tables, err := transaction.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal("synthetic crash table inventory failed")
	}
	var names []string
	for tables.Next() {
		var name string
		if tables.Scan(&name) != nil {
			t.Fatal("synthetic crash table name unreadable")
		}
		names = append(names, name)
	}
	readErr = tables.Err()
	if tables.Close() != nil || readErr != nil || len(names) != 35 {
		t.Fatal("synthetic crash fixture did not expose the complete v12 table set")
	}
	snapshot := make(map[string][]string, len(names))
	for _, name := range names {
		rows, err := transaction.QueryContext(ctx, `SELECT * FROM "`+strings.ReplaceAll(name, `"`, `""`)+`"`)
		if err != nil {
			t.Fatal("synthetic crash table could not be read")
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal("synthetic crash table columns unreadable")
		}
		snapshot[name] = []string{}
		for rows.Next() {
			values, targets := make([]any, len(columns)), make([]any, len(columns))
			for index := range values {
				targets[index] = &values[index]
			}
			if rows.Scan(targets...) != nil {
				rows.Close()
				t.Fatal("synthetic crash table row unreadable")
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal("synthetic crash row comparison failed")
			}
			snapshot[name] = append(snapshot[name], string(encoded))
		}
		readErr := rows.Err()
		if rows.Close() != nil || readErr != nil {
			t.Fatal("synthetic crash table traversal failed")
		}
		sort.Strings(snapshot[name])
	}
	if transaction.Commit() != nil {
		t.Fatal("synthetic crash snapshot could not finish")
	}
	return snapshot
}

func restoreCrashEqual(t *testing.T, expected, actual map[string][]string) {
	t.Helper()
	if len(expected) != len(actual) {
		t.Fatal("synthetic crash recovery changed the table inventory")
	}
	for table, rows := range expected {
		if !reflect.DeepEqual(rows, actual[table]) {
			t.Errorf("synthetic crash recovery changed table %s", table)
		}
	}
}

func TestRestoreCrashSubprocess(t *testing.T) {
	directory, nonce := os.Getenv("LIBERTY_RESTORE_CRASH_DIRECTORY"), os.Getenv("LIBERTY_RESTORE_CRASH_NONCE")
	if directory == "" || nonce == "" {
		t.Skip("parent-owned synthetic subprocess only")
	}
	guard, err := os.ReadFile(filepath.Join(directory, "guard"))
	if !filepath.IsAbs(directory) || len(nonce) != 32 || err != nil || string(guard) != nonce {
		t.Fatal("synthetic restore guard rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	owner, err := profile.Acquire(directory)
	if err != nil {
		t.Fatal("synthetic restore could not acquire profile ownership")
	}
	defer owner.Close()
	database := filepath.Join(owner.Directory(), "liberty.sqlite3")
	keeper := restoreCrashDB(t, database, "rw")
	defer keeper.Close()
	if _, err := keeper.ExecContext(ctx, `PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA wal_autocheckpoint=0;
 INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('crash-wal-member','Synthetic committed WAL','fixture','fixture')`); err != nil {
		t.Fatal("synthetic restore WAL write failed")
	}
	_, err = restoreLocked(ctx, database, os.Getenv("LIBERTY_RESTORE_CRASH_BACKUP"), nil, func(ctx context.Context, database string, references []string) error {
		if err := writeRestoreCredentialJournal(ctx, database, references); err != nil {
			return err
		}
		if os.WriteFile(filepath.Join(directory, "ready"), []byte(nonce), 0600) != nil {
			return ErrRestore
		}
		<-ctx.Done()
		return ErrRestore
	})
	if err != nil {
		t.Fatal("synthetic restore ended before its parent killed the process")
	}
	t.Fatal("synthetic restore unexpectedly crossed its crash boundary")
}

func TestRestoreSIGKILLAfterDurableJournalBeforeSQLiteRestore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("synthetic restore directory unavailable")
	}
	database := filepath.Join(directory, "liberty.sqlite3")
	owner, err := profile.Acquire(directory)
	if err != nil {
		t.Fatal("synthetic restore fixture ownership unavailable")
	}
	t.Cleanup(func() { owner.Close() })
	db := restoreCrashDB(t, database, "rwc")
	if _, err := Apply(ctx, db, database, nil); err != nil {
		t.Fatal("synthetic restore schema preparation failed")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO meeting_members(id,name,created_at,updated_at) VALUES('snapshot-member','Synthetic before restore','fixture','fixture');
 INSERT INTO credential_cleanup_intents VALUES('original-intent','synthetic-model','synthetic-existing','rollback',7,'synthetic-code','original-created','original-updated')`); err != nil {
		t.Fatal("synthetic restore snapshot seeding failed")
	}
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal("synthetic restore backup connection unavailable")
	}
	backupPath, backupErr := backup(ctx, connection, database, CurrentVersion, "pre-native")
	if connection.Close() != nil || backupErr != nil {
		t.Fatal("synthetic restore recovery point could not be verified")
	}
	before := restoreCrashSnapshot(t, backupPath)
	if _, err := db.ExecContext(ctx, `UPDATE meeting_members SET name='Synthetic changed after snapshot' WHERE id='snapshot-member';
 INSERT INTO ai_model_configs(id,name,base_url,model,api_key,api_key_ref,created_at,updated_at) VALUES('synthetic-model','Synthetic','https://synthetic.invalid','synthetic','','synthetic-new-model','fixture','fixture');
 INSERT INTO app_settings(id,theme_mode,liquid_glass_style,accent_color,locale,backend_url,api_token,api_token_ref,default_hotwords,summary_template,python_path,runner_script_path) VALUES(1,'auto','transparent','#2f6dff','zh-CN','','','synthetic-new-token','','','','');
 INSERT INTO app_meta(key,value) VALUES('native_credential_migration_v3','{"published":false,"entries":[{"reference":"synthetic-staged","previous":"synthetic-previous"}]}')`); err != nil {
		t.Fatal("synthetic restore post-snapshot seeding failed")
	}
	if db.Close() != nil || owner.Close() != nil {
		t.Fatal("synthetic restore fixture did not release ownership")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("synthetic restore nonce unavailable")
	}
	nonce := hex.EncodeToString(random[:])
	if os.WriteFile(filepath.Join(directory, "guard"), []byte(nonce), 0600) != nil {
		t.Fatal("synthetic restore guard publication failed")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("synthetic restore executable unavailable")
	}
	child := osexec.Command(executable, "-test.run=^TestRestoreCrashSubprocess$|^"+nonce+"$", "-test.timeout=25s")
	child.Env = append(os.Environ(), "LIBERTY_RESTORE_CRASH_DIRECTORY="+directory, "LIBERTY_RESTORE_CRASH_NONCE="+nonce, "LIBERTY_RESTORE_CRASH_BACKUP="+filepath.Base(backupPath))
	if child.Start() != nil {
		t.Fatal("synthetic restore subprocess could not start")
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	waited := false
	var identity string
	t.Cleanup(func() {
		if waited {
			return
		}
		current, err := runner.CaptureIdentity(uint32(child.Process.Pid))
		if err == nil && strings.Contains(current, nonce) && strings.Contains(current, "TestRestoreCrashSubprocess") && (identity == "" || current == identity) {
			if child.Process.Kill() != nil {
				t.Error("synthetic restore cleanup could not signal its verified subprocess")
			}
		} else if !errors.Is(err, runner.ErrProcessGone) {
			t.Error("synthetic restore cleanup refused an unverified subprocess")
		}
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("synthetic restore cleanup did not reap its subprocess")
		}
	})
	identity, err = runner.CaptureIdentity(uint32(child.Process.Pid))
	if err != nil || !strings.Contains(identity, nonce) || !strings.Contains(identity, "TestRestoreCrashSubprocess") {
		t.Fatal("synthetic restore subprocess identity could not be verified")
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := os.ReadFile(filepath.Join(directory, "ready"))
		if err == nil && string(ready) == nonce {
			break
		}
		select {
		case <-done:
			waited = true
			t.Fatal("synthetic restore subprocess exited before journal publication")
		case <-ctx.Done():
			t.Fatal("synthetic restore journal handshake timed out")
		case <-ticker.C:
		}
	}
	if other, err := profile.Acquire(directory); !errors.Is(err, profile.ErrInUse) {
		if other != nil {
			other.Close()
		}
		t.Fatal("synthetic restore did not retain exclusive profile ownership")
	}
	lockBefore, err := os.Stat(filepath.Join(directory, ".liberty-profile.lock"))
	if err != nil {
		t.Fatal("synthetic restore lock identity unavailable")
	}
	records, err := readRestoreCredentialJournals(ctx, database)
	wantReferences := []string{"synthetic-existing", "synthetic-new-model", "synthetic-new-token", "synthetic-previous", "synthetic-staged"}
	if err != nil || len(records) != 1 || records[0].info.Mode().Perm() != 0600 || !reflect.DeepEqual(records[0].journal.References, wantReferences) {
		t.Fatal("durable restore journal did not preserve the exact synthetic reference set")
	}
	safetyFiles, err := filepath.Glob(database + ".pre-restore-v12-*.bak")
	if err != nil || len(safetyFiles) != 1 || verifyBackup(ctx, safetyFiles[0], CurrentVersion) != nil {
		t.Fatal("synthetic restore did not retain one verified safety backup before its crash boundary")
	}
	currentState := restoreCrashSnapshot(t, database)
	if reflect.DeepEqual(currentState, before) || len(currentState["meeting_members"]) != 2 {
		t.Fatal("synthetic restore crossed the SQLite replacement boundary before its handshake")
	}
	restoreCrashEqual(t, currentState, restoreCrashSnapshot(t, safetyFiles[0]))
	mainBytes, err := os.ReadFile(database)
	if err != nil {
		t.Fatal("synthetic restore main database unavailable")
	}
	mainOnly := filepath.Join(t.TempDir(), "main-only.sqlite3")
	if os.WriteFile(mainOnly, mainBytes, 0600) != nil || len(restoreCrashSnapshot(t, mainOnly)["meeting_members"]) != 1 {
		t.Fatal("synthetic restore safety fixture did not contain a committed WAL-only row")
	}
	currentIdentity, err := runner.CaptureIdentity(uint32(child.Process.Pid))
	if err != nil || currentIdentity != identity || child.Process.Kill() != nil {
		t.Fatal("synthetic restore SIGKILL identity verification failed")
	}
	select {
	case waitErr := <-done:
		waited = true
		status, ok := child.ProcessState.Sys().(syscall.WaitStatus)
		if waitErr == nil || !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
			t.Fatal("synthetic restore was not reaped after SIGKILL")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("synthetic restore SIGKILL did not complete within its deadline")
	}
	if _, err := runner.CaptureIdentity(uint32(child.Process.Pid)); !errors.Is(err, runner.ErrProcessGone) {
		t.Fatal("synthetic restore process remained live after SIGKILL")
	}
	recoveryOwner, err := profile.Acquire(directory)
	if err != nil {
		t.Fatal("kernel did not release the crashed restore profile lock")
	}
	defer recoveryOwner.Close()
	lockAfter, err := os.Stat(filepath.Join(directory, ".liberty-profile.lock"))
	if err != nil || !os.SameFile(lockBefore, lockAfter) {
		t.Fatal("synthetic restore recovery replaced the lock file")
	}
	restoreCrashEqual(t, currentState, restoreCrashSnapshot(t, database))
	restoreCrashEqual(t, currentState, restoreCrashSnapshot(t, safetyFiles[0]))
	replayed, err := readRestoreCredentialJournals(ctx, database)
	if err != nil || len(replayed) != 1 || !os.SameFile(records[0].info, replayed[0].info) || string(records[0].data) != string(replayed[0].data) {
		t.Fatal("SIGKILL lost or changed the published restore journal")
	}
	retryStarted := time.Now().UTC()
	result, err := RestoreLocked(ctx, database, filepath.Base(backupPath), nil)
	if err != nil || result.RestoredVersion != CurrentVersion || result.SafetyBackup == "" || result.SafetyBackup == safetyFiles[0] {
		t.Fatal("synthetic restore retry did not complete with a separate verified safety backup")
	}
	restoreCrashEqual(t, currentState, restoreCrashSnapshot(t, result.SafetyBackup))
	db = restoreCrashDB(t, database, "rw")
	if RecoverRestoreCredentials(ctx, db, database) != nil || RecoverRestoreCredentials(ctx, db, database) != nil {
		t.Fatal("synthetic restore journal replay was not idempotent")
	}
	for _, reference := range []string{"synthetic-new-model", "synthetic-new-token", "synthetic-previous", "synthetic-staged"} {
		var created string
		if db.QueryRowContext(ctx, "SELECT created_at FROM credential_cleanup_intents WHERE credential_reference=?", reference).Scan(&created) != nil {
			t.Fatal("synthetic restore retry lost a retirement intent")
		}
		parsed, err := time.Parse(time.RFC3339Nano, created)
		if err != nil || parsed.Before(retryStarted.Add(-time.Second)) || parsed.After(time.Now().UTC().Add(time.Second)) {
			t.Fatal("synthetic restore retirement intent has an invalid publication timestamp")
		}
		digest := sha256.Sum256([]byte(reference))
		row, err := json.Marshal([]any{"restore-" + hex.EncodeToString(digest[:]), "", reference, "retire", int64(0), nil, created, created})
		if err != nil {
			t.Fatal("synthetic restore intent comparison could not be encoded")
		}
		before["credential_cleanup_intents"] = append(before["credential_cleanup_intents"], string(row))
	}
	sort.Strings(before["credential_cleanup_intents"])
	if db.Close() != nil {
		t.Fatal("synthetic restored database could not be closed")
	}
	restoreCrashEqual(t, before, restoreCrashSnapshot(t, database))
	db = restoreCrashDB(t, database, "rw")
	if RecoverRestoreCredentials(ctx, db, database) != nil || db.Close() != nil {
		t.Fatal("synthetic restored database could not replay after reopen")
	}
	restoreCrashEqual(t, before, restoreCrashSnapshot(t, database))
	remaining, err := readRestoreCredentialJournals(ctx, database)
	if err != nil || len(remaining) != 0 {
		t.Fatal("successful durable import left a restore journal")
	}
}
