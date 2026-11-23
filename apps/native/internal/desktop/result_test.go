package desktop

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestResultWindowStateKeepsCloseAction(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, state := range []string{"loading", "failed", "missing"} {
			t.Run(locale+"/"+state, func(t *testing.T) {
				controller := &client.ResultController{LoadingResult: state == "loading"}
				if state == "failed" {
					controller.ResultError = errors.New("synthetic_failure")
				}
				view := NewResultView(controller, locale)
				closed := false
				view.Close = func() { closed = true }
				tester := ui.NewTester(view.View, 1000, 760)
				key, role := "notFound", ui.RoleStatus
				if state == "loading" {
					key = "loading"
				} else if state == "failed" {
					key, role = "loadFailed", ui.RoleAlert
				}
				status, ok := tester.AccessByRole(role, view.msg(key))
				if !ok || status.Offscreen || state == "loading" && !status.Busy {
					t.Fatal("result query state lacks accessible feedback", state, status)
				}
				if state == "failed" {
					tester.Key(0, ui.KeyTab)
				}
				tester.Key(0, ui.KeyTab)
				if !tester.Focused(presentation.Message(locale, "common.closeWindow")) {
					t.Fatal("result state has no keyboard close action")
				}
				tester.Key(0, ui.KeyEnter)
				if !closed {
					t.Fatal("keyboard close was not invoked")
				}
			})
		}
	}
}

func TestResultReplacementClearsSpeakerDraftAcrossJobAndSource(t *testing.T) {
	for _, source := range []string{"local", "remote"} {
		t.Run(source, func(t *testing.T) {
			controller := &client.ResultController{Result: &domain.Result{Job: domain.JobSummary{ID: "first", Source: "local"}}}
			view := NewResultView(controller, "en-US")
			tester := ui.NewTester(view.View, 1000, 760)
			view.editingSegment, view.originalSpeaker, view.draftSpeaker = "shared-segment", "previous speaker", "unsaved label"
			view.query, view.speaker = "old search", "previous speaker"
			id := "second"
			if source == "remote" {
				id = "first"
			}
			controller.Result = &domain.Result{Job: domain.JobSummary{ID: id, Source: source}}
			tester.Frame()
			if view.editingSegment != "" || view.originalSpeaker != "" || view.draftSpeaker != "" || view.query != "" || view.speaker != "" {
				t.Fatal("replacement retained the previous job/source's draft or filters")
			}
		})
	}
}

func TestResultWithoutTranscriptCannotOpenSummary(t *testing.T) {
	view := NewResultView(&client.ResultController{Result: &domain.Result{Job: domain.JobSummary{ID: "job", Source: "local"}}}, "en-US")
	view.OpenSummary = func(string) { t.Error("AI opened without transcript") }
	tester := ui.NewTester(view.View, 1000, 760)
	assertDisabled(t, tester, view.msg("aiSummary"), true)
}
