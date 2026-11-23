package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

type diagnosticInteractionService struct{ report domain.DiagnosticReport }

func (service diagnosticInteractionService) Report(context.Context) (domain.DiagnosticReport, error) {
	return service.report, nil
}

type diagnosticInteractionFiles struct {
	calls    chan context.Context
	complete chan struct{}
	saved    bool
	err      error
}

func (files diagnosticInteractionFiles) Export(ctx context.Context) (bool, error) {
	files.calls <- ctx
	select {
	case <-files.complete:
		return files.saved, files.err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func TestDiagnosticsViewExportAvailabilityAndOutcomes(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, outcome := range []string{"cancel", "failure", "success"} {
			t.Run(locale+"/"+outcome, func(t *testing.T) {
				callbacks := make(chan func(), 8)
				service := diagnosticInteractionService{report: domain.DiagnosticReport{AppVersion: "1.1.24", SchemaVersion: 10, RuntimeStatus: "missing", SupportedPlatforms: domain.DiagnosticPlatforms(), DesktopPetLogAvailable: true}}
				controller := client.NewDiagnostics(service, func(callback func()) { callbacks <- callback }, func() {})
				defer controller.Close()
				controller.Report = &service.report
				complete := make(chan struct{})
				files := diagnosticInteractionFiles{calls: make(chan context.Context, 2), complete: complete, saved: outcome == "success"}
				if outcome == "failure" {
					files.err = errors.New("synthetic private output failure")
				}
				view := &SettingsView{Client: &client.SettingsController{Loaded: true, Draft: domain.DefaultSettings()}, Diagnostics: controller, DiagnosticFiles: files, Locale: locale, section: "diagnostics"}
				tester := ui.NewTesterWith(view.View, 1100, 1800, ui.TesterOptions{Dark: locale == "en-US"})
				label := localized(locale, "导出诊断日志", "Export Diagnostic Log")
				controller.Exported = true
				tester.Frame()
				runtimeClickControl(t, tester, ui.RoleButton, label)
				if !controller.Busy || controller.Exported {
					t.Fatal("new export retained previous success or failed to become busy")
				}
				if ctx := <-files.calls; ctx.Err() != nil {
					t.Fatal("live export received an obsolete context")
				}
				button, ok := tester.AccessByRole(ui.RoleButton, label)
				if !ok || !button.Disabled {
					t.Fatal("export remained enabled during publication", button)
				}
				tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
				if len(files.calls) != 0 {
					t.Fatal("busy export opened a second publisher")
				}
				close(complete)
				flushView(t, callbacks, tester)
				if controller.Busy || controller.Exported != (outcome == "success") || (controller.Error != nil) != (outcome == "failure") {
					t.Fatal("diagnostic export outcome was misreported", outcome, controller.Error)
				}
				message := localized(locale, "诊断操作失败，请重试。", "Diagnostic operation failed. Please retry.")
				if _, ok := tester.AccessByRole(ui.RoleAlert, message); ok != (outcome == "failure") || tester.HasText("synthetic private output failure") {
					t.Fatal("diagnostic export error was hidden or exposed its raw failure")
				}
				success := localized(locale, "诊断日志已导出。", "Diagnostic log exported.")
				if _, ok := tester.AccessByRole(ui.RoleStatus, success); ok != (outcome == "success") {
					t.Fatal("diagnostic export success was inaccessible or shown after cancellation")
				}
				if outcome == "failure" {
					runtimeClickControl(t, tester, ui.RoleButton, localized(locale, "刷新", "Refresh"))
					flushView(t, callbacks, tester)
					if controller.Error != nil || len(files.calls) != 0 {
						t.Fatal("refresh repeated publication or retained the export error")
					}
				}
			})
		}
	}
}

func TestDiagnosticsViewUnavailableLogNeverDelegatesExport(t *testing.T) {
	for _, reason := range []string{"missing-log", "missing-publisher"} {
		t.Run(reason, func(t *testing.T) {
			files := diagnosticInteractionFiles{calls: make(chan context.Context, 1)}
			view := &SettingsView{
				Client:          &client.SettingsController{Loaded: true, Draft: domain.DefaultSettings()},
				Diagnostics:     &client.DiagnosticsController{Report: &domain.DiagnosticReport{DesktopPetLogAvailable: reason != "missing-log"}},
				DiagnosticFiles: files, Locale: "en-US", section: "diagnostics",
			}
			if reason == "missing-publisher" {
				view.DiagnosticFiles = nil
			}
			tester := ui.NewTester(view.View, 1100, 1800)
			button, ok := tester.AccessByRole(ui.RoleButton, "Export Diagnostic Log")
			if !ok || !button.Disabled {
				t.Fatal("unavailable diagnostic export remained enabled", reason, button)
			}
			tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
			if len(files.calls) != 0 || view.Diagnostics.Busy {
				t.Fatal("unavailable diagnostic log reached an exporter")
			}
		})
	}
}
