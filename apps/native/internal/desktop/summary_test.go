package desktop

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestSummaryViewRetainsBilingualConfigurationHistoryAndPreview(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, status := range []string{"completed", "running"} {
			preferences := domain.DefaultUIPreferences()
			preferences.Locale = locale
			runID, latestID := "opaque-run", "opaque-latest"
			c := &client.SummaryController{Options: application.SummaryOptions{Models: []application.SummaryModelOption{{ID: "opaque-model", Name: "Synthetic model", Model: "Synthetic API", Enabled: true, IsDefault: true}}, Templates: []domain.SummaryTemplate{{ID: "opaque-template", Name: "Synthetic template", IncludeSpeaker: true}}}, Result: &domain.Result{Job: domain.JobSummary{Title: "Synthetic meeting", ActiveSummaryRunID: &runID, SourceFiles: []domain.SourceFile{{Name: "meeting-a.wav"}, {Name: "meeting-b.m4a"}}}, Transcript: []domain.Segment{{Text: "Synthetic"}}}, Runs: []domain.SummaryRun{{ID: runID, UpdatedAt: "2026-11-17T00:00:00Z", ModelConfigID: "opaque-model", TemplateID: "opaque-template", Status: status, Result: &domain.SummaryResult{Overview: "Synthetic overview"}}, {ID: latestID, UpdatedAt: "2026-11-18T00:00:00Z", ModelConfigID: "opaque-model", TemplateID: "opaque-template", Status: "completed", Result: &domain.SummaryResult{Overview: "Latest overview"}}}, SelectedID: runID}
			c.Runs[0].UpdatedAt = "2026-11-17T00:00:00Z"
			c.Runs[1].UpdatedAt = "2026-11-18T00:00:00Z"
			v := &SummaryView{Client: c, Locale: locale, Preferences: preferences}
			tt := ui.NewTester(v.View, 1120, 860)
			for _, key := range []string{"currentConfig", "model", "template", "history", "preview"} {
				if !tt.HasText(v.msg(key)) {
					t.Fatal("original AI field missing", locale, key)
				}
			}
			for _, text := range []string{"Synthetic meeting", "Synthetic model · Synthetic API", "Synthetic template", "Synthetic overview", "meeting-a.wav · meeting-b.m4a"} {
				if !tt.HasText(text) {
					t.Fatal("public names or result missing", text)
				}
			}
			if !tt.HasText(v.msg("currentResult")) || !tt.HasText(v.msg("latest")) {
				t.Fatal("original current/latest result markers missing", locale, tt.Texts())
			}
			for _, text := range []string{"opaque-model", "opaque-template", "opaque-run", "{count}"} {
				if tt.HasText(text) {
					t.Fatal("implementation ID/unformatted text shown")
				}
			}
			if status == "running" && tt.HasText(v.msg("submit")) {
				t.Fatal("duplicate enabled submit button while running")
			}
			if v.modelID != "opaque-model" || v.templateID != "opaque-template" {
				t.Fatal("default options did not resolve to IDs")
			}
			if strings.Contains(v.format("heroCopy", map[string]any{"count": 1}), "{count}") {
				t.Fatal("placeholder leaked")
			}
		}
	}
}

func TestSummaryErrorUsesMissingJobMessage(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := &SummaryView{Locale: locale}
		if got, want := summaryError(locale, application.ErrJobNotFound), view.msg("jobNotFound"); got != want {
			t.Fatalf("missing job error was not localized for %s: got %q want %q", locale, got, want)
		}
	}
}

func TestSummaryPreviewUsesSavedStatusWithoutHistory(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		preferences := domain.DefaultUIPreferences()
		preferences.Locale = locale
		view := &SummaryView{Client: &client.SummaryController{Result: &domain.Result{
			Job:     domain.JobSummary{Title: "Synthetic saved meeting"},
			Summary: domain.Summary{Overview: "Previously saved overview"},
		}}, Locale: locale, Preferences: preferences}
		tester := ui.NewTester(view.View, 1120, 860)
		completed, idle := 0, 0
		for _, control := range tester.AccessAll() {
			if control.Name == statusText(locale, "completed") {
				completed++
			}
			if control.Name == statusText(locale, "idle") {
				idle++
			}
		}
		if completed < 2 || idle != 0 {
			t.Fatal("saved summary without history was presented as idle", locale, tester.Texts())
		}
	}
}

func TestSummaryRefreshPreservesCurrentSnapshotAndStatus(t *testing.T) {
	preferences := domain.DefaultUIPreferences()
	preferences.Locale = "en-US"
	result := &domain.Result{Job: domain.JobSummary{Title: "Synthetic", SummaryStatus: "summarizing", ActiveSummaryRunID: stringPointer("run-1")}, SummaryRuns: []domain.SummaryRun{{ID: "run-1", Status: "running"}}}
	view := &SummaryView{Client: &client.SummaryController{Result: result, Runs: result.SummaryRuns, SelectedID: "run-1"}, Locale: "en-US", Preferences: preferences}
	tester := ui.NewTester(view.View, 1120, 860)
	if !tester.HasText("Generating") || !tester.HasText("Current Result") {
		t.Fatal("running status/current marker missing")
	}
	view.Client.Loading = true
	tester.Frame()
	if !tester.HasText("Synthetic") || !tester.HasText("Generating") {
		t.Fatal("background refresh hid existing summary")
	}
}

func stringPointer(value string) *string { return &value }

func TestSummaryWindowFirstLoad(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		closed := false
		view := SummaryView{Client: &client.SummaryController{Loading: true}, Locale: locale, Preferences: domain.DefaultUIPreferences(), Close: func() { closed = true }}
		tester := ui.NewTester(view.View, 1120, 860)
		if !tester.HasText(localized(locale, "正在加载任务结果", "Loading the job result")) {
			t.Fatal("missing loading state", locale)
		}
		if err := tester.Click(presentation.Message(locale, "common.closeWindow")); err != nil || !closed {
			t.Fatal("loading summary has no close action", err)
		}
	}
}

func TestSummaryUnavailableSpeakersDoNotClaimAttribution(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			preferences := domain.DefaultUIPreferences()
			preferences.Locale = locale
			speaker := "legacy speaker"
			controller := &client.SummaryController{Options: application.SummaryOptions{Templates: []domain.SummaryTemplate{{ID: "template", Name: "Synthetic template", IncludeSpeaker: true}}}, Result: &domain.Result{Job: domain.JobSummary{EnableSpeaker: true, DiarizationStatus: "legacy_unverified"}, Speakers: []domain.Segment{{Speaker: &speaker}}}}
			view := &SummaryView{Client: controller, Locale: locale, Preferences: preferences, useMembers: true}
			tester := ui.NewTester(view.View, 1120, 860)
			for _, key := range []string{"includeSpeaker", "useMemberMapping"} {
				control, ok := tester.Access(view.msg(key))
				if !ok || !control.Disabled || control.Checked {
					t.Fatal("unverified speakers were displayed as attributed", key, control)
				}
			}
			controller.Result.Job.DiarizationStatus = "completed"
			tester.Frame()
			for _, key := range []string{"includeSpeaker", "useMemberMapping"} {
				control, ok := tester.Access(view.msg(key))
				if !ok || control.Disabled || !control.Checked {
					t.Fatal("verified speakers did not restore intended attribution", key, control)
				}
			}
		})
	}
}

type summaryViewPort struct {
	state   application.SummaryWindowState
	forms   chan application.SummaryForm
	deleted chan string
}

func (p *summaryViewPort) SummaryState(context.Context) (application.SummaryWindowState, error) {
	return p.state, nil
}
func (p *summaryViewPort) GenerateSummary(_ context.Context, form application.SummaryForm) error {
	p.forms <- form
	return nil
}
func (p *summaryViewPort) SelectSummary(context.Context, string) error { return nil }
func (p *summaryViewPort) DeleteSummary(_ context.Context, id string) error {
	p.deleted <- id
	return nil
}

func TestSummaryFailedSubmitResumesSnapshotAndRunningRunCanBeDeleted(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			preferences := domain.DefaultUIPreferences()
			preferences.Locale = locale
			run := domain.SummaryRun{ID: "failed-snapshot", Status: "failed"}
			port := &summaryViewPort{state: application.SummaryWindowState{Result: domain.Result{Job: domain.JobSummary{Title: "Synthetic meeting"}}, Runs: []domain.SummaryRun{run}}, forms: make(chan application.SummaryForm, 2), deleted: make(chan string, 2)}
			callbacks := make(chan func(), 2)
			controller := client.NewSummaryController(port, func(fn func()) { callbacks <- fn }, func() {})
			defer controller.Close()
			controller.Result, controller.Runs, controller.SelectedID = &port.state.Result, port.state.Runs, run.ID
			v := &SummaryView{Client: controller, Locale: locale, Preferences: preferences, Confirm: func(context.Context, string) (bool, error) { return true, nil }}
			tester := ui.NewTester(v.View, 1120, 860)
			assertDisabled(t, tester, v.msg("submit"), false)
			if err := tester.Click(v.msg("submit")); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tester)
			select {
			case form := <-port.forms:
				if form.RunID != run.ID || form.ModelID != "" || form.TemplateID != "" {
					t.Fatal("failed run was replaced instead of resuming its saved snapshot", form)
				}
			default:
				t.Fatal("failed summary was not retried")
			}
			controller.Runs[0].Status = "running"
			tester.Frame()
			assertDisabled(t, tester, v.msg("deleteCurrent"), false)
			if err := tester.Click(v.msg("deleteCurrent")); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tester)
			select {
			case id := <-port.deleted:
				if id != run.ID {
					t.Fatal("deleted a different run")
				}
			default:
				t.Fatal("running summary could not be deleted")
			}
		})
	}
}

func TestSummaryPreviewFallsBackToSavedNotesWhileRunHasNoResult(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, status := range []string{"running", "failed"} {
			t.Run(locale+"/"+status, func(t *testing.T) {
				preferences := domain.DefaultUIPreferences()
				preferences.Locale = locale
				controller := &client.SummaryController{Result: &domain.Result{Job: domain.JobSummary{Title: "Synthetic meeting"}, Summary: domain.Summary{Overview: "Saved overview", ActionItems: []string{"Saved task"}}}, Runs: []domain.SummaryRun{{ID: "pending", Status: status}}, SelectedID: "pending"}
				view := &SummaryView{Client: controller, Locale: locale, Preferences: preferences}
				tester := ui.NewTester(view.View, 1120, 1000)
				if !tester.HasText("Saved overview") || !tester.HasText("Saved task") || tester.HasText(presentation.Message(locale, "notes.emptySummary")) {
					t.Fatal("unfinished run hid existing notes", tester.Texts())
				}
				label := view.msg("activeLabelSaved")
				if status == "running" {
					label = view.msg("activeLabelRunning")
				}
				if !tester.HasText(view.format("currentStatus", map[string]any{"status": label})) {
					t.Fatal("current summary status contradicts saved notes", tester.Texts())
				}
			})
		}
	}
}
