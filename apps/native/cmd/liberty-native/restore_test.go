package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

func TestOfflineRestoreCommandPreservesPostHandoverData(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	database := filepath.Join(directory, profile.DatabaseName)
	before := domain.MeetingMember{ID: "before", Name: "合成人员", SortOrder: 1}
	after := domain.MeetingMember{ID: "after", Name: "Synthetic second member", SortOrder: 2}
	seed, _, err := jobdb.OpenPrepared(ctx, database, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = seed.Close() })
	if err := seed.SaveMember(ctx, before); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	active, handover, err := jobdb.OpenHandover(ctx, database, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	if handover.Backup == "" {
		t.Fatal("handover did not produce a recovery point")
	}
	if err := active.SaveMember(ctx, after); err != nil {
		t.Fatal(err)
	}

	oldArgs, oldChannel := os.Args, launchChannel
	t.Cleanup(func() { os.Args, launchChannel = oldArgs, oldChannel })
	launchChannel = "preview"
	production := filepath.Join(t.TempDir(), profile.ProductionID)
	restore := func(name string) error {
		os.Args = []string{"liberty-native", "--profile-directory", directory, "--restore-backup", name}
		return runWithProfileResolver(func(candidate bool) (string, error) {
			if candidate {
				t.Fatal("explicit restore unexpectedly selected the default candidate")
			}
			return production, nil
		})
	}
	assertMembers := func(store *jobdb.Store, want ...domain.MeetingMember) {
		t.Helper()
		got, err := store.Members(ctx)
		if err != nil || !slices.Equal(got, want) {
			t.Fatal("synthetic members did not match the expected recovery point", err)
		}
	}
	if err := restore(filepath.Base(handover.Backup)); !errors.Is(err, profile.ErrInUse) {
		t.Fatal("CLI restore must reject an active profile owner", err)
	}
	assertMembers(active, before, after)
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	if err := restore(filepath.Base(handover.Backup)); err != nil {
		t.Fatal(err)
	}
	assertReopenedMembers := func(want ...domain.MeetingMember) {
		t.Helper()
		reopened, err := jobdb.Open(ctx, database)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		assertMembers(reopened, want...)
	}
	assertReopenedMembers(before)

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var safety string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), profile.DatabaseName+".pre-restore-") && strings.HasSuffix(entry.Name(), ".bak") {
			if safety != "" {
				t.Fatal("blocked restore unexpectedly created a recovery point")
			}
			safety = entry.Name()
		}
	}
	if safety == "" {
		t.Fatal("CLI restore did not preserve post-handover data")
	}
	if err := restore(safety); err != nil {
		t.Fatal(err)
	}
	assertReopenedMembers(before, after)
}
