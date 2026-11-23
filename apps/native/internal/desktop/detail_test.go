package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnostics"
)

func detailFixture(locale string) *Meetings {
	job := domain.JobSummary{
		ID: "synthetic-detail", Source: "local", Title: "Synthetic meeting with a long title for bilingual narrow layouts",
		OverallStatus: "failed", UploadStatus: "completed", ASRStatus: "failed", SummaryStatus: "idle", Lang: "zh-CN",
		EnableSpeaker: true, DiarizationStatus: "unavailable", Hotwords: []string{"Synthetic"},
		SourceFiles: []domain.SourceFile{{ID: "synthetic-source", Name: "synthetic-recording-with-a-long-file-name-for-narrow-layout.wav", Kind: "audio", SizeLabel: "2.4 MB"}},
	}
	return NewMeetings(&client.Controller{ResultController: &client.ResultController{Result: &domain.Result{Job: job}}}, locale)
}

func TestDetailLogsCopyWholeDocumentAndRemainReadOnly(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{1100, 1440} {
				t.Run(fmt.Sprintf("%s/dark=%v/width=%d", locale, dark, width), func(t *testing.T) {
					view := detailFixture(locale)
					paragraph := strings.Repeat("合成日志必须真实折行并保留完整复制内容。", 18)
					if locale == "en-US" {
						paragraph = strings.TrimSpace(strings.Repeat("Synthetic diagnostic paragraphs must wrap and remain copyable together. ", 12))
					}
					older, newer := "  older warning\talpha  beta "+paragraph+"  ", "newer completed  gamma\tdelta "+paragraph
					raw := older + "\r\n\r\n\x1b[32m" + newer + "\x1b[0m"
					view.Client.Result.ProcessLog = &raw
					label := presentation.Message(locale, "jobDetail.logSection")
					back := 0
					shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Route: "/jobs/synthetic-detail", Content: func(c *ui.Context) { view.Detail(c, func() { back++ }) }}
					tester := ui.NewTesterWith(shell.View, width, 760, ui.TesterOptions{Dark: dark})
					for index := 0; index < 25 && !tester.Focused(label); index++ {
						tester.Key(0, ui.KeyTab)
					}
					if !tester.Focused(label) {
						t.Fatal("Tab cannot reach the log document")
					}
					expected := newer + "\n\n" + older
					field, ok := tester.AccessByRole(ui.RoleTextField, label)
					if !ok || !field.ReadOnly || !field.Multiline || field.Value != expected || field.Offscreen || field.Bounds.W < 200 || field.Bounds.H > 420 {
						t.Fatal("log document lacks a bounded read-only multiline field with its complete accessible value")
					}
					if width == 1100 && dark == (locale == "en-US") {
						captureParity(t, tester, "detail-copyable-log-"+locale)
					}
					tester.Command("selectAll")
					tester.Command("copy")
					if tester.Clipboard() != expected {
						t.Fatal("whole-log copy lost paragraphs, blank lines, indentation, tabs or trailing spaces")
					}
					tester.Type("must not edit")
					tester.Key(0, ui.KeyBackspace)
					field, _ = tester.AccessByRole(ui.RoleTextField, label)
					if field.Value != expected || *view.Client.Result.ProcessLog != raw {
						t.Fatal("read-only log accepted an edit")
					}
					raw = "replacement log"
					tester.Frame()
					tester.Command("selectAll")
					tester.Command("copy")
					if tester.Clipboard() != raw {
						t.Fatal("log selection retained a replaced snapshot")
					}
					tester.Key(0, ui.KeyTab)
					if tester.Focused(label) {
						t.Fatal("Tab remained trapped in the read-only log document")
					}
					backLabel := presentation.Message(locale, "jobDetail.backToJobs")
					for index := 0; index < 25 && !tester.Focused(backLabel); index++ {
						tester.Key(0, ui.KeyTab)
					}
					if !tester.Focused(backLabel) {
						t.Fatal("Tab cannot leave the log document for the back action")
					}
					tester.Key(0, ui.KeyEnter)
					if back != 1 {
						t.Fatal("keyboard back did not invoke its action exactly once")
					}
				})
			}
		}
	}
}

func detailLogPixels(tester *ui.Tester, bounds ui.Rect) []byte {
	bitmap := tester.Image()
	var pixels []byte
	for row := int(bounds.Y + 12); row < int(bounds.Y+bounds.H-12); row++ {
		start := bitmap.PixOffset(int(bounds.X+12), row)
		end := bitmap.PixOffset(int(bounds.X+bounds.W-20), row)
		pixels = append(pixels, bitmap.Pix[start:end]...)
	}
	return pixels
}

func TestDetailLargeLogsScrollByKeyboardAndCopyOffscreenParagraphs(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := detailFixture(locale)
			lines, expectedLines := make([]string, 4096), make([]string, 4096)
			for index := range lines {
				lines[index] = fmt.Sprintf("  %04d\t%s  ", index, strings.Repeat(localized(locale, "合成日志完整复制。", "Synthetic log remains fully copyable. "), 4))
				expectedLines[len(lines)-1-index] = lines[index]
			}
			raw, expected := strings.Join(lines, "\n")+"\n", strings.Join(expectedLines, "\n")+"\n"
			view.Client.Result.ProcessLog = &raw
			label := presentation.Message(locale, "jobDetail.logSection")
			shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Route: "/jobs/synthetic-detail", Content: func(c *ui.Context) { view.Detail(c, func() {}) }}
			tester := ui.NewTesterWith(shell.View, 1100, 760, ui.TesterOptions{Dark: locale == "en-US"})
			for index := 0; index < 25 && !tester.Focused(label); index++ {
				tester.Key(0, ui.KeyTab)
			}
			field, ok := tester.AccessByRole(ui.RoleTextField, label)
			if !ok || !field.Focused || field.Offscreen || field.Value != expected || field.Bounds.H > 420 {
				t.Fatal("large log lost its complete accessible value or bounded keyboard viewport")
			}
			if len(tester.AccessAll()) > 300 {
				t.Fatal("large log expanded its paragraphs into an unbounded accessibility tree")
			}
			first := detailLogPixels(tester, field.Bounds)
			tester.Key(0, ui.KeyPageDown)
			if bytes.Equal(first, detailLogPixels(tester, field.Bounds)) {
				captureParity(t, tester, "detail-large-scroll-failure-"+locale)
				t.Fatalf("PageDown did not change the visible log text: bounds=%+v pixels=%d", field.Bounds, len(first))
			}
			tester.Key(0, ui.KeyHome)
			if !bytes.Equal(first, detailLogPixels(tester, field.Bounds)) {
				t.Fatal("Home did not return to the first log paragraph")
			}
			tester.Key(0, ui.KeyEnd)
			if bytes.Equal(first, detailLogPixels(tester, field.Bounds)) {
				t.Fatal("End did not reach the offscreen log paragraphs")
			}
			tester.Command("selectAll")
			tester.Command("copy")
			if tester.Clipboard() != expected {
				t.Fatal("large log copy omitted offscreen paragraphs or whitespace")
			}
			t.Logf("copied %d paragraphs / %d bytes with %d accessibility nodes", len(lines), len(expected), len(tester.AccessAll()))
		})
	}
}

func TestDetailFailureReasonUsesSafeProjectionForBothSources(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, source := range []string{"local", "remote"} {
			t.Run(locale+"/"+source, func(t *testing.T) {
				view := detailFixture(locale)
				value := strings.Join([]string{"synthetic", "sensitive", "value"}, "-")
				reason := "Synthetic failure Bearer " + value + " /Synthetic/private.wav"
				view.Client.Result.Job.Source, view.Client.Result.Job.FailureReason = source, &reason
				view.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read"}}
				tester := ui.NewTester(func(c *ui.Context) { view.Detail(c, func() {}) }, 980, 1500)
				safe := diagnostics.Text(reason, 4096)
				if _, ok := tester.AccessByRole(ui.RoleAlert, safe); !ok {
					t.Fatal("detail failure reason did not use the safe diagnostic projection")
				}
				if err := tester.Click(safe); err != nil {
					t.Fatal(err)
				}
				tester.Command("selectAll")
				tester.Command("copy")
				if tester.Clipboard() != safe || tester.HasText(value) || tester.HasText("private.wav") {
					t.Fatal("detail exposed a synthetic secret or path in text or clipboard")
				}
			})
		}
	}
}

func TestDetailLegacyResponsivePanelsAndSpeakerExplanation(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1440, 1180, 980, 680, 420} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				view := detailFixture(locale)
				log := strings.Repeat("synthetic-log-with-long-unbroken-text-", 8)
				view.Client.Result.ProcessLog = &log
				tester := ui.NewTesterWith(func(c *ui.Context) { view.Detail(c, func() {}) }, width, 2200, ui.TesterOptions{Dark: locale == "en-US"})
				message := func(key string) string { return presentation.Message(locale, "jobDetail."+key) }
				files, _ := tester.Find(message("filesSection"))
				settings, _ := tester.Find(message("settingsSection"))
				if width <= 1180 && settings.Y <= files.Y || width > 1180 && (settings.X <= files.X || math.Abs(float64(settings.Y-files.Y)) > 1) {
					t.Fatalf("detail panels ignored the legacy 1180px breakpoint: files=%+v settings=%+v", files, settings)
				}
				for _, text := range []string{view.Client.Result.Job.Title, view.Client.Result.Job.SourceFiles[0].Name, message("diarizationUnavailable"), message("stageUploaded"), message("stageAsr"), message("stageSummary"), message("stageOverall")} {
					node, ok := tester.Find(text)
					if !ok || node.W <= 0 || node.X < 0 || node.X+node.W > float32(width)+1 {
						t.Fatalf("detail text escaped the available width: %s %+v", text, node)
					}
				}
				logField, ok := tester.AccessByRole(ui.RoleTextField, message("logSection"))
				if !ok || logField.Value != log || !logField.ReadOnly || logField.Bounds.W <= 0 || logField.Bounds.X < 0 || logField.Bounds.X+logField.Bounds.W > float32(width)+1 {
					t.Fatal("detail log text escaped the available width or lost its complete read-only value", logField.Bounds)
				}
				first, _ := tester.Find(message("stageUploaded"))
				last, _ := tester.Find(message("stageOverall"))
				if width <= 680 && last.Y <= first.Y || width > 680 && math.Abs(float64(last.Y-first.Y)) > 1 {
					t.Fatal("stage status grid did not adapt to narrow bilingual labels", first, last)
				}
				captureParity(t, tester, fmt.Sprintf("detail-%s-%d", locale, width))
			})
		}
	}
}

func TestDetailReadFailureIsNotReportedAsMissingAndKeepsRetry(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := detailFixture(locale)
		view.Client.Result = nil
		view.Client.ResultError = errors.New("synthetic read failure")
		back := 0
		tester := ui.NewTester(func(c *ui.Context) { view.Detail(c, func() { back++ }) }, 680, 700)
		if tester.HasText(presentation.Message(locale, "jobDetail.notFound")) {
			t.Fatal("failed detail read was reported as a missing job")
		}
		if _, ok := tester.AccessByRole(ui.RoleAlert, view.text("详情刷新失败，请重试。", "Could not refresh details. Please retry.")); !ok {
			t.Fatal("read error has no accessible alert")
		}
		if err := tester.Click(presentation.Message(locale, "jobDetail.backToJobs")); err != nil || back != 1 {
			t.Fatal("read failure lost the back path", err)
		}
		view.Client.ResultError = nil
		tester.Frame()
		if !tester.HasText(presentation.Message(locale, "jobDetail.notFound")) {
			t.Fatal("empty detail has no distinct missing state")
		}
	}
}

type detailRetryCall struct{ source, id string }
type detailRetryPort struct{ calls chan detailRetryCall }

func (port detailRetryPort) Retry(context.Context, string) error {
	return errors.New("unscoped retry")
}
func (port detailRetryPort) RetryForSource(_ context.Context, source, id string) error {
	port.calls <- detailRetryCall{source: source, id: id}
	return nil
}

func TestDetailRetryBindsSnapshotSourceAndCapabilities(t *testing.T) {
	for _, source := range []string{"local", "remote"} {
		view := detailFixture("en-US")
		view.Client.Result.Job.Source = source
		view.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read"}}
		callbacks := make(chan func(), 8)
		port := detailRetryPort{calls: make(chan detailRetryCall, 1)}
		view.Actions = client.NewJobActions(port, nil, nil, func(callback func()) { callbacks <- callback }, func() {}, func(string, string, bool) {})
		defer view.Actions.Close()
		view.Actions.Source = "other-queue-source"
		tester := ui.NewTester(func(c *ui.Context) { view.Detail(c, func() {}) }, 1180, 1300)
		label := presentation.Message(view.Locale, "jobDetail.retryJob")
		if source == "remote" {
			assertDisabled(t, tester, label, true)
			view.RemoteCapabilities.Operations = append(view.RemoteCapabilities.Operations, "jobs.retry")
			tester.Frame()
		}
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
		flushView(t, callbacks, tester)
		call := <-port.calls
		if call.source != source || call.id != view.Client.Result.Job.ID || view.Actions.Source != "other-queue-source" {
			t.Fatal("detail retry used another queue's source or changed subsequent queue actions", call)
		}
	}
}

func TestDetailWorkbenchFailureAndRuntimeErrorsRemainActionable(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := detailFixture(locale)
		view.OpenError = true
		view.Actions = &client.JobActions{Error: domain.ErrRuntime}
		tester := ui.NewTester(func(c *ui.Context) { view.Detail(c, func() {}) }, 980, 1500)
		for _, message := range []string{view.text("无法打开结果窗口，请重试。", "Could not open result. Please retry."), presentation.Message(locale, "common.modelUnavailableMessage")} {
			if _, ok := tester.AccessByRole(ui.RoleAlert, message); !ok {
				t.Fatal("action failure was silent or lost its runtime recovery guidance", message)
			}
		}
		view.Client.Result.Job.OverallStatus = "completed"
		opened := ""
		view.OpenResult = func(id, language string) { opened = id + ":" + language }
		tester.Frame()
		if err := tester.Click(presentation.Message(locale, "jobDetail.viewWorkbench")); err != nil || opened != view.Client.Result.Job.ID+":"+locale {
			t.Fatal("result open failure prevented a new workbench attempt", err)
		}
	}
}

func TestDetailRetryChecksLocalRuntimeWithoutBlockingRemote(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, source := range []string{"local", "remote"} {
			for _, state := range []string{"absent", "loading", "missing", "stale", "ready"} {
				t.Run(locale+"/"+source+"/"+state, func(t *testing.T) {
					var runtime *client.RuntimeController
					if state != "absent" {
						runtime = &client.RuntimeController{}
						if state != "loading" {
							runtime.Panel = &domain.RuntimePanel{Overview: domain.RuntimeOverview{ShellReady: state != "missing"}}
						}
						if state == "stale" {
							runtime.Error = errors.New("synthetic runtime refresh failure")
						}
					}
					view := detailFixture(locale)
					view.Client.Result.Job.Source = source
					view.Source = "other-queue-source"
					view.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read", "jobs.retry"}}
					callbacks := make(chan func(), 8)
					port := detailRetryPort{calls: make(chan detailRetryCall, 1)}
					view.Actions = client.NewJobActions(port, nil, nil, func(callback func()) { callbacks <- callback }, func() {}, func(string, string, bool) {})
					t.Cleanup(view.Actions.Close)
					view.Actions.Source = view.Source
					checks, warnings := 0, 0
					view.confirmLocalRuntime = func() bool {
						checks++
						if !localJobRuntimeReady(runtime) {
							warnings++
							return false
						}
						return true
					}
					tester := ui.NewTester(func(c *ui.Context) { view.Detail(c, func() {}) }, 980, 1500)
					label := presentation.Message(locale, "jobDetail.retryJob")
					if source == "remote" {
						view.RemoteCapabilities.Operations = []string{"jobs.read"}
						tester.Frame()
						assertDisabled(t, tester, label, true)
						view.RemoteCapabilities.Operations = append(view.RemoteCapabilities.Operations, "jobs.retry")
						tester.Frame()
					}
					if err := tester.Click(label); err != nil {
						t.Fatal(err)
					}
					if source == "local" && state != "ready" {
						if checks != 1 || warnings != 1 || view.Actions.Busy || len(port.calls) != 0 {
							t.Fatal("unready local retry was not stopped by its runtime warning")
						}
						runtime = &client.RuntimeController{Panel: &domain.RuntimePanel{Overview: domain.RuntimeOverview{ShellReady: true}}}
						tester.Frame()
						if err := tester.Click(label); err != nil {
							t.Fatal(err)
						}
					}
					flushView(t, callbacks, tester)
					call := <-port.calls
					if call.source != source || call.id != view.Client.Result.Job.ID || view.Actions.Source != view.Source {
						t.Fatal("retry changed the job's bound source", call)
					}
					if source == "remote" && (checks != 0 || warnings != 0) {
						t.Fatal("remote retry depended on local runtime readiness")
					}
				})
			}
		}
	}
}
