package host

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"liberty.local/liberty/internal/infrastructure/diagnosticlog"
	"liberty.local/liberty/internal/infrastructure/fileout"
)

func TestDiagnosticsHostProvidesExportablePetLogWithoutDesktopInteraction(t *testing.T) {
	ctx := context.Background()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("isolated directory unavailable")
	}
	profile, _, err := OpenPrepared(ctx, filepath.Join(directory, "liberty.sqlite3"), true)
	if err != nil {
		t.Fatal("isolated host setup failed")
	}
	defer profile.Close()
	report, err := profile.Diagnostics.Report(ctx)
	if err != nil || !report.DesktopPetLogAvailable {
		t.Fatal("assembled host has no exportable pet diagnostic log")
	}
	output := filepath.Join(directory, "export.log")
	var published []byte
	if err := profile.Diagnostics.ExportPetLog(ctx, func(data []byte) error {
		published = append([]byte(nil), data...)
		return fileout.Write(ctx, output, data)
	}); err != nil {
		t.Fatal("host diagnostic export failed")
	}
	data, err := os.ReadFile(output)
	if err != nil || len(data) == 0 || string(data) != string(published) {
		t.Fatal("diagnostic export did not publish the source snapshot")
	}
	if !bytes.Contains(data, []byte(`"event":"session_started"`)) {
		t.Fatal("fresh host did not record its diagnostic session")
	}
}

func TestDiagnosticsHostRetainsCompanionEventsWhenReopened(t *testing.T) {
	for _, mode := range []string{"open", "handover"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			database := filepath.Join(t.TempDir(), "liberty.sqlite3")
			profile, _, err := OpenPrepared(ctx, database, true)
			if err != nil {
				t.Fatal("host setup failed")
			}
			profile.RecordPetDiagnostic(diagnosticlog.DragFinished, diagnosticlog.Extra)
			profile.Close()
			if mode == "open" {
				profile, err = Open(ctx, database)
			} else {
				profile, _, err = OpenHandover(ctx, database, false)
			}
			if err != nil {
				t.Fatal("host reopen failed")
			}
			defer profile.Close()
			if err := profile.Diagnostics.ExportPetLog(ctx, func(data []byte) error {
				if !bytes.Contains(data, []byte(`"event":"drag_finished","instance":"extra"`)) || !bytes.Contains(data, []byte(`"event":"session_ended"`)) {
					t.Fatal("host reopen lost persisted companion diagnostics")
				}
				return nil
			}); err != nil {
				t.Fatal("reopened host source unavailable")
			}
		})
	}
}

func TestDiagnosticsHostPersistenceFailureDoesNotDisableExportOrStartup(t *testing.T) {
	ctx := context.Background()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil || os.Mkdir(filepath.Join(directory, diagnosticlog.Filename), 0700) != nil {
		t.Fatal("blocked log fixture failed")
	}
	profile, _, err := OpenPrepared(ctx, filepath.Join(directory, "liberty.sqlite3"), true)
	if err != nil {
		t.Fatal("diagnostic persistence failure blocked host assembly")
	}
	defer profile.Close()
	if err := profile.Start(ctx); err != nil {
		t.Fatal("diagnostic persistence failure blocked host startup")
	}
	report, err := profile.Diagnostics.Report(ctx)
	if err != nil || !report.DesktopPetLogAvailable {
		t.Fatal("safe in-memory diagnostics unavailable")
	}
	output := filepath.Join(directory, "export.log")
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticks := time.NewTicker(time.Millisecond)
	defer ticks.Stop()
	for {
		failed := false
		if err := profile.Diagnostics.ExportPetLog(ctx, func(data []byte) error {
			failed = bytes.Contains(data, []byte(`"event":"persistence_failed"`))
			return nil
		}); err != nil {
			t.Fatal("fallback source unavailable")
		}
		if failed {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("diagnostic persistence failure was not recorded")
		case <-ticks.C:
		}
	}
	if err := profile.Diagnostics.ExportPetLog(ctx, func(data []byte) error {
		if !bytes.Contains(data, []byte(`"event":"persistence_failed"`)) {
			t.Fatal("persistence failure was not represented by a safe event")
		}
		return fileout.Write(ctx, output, data)
	}); err != nil {
		t.Fatal("safe fallback could not be exported")
	}
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		t.Fatal("fallback export artifact absent")
	}
}

func TestDiagnosticsHostExportCancellationAndWriteFailureKeepPreviousArtifact(t *testing.T) {
	ctx := context.Background()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("isolated directory unavailable")
	}
	profile, _, err := OpenPrepared(ctx, filepath.Join(directory, "liberty.sqlite3"), true)
	if err != nil {
		t.Fatal("host setup failed")
	}
	defer profile.Close()
	output := filepath.Join(directory, "export.log")
	previous := []byte("previous export\n")
	if os.WriteFile(output, previous, 0600) != nil {
		t.Fatal("previous artifact unavailable")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := profile.Diagnostics.ExportPetLog(cancelled, func(data []byte) error {
		t.Fatal("cancelled host export reached publisher")
		return fileout.Write(cancelled, output, data)
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled host export succeeded")
	}
	if err := profile.Diagnostics.ExportPetLog(ctx, func(data []byte) error {
		return fileout.Write(ctx, filepath.Join(output, "blocked.log"), data)
	}); !errors.Is(err, fileout.ErrOutput) {
		t.Fatal("filesystem publication failure was hidden")
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(after, previous) {
		t.Fatal("failed or cancelled host export changed the previous artifact")
	}
}

func TestDiagnosticsHostLateRecorderStaysWithItsOriginalProfile(t *testing.T) {
	ctx := context.Background()
	previous, _, err := OpenPrepared(ctx, filepath.Join(t.TempDir(), "liberty.sqlite3"), true)
	if err != nil {
		t.Fatal("previous host setup failed")
	}
	defer previous.Close()
	late := previous.RecordPetDiagnostic
	current, _, err := OpenPrepared(ctx, filepath.Join(t.TempDir(), "liberty.sqlite3"), true)
	if err != nil {
		t.Fatal("current host setup failed")
	}
	defer current.Close()
	late(diagnosticlog.DragFinished, diagnosticlog.Extra)
	if err := current.Diagnostics.ExportPetLog(ctx, func(data []byte) error {
		if bytes.Contains(data, []byte(`"event":"drag_finished"`)) {
			t.Error("previous profile's late event entered the current profile")
		}
		return nil
	}); err != nil {
		t.Fatal("current host export failed")
	}
	previous.Close()
	late(diagnosticlog.Tapped, diagnosticlog.Primary)
	if err := current.Diagnostics.ExportPetLog(ctx, func(data []byte) error {
		if bytes.Contains(data, []byte(`"event":"tapped"`)) {
			t.Error("closed profile's late event entered the current profile")
		}
		return nil
	}); err != nil {
		t.Fatal("current host export failed")
	}
}
