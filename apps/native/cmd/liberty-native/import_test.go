package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

func TestImportOptionsRequireOfflineIsolatedDirectoryWithoutChangingReadOnlyPreview(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "native")
	for _, channel := range []string{"preview", "candidate"} {
		options, err := parseOptions([]string{"--import-profile", source, "--profile-directory", target}, channel, io.Discard)
		if err != nil || options.importProfile != source || options.writable || options.prepare || options.database != "" {
			t.Fatal("import mode selected active writes", err)
		}
		for _, extras := range [][]string{{"--writable"}, {"--prepare-database"}, {"--restore-backup", profile.DatabaseName + ".pre-native-v10-synthetic.bak"}, {"--version"}, {"--database", filepath.Join(source, profile.DatabaseName)}, {"--default-profile"}, {"--runtime-profile", "security-candidate"}} {
			args := append([]string{"--import-profile", source, "--profile-directory", target}, extras...)
			if _, err := parseOptions(args, channel, io.Discard); err == nil {
				t.Fatal("import accepted incompatible mode")
			}
		}
		for _, args := range [][]string{{"--import-profile", source}, {"--import-profile=", "--profile-directory", target}, {"--import-profile", "relative", "--profile-directory", target}} {
			if _, err := parseOptions(args, channel, io.Discard); err == nil {
				t.Fatal("invalid import selection accepted")
			}
		}
		preview, err := parseOptions([]string{"--database", filepath.Join(source, profile.DatabaseName)}, channel, io.Discard)
		if err != nil || preview.writable || preview.prepare || preview.importProfile != "" {
			t.Fatal("read-only preview changed", err)
		}
	}
	for _, argument := range []string{"--import-profile", "--import-profile=synthetic", "-import-profile", "-import-profile=synthetic"} {
		if !isProfileImportCommand([]string{argument}) {
			t.Fatal("offline import could open a startup GUI")
		}
	}
	if isProfileImportCommand([]string{"--database", "synthetic-import-profile.sqlite3"}) {
		t.Fatal("read-only preview was mistaken for an offline command")
	}
}

func TestOfflineImportCommandIsIdempotentAndDoesNotDiscloseProfilePaths(t *testing.T) {
	root := t.TempDir()
	source, target, production := filepath.Join(root, "source-private"), filepath.Join(root, "native-private"), filepath.Join(root, "production-private")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	seed, _, err := jobdb.OpenPrepared(t.Context(), filepath.Join(source, profile.DatabaseName), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.SaveMember(t.Context(), domain.MeetingMember{ID: "synthetic-member", Name: "Synthetic person", SortOrder: 1}); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	options, err := parseOptions([]string{"--import-profile", source, "--profile-directory", target}, "candidate", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(bool) (string, error) { return production, nil }
	var output bytes.Buffer
	if err := importProfileSnapshot(t.Context(), options, resolve, nil, &output); err != nil {
		t.Fatal(err)
	}
	if err := importProfileSnapshot(t.Context(), options, resolve, nil, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "already imported: true") || strings.Contains(output.String(), root) || strings.Contains(output.String(), "Synthetic person") {
		t.Fatal("offline import status is missing or discloses data")
	}
	reopened, err := jobdb.Open(t.Context(), filepath.Join(target, profile.DatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	members, err := reopened.Members(t.Context())
	if err != nil || len(members) != 1 || members[0].ID != "synthetic-member" {
		t.Fatal("offline import lost business data", err)
	}
	options.profileDirectory = production
	if err := importProfileSnapshot(t.Context(), options, resolve, nil, io.Discard); !errors.Is(err, profile.ErrProfileOverlap) {
		t.Fatal("offline import selected production database", err)
	}
}
