package application

import (
	"context"
	"encoding/json"
	"errors"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/fileout"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type diagnosticDB struct{ err error }

func (d diagnosticDB) SchemaVersion(context.Context) (int64, error) { return 10, d.err }

type diagnosticRuntime struct{ err error }

func (d diagnosticRuntime) Status(context.Context) (domain.RuntimeOverview, error) {
	return domain.RuntimeOverview{Status: "ready", InstallRoot: ptrString("/synthetic/private/profile"), LastError: ptrString("synthetic-token")}, d.err
}
func ptrString(s string) *string { return &s }
func TestDiagnosticsProjectsHealthWithoutPathsOrErrors(t *testing.T) {
	for _, failure := range []error{nil, errors.New("/synthetic/profile secret")} {
		d := NewDiagnostics("1.1.24", "darwin-aarch64", diagnosticDB{}, diagnosticRuntime{failure}, nil)
		report, err := d.Report(context.Background())
		if err != nil || report.SchemaVersion != 10 || report.CurrentPlatform == nil || len(report.SupportedPlatforms) != 4 {
			t.Fatal(report, err)
		}
		data, _ := json.Marshal(report)
		if strings.Contains(string(data), "synthetic") || report.DesktopPetLogAvailable {
			t.Fatal("private information in diagnostics")
		}
		if failure == nil && report.RuntimeStatus != "ready" || failure != nil && report.RuntimeStatus != "unknown" {
			t.Fatal(report.RuntimeStatus)
		}
		if !errors.Is(d.ExportPetLog(context.Background(), func([]byte) error { t.Fatal("published nonexistent log"); return nil }), domain.ErrDiagnosticLog) {
			t.Fatal("missing log accepted")
		}
	}
	d := NewDiagnostics("1.1.24", "unsupported", diagnosticDB{errors.New("private")}, diagnosticRuntime{}, nil)
	if _, err := d.Report(context.Background()); !errors.Is(err, domain.ErrDiagnostics) {
		t.Fatal(err)
	}
}

type countedDiagnosticLog struct {
	data  []byte
	err   error
	reads int
	hook  func()
}

func (source *countedDiagnosticLog) DiagnosticLog(context.Context) ([]byte, error) {
	source.reads++
	if source.hook != nil {
		source.hook()
	}
	return source.data, source.err
}

func TestDiagnosticsPrecancelledExportDoesNotReadOrPublish(t *testing.T) {
	source := &countedDiagnosticLog{data: []byte("synthetic diagnostic\n")}
	service := NewDiagnostics("1.1.24", "unsupported", diagnosticDB{}, diagnosticRuntime{}, source)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	published := false
	err := service.ExportPetLog(ctx, func([]byte) error { published = true; return nil })
	if !errors.Is(err, context.Canceled) || source.reads != 0 || published {
		t.Fatal("pre-cancelled diagnostic export read or published its source")
	}
}

func TestDiagnosticsAvailabilityMatchesExportSizeLimit(t *testing.T) {
	source := &countedDiagnosticLog{data: make([]byte, (4<<20)+1)}
	service := NewDiagnostics("1.1.24", "unsupported", diagnosticDB{}, diagnosticRuntime{}, source)
	report, err := service.Report(context.Background())
	if err != nil || report.DesktopPetLogAvailable {
		t.Fatal("non-exportable oversized log was advertised as available")
	}
}

func TestDiagnosticsUnavailableLogsNeverReachPublisher(t *testing.T) {
	for name, source := range map[string]*countedDiagnosticLog{
		"empty":      {},
		"read-error": {data: []byte("partial"), err: errors.New("synthetic read failure")},
		"oversized":  {data: make([]byte, diagnosticLogLimit+1)},
	} {
		t.Run(name, func(t *testing.T) {
			service := NewDiagnostics("1.1.24", "unsupported", diagnosticDB{}, diagnosticRuntime{}, source)
			if err := service.ExportPetLog(context.Background(), func([]byte) error {
				t.Fatal("unavailable log reached publisher")
				return nil
			}); !errors.Is(err, domain.ErrDiagnosticLog) {
				t.Fatal("unavailable source was accepted")
			}
		})
	}
	source := &countedDiagnosticLog{data: make([]byte, diagnosticLogLimit)}
	service := NewDiagnostics("1.1.24", "unsupported", diagnosticDB{}, diagnosticRuntime{}, source)
	report, err := service.Report(context.Background())
	if err != nil || !report.DesktopPetLogAvailable {
		t.Fatal("exportable boundary size was not available")
	}
	if err := service.ExportPetLog(context.Background(), func(data []byte) error {
		if len(data) != diagnosticLogLimit {
			t.Fatal("boundary export was truncated")
		}
		return nil
	}); err != nil {
		t.Fatal("boundary export was rejected")
	}
	reads := source.reads
	if !errors.Is(service.ExportPetLog(context.Background(), nil), domain.ErrDiagnosticLog) || source.reads != reads {
		t.Fatal("missing publisher read the source")
	}
}

func TestDiagnosticsExportCancellationAndFailurePreservePublishedFile(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("isolated destination unavailable")
	}
	output := filepath.Join(directory, "diagnostics.log")
	original := []byte("previous diagnostic export\n")
	if os.WriteFile(output, original, 0600) != nil {
		t.Fatal("previous export fixture failed")
	}
	for _, stage := range []string{"source-cancel", "publish-cancel", "publish-failure", "success", "cancel-after-publication"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := &countedDiagnosticLog{data: []byte("new diagnostic export\n")}
			if stage == "source-cancel" {
				source.hook = cancel
			}
			service := NewDiagnostics("1.1.24", "unsupported", diagnosticDB{}, diagnosticRuntime{}, source)
			published := false
			failure := errors.New("synthetic publication failure")
			err := service.ExportPetLog(ctx, func(data []byte) error {
				published = true
				if stage == "publish-cancel" {
					cancel()
				}
				if stage == "publish-failure" {
					return failure
				}
				writeErr := fileout.Write(ctx, output, data)
				if stage == "cancel-after-publication" {
					cancel()
				}
				return writeErr
			})
			data, readErr := os.ReadFile(output)
			if readErr != nil {
				t.Fatal("export removed the previous file")
			}
			switch stage {
			case "source-cancel", "publish-cancel":
				if !errors.Is(err, context.Canceled) || stage == "source-cancel" && published || string(data) != string(original) {
					t.Fatal("cancelled export published or lost the original")
				}
			case "publish-failure":
				if !errors.Is(err, failure) || string(data) != string(original) {
					t.Fatal("failed export lost the original or hid its failure")
				}
			default:
				if err != nil || !published || string(data) != string(source.data) {
					t.Fatal("completed export was lost or late cancellation reversed success")
				}
			}
		})
	}
}
