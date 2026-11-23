package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"liberty.local/liberty/internal/infrastructure/profile"
)

func TestInvalidRestoreNamesStopBeforeProfileResolution(t *testing.T) {
	directory := t.TempDir()
	backup := profile.DatabaseName + ".pre-native-v10-synthetic.bak"
	for _, selection := range []struct {
		name, channel string
		args          []string
	}{
		{"preview directory", "preview", []string{"--profile-directory", directory}},
		{"candidate directory", "candidate", []string{"--profile-directory", directory}},
		{"production selection", "candidate", []string{"--default-profile"}},
		{"candidate default", "candidate", nil},
	} {
		for _, invalid := range []struct {
			name, backup string
		}{
			{"parent path", "../" + backup},
			{"absolute path", filepath.Join(directory, backup)},
			{"windows parent path", `..\` + backup},
			{"windows drive", `C:\` + backup},
			{"alternate stream", backup + ":stream.bak"},
			{"unrelated database", "foreign.sqlite3.pre-native-v10-synthetic.bak"},
			{"not a backup", profile.DatabaseName},
			{"wrong extension", profile.DatabaseName + ".pre-native-v10-synthetic.txt"},
			{"embedded nul", profile.DatabaseName + ".pre-native-v10-\x00.bak"},
		} {
			t.Run(selection.name+"/"+invalid.name, func(t *testing.T) {
				args := append(append([]string{}, selection.args...), "--restore-backup", invalid.backup)
				var output bytes.Buffer
				options, err := parseOptions(args, selection.channel, &output)
				if err == nil {
					resolutions := 0
					resolveErr := options.resolveProfile(func(bool) (string, error) {
						resolutions++
						return directory, nil
					})
					t.Fatalf("invalid restore reached profile resolution: callbacks=%d databaseSelected=%t resolveError=%v", resolutions, options.database != "", resolveErr)
				}
				if err.Error() != "invalid_launch_arguments" || output.Len() != 0 {
					t.Fatal("invalid restore did not return a private launch error", err)
				}
				entries, err := os.ReadDir(directory)
				if err != nil || len(entries) != 0 {
					t.Fatal("invalid restore changed its synthetic profile", err)
				}
			})
		}
	}
}

func TestLaunchModesResolveOnlyTheirSelectedSyntheticProfile(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "explicit")
	candidate := filepath.Join(root, "candidate")
	production := filepath.Join(root, "production")
	for _, directory := range []string{explicit, production} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	backup := profile.DatabaseName + ".pre-native-v10-synthetic.bak"
	for _, test := range []struct {
		name, channel, directory string
		args                     []string
		writable, prepare        bool
		resolutions              []bool
	}{
		{name: "candidate isolated writable default", channel: "candidate", directory: candidate, writable: true, prepare: true, resolutions: []bool{true, false, false}},
		{name: "candidate default restore", channel: "candidate", directory: candidate, args: []string{"--restore-backup", backup}, resolutions: []bool{true, false, false}},
		{name: "candidate snapshot", channel: "candidate", args: []string{"--database", filepath.Join(explicit, profile.DatabaseName)}},
		{name: "preview snapshot", channel: "preview", args: []string{"--database", filepath.Join(explicit, profile.DatabaseName)}},
		{name: "candidate explicit read only", channel: "candidate", directory: explicit, args: []string{"--profile-directory", explicit}},
		{name: "preview explicit read only", channel: "preview", directory: explicit, args: []string{"--profile-directory", explicit}},
		{name: "candidate explicit writable", channel: "candidate", directory: explicit, args: []string{"--profile-directory", explicit, "--writable"}, writable: true, resolutions: []bool{false, false}},
		{name: "preview explicit prepared", channel: "preview", directory: explicit, args: []string{"--profile-directory", explicit, "--writable", "--prepare-database"}, writable: true, prepare: true, resolutions: []bool{false, false}},
		{name: "candidate production read only", channel: "candidate", directory: production, args: []string{"--default-profile"}, resolutions: []bool{false}},
		{name: "explicit restore", channel: "preview", directory: explicit, args: []string{"--profile-directory", explicit, "--restore-backup", backup}, resolutions: []bool{false, false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, err := parseOptions(test.args, test.channel, io.Discard)
			if err != nil || options.writable != test.writable || options.prepare != test.prepare {
				t.Fatal("launch mode crossed its write boundary", err, options.writable, options.prepare)
			}
			resolutions := 0
			err = options.resolveProfile(func(isCandidate bool) (string, error) {
				if resolutions >= len(test.resolutions) || isCandidate != test.resolutions[resolutions] {
					return "", errors.New("unexpected_default_profile")
				}
				resolutions++
				if isCandidate {
					return candidate, nil
				}
				return production, nil
			})
			wantDirectory := test.directory
			if wantDirectory != "" {
				wantDirectory, err = profile.ResolveDirectory(wantDirectory)
				if err != nil {
					t.Fatal("test profile directory could not be resolved", err)
				}
			}
			wantDatabase := filepath.Join(wantDirectory, profile.DatabaseName)
			if test.directory == "" {
				wantDatabase = filepath.Join(explicit, profile.DatabaseName)
			}
			if err != nil || resolutions != len(test.resolutions) || options.profileDirectory != wantDirectory || options.database != wantDatabase {
				t.Fatal("launch resolved an unselected profile", err, resolutions)
			}
		})
	}
	for _, directory := range []string{explicit, candidate, production} {
		entries, err := os.ReadDir(directory)
		if err != nil || len(entries) != 0 {
			t.Fatal("option resolution opened a database or created profile state", err)
		}
	}
}

func TestLaunchWriteAndRestoreConflictsHaveNoProfileSideEffects(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing-profile")
	database := filepath.Join(directory, profile.DatabaseName)
	backup := profile.DatabaseName + ".pre-native-v10-synthetic.bak"
	for _, channel := range []string{"preview", "candidate"} {
		for _, test := range []struct {
			name, code string
			args       []string
		}{
			{"snapshot and production", "conflicting_profile_arguments", []string{"--database", database, "--default-profile"}},
			{"snapshot restore", "restore_requires_profile_directory", []string{"--database", database, "--restore-backup", backup}},
			{"directory restore and write", "restore_cannot_be_combined_with_write_mode", []string{"--profile-directory", directory, "--restore-backup", backup, "--writable"}},
			{"directory restore and prepare", "restore_cannot_be_combined_with_write_mode", []string{"--profile-directory", directory, "--restore-backup", backup, "--prepare-database"}},
			{"production restore and write", "restore_cannot_be_combined_with_write_mode", []string{"--default-profile", "--restore-backup", backup, "--writable"}},
			{"production restore and prepare", "restore_cannot_be_combined_with_write_mode", []string{"--default-profile", "--restore-backup", backup, "--prepare-database"}},
			{"directory false write and prepare", "database_preparation_requires_writable_mode", []string{"--profile-directory", directory, "--writable=false", "--prepare-database"}},
			{"snapshot prepare", "database_preparation_requires_writable_mode", []string{"--database", database, "--prepare-database"}},
			{"version restore", "version_cannot_be_combined_with_profile_options", []string{"--version", "--default-profile", "--restore-backup", backup}},
			{"version write", "version_cannot_be_combined_with_profile_options", []string{"--version", "--writable"}},
		} {
			t.Run(channel+"/"+test.name, func(t *testing.T) {
				var output bytes.Buffer
				_, err := parseOptions(test.args, channel, &output)
				if err == nil || err.Error() != test.code || output.Len() != 0 {
					t.Fatal("invalid launch did not stop at private argument validation", err)
				}
				if _, err := os.Stat(directory); !os.IsNotExist(err) {
					t.Fatal("invalid launch prepared its synthetic profile", err)
				}
			})
		}
	}
}

func TestCandidateExplicitModeFlagsCannotEnableImplicitProfileWrites(t *testing.T) {
	backup := profile.DatabaseName + ".pre-native-v10-synthetic.bak"
	for _, mode := range []string{"--writable", "--writable=false", "--prepare-database", "--prepare-database=false"} {
		for _, restore := range []bool{false, true} {
			args := []string{mode}
			wantCode := "explicit_mode_requires_profile"
			if restore {
				args = append(args, "--restore-backup", backup)
				wantCode = "restore_cannot_be_combined_with_write_mode"
			}
			if _, err := parseOptions(args, "candidate", io.Discard); err == nil || err.Error() != wantCode {
				t.Fatal("implicit candidate selection ignored an explicit mode flag", mode, restore, err)
			}
		}
	}
}

func TestRecognizedRestoreNamesRemainCompatibleBeforeProfileAccess(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing-profile")
	for _, purpose := range []string{"pre-native-v10-synthetic", "pre-native-v9-synthetic", "pre-migration-v2-synthetic", "pre-restore-v10-synthetic"} {
		backup := profile.DatabaseName + "." + purpose + ".bak"
		options, err := parseOptions([]string{"--profile-directory", directory, "--restore-backup", backup}, "preview", io.Discard)
		if err != nil || options.restoreBackup != backup || options.writable || options.prepare || options.database != "" {
			t.Fatal("recognized restore name changed its launch mode", purpose, err)
		}
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("restore validation prepared its synthetic profile", err)
	}
}
