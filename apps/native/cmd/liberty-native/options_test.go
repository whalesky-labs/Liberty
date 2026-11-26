package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/host"
	"liberty.local/liberty/internal/infrastructure/managed"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

func TestLaunchSelectionValidationHasNoProfileSideEffects(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing-profile")
	database := filepath.Join(directory, profile.DatabaseName)
	cases := []struct {
		name, channel, code string
		args                []string
	}{
		{"preview requires selection", "preview", "database_argument_required", nil},
		{"exclusive snapshot and directory", "preview", "conflicting_profile_arguments", []string{"--database", database, "--profile-directory", directory}},
		{"exclusive default and directory", "candidate", "conflicting_profile_arguments", []string{"--default-profile", "--profile-directory", directory}},
		{"prepare requires write", "preview", "database_preparation_requires_writable_mode", []string{"--profile-directory", directory, "--prepare-database"}},
		{"database is read only", "preview", "explicit_database_is_read_only", []string{"--database", database, "--writable"}},
		{"database cannot be prepared", "preview", "explicit_database_is_read_only", []string{"--database", database, "--writable", "--prepare-database"}},
		{"relative database", "preview", "profile_directory_unavailable", []string{"--database", "relative.sqlite3"}},
		{"relative directory", "preview", "profile_directory_unavailable", []string{"--profile-directory", "relative"}},
		{"locale", "candidate", "unsupported_locale", []string{"--locale", "invalid"}},
		{"theme", "candidate", "unsupported_theme", []string{"--theme", "invalid"}},
		{"unexpected argument", "candidate", "invalid_launch_arguments", []string{directory}},
		{"unknown flag", "candidate", "invalid_launch_arguments", []string{"--unknown=" + directory}},
		{"empty database", "candidate", "invalid_launch_arguments", []string{"--database="}},
		{"empty directory", "candidate", "invalid_launch_arguments", []string{"--profile-directory="}},
		{"empty restore", "candidate", "invalid_launch_arguments", []string{"--restore-backup="}},
		{"preview restore needs explicit profile", "preview", "database_argument_required", []string{"--restore-backup", "liberty.sqlite3.pre-native-v10-test.bak"}},
		{"implicit explicit mode", "candidate", "explicit_mode_requires_profile", []string{"--writable"}},
		{"implicit false mode", "candidate", "explicit_mode_requires_profile", []string{"--writable=false"}},
		{"restore isolated candidate default", "candidate", "restore_cannot_be_combined_with_write_mode", []string{"--restore-backup", "liberty.sqlite3.pre-native-v10-test.bak", "--prepare-database"}},
		{"invalid build channel", "production", "invalid_launch_arguments", nil},
		{"unknown runtime", "candidate", "unsupported_runtime_profile", []string{"--runtime-profile", "unknown"}},
		{"empty runtime", "candidate", "unsupported_runtime_profile", []string{"--runtime-profile="}},
		{"candidate runtime needs explicit profile", "candidate", "candidate_runtime_requires_explicit_writable_profile", []string{"--runtime-profile", "security-candidate"}},
		{"candidate runtime needs writable profile", "preview", "candidate_runtime_requires_explicit_writable_profile", []string{"--runtime-profile", "security-candidate", "--profile-directory", directory}},
		{"candidate runtime refuses production selection", "candidate", "candidate_runtime_requires_explicit_writable_profile", []string{"--runtime-profile", "security-candidate", "--default-profile", "--writable"}},
		{"candidate runtime refuses snapshots", "candidate", "candidate_runtime_requires_explicit_writable_profile", []string{"--runtime-profile", "security-candidate", "--database", database}},
		{"candidate runtime refuses restore", "candidate", "candidate_runtime_requires_explicit_writable_profile", []string{"--runtime-profile", "security-candidate", "--profile-directory", directory, "--writable", "--restore-backup", "liberty.sqlite3.pre-native-v10-test.bak"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			_, err := parseOptions(test.args, test.channel, &output)
			if err == nil || err.Error() != test.code {
				t.Fatal("unexpected launch result", err)
			}
			if output.Len() != 0 || strings.Contains(err.Error(), directory) {
				t.Fatal("launch errors exposed arguments")
			}
		})
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("invalid launch touched profile")
	}
}

func TestCandidateRuntimeSelectionIsExplicitAndDoesNotPrepareProfile(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "isolated-candidate")
	options, err := parseOptions([]string{"--profile-directory", directory, "--writable", "--prepare-database", "--runtime-profile", "security-candidate"}, "candidate", io.Discard)
	if err != nil || options.runtimeProfile != "security-candidate" || !options.writable || !options.prepare || options.defaultProfile {
		t.Fatal("candidate runtime selection failed", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("runtime selection prepared an unverified profile")
	}
	upgrade, err := parseOptions([]string{"--profile-directory", directory, "--writable", "--prepare-database", "--runtime-profile", "funasr-upgrade"}, "candidate", io.Discard)
	if err != nil || upgrade.runtimeProfile != "funasr-upgrade" {
		t.Fatal("upgrade candidate selection failed", err)
	}
	if _, err := parseOptions([]string{"--runtime-profile", "funasr-upgrade"}, "candidate", io.Discard); err == nil {
		t.Fatal("upgrade candidate selected default profile")
	}
	production, err := parseOptions(nil, "candidate", io.Discard)
	if err != nil || production.runtimeProfile != "production" {
		t.Fatal("default runtime changed", err)
	}
}

func TestExplicitCandidateDirectoryCannotSelectProduction(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, "production")
	options, err := parseOptions([]string{"--profile-directory", production, "--writable", "--prepare-database", "--runtime-profile", "security-candidate"}, "candidate", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := options.resolveProfile(func(candidate bool) (string, error) {
		if candidate {
			t.Fatal("candidate check requested the wrong default profile")
		}
		return production, nil
	}); !errors.Is(err, profile.ErrProfileOverlap) {
		t.Fatal("explicit production path bypassed candidate isolation", err)
	}
	if _, err := os.Stat(production); !os.IsNotExist(err) {
		t.Fatal("rejected production selection created the directory")
	}
}

func TestMutableLaunchCannotSelectProductionThroughProfileDirectory(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, profile.ProductionID)
	for _, directory := range []string{production, filepath.Join(root, "linked-copy")} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	database := filepath.Join(production, profile.DatabaseName)
	if err := os.WriteFile(database, []byte("synthetic data"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedCopy := filepath.Join(root, "linked-copy")
	if err := os.Link(database, filepath.Join(linkedCopy, profile.DatabaseName)); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	aliasErr := os.Symlink(root, alias)
	backup := profile.DatabaseName + ".pre-native-v10-synthetic.bak"
	child := filepath.Join(production, "must-not-create")
	for _, test := range []struct {
		name, directory string
		mode            []string
		needsAlias      bool
	}{
		{name: "write", directory: production, mode: []string{"--writable"}},
		{name: "restore", directory: production, mode: []string{"--restore-backup", backup}},
		{name: "ancestor", directory: root, mode: []string{"--writable"}},
		{name: "prepare descendant", directory: child, mode: []string{"--writable", "--prepare-database"}},
		{name: "case alias", directory: filepath.Join(root, strings.ToUpper(profile.ProductionID)), mode: []string{"--writable"}},
		{name: "parent alias", directory: filepath.Join(alias, profile.ProductionID), mode: []string{"--writable"}, needsAlias: true},
		{name: "linked database", directory: linkedCopy, mode: []string{"--restore-backup", backup}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.needsAlias && aliasErr != nil {
				t.Skip("symlinks unavailable on this test host")
			}
			options, err := parseOptions(append([]string{"--profile-directory", test.directory}, test.mode...), "candidate", io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			err = options.resolveProfile(func(candidate bool) (string, error) {
				if candidate {
					t.Fatal("explicit directory was replaced by the default candidate")
				}
				return production, nil
			})
			if !errors.Is(err, profile.ErrProfileOverlap) || options.database != "" {
				t.Fatal("production mutation was not rejected before accessing the database", err)
			}
		})
	}
	if _, err := os.Stat(child); !os.IsNotExist(err) {
		t.Fatal("production preflight created the requested descendant")
	}
}

func TestImplicitCandidateCannotUseLinkedProductionDatabase(t *testing.T) {
	root := t.TempDir()
	production := filepath.Join(root, profile.ProductionID)
	candidateDirectory := filepath.Join(root, profile.CandidateID)
	for _, directory := range []string{production, candidateDirectory} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	database := filepath.Join(production, profile.DatabaseName)
	if err := os.WriteFile(database, []byte("synthetic data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(database, filepath.Join(candidateDirectory, profile.DatabaseName)); err != nil {
		t.Fatal(err)
	}
	options, err := parseOptions(nil, "candidate", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	err = options.resolveProfile(func(candidate bool) (string, error) {
		if candidate {
			return candidateDirectory, nil
		}
		return production, nil
	})
	if !errors.Is(err, profile.ErrProfileOverlap) || options.database != "" {
		t.Fatal("implicit candidate selected the production database", err)
	}
}

func TestMutableProfileRejectsAmbiguousParentComponent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "subdirectory")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory := parent + string(filepath.Separator) + ".." + string(filepath.Separator) + "isolated"
	options, err := parseOptions([]string{"--profile-directory", directory, "--writable", "--prepare-database"}, "candidate", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := options.resolveProfile(func(bool) (string, error) { return filepath.Join(root, "production"), nil }); !errors.Is(err, profile.ErrLocation) || options.database != "" {
		t.Fatal("ambiguous path was not rejected before profile preparation", err)
	}
	if _, err := os.Stat(filepath.Join(root, "isolated")); !os.IsNotExist(err) {
		t.Fatal("rejected path left a directory behind")
	}
}

func TestStartupFailureProvidesSafeRecoveryInstructions(t *testing.T) {
	cases := []struct {
		err             error
		chinese, action string
	}{
		{profile.ErrProfileOverlap, "资料重叠", "overlaps the existing production profile"},
		{profile.ErrProductionMutation, "独立 Native 资料", "--import-profile"},
		{profile.ErrInUse, "先退出", "Quit that client"},
		{schema.ErrBackup, "磁盘空间", "free disk space"},
		{schema.ErrRestore, "保留现有资料库", "Keep the current database"},
		{schema.ErrVersion, "隔离副本", "isolated copy"},
		{domain.ErrCredentialStore, "系统凭据", "system credential store"},
		{domain.ErrCredentialCleanup, "系统凭据", "system credential store"},
		{managed.ErrChecksum, "完整性检查", "integrity checks"},
		{managed.ErrResources, "完整的候选应用包", "complete candidate app package"},
		{errors.New("synthetic-private-path"), "不要删除", "Keep the original database"},
	}
	for _, test := range cases {
		message := startupFailureMessage(test.err)
		if !strings.Contains(message, test.chinese) || !strings.Contains(message, test.action) || strings.Contains(message, "synthetic-private-path") {
			t.Fatal("startup error lacks safe bilingual recovery guidance")
		}
	}
}

func TestCandidateDefaultProfileIsSeparateAndPersistent(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "candidate")
	var selections []bool
	resolve := func(candidate bool) (string, error) {
		selections = append(selections, candidate)
		if candidate {
			return directory, nil
		}
		return filepath.Join(root, "production"), nil
	}
	options, err := parseOptions(nil, "candidate", io.Discard)
	if err != nil || !options.writable || !options.prepare {
		t.Fatal("candidate launch is not writable/prepared", err)
	}
	resolvedDirectory, resolveErr := profile.ResolveDirectory(directory)
	if resolveErr != nil {
		t.Fatal("candidate directory could not be resolved", resolveErr)
	}
	if err := options.resolveProfile(resolve); err != nil || len(selections) != 3 || !selections[0] || selections[1] || selections[2] || options.profileDirectory != resolvedDirectory {
		t.Fatal("candidate selected production profile", err)
	}
	ctx := context.Background()
	p, _, err := host.OpenHandover(ctx, options.database, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(ctx); err != nil {
		p.Close()
		t.Fatal(err)
	}
	if _, err := host.Open(ctx, options.database); !errors.Is(err, profile.ErrInUse) {
		p.Close()
		t.Fatal("second candidate acquired profile", err)
	}
	p.Close()
	reopened, _, err := host.OpenHandover(ctx, options.database, false)
	if err != nil {
		t.Fatal("candidate could not restart", err)
	}
	defer reopened.Close()
	values, err := reopened.Resources.Templates(ctx)
	if err != nil || len(values) != 5 {
		t.Fatal("candidate database was not persisted", err)
	}
}

func TestExplicitSnapshotNeverResolvesDefaultOrBecomesWritable(t *testing.T) {
	database := filepath.Join(t.TempDir(), profile.DatabaseName)
	options, err := parseOptions([]string{"--database", database}, "candidate", io.Discard)
	if err != nil || options.writable || options.prepare {
		t.Fatal("snapshot launch inherited candidate writes", err)
	}
	if err := options.resolveProfile(func(bool) (string, error) {
		t.Fatal("explicit snapshot resolved a default profile")
		return "", profile.ErrLocation
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(database); !os.IsNotExist(err) {
		t.Fatal("snapshot resolution created a database")
	}
}

func TestProductionProfileIsReadOnlyAndRequiresIsolatedImportForMutation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "production")
	resolve := func(candidate bool) (string, error) {
		if candidate {
			t.Fatal("explicit default selected candidate")
		}
		return directory, nil
	}
	options, err := parseOptions([]string{"--default-profile"}, "candidate", io.Discard)
	if err != nil || options.writable || options.prepare {
		t.Fatal("production selection implicitly enabled writes", err)
	}
	if err := options.resolveProfile(resolve); !errors.Is(err, profile.ErrLocation) {
		t.Fatal("unprepared default directory accepted", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("read-only default created profile")
	}
	for _, channel := range []string{"preview", "candidate"} {
		for _, mode := range [][]string{{"--writable"}, {"--writable", "--prepare-database"}, {"--restore-backup", profile.DatabaseName + ".pre-native-v10-synthetic.bak"}} {
			if _, err := parseOptions(append([]string{"--default-profile"}, mode...), channel, io.Discard); !errors.Is(err, profile.ErrProductionMutation) {
				t.Fatal("production mutation bypassed isolated import", channel, err)
			}
		}
	}
}

func TestHelpVersionAndResolutionFailures(t *testing.T) {
	var output bytes.Buffer
	if _, err := parseOptions([]string{"--help"}, "preview", &output); !errors.Is(err, flag.ErrHelp) || !strings.Contains(output.String(), "profile-directory") {
		t.Fatal("help failed", err)
	}
	options, err := parseOptions([]string{"--version"}, "preview", io.Discard)
	if err != nil || !options.version {
		t.Fatal("version requires profile", err)
	}
	options, err = parseOptions(nil, "candidate", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := options.resolveProfile(func(bool) (string, error) { return "", profile.ErrLocation }); !errors.Is(err, profile.ErrLocation) || options.database != "" {
		t.Fatal("default resolution failure ignored", err)
	}
}
