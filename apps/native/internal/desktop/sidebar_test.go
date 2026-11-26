package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestShellModulesExposeOnlyTheirOwnPages(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			shell := &Shell{Locale: locale, Route: "/", Preferences: domain.DefaultUIPreferences()}
			shell.Navigate = func(path string) { shell.Route = path }
			tester := ui.NewTester(shell.View, 1100, 760)
			for _, section := range presentation.Navigation {
				label := localized(locale, section.Zh, section.En)
				if err := tester.Click(label); err != nil {
					t.Fatal(err)
				}
				for _, item := range section.Items {
					if err := tester.Click(shell.msg("nav." + item.Key)); err != nil || shell.Route != item.Path {
						t.Fatal("module page is unreachable", item.Path, shell.Route, err)
					}
					link, ok := tester.AccessByRole(ui.RoleButton, shell.msg("nav."+item.Key))
					if !ok || link.Disabled || link.Offscreen || link.Current != ui.CurrentPage {
						t.Fatal("selected menu lacks current page semantics", link)
					}
				}
				for _, other := range presentation.Navigation {
					if other.Key == section.Key {
						continue
					}
					for _, item := range other.Items {
						if _, ok := tester.AccessByRole(ui.RoleButton, shell.msg("nav."+item.Key)); ok {
							t.Fatal("inactive module still occupies the secondary menu", item.Path)
						}
					}
				}
			}
			if err := tester.Click(shell.msg("nav.settings")); err != nil || shell.Route != "/settings" {
				t.Fatal("pinned settings entry is unreachable", err)
			}
		})
	}
}

func TestShellModuleSelectionPreservesDetailsAndRespectsCapabilities(t *testing.T) {
	shell := &Shell{Locale: "en-US", Route: "/jobs/synthetic-detail", Preferences: domain.DefaultUIPreferences()}
	navigations := 0
	shell.Navigate = func(path string) { shell.Route = path; navigations++ }
	tester := ui.NewTester(shell.View, 1100, 760)
	for _, sample := range []struct{ route, module, page string }{
		{"/jobs/synthetic-detail", "Work", "Job Queue"},
		{"/jobs?status=completed", "Work", "Job Queue"},
		{"/jobs/new", "Work", "New Job"},
		{"/farm", "Work Games", "Niuma Market"},
		{"/work-game/factory/assembly", "Work Games", "Niuma Market"},
	} {
		shell.Route = sample.route
		tester.Frame()
		for _, label := range []string{sample.module, sample.page} {
			link, ok := tester.AccessByRole(ui.RoleButton, label)
			if !ok || link.Current != ui.CurrentPage {
				t.Fatal("nested page lost its module or parent selection", sample.route, label, link)
			}
		}
		if err := tester.Click(sample.module); err != nil || shell.Route != sample.route || navigations != 0 {
			t.Fatal("clicking the current module reset its page", sample.route, err)
		}
	}
	shell.Route = "/jobs/synthetic-detail"
	tester.Frame()
	if err := tester.Click("Job Queue"); err != nil || shell.Route != "/jobs" || navigations != 1 {
		t.Fatal("selected parent menu cannot return from a detail", shell.Route, err)
	}
	shell.Available = func(path string) bool { return path == "/templates" || path == "/" }
	tester.Frame()
	if err := tester.Click("Resources"); err != nil || shell.Route != "/templates" {
		t.Fatal("module did not choose its first available page", shell.Route, err)
	}
	for _, label := range []string{"Work Games", "Companion", "Benefits", "Settings", "Models", "Members"} {
		assertDisabled(t, tester, label, true)
	}
	shell.Available = nil
	shell.Navigate = nil
	tester.Frame()
	for _, section := range presentation.Navigation {
		assertDisabled(t, tester, section.En, true)
	}
}

func TestShellModuleRailSupportsKeyboardNavigation(t *testing.T) {
	shell := &Shell{Locale: "en-US", Route: "/", Preferences: domain.DefaultUIPreferences()}
	shell.Navigate = func(path string) { shell.Route = path }
	tester := ui.NewTester(shell.View, 1100, 760)
	if err := tester.Click("Work"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyTab)
	if !tester.Focused("Work Games") {
		t.Fatal("keyboard focus does not follow the visible module order")
	}
	tester.Key(0, ui.KeyEnter)
	if shell.Route != "/work-market" {
		t.Fatal("keyboard cannot activate a module", shell.Route)
	}
}

func TestShellModuleLayoutKeepsSettingsPinnedAndContentClear(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{1100, 1440} {
				name := fmt.Sprintf("%s-dark-%v-%d", locale, dark, width)
				t.Run(name, func(t *testing.T) {
					dashboard := NewDashboardView(&client.DashboardController{Overview: dashboardFixture()}, locale)
					shell := &Shell{Locale: locale, Route: "/", Source: "local", Version: presentation.Version, Preferences: domain.DefaultUIPreferences(), Content: dashboard.View}
					shell.Navigate = func(path string) { shell.Route = path }
					dashboard.Navigate = shell.Navigate
					tester := ui.NewTesterWith(shell.View, width, 900, ui.TesterOptions{Dark: dark})
					rail, railOK := tester.AccessByRole(ui.RoleNavigation, localized(locale, "主导航", "Primary navigation"))
					menu, menuOK := tester.AccessByRole(ui.RoleNavigation, localized(locale, "模块菜单", "Module navigation"))
					settings, settingsOK := tester.AccessByRole(ui.RoleButton, shell.msg("nav.settings"))
					if !railOK || !menuOK || rail.Bounds.X != 0 || rail.Bounds.W != presentation.ActivityBarWidth || menu.Bounds.X < rail.Bounds.X+rail.Bounds.W || menu.Bounds.X+menu.Bounds.W > presentation.SidebarWidth {
						t.Fatal("navigation columns overlap", rail, menu)
					}
					if !settingsOK || settings.Offscreen || settings.Bounds.Y < 840 || settings.Bounds.X+settings.Bounds.W > rail.Bounds.W {
						t.Fatal("settings did not stay pinned to the bottom of the rail", settings)
					}
					captureParity(t, tester, "sidebar-work-"+name)
					view := settingsParityView(locale, "appearance")
					shell.Route, shell.Sidebar, shell.Content = "/settings", view.Sidebar, view.View
					tester.Frame()
					rail, railOK = tester.AccessByRole(ui.RoleNavigation, localized(locale, "主导航", "Primary navigation"))
					heading, headingOK := tester.AccessByRole(ui.RoleHeading, view.msg("appearance"))
					if !railOK || !headingOK || heading.Bounds.X < presentation.SidebarWidth {
						t.Fatal("settings discarded the module rail or overlapped its content", rail, heading)
					}
					captureParity(t, tester, "sidebar-settings-"+name)
				})
			}
		}
	}
}
