package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

func TestResourceBrowsingFiltersSearchAndRecoversWithoutChangingResources(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
			t.Run(string(kind)+"-"+locale, func(t *testing.T) {
				view := resourceParityView(kind, locale, 3)
				view.Client.Models[1].Enabled = false
				view.Client.Templates[1].Builtin = false
				view.Client.Members[1].IsRecorder = false
				view.Client.Models[1].Model = "Target 研发"
				view.Client.Templates[1].Description = "Target 研发"
				view.Client.Members[1].Department = "Target 研发"
				opened := ""
				view.Open = func(_ application.ResourceKind, id string) { opened = id }
				tester := ui.NewTester(view.View, 1100, 900)
				if err := tester.Click(view.layoutCopy().search); err != nil {
					t.Fatal(err)
				}
				tester.Type("  tArGeT 研发  ")
				tester.Frame()
				role := ui.RoleListItem
				if kind == application.Members {
					role = ui.RoleRow
				}
				if _, ok := tester.AccessByRole(role, "Synthetic resource 00"); ok {
					t.Fatal("search retained an unrelated resource")
				}
				if _, ok := tester.AccessByRole(role, "Synthetic resource 01"); !ok {
					t.Fatal("search did not match the resource details")
				}
				if err := tester.Click(localized(locale, "编辑", "Edit") + ": Synthetic resource 01"); err != nil || opened != "synthetic-01" {
					t.Fatal("filtered action opened the wrong resource", err, opened)
				}
				choice, ok := tester.AccessByRole(ui.RoleRadio, view.filterLabels()[1])
				if !ok || choice.Offscreen {
					t.Fatal("resource filter is unavailable", choice)
				}
				tester.ClickAt(choice.Bounds.X+choice.Bounds.W/2, choice.Bounds.Y+choice.Bounds.H/2)
				tester.Frame()
				if !tester.HasText(localized(locale, "没有找到匹配的资源", "No matching resources")) || tester.HasText(view.msg("empty")) {
					t.Fatal("filtered empty result confused with an empty directory")
				}
				if err := tester.Click(localized(locale, "清除筛选", "Clear filters")); err != nil {
					t.Fatal(err)
				}
				tester.Frame()
				for index := 0; index < 3; index++ {
					if _, ok := tester.AccessByRole(role, fmt.Sprintf("Synthetic resource %02d", index)); !ok {
						t.Fatal("clearing filters did not restore the directory")
					}
				}
				if view.count() != 3 {
					t.Fatal("browsing changed the resource snapshot")
				}
			})
		}
	}
}

func TestResourceEmptyLayoutKeepsOneReachableAddAction(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
				for _, width := range []int{1100, 1440} {
					t.Run(fmt.Sprintf("%s-%s-%t-%d", kind, locale, dark, width), func(t *testing.T) {
						view := resourceParityView(kind, locale, 0)
						view.Files = &NativeMemberFiles{}
						opened := false
						view.Open = func(openKind application.ResourceKind, id string) { opened = openKind == kind && id == "" }
						shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Route: "/" + string(kind), Content: view.View}
						tester := ui.NewTesterWith(shell.View, width, 760, ui.TesterOptions{Dark: dark})
						count := 0
						for _, node := range tester.AccessAll() {
							if node.Role == ui.RoleButton && node.Name == view.msg("add") {
								count++
								if node.Offscreen || node.Bounds.X < 0 || node.Bounds.X+node.Bounds.W > float32(width) || node.Bounds.Y+node.Bounds.H > 760 {
									t.Fatal("empty-state action is clipped", node)
								}
							}
						}
						if count != 1 || !tester.HasText(view.layoutCopy().emptyCopy) || tester.HasText(view.msg("copy")) {
							t.Fatal("empty layout lacks focused guidance or duplicates editor instructions", count)
						}
						if err := tester.Click(view.msg("add")); err != nil || !opened {
							t.Fatal("empty-state action did not open a new scoped editor", err)
						}
						captureParity(t, tester, fmt.Sprintf("resources-empty-%s-%s-%t-%d", kind, locale, dark, width))
					})
				}
			}
		}
	}
}

func TestResourceBrowserStateStaysWithItsPage(t *testing.T) {
	view := resourceParityView(application.Models, "en-US", 2)
	tester := ui.NewTester(view.View, 1100, 900)
	if err := tester.Click(view.layoutCopy().search); err != nil {
		t.Fatal(err)
	}
	tester.Type("01")
	view.Kind = application.Templates
	tester.Frame()
	if _, ok := tester.AccessByRole(ui.RoleListItem, "Synthetic resource 00"); !ok {
		t.Fatal("model search leaked into templates")
	}
	view.Kind = application.Models
	tester.Frame()
	if _, ok := tester.AccessByRole(ui.RoleListItem, "Synthetic resource 00"); ok {
		t.Fatal("model search was lost after switching pages")
	}
}
