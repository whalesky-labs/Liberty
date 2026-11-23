package desktop

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func resourceParityView(kind application.ResourceKind, locale string, count int) *ResourcesView {
	controller := &client.ResourceController{}
	for index := 0; index < count; index++ {
		id, name := fmt.Sprintf("synthetic-%02d", index), fmt.Sprintf("Synthetic resource %02d", index)
		updated := "2026-11-19T04:30:00Z"
		controller.Models = append(controller.Models, domain.AIModel{ID: id, Name: name, Model: "synthetic-model", Enabled: true, IsDefault: index == 0, UpdatedAt: updated})
		controller.Templates = append(controller.Templates, domain.SummaryTemplate{ID: id, Name: name, Description: "Synthetic description that is deliberately long enough to wrap onto two lines inside a narrow card without stretching the grid.", Builtin: index == 0, UpdatedAt: updated})
		controller.Members = append(controller.Members, domain.MeetingMember{ID: id, Name: name, Department: "Synthetic department", SortOrder: int64(index), IsRecorder: index == 0, UpdatedAt: updated})
	}
	return &ResourcesView{Client: controller, Kind: kind, Locale: locale, ContentWidth: shellContentWidth, Open: func(application.ResourceKind, string) {}}
}

func TestResourceCardsUseAvailablePaneWidthAndKeepLastRowColumns(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, kind := range []application.ResourceKind{application.Models, application.Templates} {
			for _, sample := range []struct{ width, columns int }{{1440, 3}, {1100, 2}, {760, 1}, {720, 1}} {
				t.Run(fmt.Sprintf("%s-%s-%d", kind, locale, sample.width), func(t *testing.T) {
					view := resourceParityView(kind, locale, 4)
					shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
					tester := ui.NewTesterWith(shell.View, sample.width, 1600, ui.TesterOptions{Dark: locale == "en-US"})
					first, ok := tester.AccessByRole(ui.RoleListItem, "Synthetic resource 00")
					if !ok || first.Offscreen {
						t.Fatal("resource list lost its first card", first)
					}
					wantWidth := (float32(sample.width) - presentation.SidebarWidth - 24 - 16 - float32(sample.columns-1)*16) / float32(sample.columns)
					for index := 0; index < 4; index++ {
						card, ok := tester.AccessByRole(ui.RoleListItem, fmt.Sprintf("Synthetic resource %02d", index))
						wantX := first.Bounds.X + float32(index%sample.columns)*(wantWidth+16)
						if !ok || card.Offscreen || math.Abs(float64(card.Bounds.X-wantX)) > 1 || math.Abs(float64(card.Bounds.W-wantWidth)) > 1 || card.Bounds.H < 226 {
							t.Fatalf("card ignores the pane width or expands the final row: index=%d card=%+v expected width=%.1f x=%.1f", index, card, wantWidth, wantX)
						}
						if index >= sample.columns && card.Bounds.Y <= first.Bounds.Y+first.Bounds.H {
							t.Fatal("resource grid did not wrap at the expected column", card)
						}
					}
					if kind == application.Models && (!tester.HasText(view.msg("enabledTag")) || !tester.HasText(view.msg("defaultTag"))) {
						t.Fatal("enabled and default status badges were merged or lost")
					}
					captureParity(t, tester, fmt.Sprintf("resources-%s-%s-%d", kind, locale, sample.width))
					tester.SetSize(1440, 1600)
					tester.Frame()
					second, _ := tester.AccessByRole(ui.RoleListItem, "Synthetic resource 01")
					first, _ = tester.AccessByRole(ui.RoleListItem, "Synthetic resource 00")
					if second.Bounds.X <= first.Bounds.X || math.Abs(float64(second.Bounds.Y-first.Bounds.Y)) > 1 {
						t.Fatal("resize retained stale card columns")
					}
				})
			}
		}
	}
}

func TestMemberTableKeepsSixCellsAndFixedActionsDuringCombinedScroll(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := resourceParityView(application.Members, locale, 12)
			shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
			tester := ui.NewTester(shell.View, 1100, 1400)
			table, ok := tester.AccessByRole(ui.RoleTable, view.msg("listTitle"))
			if !ok || table.ColumnCount != 6 || table.RowCount != 13 {
				t.Fatal("member table lacks complete dimensions", table)
			}
			for index, column := range memberColumns {
				header, ok := tester.AccessByRole(ui.RoleColumnHeader, view.msg(column.key))
				if !ok || header.RowIndex != 1 || header.ColumnIndex != index+1 {
					t.Fatal("member header lacks its table position", header)
				}
			}
			cells := map[int]int{}
			for _, node := range tester.AccessAll() {
				if node.Role == ui.RoleCell {
					if node.RowIndex < 2 || node.RowIndex > 13 || node.ColumnIndex < 1 || node.ColumnIndex > 6 {
						t.Fatal("member cell has invalid coordinates", node)
					}
					cells[node.RowIndex]++
				}
			}
			for index := range view.Client.Members {
				if cells[index+2] != 6 {
					t.Fatal("member row lacks six logical cells", cells)
				}
			}
			edit := presentation.Message(locale, "common.edit")
			firstLabel := edit + ": " + view.Client.Members[0].Name
			before, ok := tester.Find(firstLabel)
			if !ok || before.W < 30 {
				t.Fatal("member fixed action starts outside the viewport", before)
			}
			tester.Scroll(table.Bounds.X+200, before.Y+10, 500, 0)
			tester.Frame()
			after, _ := tester.Find(firstLabel)
			if math.Abs(float64(before.X-after.X)) > 1 || math.Abs(float64(before.Y-after.Y)) > 1 {
				t.Fatal("member action scrolled horizontally", before, after)
			}
			nameHeader, _ := tester.Find(view.msg("name"))
			if nameHeader.W > 1 {
				t.Fatal("member data did not scroll under the fixed action", nameHeader)
			}
			tester.SetSize(1100, 650)
			tester.Frame()
			tester.Scroll(table.Bounds.X+200, before.Y+10, 0, 2000)
			tester.Frame()
			last := view.Client.Members[len(view.Client.Members)-1]
			row, _ := tester.AccessByRole(ui.RoleRow, last.Name)
			action, ok := tester.AccessByRole(ui.RoleButton, edit+": "+last.Name)
			if !ok || action.Offscreen || row.Offscreen || action.Bounds.Y < row.Bounds.Y || action.Bounds.Y+action.Bounds.H > row.Bounds.Y+row.Bounds.H || math.Abs(float64(action.Bounds.X-before.X)) > 1 {
				t.Fatal("member action detached from its vertically scrolled row", row, action)
			}
			captureParity(t, tester, "resources-members-scrolled-"+locale)
		})
	}
}

func TestResourceCardAndMemberActionsDoNotBubbleIntoDoubleClick(t *testing.T) {
	for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
		t.Run(string(kind), func(t *testing.T) {
			view := resourceParityView(kind, "en-US", 1)
			calls := 0
			view.Open = func(openKind application.ResourceKind, id string) {
				if openKind != kind || id != "synthetic-00" {
					t.Error("opened an unrelated editor")
				}
				calls++
			}
			tester := ui.NewTester(view.View, 1100, 900)
			label := presentation.Message(view.Locale, "common.edit") + ": Synthetic resource 00"
			for index := 0; index < 2; index++ {
				if err := tester.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 2 {
				t.Fatal("double-clicking edit also triggered the parent", calls)
			}
			calls = 0
			tester.Advance(time.Second)
			rowRole := ui.RoleListItem
			if kind == application.Members {
				rowRole = ui.RoleRow
			}
			row, _ := tester.AccessByRole(rowRole, "Synthetic resource 00")
			tester.ClickAt(row.Bounds.X+10, row.Bounds.Y+10)
			tester.ClickAt(row.Bounds.X+10, row.Bounds.Y+10)
			if calls != 1 {
				t.Fatal("double-clicking a resource did not open its editor", calls)
			}
			tester.Key(0, ui.KeyEnter)
			if calls != 2 {
				t.Fatal("Enter on the focused resource did not open its editor", calls)
			}
			tester.KeyRepeat(0, ui.KeyEnter)
			if calls != 2 {
				t.Fatal("held Enter repeatedly opened resource editors", calls)
			}
			tester.Key(0, ui.KeyTab)
			if !tester.Focused(label) {
				t.Fatal("Tab from a resource did not reach its edit action")
			}
			tester.Key(0, ui.KeyEnter)
			if calls != 3 {
				t.Fatal("keyboard edit did not open exactly one editor", calls)
			}
		})
	}
}

func TestMemberEditorKeepsClearedSortAndFocusesValidation(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 4)
			draft := application.NewResourceDraft(application.Members)
			draft.Member.Name = "Synthetic member"
			port := &resourceEditorPort{draft: draft, saved: make(chan domain.CredentialUpdate, 1)}
			controller := client.NewResourceEditor(port, draft, func(callback func()) { callbacks <- callback }, func() {}, func() {})
			defer controller.Close()
			view := &ResourceEditorView{Client: controller, Locale: locale, Preferences: domain.DefaultUIPreferences()}
			tester := ui.NewTester(view.View, 640, 560)
			if err := tester.Click(view.msg("sortOrder")); err != nil {
				t.Fatal(err)
			}
			tester.Key(0, ui.KeyEnd)
			tester.Key(0, ui.KeyBackspace)
			tester.Frame()
			if controller.MemberSortOrder != "" || !controller.Dirty() {
				t.Fatal("cleared sort was replaced by the typed integer or ignored by dirty state")
			}
			if err := tester.Click(view.msg("save")); err != nil {
				t.Fatal(err)
			}
			tester.Frame()
			field, _ := tester.AccessByRole(ui.RoleTextField, view.msg("sortOrder"))
			if !field.Invalid || !field.Focused || field.Description != view.msg("validationSortOrder") || !tester.HasText(view.msg("validationSortOrder")) || len(port.saved) != 0 {
				t.Fatal("invalid sort was persisted or lacks focused bilingual feedback", field)
			}
			tester.Type("17")
			if controller.MemberSortOrder != "17" || controller.Error != nil {
				t.Fatal("sort input did not recover after validation")
			}
			if err := tester.Click(view.msg("save")); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tester)
			if controller.Error != nil || controller.Dirty() || controller.Draft.Member.SortOrder != 17 {
				t.Fatal("corrected member was not saved")
			}
		})
	}
}

func TestResourceEditorGeometryMatchesLegacyWindows(t *testing.T) {
	// Frozen on 2026-11-21 from the historical openModelEditorWindow,
	// openTemplateEditorWindow and openMemberEditorWindow options in
	// apps/desktop/src/shared/services/ui/windows.ts before its retirement.
	for _, sample := range []struct {
		kind     application.ResourceKind
		geometry [4]int // Width, height, minimum width, minimum height.
	}{
		{application.Models, [4]int{880, 760, 760, 680}},
		{application.Templates, [4]int{960, 820, 820, 720}},
		{application.Members, [4]int{760, 680, 640, 560}},
	} {
		for _, locale := range []string{"zh-CN", "en-US"} {
			for _, id := range []string{"", "synthetic-existing"} {
				options := resourceEditorOptions(sample.kind, locale, id)
				for index, value := range []int{options.Width, options.Height, options.MinWidth, options.MinHeight} {
					if value != sample.geometry[index] {
						t.Fatalf("native editor geometry drifted from %s: field %d", sample.kind, index)
					}
				}
				key := "editorNewTitle"
				if id != "" {
					key = "editorEditTitle"
				}
				if options.Title != presentation.Message(locale, string(sample.kind)+"."+key) {
					t.Fatal("editor title lost its locale or new/edit state")
				}
			}
		}
	}
}

func TestResourceEditorsExposeValidationAndFooterAtLegacyMinimum(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
			t.Run(string(kind)+"-"+locale, func(t *testing.T) {
				draft := application.NewResourceDraft(kind)
				port := &resourceEditorPort{draft: draft, saved: make(chan domain.CredentialUpdate, 1)}
				controller := client.NewResourceEditor(port, draft, func(func()) { t.Error("invalid draft dispatched a save") }, func() {}, func() {})
				defer controller.Close()
				view := &ResourceEditorView{Client: controller, Locale: locale, Preferences: domain.DefaultUIPreferences()}
				options := resourceEditorOptions(kind, locale, "")
				tester := ui.NewTesterWith(view.View, options.MinWidth, options.MinHeight, ui.TesterOptions{Dark: locale == "en-US"})
				for _, key := range []string{"save", "reset"} {
					button, ok := tester.AccessByRole(ui.RoleButton, view.msg(key))
					if !ok || button.Offscreen || button.Bounds.Y+button.Bounds.H > float32(options.MinHeight) {
						t.Fatal("editor footer is clipped at the legacy minimum", kind, key, button)
					}
				}
				if err := tester.Click(view.msg("save")); err != nil {
					t.Fatal(err)
				}
				tester.Frame()
				field, ok := tester.AccessByRole(ui.RoleTextField, view.msg("name"))
				if !ok || !field.Invalid || !field.Focused || field.Description != view.msg("validationName") || controller.Busy || len(port.saved) != 0 {
					t.Fatal("missing-name validation is not focused or accessible", field)
				}
				if kind != application.Templates && field.Placeholder != view.msg("namePlaceholder") {
					t.Fatal("editor placeholder was lost", field)
				}
				captureParity(t, tester, fmt.Sprintf("resource-editor-%s-%s-minimum", kind, locale))
			})
		}
	}
}

func TestBuiltinTemplateMustBeDuplicatedBeforeEditing(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		callbacks := make(chan func(), 4)
		draft := application.NewResourceDraft(application.Templates)
		draft.Template = domain.SummaryTemplate{ID: "synthetic-builtin", Name: "Synthetic built-in", Prompt: "Summarize the synthetic meeting.", Builtin: true}
		port := &resourceEditorPort{draft: draft, saved: make(chan domain.CredentialUpdate, 1)}
		controller := client.NewResourceEditor(port, draft, func(callback func()) { callbacks <- callback }, func() {}, func() {})
		defer controller.Close()
		view := &ResourceEditorView{Client: controller, Locale: locale, Preferences: domain.DefaultUIPreferences()}
		tester := ui.NewTester(view.View, 820, 720)
		assertDisabled(t, tester, view.msg("save"), true)
		for _, key := range []string{"name", "description", "prompt"} {
			field, ok := tester.AccessByRole(ui.RoleTextField, view.msg(key))
			if !ok || !field.ReadOnly {
				t.Fatal("built-in template field is editable", key)
			}
		}
		if !tester.HasText(view.msg("builtinReadonly")) {
			t.Fatal("built-in template lacks duplication guidance")
		}
		if _, ok := tester.AccessByRole(ui.RoleButton, presentation.Message(locale, "common.delete")); ok {
			t.Fatal("built-in template exposes delete")
		}
		if err := tester.Click(view.msg("duplicate")); err != nil {
			t.Fatal(err)
		}
		flushView(t, callbacks, tester)
		field, _ := tester.AccessByRole(ui.RoleTextField, view.msg("name"))
		if field.ReadOnly || controller.Draft.ID() == "" || controller.Draft.ID() == draft.ID() || controller.Draft.Template.Builtin || controller.Dirty() {
			t.Fatal("duplicated template is not an independent saved editable template")
		}
		assertDisabled(t, tester, view.msg("save"), false)
	}
}

func TestResourcesDoNotShowEmptyWhileLoadingOrAfterReadFailure(t *testing.T) {
	for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
		view := resourceParityView(kind, "en-US", 0)
		view.Client.Busy = true
		tester := ui.NewTester(view.View, 1100, 900)
		if tester.HasText(view.msg("empty")) || !tester.HasText("Loading…") {
			t.Fatal("loading resources are presented as empty", kind)
		}
		view.Client.Busy, view.Client.Error = false, domain.ErrResourceInvalid
		tester.Frame()
		if tester.HasText(view.msg("empty")) {
			t.Fatal("failed resource read is presented as an empty result", kind)
		}
	}
}

func TestResourceMetricsPreferEnabledDefaultAndTrimDepartments(t *testing.T) {
	view := resourceParityView(application.Models, "en-US", 3)
	view.Client.Models[0].Enabled = false
	view.Client.Models[1].IsDefault, view.Client.Models[2].IsDefault = true, true
	_, values := view.metrics()
	if values[3] != view.Client.Models[1].Name {
		t.Fatal("default metric did not prefer the first enabled default")
	}
	view.Kind = application.Members
	view.Client.Members[0].Department, view.Client.Members[1].Department, view.Client.Members[2].Department = " Sales ", "Sales", "  "
	_, values = view.metrics()
	if values[1] != "1" {
		t.Fatal("department metric counted empty or equivalent trimmed values")
	}
}
