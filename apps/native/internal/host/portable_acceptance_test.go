//go:build portable_acceptance

package host

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

// This opt-in workflow runs real candidate binaries in one disposable install
// location. The operator verifies each GUI phase and quits it normally.
func TestPortableUpgradeRollbackAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	root := os.Getenv("LIBERTY_NATIVE_PORTABLE_ACCEPTANCE_DIR")
	marker, err := os.ReadFile(filepath.Join(root, ".native-portable-acceptance"))
	if !filepath.IsAbs(root) || err != nil || string(marker) != "isolated-portable-acceptance-v1\n" {
		t.Fatal("run native:portable:acceptance with verified candidate archives")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Fatal("portable GUI acceptance requires a supported desktop host")
	}
	installed, retained := filepath.Join(root, "installed"), filepath.Join(root, "retained")
	profileDirectory := filepath.Join(root, "profile")
	if err := os.Mkdir(profileDirectory, 0700); err != nil {
		t.Fatal("acceptance profile must be new", err)
	}
	database := filepath.Join(profileDirectory, "liberty.sqlite3")
	var memberID string
	memberName := func(file string) string {
		connection := recoverySQL(t, file, "ro")
		defer connection.Close()
		var name string
		recoveryRequire(t, connection.QueryRow(`SELECT name FROM meeting_members WHERE id=?`, memberID).Scan(&name))
		return name
	}
	schemaVersion := func(file string) int {
		connection := recoverySQL(t, file, "ro")
		defer connection.Close()
		var version int
		recoveryRequire(t, connection.QueryRow(`SELECT value FROM app_meta WHERE key='schema_version'`).Scan(&version))
		return version
	}
	requireName := func(file, expected string) {
		if memberName(file) != expected {
			t.Fatal("portable profile content differs from the expected phase")
		}
	}
	install := func(source string) {
		t.Helper()
		recoveryRequire(t, os.CopyFS(installed, os.DirFS(filepath.Join(root, source))))
	}
	binary := filepath.Join(installed, "Liberty Native Candidate.exe")
	if runtime.GOOS == "darwin" {
		binary = filepath.Join(installed, "Liberty Native Candidate.app", "Contents", "MacOS", "liberty-native")
	}
	verifyVersion := func(source string) {
		var manifest struct{ Version string }
		data, err := os.ReadFile(filepath.Join(root, source, "candidate-manifest.json"))
		recoveryRequire(t, err)
		recoveryRequire(t, json.Unmarshal(data, &manifest))
		output, err := exec.CommandContext(ctx, binary, "--version").Output()
		recoveryRequire(t, err)
		if strings.TrimSpace(string(output)) != "Liberty Native "+manifest.Version+" (candidate)" {
			t.Fatal("installed binary version/channel mismatch")
		}
	}
	run := func(phase string, args ...string) {
		t.Helper()
		t.Log(phase)
		command := exec.CommandContext(ctx, binary, args...)
		// Do not print program output: failure paths can contain profile details.
		if err := command.Run(); err != nil {
			t.Fatal("portable candidate did not exit normally", err)
		}
		lock, err := profile.Acquire(profileDirectory)
		recoveryRequire(t, err)
		recoveryRequire(t, lock.Close())
	}
	openArgs := []string{"--profile-directory", profileDirectory, "--writable", "--locale", "en-US"}
	install("previous")
	verifyVersion("previous")
	// The previous executable must create its own schema. Seeding through the
	// current host prevents a genuine v12 -> v13 upgrade and makes old startup
	// fail before the first interaction can be exercised.
	run("PHASE 1: previous candidate; create exactly one Member named Portable baseline, save, then quit normally.", append(openArgs, "--prepare-database")...)
	connection := recoverySQL(t, database, "ro")
	var count int
	recoveryRequire(t, connection.QueryRow(`SELECT COUNT(*) FROM meeting_members`).Scan(&count))
	if count != 1 {
		t.Fatal("previous candidate must save exactly one baseline member")
	}
	recoveryRequire(t, connection.QueryRow(`SELECT id FROM meeting_members WHERE name='Portable baseline'`).Scan(&memberID))
	recoveryRequire(t, connection.Close())
	requireName(database, "Portable baseline")
	hostRecoveryWithoutCredentials(t, database)
	previousSchema := schemaVersion(database)
	if previousSchema < 10 || previousSchema > schema.CurrentVersion {
		t.Fatal("previous candidate schema is outside the supported acceptance range")
	}
	// Take a WAL-consistent snapshot under the same profile lock. Do not open
	// the old database through current repositories, which would upgrade it.
	owner, err := profile.Acquire(profileDirectory)
	recoveryRequire(t, err)
	snapshot, err := os.CreateTemp(profileDirectory, filepath.Base(database)+".pre-portable-*.bak")
	recoveryRequire(t, err)
	backup := snapshot.Name()
	recoveryRequire(t, snapshot.Close())
	version, snapshotErr := schema.CopyProfileSnapshot(ctx, database, backup)
	closeErr := owner.Close()
	recoveryRequire(t, snapshotErr)
	recoveryRequire(t, closeErr)
	if version != previousSchema || schemaVersion(backup) != previousSchema {
		t.Fatal("pre-upgrade snapshot lost the previous schema")
	}
	requireName(backup, "Portable baseline")

	// Model a failed replacement after retaining the old app, before publishing
	// the new one. Both renames are confined to this test's newly created root.
	before, err := os.ReadFile(database)
	recoveryRequire(t, err)
	recoveryRequire(t, os.Rename(installed, retained))
	if _, err := os.Stat(binary); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failure fixture did not remove only the active install location")
	}
	recoveryRequire(t, os.Rename(retained, installed))
	verifyVersion("previous")
	after, err := os.ReadFile(database)
	recoveryRequire(t, err)
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("failed package replacement changed the profile database")
	}
	t.Log("Failed replacement restored the prior executable and preserved profile bytes.")

	recoveryRequire(t, os.Rename(installed, retained))
	install("candidate")
	verifyVersion("candidate")
	run("PHASE 2: upgraded candidate; rename Portable baseline to Portable upgraded, save, then quit normally.", append(openArgs, "--prepare-database")...)
	requireName(database, "Portable upgraded")
	hostRecoveryWithoutCredentials(t, database)
	if schemaVersion(database) != schema.CurrentVersion {
		t.Fatal("candidate did not reach the current schema")
	}
	requireName(backup, "Portable baseline")
	backupsBefore, err := filepath.Glob(filepath.Join(profileDirectory, "liberty.sqlite3.pre-restore-*.bak"))
	recoveryRequire(t, err)
	if len(backupsBefore) != 0 {
		t.Fatal("acceptance fixture unexpectedly already has a restore safety snapshot")
	}
	run("PHASE 3: restore the pre-upgrade snapshot; acknowledge the native restore result dialog.", "--profile-directory", profileDirectory, "--restore-backup", filepath.Base(backup))
	requireName(database, "Portable baseline")
	if schemaVersion(database) != previousSchema {
		t.Fatal("restore did not recover the previous schema")
	}
	backups, err := filepath.Glob(filepath.Join(profileDirectory, "liberty.sqlite3.pre-restore-*.bak"))
	recoveryRequire(t, err)
	if len(backups) != 1 {
		t.Fatal("restore did not retain exactly one safety snapshot")
	}
	requireName(backups[0], "Portable upgraded")
	if schemaVersion(backups[0]) != schema.CurrentVersion {
		t.Fatal("pre-restore safety snapshot lost the upgraded schema")
	}
	recoveryRequire(t, os.Rename(installed, filepath.Join(root, "upgraded")))
	recoveryRequire(t, os.Rename(retained, installed))
	verifyVersion("previous")
	run("PHASE 4: rolled-back candidate; verify Members contains Portable baseline, then quit without editing.", openArgs...)
	requireName(database, "Portable baseline")
	requireName(backups[0], "Portable upgraded")
	if schemaVersion(database) != previousSchema {
		t.Fatal("previous executable did not preserve its restored schema")
	}
	t.Logf("Real candidate replacement, schema %d -> %d -> %d, failed-update rollback, SQLite restore, safety-snapshot retention and previous-version reopen verified.", previousSchema, schema.CurrentVersion, previousSchema)
}
