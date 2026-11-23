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

func resultParityView(locale string, count int) *ResultView {
	result := &domain.Result{Job: domain.JobSummary{ID: "synthetic-result", Source: "local", Title: "Synthetic meeting", OverallStatus: "completed", DiarizationStatus: "completed"}}
	for index := range count {
		speaker := fmt.Sprintf("Speaker %d", index%2)
		result.Speakers = append(result.Speakers, domain.Segment{ID: fmt.Sprintf("segment-%04d", index), Speaker: &speaker, Text: fmt.Sprintf("Synthetic transcript %04d", index), StartMS: uint64(index * 1000), EndMS: uint64((index + 1) * 1000)})
	}
	view := NewResultView(&client.ResultController{Result: result}, locale)
	view.Close = func() {}
	view.EditingEnabled, view.WordExportEnabled = true, true
	return view
}

func TestResultLongTextKeepsActionsAndVirtualTranscriptUsable(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{960, 1280} {
			t.Run(fmt.Sprintf("%s/%d", locale, width), func(t *testing.T) {
				view := resultParityView(locale, 400)
				result := view.Client.Result
				result.Job.Title = strings.Repeat("合成长标题会议记录", 12)
				text := strings.Repeat("合成逐字稿应当在窄窗口折行且保留完整复制内容。", 14)
				longSpeaker := strings.Repeat("合成讲话人长姓名", 12)
				if locale == "en-US" {
					result.Job.Title = strings.Repeat("Synthetic meeting with a long title ", 9)
					text = strings.Repeat("A synthetic transcript must wrap and remain selectable inside a narrow result window. ", 12)
					longSpeaker = strings.Repeat("Synthetic participant ", 8)
				}
				longSpeaker = strings.TrimSpace(longSpeaker)
				result.Speakers[0].Speaker = &longSpeaker
				result.Speakers[0].Text = text + "\n  " + strings.Repeat("unbroken", 32)
				view.Actions = &client.ResultActionController{}
				tester := ui.NewTesterWith(view.View, width, 720, ui.TesterOptions{Dark: locale == "en-US"})
				filter, ok := tester.Access(fmt.Sprintf("%s 1", longSpeaker))
				if !ok || filter.Offscreen || filter.Bounds.X+filter.Bounds.W > float32(width) || filter.Bounds.H < 15 {
					t.Fatal("long speaker filter overflowed the window", filter)
				}
				for _, label := range []string{presentation.Message(locale, "common.closeWindow"), view.msg("exportBundle"), view.msg("searchPlaceholder"), presentation.Message(locale, "common.edit")} {
					control, ok := tester.Access(label)
					if !ok || control.Offscreen || control.Bounds.W < 30 || control.Bounds.H < 15 || control.Bounds.X < 0 || control.Bounds.X+control.Bounds.W > float32(width) || control.Bounds.Y+control.Bounds.H > 720 {
						t.Fatal("long title displaced a workbench control", label, control)
					}
				}
				list, ok := tester.AccessByRole(ui.RoleList, view.msg("transcript"))
				if !ok || list.Bounds.H < 120 || list.Bounds.Y+list.Bounds.H > 720 {
					t.Fatal("long title squeezed the virtual transcript out of the window", list)
				}
				paragraph, ok := tester.Find(result.Speakers[0].Text)
				if !ok || paragraph.W < 100 || paragraph.H < 40 || paragraph.X+paragraph.W > float32(width) {
					t.Fatal("long transcript did not visibly wrap inside the pane", paragraph)
				}
				if err := tester.Click(result.Speakers[0].Text); err != nil {
					t.Fatal(err)
				}
				tester.Command("selectAll")
				tester.Command("copy")
				if tester.Clipboard() != result.Speakers[0].Text {
					t.Fatal("long transcript copy lost text or whitespace")
				}
				items := 0
				for _, node := range tester.AccessAll() {
					if node.Role == ui.RoleListItem {
						items++
						if node.PosInSet < 1 || node.SetSize != 400 {
							t.Fatal("virtual transcript lost list position semantics", node)
						}
					}
				}
				if items == 0 || items > 30 {
					t.Fatal("transcript stopped virtualizing its rows", items)
				}
				captureParity(t, tester, fmt.Sprintf("result-%s-%d", locale, width))
			})
		}
	}
}

func TestResultKeyboardSearchFiltersAndScrollsVirtualRows(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := resultParityView(locale, 400)
			tester := ui.NewTester(view.View, 960, 720)
			if err := tester.Click(view.msg("searchPlaceholder")); err != nil {
				t.Fatal(err)
			}
			tester.Type("TRANSCRIPT 0201")
			tester.Key(0, ui.KeyEnter)
			if !tester.HasText("Synthetic transcript 0201") || tester.HasText("Synthetic transcript 0000") || !tester.Focused(view.msg("searchPlaceholder")) {
				t.Fatal("keyboard search did not filter the transcript in place")
			}
			if err := tester.Click("Speaker 0 200"); err != nil {
				t.Fatal(err)
			}
			choice, ok := tester.AccessByRole(ui.RoleToggleButton, "Speaker 0 200")
			if !ok || !choice.Checked || view.speaker != "Speaker 0" {
				t.Fatal("speaker filter does not expose its active state", choice)
			}
			if _, ok := tester.AccessByRole(ui.RoleStatus, view.msg("emptyFilteredTranscript")); !ok || tester.HasText("Synthetic transcript 0201") {
				t.Fatal("combined speaker and text filtering did not expose an empty result")
			}
			if err := tester.Click(view.msg("searchPlaceholder")); err != nil {
				t.Fatal(err)
			}
			tester.Command("selectAll")
			tester.Key(0, ui.KeyBackspace)
			if err := tester.Click("Speaker 1 200"); err != nil {
				t.Fatal(err)
			}
			tester.Key(0, ui.KeyTab)
			if !tester.Focused(view.msg("transcript")) {
				t.Fatal("Tab from speaker filters cannot reach the virtual transcript")
			}
			tester.Key(0, ui.KeyEnd)
			last, ok := tester.Find("Synthetic transcript 0399")
			if !ok || last.H < 10 || last.Y+last.H > 720 {
				t.Fatal("keyboard scrolling cannot reach the last filtered segment", last)
			}
			tester.Key(0, ui.KeyHome)
			if !tester.HasText("Synthetic transcript 0001") {
				t.Fatal("keyboard scrolling cannot return to the first filtered segment")
			}
		})
	}
}

func TestTranscriptProjectionCacheTracksResultAndDisplaySourceChanges(t *testing.T) {
	view := resultParityView("en-US", 2)
	result := view.Client.Result

	options, visible := view.transcriptData(result, result.DisplaySegments())
	if len(options) != 2 || len(visible) != 2 {
		t.Fatal("initial transcript projection is incomplete", options, len(visible))
	}
	optionsData := &options[0]
	view.query = "TRANSCRIPT 0001"
	filteredOptions, filtered := view.transcriptData(result, result.DisplaySegments())
	if &filteredOptions[0] != optionsData || len(filtered) != 1 || filtered[0].ID != "segment-0001" {
		t.Fatal("query changes rebuilt source projection or returned stale rows", filteredOptions, filtered)
	}

	view.query = ""
	result.Speakers = append([]domain.Segment(nil), result.Speakers...)
	result.Speakers[0].Text = "Replacement transcript"
	options, visible = view.transcriptData(result, result.DisplaySegments())
	if len(visible) != 2 || !strings.Contains(visible[0].Text, "Replacement") {
		t.Fatal("same-result source replacement retained stale transcript rows", visible)
	}

	replacement := *result
	replacement.Job.ID = result.Job.ID
	replacement.Speakers = append([]domain.Segment(nil), result.Speakers...)
	replacement.Speakers[0].Text = "Same-ID refreshed transcript"
	options, visible = view.transcriptData(&replacement, replacement.DisplaySegments())
	if len(options) != 2 || len(visible) != 2 || visible[0].Text != "Same-ID refreshed transcript" {
		t.Fatal("same-ID result refresh retained stale transcript rows", visible)
	}

	speaker := "Legacy speaker"
	legacy := &domain.Result{Job: domain.JobSummary{ID: "legacy", DiarizationStatus: "legacy_unverified"}, Transcript: []domain.Segment{{ID: "legacy-segment", Text: "Legacy transcript", StartMS: 0, EndMS: 1000}}, Speakers: []domain.Segment{{ID: "legacy-speaker", Speaker: &speaker, Text: "Legacy transcript", StartMS: 0, EndMS: 1000}}}
	legacyOptions, _ := view.transcriptData(legacy, legacy.DisplaySegments())
	if len(legacyOptions) != 1 || legacyOptions[0].label != "Legacy speaker" {
		t.Fatal("legacy speaker projection was not displayed", legacyOptions)
	}
	legacy.Speakers[0].EndMS++
	legacyOptions, legacyVisible := view.transcriptData(legacy, legacy.DisplaySegments())
	if len(legacyOptions) != 1 || legacyOptions[0].label != presentation.Message("en-US", "common.unknownSpeaker") || len(legacyVisible) != 1 || legacyVisible[0].ID != "legacy-segment" {
		t.Fatal("legacy projection fallback retained stale speaker filters", legacyOptions, legacyVisible)
	}
}

func TestWorkbenchShowsWhetherAnAISummaryResultIsSelected(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			result := &domain.Result{
				Job:         domain.JobSummary{ID: "synthetic-result", Source: "local", Title: "Synthetic meeting"},
				SummaryRuns: []domain.SummaryRun{{ID: "completed-run", Status: "completed"}, {ID: "other-run", Status: "failed"}},
			}
			view := NewResultView(&client.ResultController{Result: result}, locale)
			tester := ui.NewTester(view.View, 1000, 760)
			if !tester.HasText(view.msg("summaryEmpty")) || tester.HasText(view.msg("activeResultReady")) {
				t.Fatal("workbench reported a selected summary without an active run")
			}

			activeID := "completed-run"
			result.Job.ActiveSummaryRunID = &activeID
			tester.Frame()
			if !tester.HasText(view.msg("activeResultReady")) || tester.HasText(view.msg("summaryEmpty")) {
				t.Fatal("workbench did not report the selected summary result")
			}

			activeID = "missing-run"
			tester.Frame()
			if !tester.HasText(view.msg("summaryEmpty")) || tester.HasText(view.msg("activeResultReady")) {
				t.Fatal("workbench treated an unmatched active run ID as a selected result")
			}
		})
	}
}

func TestResultSpeakerCancelClearsDraftAndRestoresKeyboardFocus(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := resultParityView(locale, 2)
			port := &workbenchPort{}
			callbacks := make(chan func(), 4)
			view.Actions = client.NewResultActions(port, port, func(context.Context, string, string, string) (bool, error) { return true, nil }, func(callback func()) { callbacks <- callback }, func() {}, func(string, string) {})
			defer view.Actions.Close()
			tester := ui.NewTester(view.View, 960, 720)
			edit := presentation.Message(locale, "common.edit")
			if err := tester.Click(edit); err != nil {
				t.Fatal(err)
			}
			tester.Command("selectAll")
			tester.Type("discard this draft")
			tester.Key(0, ui.KeyEscape)
			if view.editingSegment != "" || view.originalSpeaker != "" || view.draftSpeaker != "" || !tester.Focused(edit) || port.to != "" {
				t.Fatal("Escape did not clear the editor and return focus without renaming")
			}
			tester.Key(0, ui.KeyEnter)
			tester.Key(0, ui.KeyTab)
			if !tester.Focused(presentation.Message(locale, "common.save")) {
				t.Fatal("Tab from speaker input did not reach Save")
			}
			tester.Key(0, ui.KeyEscape)
			if view.editingSegment != "" || !tester.Focused(edit) {
				t.Fatal("Escape from the editor actions did not cancel and restore focus")
			}
			tester.Key(0, ui.KeyEnter)
			tester.Command("selectAll")
			tester.Type("   ")
			tester.Key(0, ui.KeyEnter)
			if view.editingSegment == "" || view.Actions.Busy {
				t.Fatal("empty speaker was submitted")
			}
			tester.Command("selectAll")
			tester.Type("Renamed speaker")
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			if port.from != "Speaker 0" || port.to != "Renamed speaker" || view.editingSegment != "" || view.originalSpeaker != "" || view.draftSpeaker != "" {
				t.Fatal("Enter did not submit the bound speaker once and clear its draft", port)
			}
		})
	}
}

type resultParityFiles struct{ failure error }

func (files *resultParityFiles) Export(context.Context, domain.TextExportKind, string, string) (bool, error) {
	return files.failure == nil, files.failure
}

func TestResultExportFailureAndSuccessStayReadableAndRecover(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := resultParityView(locale, 2)
			files := &resultParityFiles{failure: errors.New("synthetic export failure")}
			callbacks := make(chan func(), 4)
			view.Actions = client.NewResultActions(nil, files, nil, func(callback func()) { callbacks <- callback }, func() {}, func(string, string) {})
			defer view.Actions.Close()
			tester := ui.NewTester(view.View, 960, 720)
			if err := tester.Click(view.msg("exportBundle")); err != nil {
				t.Fatal(err)
			}
			assertDisabled(t, tester, view.msg("exporting"), true)
			flushView(t, callbacks, tester)
			message := view.text("操作失败，请重试。", "The operation failed. Please retry.")
			alert, ok := tester.AccessByRole(ui.RoleAlert, message)
			if !ok || alert.Offscreen || alert.Bounds.H < 10 {
				t.Fatal("export failure is not an accessible, visible alert", alert)
			}
			if err := tester.Click(message); err != nil {
				t.Fatal(err)
			}
			tester.Command("selectAll")
			tester.Command("copy")
			if tester.Clipboard() != message || tester.HasText("synthetic export failure") {
				t.Fatal("operation error cannot be copied safely")
			}
			files.failure = nil
			if err := tester.Click(view.msg("exportBundle")); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tester)
			if _, ok := tester.AccessByRole(ui.RoleStatus, view.text("文件已导出。", "File exported.")); !ok || tester.HasText(message) {
				t.Fatal("successful retry did not replace the error with export feedback")
			}
			for _, heading := range []struct {
				label string
				level int
			}{{view.Client.Result.Job.Title, 1}, {view.msg("transcript"), 2}} {
				node, ok := tester.AccessByRole(ui.RoleHeading, heading.label)
				if !ok || node.Level != heading.level {
					t.Fatal("workbench lacks its heading hierarchy", heading, node)
				}
			}
		})
	}
}
