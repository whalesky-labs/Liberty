package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func summaryParityState(locale string) application.SummaryWindowState {
	title := strings.Repeat("合成会议的完整长标题与后续工作", 5)
	name := strings.Repeat("合成长名称配置", 5)
	paragraph := strings.Repeat("合成会议总结需要在窄窗口中完整折行，保留文本和全部后续章节。", 6)
	if locale == "en-US" {
		title = strings.Repeat("Synthetic meeting title with follow-up work ", 4)
		name = strings.Repeat("Synthetic configuration with a long name ", 4)
		paragraph = strings.Repeat("Synthetic summary text must wrap in a narrow window and keep every later section available. ", 6)
	}
	run := domain.SummaryRun{ID: "selected", ModelConfigID: "model", TemplateID: "template", Status: "completed", CreatedAt: "2026-11-19T00:00:00Z", UpdatedAt: "2026-11-19T00:00:00Z", Result: &domain.SummaryResult{
		Overview: paragraph + "\n  " + strings.Repeat("unbroken", 24), Topics: []string{paragraph}, Decisions: []string{"Synthetic decision"},
		ActionItems: []domain.SummaryAction{{Task: "Synthetic task", Owner: "Synthetic owner", DueDate: "2026-11-20"}}, Risks: []string{"Synthetic risk"}, FollowUps: []string{"Synthetic final follow-up"},
	}}
	return application.SummaryWindowState{
		Result: domain.Result{Job: domain.JobSummary{Title: title}, Transcript: []domain.Segment{{Text: "Synthetic transcript"}}},
		Runs:   []domain.SummaryRun{run},
		Options: application.SummaryOptions{
			Models:    []application.SummaryModelOption{{ID: "model", Name: name, Model: "Synthetic API", Enabled: true, IsDefault: true}, {ID: "other-model", Name: "Other model", Model: "Other API", Enabled: true}},
			Templates: []domain.SummaryTemplate{{ID: "template", Name: name, IncludeTimestamp: true}, {ID: "other-template", Name: "Other template"}},
		},
	}
}

func summaryTabTo(t *testing.T, tester *ui.Tester, label string, width, height int) ui.AccessInfo {
	t.Helper()
	for attempt := 0; attempt < 30; attempt++ {
		tester.Key(0, ui.KeyTab)
		for _, control := range tester.AccessAll() {
			if control.Name != label || !control.Focused {
				continue
			}
			if !control.Focusable || control.Offscreen || control.Bounds.W < 20 || control.Bounds.H < 10 || control.Bounds.X < 0 || control.Bounds.X+control.Bounds.W > float32(width) || control.Bounds.Y < 0 || control.Bounds.Y+control.Bounds.H > float32(height) {
				t.Fatal("Tab reached a clipped or offscreen control", label, control)
			}
			return control
		}
	}
	t.Fatal("Tab cannot reach the control", label)
	return ui.AccessInfo{}
}

func TestSummaryLongNamesReflowAndKeyboardReachAtLegacyWindowSizes(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1120, 980, 960} {
			t.Run(fmt.Sprintf("%s/%d", locale, width), func(t *testing.T) {
				state := summaryParityState(locale)
				preferences := domain.DefaultUIPreferences()
				preferences.Locale = locale
				closed := 0
				port := &summaryViewPort{state: state, forms: make(chan application.SummaryForm, 1)}
				callbacks := make(chan func(), 2)
				controller := client.NewSummaryController(port, func(callback func()) { callbacks <- callback }, func() {})
				defer controller.Close()
				controller.Result, controller.Runs, controller.Options, controller.SelectedID = &state.Result, state.Runs, state.Options, "selected"
				view := &SummaryView{Client: controller, Locale: locale, Preferences: preferences, Close: func() { closed++ }, Confirm: func(context.Context, string) (bool, error) { return true, nil }}
				tester := ui.NewTesterWith(view.View, width, 720, ui.TesterOptions{Dark: locale == "en-US"})
				title, ok := tester.AccessByRole(ui.RoleHeading, state.Result.Job.Title)
				if !ok || title.Bounds.H < 40 || title.Bounds.X+title.Bounds.W > float32(width) {
					t.Fatal("long title is not visibly multiline", title)
				}
				tester.ClickAt(title.Bounds.X+20, title.Bounds.Y+10)
				tester.Command("selectAll")
				tester.Command("copy")
				if tester.Clipboard() != state.Result.Job.Title {
					t.Fatal("long title could not be copied in full")
				}
				closeLabel := presentation.Message(locale, "common.closeWindow")
				summaryTabTo(t, tester, closeLabel, width, 720)
				tester.Key(0, ui.KeyEnter)
				if closed != 1 {
					t.Fatal("long title displaced keyboard close")
				}
				if width == 960 {
					captureParity(t, tester, "summary-"+locale+"-960-config")
				}
				model := summaryTabTo(t, tester, view.msg("model"), width, 720)
				if model.Value != state.Options.Models[0].Name+" · Synthetic API" {
					t.Fatal("long model name is not available in full", model)
				}
				tester.Key(0, ui.KeyEnter)
				tester.Key(0, ui.KeyEnd)
				tester.Key(0, ui.KeyEscape)
				if view.modelID != "model" || !tester.Focused(view.msg("model")) {
					t.Fatal("Escape changed the model or lost selector focus")
				}
				tester.Key(0, ui.KeyEnter)
				tester.Key(0, ui.KeyEnd)
				tester.Key(0, ui.KeyEnter)
				if view.modelID != "other-model" {
					t.Fatal("keyboard model selection did not commit")
				}
				template := summaryTabTo(t, tester, view.msg("template"), width, 720)
				if template.Value != state.Options.Templates[0].Name {
					t.Fatal("long template name is not available in full", template)
				}
				action := summaryTabTo(t, tester, view.msg("setCurrent"), width, 720)
				if width <= 980 && (action.Bounds.X != model.Bounds.X || model.Bounds.W < 700) || width > 980 && action.Bounds.X <= model.Bounds.X+model.Bounds.W {
					t.Fatal("layout does not follow the legacy 980px single-column breakpoint", model.Bounds, action.Bounds)
				}
				if width <= 980 {
					tester.Key(0, ui.KeyPageDown)
				}
				overview := state.Runs[0].Result.Overview
				paragraph := ui.Rect{}
				for _, node := range tester.AccessAll() {
					if node.Name == overview {
						paragraph = node.Bounds
					}
				}
				if paragraph.W < 200 || paragraph.H < 70 || paragraph.X < 0 || paragraph.X+paragraph.W > float32(width) || paragraph.Y < 0 || paragraph.Y+paragraph.H > 720 {
					t.Fatal("long bilingual preview did not visibly wrap within the window", paragraph)
				}
				if width != 980 {
					captureParity(t, tester, fmt.Sprintf("summary-%s-%d-preview", locale, width))
				}
				tester.ClickAt(paragraph.X+20, paragraph.Y+20)
				tester.Command("selectAll")
				tester.Command("copy")
				parts := []string{presentation.Message(locale, "notes.summary"), overview}
				for _, section := range []struct{ key, text string }{{"topics", state.Runs[0].Result.Topics[0]}, {"decisions", "Synthetic decision"}, {"actionItems", "Synthetic task（Synthetic owner / 2026-11-20）"}, {"risks", "Synthetic risk"}, {"followUps", "Synthetic final follow-up"}} {
					parts = append(parts, presentation.Message(locale, "notes."+section.key), section.text)
				}
				if tester.Clipboard() != strings.Join(parts, "\n") {
					t.Fatalf("preview copy omitted offscreen sections, lost whitespace, or included controls: bounds=%v copy=%q", paragraph, tester.Clipboard())
				}
				tester.Type("must remain read only")
				if state.Runs[0].Result.Overview != overview || !tester.HasText(overview) {
					t.Fatal("preview accepted editing")
				}
				summaryTabTo(t, tester, view.msg("heroTitle"), width, 720)
				tester.Key(0, ui.KeyEnd)
				last, ok := tester.AccessByRole(ui.RoleHeading, presentation.Message(locale, "notes.followUps"))
				if !ok || last.Offscreen || last.Bounds.H < 10 {
					t.Fatal("keyboard scrolling cannot reach the last preview section", last)
				}
				summaryTabTo(t, tester, view.msg("template"), width, 720)
				tester.Key(0, ui.KeyEnter)
				tester.Key(0, ui.KeyEnd)
				tester.Key(0, ui.KeyEscape)
				if view.templateID != "template" || !view.includeTimestamp {
					t.Fatal("Escape changed the template defaults")
				}
				tester.Key(0, ui.KeyEnter)
				tester.Key(0, ui.KeyEnd)
				tester.Key(0, ui.KeyEnter)
				if view.templateID != "other-template" || view.includeTimestamp {
					t.Fatal("keyboard template selection did not apply its defaults")
				}
				summaryTabTo(t, tester, view.msg("extraInstructions"), width, 720)
				extra := "Synthetic instructions\n  保留双语与缩进"
				tester.Type(extra)
				tester.Command("selectAll")
				tester.Command("copy")
				if tester.Clipboard() != extra {
					t.Fatal("keyboard instructions lost text or indentation")
				}
				summaryTabTo(t, tester, view.msg("submit"), width, 720)
				tester.Key(0, ui.KeyEnter)
				flushView(t, callbacks, tester)
				select {
				case form := <-port.forms:
					if form != (application.SummaryForm{ModelID: "other-model", TemplateID: "other-template", Extra: extra}) {
						t.Fatal("keyboard generation lost the selected configuration", form)
					}
				default:
					t.Fatal("keyboard submit did not generate a summary")
				}
			})
		}
	}
}

type summaryParityReply struct {
	state application.SummaryWindowState
	err   error
}

type summaryParityPort struct {
	summaryViewPort
	replies chan summaryParityReply
	selects chan string
	err     error
}

func (port *summaryParityPort) SummaryState(ctx context.Context) (application.SummaryWindowState, error) {
	if port.replies == nil {
		return port.state, nil
	}
	select {
	case reply := <-port.replies:
		return reply.state, reply.err
	case <-ctx.Done():
		return application.SummaryWindowState{}, ctx.Err()
	}
}

func (port *summaryParityPort) SelectSummary(_ context.Context, id string) error {
	port.selects <- id
	return port.err
}

func TestSummaryKeyboardHistoryPreviewApplyAndDeleteConfirmation(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			state := summaryParityState(locale)
			state.Result.Job.ActiveSummaryRunID = stringPointer("current")
			state.Runs = append(state.Runs, domain.SummaryRun{ID: "current", TemplateID: "other-template", Status: "completed", CreatedAt: "2026-11-18T00:00:00Z", Result: &domain.SummaryResult{Overview: "Current saved summary"}})
			port := &summaryParityPort{summaryViewPort: summaryViewPort{state: state, deleted: make(chan string, 2)}, selects: make(chan string, 2), err: domain.ErrSummaryEndpoint}
			callbacks := make(chan func(), 4)
			controller := client.NewSummaryController(port, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			controller.Result, controller.Runs, controller.Options, controller.SelectedID = &state.Result, state.Runs, state.Options, "current"
			confirmed, allowDelete := 0, false
			view := &SummaryView{Client: controller, Locale: locale, Preferences: domain.DefaultUIPreferences(), Close: func() {}, Confirm: func(_ context.Context, language string) (bool, error) {
				if language != locale {
					t.Error("confirmation lost the view locale")
				}
				confirmed++
				return allowDelete, nil
			}}
			tester := ui.NewTester(view.View, 960, 720)
			label := strings.Join([]string{localized(locale, "记录 1", "Record 1"), state.Options.Templates[0].Name, state.Options.Models[0].Name, createdText(locale, state.Runs[0].CreatedAt), statusText(locale, "completed"), view.msg("latest")}, " · ")
			summaryTabTo(t, tester, label, 960, 720)
			tester.Key(0, ui.KeyEnter)
			choice, ok := tester.AccessByRole(ui.RoleToggleButton, label)
			if !ok || !choice.Checked || controller.SelectedID != "selected" || *controller.Result.Job.ActiveSummaryRunID != "current" {
				t.Fatal("keyboard history selection did not preview without applying", choice)
			}
			select {
			case id := <-port.selects:
				t.Fatal("history selection mutated the current summary", id)
			default:
			}
			summaryTabTo(t, tester, view.msg("setCurrent"), 960, 720)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			if id := <-port.selects; id != "selected" {
				t.Fatal("apply targeted a different summary", id)
			}
			message := summaryError(locale, domain.ErrSummaryEndpoint)
			alert, ok := tester.AccessByRole(ui.RoleAlert, message)
			if !ok {
				t.Fatal("apply error has no readable alert", alert)
			}
			summaryTabTo(t, tester, presentation.Message(locale, "common.retry"), 960, 720)
			if err := tester.Click(message); err != nil {
				t.Fatal(err)
			}
			tester.Command("selectAll")
			tester.Command("copy")
			if tester.Clipboard() != message {
				t.Fatal("operation error could not be copied independently", tester.Clipboard())
			}
			for _, allow := range []bool{false, true} {
				allowDelete = allow
				summaryTabTo(t, tester, view.msg("deleteCurrent"), 960, 720)
				tester.Key(0, ui.KeyEnter)
				flushView(t, callbacks, tester)
				select {
				case id := <-port.deleted:
					if !allow || id != "selected" {
						t.Fatal("delete ignored confirmation or targeted another run", allow, id)
					}
				default:
					if allow {
						t.Fatal("confirmed keyboard delete did not reach the service")
					}
				}
			}
			if confirmed != 2 {
				t.Fatal("delete did not confirm each attempt", confirmed)
			}
			for _, heading := range []struct {
				name  string
				level int
			}{{state.Result.Job.Title, 1}, {view.msg("currentConfig"), 2}, {view.msg("history"), 2}, {view.msg("preview"), 2}, {presentation.Message(locale, "notes.summary"), 3}, {presentation.Message(locale, "notes.followUps"), 3}} {
				node, ok := tester.AccessByRole(ui.RoleHeading, heading.name)
				if !ok || node.Level != heading.level {
					t.Fatal("summary heading hierarchy is missing", heading, node)
				}
			}
			if _, ok := tester.AccessByRole(ui.RoleList, view.msg("history")); !ok {
				t.Fatal("summary history has no list semantics")
			}
			item, ok := tester.AccessByRole(ui.RoleListItem, "Synthetic final follow-up")
			if !ok || item.PosInSet != 1 || item.SetSize != 1 {
				t.Fatal("preview lost the original notes list semantics", item)
			}
		})
	}
}

func TestSummaryNarrowKeyboardRetriesSelectedAndLatestSavedRuns(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			state := application.SummaryWindowState{
				Result: domain.Result{Job: domain.JobSummary{Title: "Synthetic failed summary"}},
				Runs:   []domain.SummaryRun{{ID: "latest", Status: "failed", UpdatedAt: "2026-11-19T00:00:00Z"}, {ID: "selected", Status: "failed", UpdatedAt: "2026-11-18T00:00:00Z"}},
			}
			port := &summaryViewPort{state: state, forms: make(chan application.SummaryForm, 2)}
			callbacks := make(chan func(), 2)
			controller := client.NewSummaryController(port, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			controller.Result, controller.Runs, controller.SelectedID = &state.Result, state.Runs, "selected"
			view := &SummaryView{Client: controller, Locale: locale, Preferences: domain.DefaultUIPreferences(), Close: func() {}}
			tester := ui.NewTester(view.View, 960, 720)
			for _, action := range []struct{ label, runID string }{{presentation.Message(locale, "common.retry"), "selected"}, {view.msg("submit"), "latest"}} {
				summaryTabTo(t, tester, action.label, 960, 720)
				tester.Key(0, ui.KeyEnter)
				flushView(t, callbacks, tester)
				select {
				case form := <-port.forms:
					if form != (application.SummaryForm{RunID: action.runID}) {
						t.Fatal("retry rebuilt configuration or used another run snapshot", action, form)
					}
				default:
					t.Fatal("keyboard retry did not reach the service", action)
				}
				if controller.SelectedID != action.runID {
					t.Fatal("retry did not retain the accepted run selection", controller.SelectedID)
				}
			}
		})
	}
}

func TestSummarySelectedHistoryRetainsReadableThemeText(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			preferences := domain.DefaultUIPreferences()
			colors := presentation.PreferenceColors(dark, preferences)
			view := &SummaryView{Locale: "en-US", Client: &client.SummaryController{
				SelectedID: "selected", Options: application.SummaryOptions{Templates: []domain.SummaryTemplate{{ID: "template", Name: "Synthetic readable history"}}},
			}}
			tester := ui.NewTesterWith(func(c *ui.Context) {
				ui.Themed(c, &colors.Theme).Fill().Children(func() {
					ui.Column(c).Fill().Padding(16).Background(colors.Panel).Children(func() {
						view.history(c, domain.SummaryRun{ID: "selected", TemplateID: "template", Status: "completed"}, 1)
					})
				})
			}, 400, 200, ui.TesterOptions{Dark: dark})
			title, ok := tester.Find("Synthetic readable history")
			if !ok || title.H < 10 {
				t.Fatal("history title is missing", title)
			}
			readablePixels := 0
			// AppKit label colors carry alpha; compare the rendered foreground
			// after compositing over the selected row, not its unblended RGB.
			foreground := presentation.SolidSurface(colors.Theme.Text, presentation.SolidSurface(colors.Selected, colors.Panel))
			matches := func(actual, expected uint8) bool {
				difference := int(actual) - int(expected)
				return difference >= -16 && difference <= 16
			}
			image := tester.Image()
			for row := int(title.Y); row < int(title.Y+title.H); row++ {
				for column := int(title.X); column < int(title.X+title.W); column++ {
					pixel := image.RGBAAt(column, row)
					if matches(pixel.R, foreground.R) && matches(pixel.G, foreground.G) && matches(pixel.B, foreground.B) {
						readablePixels++
					}
				}
			}
			if readablePixels < 10 {
				t.Fatal("selected history title lost the theme's readable foreground", readablePixels)
			}
		})
	}
}

func TestSummaryKeyboardLoadingFailureRetryAndSnapshotSemantics(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			port := &summaryParityPort{replies: make(chan summaryParityReply, 3)}
			callbacks := make(chan func(), 3)
			controller := client.NewSummaryController(port, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			view := &SummaryView{Client: controller, Locale: locale, Preferences: domain.DefaultUIPreferences(), Close: func() {}}
			controller.Refresh()
			tester := ui.NewTester(view.View, 960, 720)
			assertLoading := func() {
				t.Helper()
				status, ok := tester.AccessByRole(ui.RoleStatus, presentation.Message(locale, "workbench.loading"))
				region, regionOK := tester.AccessByRole(ui.RoleScroll, view.msg("heroTitle"))
				if !ok || status.Offscreen || !regionOK || !region.Busy || tester.HasText(view.msg("emptyRuns")) {
					t.Fatal("initial load lacks a pending status or exposes empty content", status, region)
				}
			}
			assertLoading()
			port.replies <- summaryParityReply{err: errors.New("synthetic load failure")}
			flushView(t, callbacks, tester)
			message := view.msg("requestFailed")
			alert, ok := tester.AccessByRole(ui.RoleAlert, message)
			if !ok || alert.Offscreen || alert.Bounds.H < 10 {
				t.Fatal("load failure is not a visible alert", alert)
			}
			summaryTabTo(t, tester, presentation.Message(locale, "common.retry"), 960, 720)
			tester.Key(0, ui.KeyEnter)
			assertLoading()
			state := summaryParityState(locale)
			port.replies <- summaryParityReply{state: state}
			flushView(t, callbacks, tester)
			if !tester.HasText(state.Result.Job.Title) || tester.HasText(message) || controller.SelectedID != "selected" {
				t.Fatal("successful retry did not restore the selected snapshot")
			}
			controller.Refresh()
			port.replies <- summaryParityReply{err: errors.New("synthetic refresh failure")}
			flushView(t, callbacks, tester)
			alerts := 0
			for _, node := range tester.AccessAll() {
				if node.Role == ui.RoleAlert {
					alerts++
				}
			}
			if alerts != 1 || !tester.HasText(state.Runs[0].Result.Overview) || controller.SelectedID != "selected" {
				t.Fatal("refresh failure duplicated alerts or hid the selected preview", alerts)
			}
		})
	}
}

func TestSummarySelectorsKeepCollidingNamesIndividuallyReachable(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, kind := range []string{"model", "template"} {
			t.Run(locale+"/"+kind, func(t *testing.T) {
				view := &SummaryView{Locale: locale}
				placeholder := view.msg("chooseTemplate")
				labels := []string{placeholder, "A", "A", "A (1)", placeholder, placeholder + " (1)", "A (2)"}
				if kind == "model" {
					labels = []string{view.msg("chooseModel"), "A · API", "A · API", "A · API (1)", "A · API (2)"}
				}
				ids := []string{""}
				for index := 1; index < len(labels); index++ {
					ids = append(ids, fmt.Sprintf("opaque-%s-%d", kind, index))
				}
				selected := ids[1]
				tester := ui.NewTester(func(c *ui.Context) {
					ui.Column(c).Fill().Padding(16).Children(func() { view.selectOption(c, &selected, append([]string{}, labels...), ids, kind, false) })
				}, 960, 720)
				summaryTabTo(t, tester, view.msg(kind), 960, 720)
				for _, index := range []int{3, 2, 4, 0, 1} {
					tester.Key(0, ui.KeyEnter)
					seen := map[string]bool{}
					for _, option := range tester.AccessAll() {
						if option.Role != ui.RoleOption {
							continue
						}
						if seen[option.Name] || strings.Contains(option.Name, "opaque-") {
							t.Fatal("selector has colliding labels or exposes an ID", option.Name)
						}
						seen[option.Name] = true
					}
					if len(seen) != len(labels) || !seen[labels[3]] || !seen[labels[0]] {
						t.Fatal("selector lost an original name or placeholder", seen)
					}
					tester.Key(0, ui.KeyHome)
					for step := 0; step < index; step++ {
						tester.Key(0, ui.KeyDown)
					}
					tester.Key(0, ui.KeyEnter)
					if selected != ids[index] {
						t.Fatal("keyboard selection resolved to another option ID", index, selected)
					}
				}
				state := summaryParityState(locale)
				if kind == "template" {
					state.Options.Templates = nil
					for index := 1; index < len(labels); index++ {
						state.Options.Templates = append(state.Options.Templates, domain.SummaryTemplate{ID: ids[index], Name: labels[index]})
					}
				} else {
					state.Options.Models = nil
					for index := 1; index < len(labels); index++ {
						parts := strings.SplitN(labels[index], " · ", 2)
						state.Options.Models = append(state.Options.Models, application.SummaryModelOption{ID: ids[index], Name: parts[0], Model: parts[1], Enabled: true})
					}
				}
				port := &summaryViewPort{state: state, forms: make(chan application.SummaryForm, 1)}
				callbacks := make(chan func(), 1)
				controller := client.NewSummaryController(port, func(callback func()) { callbacks <- callback }, func() {})
				defer controller.Close()
				controller.Result, controller.Runs, controller.Options = &state.Result, state.Runs, state.Options
				view.Client = controller
				tester = ui.NewTester(view.View, 960, 720)
				summaryTabTo(t, tester, view.msg(kind), 960, 720)
				tester.Key(0, ui.KeyEnter)
				tester.Key(0, ui.KeyHome)
				for step := 0; step < 3; step++ {
					tester.Key(0, ui.KeyDown)
				}
				tester.Key(0, ui.KeyEnter)
				summaryTabTo(t, tester, view.msg("submit"), 960, 720)
				tester.Key(0, ui.KeyEnter)
				flushView(t, callbacks, tester)
				select {
				case form := <-port.forms:
					if kind == "model" && form.ModelID != ids[3] || kind == "template" && form.TemplateID != ids[3] {
						t.Fatal("third displayed option did not reach generation with its own ID", form)
					}
				default:
					t.Fatal("third option could not be submitted")
				}
			})
		}
	}
}

func TestSummaryHistoryAccessibleNamesDistinguishRunsAndExposeTheirState(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			state := summaryParityState(locale)
			state.Result.Job.Title = "Synthetic history"
			state.Result.Job.ActiveSummaryRunID = stringPointer("opaque-current")
			state.Runs = []domain.SummaryRun{
				{ID: "opaque-current", TemplateID: "template", ModelConfigID: "model", CreatedAt: "2026-11-19T00:00:00Z", UpdatedAt: "2026-11-19T00:00:00Z", Status: "completed"},
				{ID: "opaque-failed", TemplateID: "template", ModelConfigID: "model", CreatedAt: "2026-11-19T00:00:00Z", UpdatedAt: "2026-11-19T00:00:02Z", Status: "failed", ErrorMessage: stringPointer(domain.ErrAIStructured.Error())},
			}
			controller := &client.SummaryController{Result: &state.Result, Runs: state.Runs, Options: state.Options, SelectedID: "opaque-current"}
			view := &SummaryView{Client: controller, Locale: locale, Close: func() {}}
			tester := ui.NewTester(view.View, 960, 720)
			labels := []string{}
			for _, node := range tester.AccessAll() {
				if node.Role == ui.RoleToggleButton {
					labels = append(labels, node.Name)
				}
			}
			if len(labels) != 2 || labels[0] == labels[1] {
				t.Fatal("same-minute records have indistinguishable accessible names", labels)
			}
			for index, label := range labels {
				run := state.Runs[index]
				marker := view.msg("currentResult")
				if index == 1 {
					marker = view.msg("latest")
				}
				for _, part := range []string{state.Options.Templates[0].Name, state.Options.Models[0].Name, createdText(locale, run.CreatedAt), statusText(locale, summaryStatus(&run)), marker, fmt.Sprintf(localized(locale, "记录 %d", "Record %d"), index+1)} {
					if !strings.Contains(label, part) {
						t.Fatal("history omits a public field from its accessible name", part, label)
					}
				}
				if strings.Contains(label, "opaque-") || index == 1 && !strings.Contains(label, view.msg("invalidStructuredJson")) {
					t.Fatal("history exposes IDs or omits the localized failure", label)
				}
			}
			summaryTabTo(t, tester, labels[1], 960, 720)
			tester.Key(0, ui.KeyEnter)
			choice, _ := tester.AccessByRole(ui.RoleToggleButton, labels[1])
			if !choice.Checked || controller.SelectedID != "opaque-failed" || *controller.Result.Job.ActiveSummaryRunID != "opaque-current" {
				t.Fatal("accessible history action selected or applied the wrong record", choice)
			}
		})
	}
}

type summaryRetryCall struct {
	kind, id string
	form     application.SummaryForm
	reply    chan error
}

type summaryRetryPort struct {
	calls   chan summaryRetryCall
	replies chan summaryParityReply
}

func (port *summaryRetryPort) SummaryState(ctx context.Context) (application.SummaryWindowState, error) {
	select {
	case reply := <-port.replies:
		return reply.state, reply.err
	case <-ctx.Done():
		return application.SummaryWindowState{}, ctx.Err()
	}
}

func (port *summaryRetryPort) action(ctx context.Context, call summaryRetryCall) error {
	call.reply = make(chan error, 1)
	port.calls <- call
	select {
	case err := <-call.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (port *summaryRetryPort) GenerateSummary(ctx context.Context, form application.SummaryForm) error {
	return port.action(ctx, summaryRetryCall{kind: "generate", form: form})
}

func (port *summaryRetryPort) SelectSummary(ctx context.Context, id string) error {
	return port.action(ctx, summaryRetryCall{kind: "select", id: id})
}

func (port *summaryRetryPort) DeleteSummary(ctx context.Context, id string) error {
	return port.action(ctx, summaryRetryCall{kind: "delete", id: id})
}

func summaryRetryReceived(t *testing.T, port *summaryRetryPort) summaryRetryCall {
	t.Helper()
	select {
	case call := <-port.calls:
		return call
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not reach the original service operation")
		return summaryRetryCall{}
	}
}

func TestSummaryTopKeyboardRetryReplaysFailedOperationAndOriginalSnapshot(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, kind := range []string{"generate", "select", "delete"} {
			t.Run(locale+"/"+kind, func(t *testing.T) {
				state := summaryParityState(locale)
				state.Result.Job.Title = "Synthetic retry"
				state.Runs = append(state.Runs, domain.SummaryRun{ID: "other", Status: "completed", Result: &domain.SummaryResult{Overview: "Other preview"}})
				port := &summaryRetryPort{calls: make(chan summaryRetryCall, 4), replies: make(chan summaryParityReply, 4)}
				callbacks := make(chan func(), 4)
				controller := client.NewSummaryController(port, func(callback func()) { callbacks <- callback }, func() {})
				defer controller.Close()
				controller.Result, controller.Runs, controller.Options, controller.SelectedID = &state.Result, state.Runs, state.Options, "selected"
				confirmed := 0
				view := &SummaryView{Client: controller, Locale: locale, extra: "Original instructions 原快照", Close: func() {}, Confirm: func(_ context.Context, language string) (bool, error) {
					if language != locale {
						t.Error("retry confirmation lost locale")
					}
					confirmed++
					return true, nil
				}}
				tester := ui.NewTester(view.View, 960, 720)
				action := view.msg("submit")
				if kind == "select" {
					action = view.msg("setCurrent")
				} else if kind == "delete" {
					action = view.msg("deleteCurrent")
				}
				summaryTabTo(t, tester, action, 960, 720)
				tester.Key(0, ui.KeyEnter)
				original := summaryRetryReceived(t, port)
				original.reply <- domain.ErrSummaryEndpoint
				port.replies <- summaryParityReply{state: state}
				flushView(t, callbacks, tester)
				controller.Refresh()
				port.replies <- summaryParityReply{state: state}
				flushView(t, callbacks, tester)
				if !errors.Is(controller.Error, domain.ErrSummaryEndpoint) {
					t.Fatal("polling discarded the failed operation")
				}
				view.modelID, view.templateID, view.extra = "other-model", "other-template", "Changed instructions"
				controller.SelectedID = "other"
				tester.Frame()
				summaryTabTo(t, tester, presentation.Message(locale, "common.retry"), 960, 720)
				tester.Key(0, ui.KeyEnter)
				retry := summaryRetryReceived(t, port)
				if retry.kind != kind || retry.form != original.form || retry.id != original.id {
					t.Fatal("retry used the new configuration or preview instead of its saved operation", original, retry)
				}
				if kind == "delete" && confirmed != 2 {
					t.Fatal("retry bypassed fresh delete confirmation", confirmed)
				}
				retry.reply <- nil
				port.replies <- summaryParityReply{state: state}
				flushView(t, callbacks, tester)
				if controller.Error != nil || tester.HasText(summaryError(locale, domain.ErrSummaryEndpoint)) {
					t.Fatal("successful retry retained the operation error")
				}
			})
		}
	}
}

func TestSummaryTopRetryOnlyObservesAcceptedGenerationAfterReadFailure(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			state := summaryParityState(locale)
			state.Result.Job.Title = "Synthetic accepted action"
			port := &summaryRetryPort{calls: make(chan summaryRetryCall, 2), replies: make(chan summaryParityReply, 2)}
			callbacks := make(chan func(), 2)
			controller := client.NewSummaryController(port, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			controller.Result, controller.Runs, controller.Options, controller.SelectedID = &state.Result, state.Runs, state.Options, "selected"
			view := &SummaryView{Client: controller, Locale: locale, Close: func() {}}
			tester := ui.NewTester(view.View, 960, 720)
			summaryTabTo(t, tester, view.msg("submit"), 960, 720)
			tester.Key(0, ui.KeyEnter)
			call := summaryRetryReceived(t, port)
			call.reply <- nil
			port.replies <- summaryParityReply{err: errors.New("synthetic read failure")}
			flushView(t, callbacks, tester)
			summaryTabTo(t, tester, presentation.Message(locale, "common.retry"), 960, 720)
			tester.Key(0, ui.KeyEnter)
			state.Runs = append([]domain.SummaryRun{{ID: "accepted", Status: "running"}}, state.Runs...)
			port.replies <- summaryParityReply{state: state}
			flushView(t, callbacks, tester)
			if len(port.calls) != 0 || controller.SelectedID != "accepted" || controller.Error != nil {
				t.Fatal("read retry resubmitted an accepted generation or lost its pending selection")
			}
		})
	}
}
