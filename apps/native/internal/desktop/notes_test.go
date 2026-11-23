package desktop

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestNotesWindowKeepsOriginalReadOnlySections(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			preferences := domain.DefaultUIPreferences()
			preferences.Locale = locale
			result := &domain.Result{Job: domain.JobSummary{ID: "job-1700000000000-1", Source: "local", Title: "Synthetic notes", SummaryStatus: "completed"}, Summary: domain.Summary{Overview: "Synthetic overview", Topics: []string{"Topic"}, Decisions: []string{"Decision"}, ActionItems: []string{"Action"}, Risks: []string{"Risk"}, FollowUps: []string{"Follow-up"}}}
			closed := false
			v := &NotesView{Client: &client.ResultController{Result: result}, Preferences: preferences, Close: func() { closed = true }}
			tt := ui.NewTester(v.View, 1120, 1200)
			for _, key := range []string{"summary", "topics", "decisions", "actionItems", "risks", "followUps"} {
				if !tt.HasText(presentation.Message(locale, "notes."+key)) {
					t.Fatal("missing original section", key)
				}
			}
			if !tt.HasText("Synthetic overview") {
				t.Fatal("summary not displayed")
			}
			if err := tt.Click(presentation.Message(locale, "common.closeWindow")); err != nil || !closed {
				t.Fatal("close action", err)
			}
			result.Summary = domain.EmptySummary()
			tt.Frame()
			if !tt.HasText(presentation.Message(locale, "notes.emptySummary")) || tt.HasText(presentation.Message(locale, "notes.topics")) {
				t.Fatal("empty summary behavior differs")
			}
		})
	}
}

func TestNotesLoadingFailureAndMissingDoNotClaimEmptySummary(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, state := range []string{"loading", "failed", "missing"} {
			t.Run(locale+"/"+state, func(t *testing.T) {
				preferences := domain.DefaultUIPreferences()
				preferences.Locale = locale
				controller := &client.ResultController{LoadingResult: state == "loading"}
				key := "notFound"
				if state == "loading" {
					key = "loading"
				} else if state == "failed" {
					key = "loadFailed"
					controller.ResultError = errors.New("synthetic_failure")
				}
				closed := false
				view := &NotesView{Client: controller, Preferences: preferences, Close: func() { closed = true }}
				tester := ui.NewTester(view.View, 1120, 860)
				if !tester.HasText(presentation.Message(locale, "workbench."+key)) || tester.HasText(presentation.Message(locale, "notes.emptySummary")) {
					t.Fatal("notes query state was presented as an empty summary", state, tester.Texts())
				}
				if err := tester.Click(presentation.Message(locale, "common.closeWindow")); err != nil || !closed {
					t.Fatal("notes state has no close action", err)
				}
			})
		}
	}
}
