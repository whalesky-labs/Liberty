package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func notesParityResult(locale string) *domain.Result {
	paragraph := strings.Repeat("这是一段用于验证窄窗口真实折行与完整复制的合成会议纪要。", 8)
	title := strings.Repeat("合成会议纪要长标题", 7)
	if locale == "en-US" {
		paragraph = strings.Repeat("Synthetic meeting notes must wrap and remain fully selectable in a narrow window. ", 8)
		title = strings.Repeat("Synthetic meeting notes with a long title ", 5)
	}
	return &domain.Result{
		Job: domain.JobSummary{ID: "synthetic-notes", Source: "local", Title: title, SummaryStatus: "completed"},
		Summary: domain.Summary{
			Overview: paragraph + "\n  " + strings.Repeat("unbroken", 32),
			Topics:   []string{paragraph}, Decisions: []string{"Synthetic decision"},
			ActionItems: []string{"Synthetic action"}, Risks: []string{"Synthetic risk"}, FollowUps: []string{"Synthetic follow-up"},
		},
	}
}

func TestNotesLongTextWrapsAndCopiesAcrossSectionsAtNarrowAndWideSizes(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dark=%v", locale, dark), func(t *testing.T) {
				preferences := domain.DefaultUIPreferences()
				preferences.Locale = locale
				result := notesParityResult(locale)
				closed := 0
				view := &NotesView{Client: &client.ResultController{Result: result}, Preferences: preferences, Close: func() { closed++ }}
				tester := ui.NewTesterWith(view.View, 1120, 860, ui.TesterOptions{Dark: dark})
				tester.Key(0, ui.KeyTab)
				tester.Key(0, ui.KeyTab)
				if !tester.Focused(presentation.Message(locale, "notes.sectionTitle")) {
					t.Fatal("Tab cannot reach the notes scroll area")
				}
				tester.Key(0, ui.KeyEnd)
				last, ok := tester.AccessByRole(ui.RoleHeading, presentation.Message(locale, "notes.followUps"))
				if !ok || last.Offscreen || last.Bounds.H < 10 {
					t.Fatal("keyboard scrolling cannot reach the last notes section", last)
				}
				tester.Key(0, ui.KeyHome)
				wide, ok := tester.Find(result.Summary.Overview)
				if !ok || wide.H < 60 {
					t.Fatal("long overview is not visibly multiline", wide)
				}
				for _, size := range []struct{ width, height int }{{920, 720}, {640, 720}, {1120, 860}} {
					tester.SetSize(size.width, size.height)
					tester.Frame()
					closeLabel := presentation.Message(locale, "common.closeWindow")
					closeButton, ok := tester.AccessByRole(ui.RoleButton, closeLabel)
					if !ok || closeButton.Offscreen || closeButton.Bounds.W < 20 || closeButton.Bounds.X < 0 || closeButton.Bounds.X+closeButton.Bounds.W > float32(size.width) || closeButton.Bounds.Y+closeButton.Bounds.H > float32(size.height) {
						t.Fatal("long title displaced the close action", size, closeButton)
					}
					overview, ok := tester.Find(result.Summary.Overview)
					if !ok || overview.W < 100 || overview.X < 0 || overview.X+overview.W > float32(size.width) || overview.H < 60 || size.width == 920 && overview.H <= wide.H {
						t.Fatal("long paragraph did not reflow within the window", size, wide, overview)
					}
					if err := tester.Click(closeLabel); err != nil {
						t.Fatal("close action cannot be clicked", err)
					}
					captureParity(t, tester, fmt.Sprintf("notes-%s-dark-%v-%d", locale, dark, size.width))
				}
				if closed != 3 {
					t.Fatal("close action was not invoked once per click", closed)
				}
				if err := tester.Click(result.Summary.Overview); err != nil {
					t.Fatal(err)
				}
				tester.Command("selectAll")
				tester.Command("copy")
				parts := []string{presentation.Message(locale, "notes.summary"), result.Summary.Overview}
				for _, section := range []struct {
					key   string
					items []string
				}{{"topics", result.Summary.Topics}, {"decisions", result.Summary.Decisions}, {"actionItems", result.Summary.ActionItems}, {"risks", result.Summary.Risks}, {"followUps", result.Summary.FollowUps}} {
					parts = append(parts, presentation.Message(locale, "notes."+section.key))
					for _, item := range section.items {
						parts = append(parts, "• "+item)
					}
				}
				if tester.Clipboard() != strings.Join(parts, "\n") {
					t.Fatal("copy did not preserve the full document, whitespace and offscreen sections")
				}
				tester.Type("must not edit")
				if !tester.HasText(result.Summary.Overview) {
					t.Fatal("read-only notes accepted editing")
				}
				tester.Key(0, ui.KeyTab)
				if !tester.Focused(presentation.Message(locale, "common.closeWindow")) {
					t.Fatal("Tab from selected notes did not reach the close action")
				}
				tester.Key(0, ui.KeyEnter)
				if closed != 4 {
					t.Fatal("keyboard close did not invoke the close action")
				}
			})
		}
	}
}

func TestNotesExposeHeadingListAndStatusSemantics(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			preferences := domain.DefaultUIPreferences()
			preferences.Locale = locale
			result := notesParityResult(locale)
			view := &NotesView{Client: &client.ResultController{Result: result}, Preferences: preferences, Close: func() {}}
			tester := ui.NewTester(view.View, 920, 720)
			for _, heading := range []struct {
				label string
				level int
			}{{result.Job.Title, 1}, {presentation.Message(locale, "notes.sectionTitle"), 2}, {presentation.Message(locale, "notes.summary"), 3}, {presentation.Message(locale, "notes.topics"), 3}, {presentation.Message(locale, "notes.decisions"), 3}, {presentation.Message(locale, "notes.actionItems"), 3}, {presentation.Message(locale, "notes.risks"), 3}, {presentation.Message(locale, "notes.followUps"), 3}} {
				node, ok := tester.AccessByRole(ui.RoleHeading, heading.label)
				if !ok || node.Level != heading.level {
					t.Fatal("notes heading lost its hierarchy", heading, node)
				}
			}
			for _, section := range []struct {
				key  string
				item string
			}{{"topics", result.Summary.Topics[0]}, {"decisions", result.Summary.Decisions[0]}, {"actionItems", result.Summary.ActionItems[0]}, {"risks", result.Summary.Risks[0]}, {"followUps", result.Summary.FollowUps[0]}} {
				list, ok := tester.AccessByRole(ui.RoleList, presentation.Message(locale, "notes."+section.key))
				item, itemOK := tester.AccessByRole(ui.RoleListItem, "• "+section.item)
				if !ok || !itemOK || item.PosInSet != 1 || item.SetSize != 1 || list.Name == "" {
					t.Fatal("notes lost the original list semantics", section.key, list, item)
				}
			}
			if _, ok := tester.AccessByRole(ui.RoleStatus, statusText(locale, "completed")); !ok {
				t.Fatal("summary status is not exposed as a status")
			}
			result.Job.SummaryStatus = "summarizing"
			tester.Frame()
			statuses := 0
			for _, node := range tester.AccessAll() {
				if node.Role == ui.RoleStatus {
					statuses++
					if node.Name != statusText(locale, "summarizing") {
						t.Fatal("summary status was not updated", node)
					}
				}
			}
			if statuses != 1 {
				t.Fatal("summary has duplicate or missing live status regions", statuses)
			}
			view.Client.ResultError = errors.New("synthetic refresh failure")
			tester.Frame()
			if _, ok := tester.AccessByRole(ui.RoleAlert, presentation.Message(locale, "workbench.loadFailed")); !ok || !tester.HasText(result.Summary.Overview) {
				t.Fatal("refresh failure did not expose an alert while retaining the snapshot")
			}
		})
	}
}

type notesParityReply struct {
	result domain.Result
	err    error
}

type notesParityReader struct{ replies chan notesParityReply }

func (reader notesParityReader) Result(ctx context.Context, id string) (domain.Result, error) {
	select {
	case reply := <-reader.replies:
		return reply.result, reply.err
	case <-ctx.Done():
		return domain.Result{}, ctx.Err()
	}
}

func TestNotesKeyboardRetryKeepsLoadingUntilResultArrives(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{920, 1120} {
			t.Run(fmt.Sprintf("%s/%d", locale, width), func(t *testing.T) {
				preferences := domain.DefaultUIPreferences()
				preferences.Locale = locale
				callbacks := make(chan func(), 4)
				reader := notesParityReader{replies: make(chan notesParityReply, 2)}
				controller := client.NewResult(reader, func(callback func()) { callbacks <- callback }, func() {})
				defer controller.Close()
				view := &NotesView{Client: controller, Preferences: preferences, Close: func() {}}
				controller.Select("synthetic-notes")
				tester := ui.NewTester(view.View, width, 720)
				assertNotesLoading := func() {
					t.Helper()
					status, ok := tester.AccessByRole(ui.RoleStatus, presentation.Message(locale, "workbench.loading"))
					region, regionOK := tester.AccessByRole(ui.RoleScroll, presentation.Message(locale, "notes.sectionTitle"))
					if !ok || status.Offscreen || !regionOK || !region.Busy || tester.HasText(presentation.Message(locale, "workbench.notFound")) || tester.HasText(presentation.Message(locale, "notes.emptySummary")) {
						t.Fatal("pending notes query lacks loading semantics", status, region)
					}
				}
				assertNotesLoading()
				reader.replies <- notesParityReply{err: errors.New("synthetic read failure")}
				flushView(t, callbacks, tester)
				if _, ok := tester.AccessByRole(ui.RoleAlert, presentation.Message(locale, "workbench.loadFailed")); !ok {
					t.Fatal("failed query lacks an alert")
				}
				tester.Key(0, ui.KeyTab)
				if !tester.Focused(presentation.Message(locale, "common.closeWindow")) {
					t.Fatal("first Tab did not reach close")
				}
				tester.Key(0, ui.KeyTab)
				if !tester.Focused(presentation.Message(locale, "common.retry")) {
					t.Fatal("second Tab did not reach retry")
				}
				tester.Key(0, ui.KeyEnter)
				assertNotesLoading()
				reader.replies <- notesParityReply{err: errors.New("synthetic retry failure")}
				flushView(t, callbacks, tester)
				region, _ := tester.AccessByRole(ui.RoleScroll, presentation.Message(locale, "notes.sectionTitle"))
				if _, ok := tester.AccessByRole(ui.RoleAlert, presentation.Message(locale, "workbench.loadFailed")); !ok || region.Busy {
					t.Fatal("failed retry did not leave the loading state")
				}
				if err := tester.Click(presentation.Message(locale, "common.retry")); err != nil {
					t.Fatal("retry was not available after another failure", err)
				}
				assertNotesLoading()
				reader.replies <- notesParityReply{result: *notesParityResult(locale)}
				flushView(t, callbacks, tester)
				if !tester.HasText(notesParityResult(locale).Summary.Overview) || tester.HasText(presentation.Message(locale, "workbench.loading")) || tester.HasText(presentation.Message(locale, "common.retry")) {
					t.Fatal("successful retry did not restore notes")
				}
			})
		}
	}
}

func TestNotesRetryKeepsFeedbackWhileRefreshingVisibleSnapshot(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			preferences := domain.DefaultUIPreferences()
			preferences.Locale = locale
			callbacks := make(chan func(), 4)
			reader := notesParityReader{replies: make(chan notesParityReply, 3)}
			controller := client.NewResult(reader, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			view := &NotesView{Client: controller, Preferences: preferences, Close: func() {}}
			controller.Select("synthetic-notes")
			tester := ui.NewTester(view.View, 920, 720)
			result := notesParityResult(locale)
			reader.replies <- notesParityReply{result: *result}
			flushView(t, callbacks, tester)
			if !tester.HasText(result.Summary.Overview) {
				t.Fatal("initial notes snapshot was not loaded")
			}

			controller.ResultError = errors.New("synthetic refresh failure")
			tester.Frame()
			if _, ok := tester.AccessByRole(ui.RoleAlert, presentation.Message(locale, "workbench.loadFailed")); !ok {
				t.Fatal("failed background refresh did not expose retry")
			}
			if err := tester.Click(presentation.Message(locale, "common.retry")); err != nil {
				t.Fatal(err)
			}
			region, ok := tester.AccessByRole(ui.RoleScroll, presentation.Message(locale, "notes.sectionTitle"))
			loading, loadingOK := tester.AccessByRole(ui.RoleStatus, presentation.Message(locale, "workbench.loading"))
			if !ok || !region.Busy || !loadingOK || loading.Offscreen || !tester.HasText(result.Summary.Overview) || tester.HasText(presentation.Message(locale, "workbench.notFound")) {
				t.Fatal("retry hid the saved snapshot or gave no pending feedback", region, loading)
			}
			if _, retryVisible := tester.AccessByRole(ui.RoleButton, presentation.Message(locale, "common.retry")); retryVisible {
				t.Fatal("retry remained available while its request was pending")
			}
			reader.replies <- notesParityReply{err: errors.New("synthetic retry failure")}
			flushView(t, callbacks, tester)
			region, _ = tester.AccessByRole(ui.RoleScroll, presentation.Message(locale, "notes.sectionTitle"))
			if _, ok := tester.AccessByRole(ui.RoleAlert, presentation.Message(locale, "workbench.loadFailed")); !ok || region.Busy || !tester.HasText(result.Summary.Overview) {
				t.Fatal("failed retry did not restore the error and saved snapshot", region)
			}
		})
	}
}
