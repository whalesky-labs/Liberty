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

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
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
	seed, _, err := OpenPrepared(ctx, database, true)
	recoveryRequire(t, err)
	_, err = seed.Resources.SaveMember(ctx, domain.MeetingMember{ID: "portable-member", Name: "Portable baseline", Department: "Synthetic acceptance", SortOrder: 10})
	recoveryRequire(t, err)
	seed.Close()
	hostRecoveryWithoutCredentials(t, database)
	owner, takeover, err := OpenHandover(ctx, database, false)
	recoveryRequire(t, err)
	owner.Close()
	if takeover.Backup == "" {
		t.Fatal("nonempty profile lacks the pre-upgrade recovery point")
	}
	memberName := func(file string) string {
		connection := recoverySQL(t, file, "ro")
		defer connection.Close()
		var name string
		recoveryRequire(t, connection.QueryRow(`SELECT name FROM meeting_members WHERE id='portable-member'`).Scan(&name))
		return name
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
	run("PHASE 1: previous candidate; verify Members contains Portable baseline, then quit without editing.", openArgs...)
	requireName(database, "Portable baseline")

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
	run("PHASE 2: upgraded candidate; rename Portable baseline to Portable upgraded, save, then quit normally.", openArgs...)
	requireName(database, "Portable upgraded")
	hostRecoveryWithoutCredentials(t, database)
	requireName(takeover.Backup, "Portable baseline")
	backupsBefore, err := filepath.Glob(filepath.Join(profileDirectory, "liberty.sqlite3.pre-restore-*.bak"))
	recoveryRequire(t, err)
	if len(backupsBefore) != 0 {
		t.Fatal("acceptance fixture unexpectedly already has a restore safety snapshot")
	}
	run("PHASE 3: restore the pre-upgrade snapshot; acknowledge the native restore result dialog.", "--profile-directory", profileDirectory, "--restore-backup", filepath.Base(takeover.Backup))
	requireName(database, "Portable baseline")
	backups, err := filepath.Glob(filepath.Join(profileDirectory, "liberty.sqlite3.pre-restore-*.bak"))
	recoveryRequire(t, err)
	if len(backups) != 1 {
		t.Fatal("restore did not retain exactly one safety snapshot")
	}
	requireName(backups[0], "Portable upgraded")
	recoveryRequire(t, os.Rename(installed, filepath.Join(root, "upgraded")))
	recoveryRequire(t, os.Rename(retained, installed))
	verifyVersion("previous")
	run("PHASE 4: rolled-back candidate; verify Members contains Portable baseline, then quit without editing.", openArgs...)
	requireName(database, "Portable baseline")
	requireName(backups[0], "Portable upgraded")
	t.Log("Real candidate replacement, failed-update rollback, SQLite restore, safety-snapshot retention and previous-version reopen verified.")
}
