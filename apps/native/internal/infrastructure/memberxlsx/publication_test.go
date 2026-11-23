package memberxlsx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func memberExportDirectory(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestMemberExportRejectsLinkedParent(t *testing.T) {
	directory, target := memberExportDirectory(t), memberExportDirectory(t)
	filename := filepath.Join(target, "members.xlsx")
	if err := os.WriteFile(filename, []byte("keep existing destination"), 0600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(directory, "linked")
	if err := os.Symlink(target, linked); err != nil {
		t.Skip("symlink unavailable")
	}
	if err := SaveFile(context.Background(), filepath.Join(linked, "members.xlsx"), members()); err != ErrFile {
		t.Errorf("linked parent accepted: %v", err)
	}
	data, err := os.ReadFile(filename)
	if err != nil || string(data) != "keep existing destination" {
		t.Fatal("export through a linked parent replaced the existing destination")
	}
}

func TestMemberExportCancellationPreservesExistingFileAndCleansUp(t *testing.T) {
	filename := filepath.Join(memberExportDirectory(t), "members.xlsx")
	if err := SaveFile(context.Background(), filename, members()); err != nil {
		t.Fatal(err)
	}
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		want := context.Canceled
		if deadline {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		}
		cancel()
		if err := SaveFile(ctx, filename, nil); !errors.Is(err, want) {
			t.Fatalf("cancellation was not preserved: %v", err)
		}
		got, err := ReadFile(filename)
		if err != nil || !reflect.DeepEqual(got, members()) {
			t.Fatal("cancelled export changed the original workbook")
		}
	}
	entries, err := os.ReadDir(filepath.Dir(filename))
	if err != nil || len(entries) != 1 {
		t.Fatal("cancelled export left a staging file")
	}
}

func TestMemberExportReplacesOnlySupportedDestination(t *testing.T) {
	directory := memberExportDirectory(t)
	for _, name := range []string{"members.xlsx", "members.XLSX"} {
		filename := filepath.Join(directory, name)
		if err := os.WriteFile(filename, []byte("previous artifact"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := SaveFile(context.Background(), filename, members()); err != nil {
			t.Fatal(err)
		}
		got, err := ReadFile(filename)
		if err != nil || !reflect.DeepEqual(got, members()) {
			t.Fatal("replacement did not publish the complete workbook")
		}
	}
	for _, filename := range []string{"relative.xlsx", filepath.Join(directory, "members.txt"), directory} {
		if err := SaveFile(context.Background(), filename, members()); err != ErrFile {
			t.Fatal("unsupported export destination was accepted", err)
		}
	}
}
