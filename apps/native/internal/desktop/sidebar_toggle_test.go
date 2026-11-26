package desktop

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestShellSecondaryMenuToggleReclaimsSpace(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, sample := range []struct {
				route string
				width int
			}{
				{"/jobs", 1100}, {"/jobs", 1440}, {"/jobs", 2560},
				{"/settings", 1100}, {"/settings", 760}, {"/farm", 2560},
			} {
				name := fmt.Sprintf("%s-dark-%v-%s-%d", locale, dark, sample.route[1:], sample.width)
				t.Run(name, func(t *testing.T) {
					shell := &Shell{Locale: locale, Route: sample.route, Preferences: domain.DefaultUIPreferences()}
					shell.Navigate = func(path string) { shell.Route = path }
					var measuredWidth float32
					shell.Content = func(c *ui.Context) {
						measuredWidth = shellContentWidth(c)
						ui.Column(c).Fill().Label("Page content")
					}
					menuLabel := localized(locale, "模块菜单", "Module navigation")
					compact := sample.route == "/settings" && sample.width <= 760
					if sample.route == "/settings" {
						view := settingsParityView(locale, "appearance")
						shell.Sidebar = view.Sidebar
						menuLabel = view.msg("navigationLabel")
					}
					tester := ui.NewTesterWith(shell.View, sample.width, 900, ui.TesterOptions{Dark: dark})
					before, ok := tester.Find("Page content")
					if !ok {
						t.Fatal("page content is missing")
					}
					collapseLabel, expandLabel := shell.msg("shell.collapseMenu"), shell.msg("shell.expandMenu")
					toggle, ok := tester.AccessByRole(ui.RoleButton, collapseLabel)
					if !ok || toggle.Disabled || toggle.Offscreen || !toggle.Expanded {
						t.Fatal("menu toggle does not expose its initial expanded state", toggle)
					}
					if err := tester.Click(collapseLabel); err != nil {
						t.Fatal(err)
					}
					if _, ok := tester.AccessByRole(ui.RoleNavigation, menuLabel); ok {
						t.Fatal("collapsed secondary menu remains in the accessibility tree")
					}
					if rail, ok := tester.AccessByRole(ui.RoleNavigation, localized(locale, "主导航", "Primary navigation")); !ok || rail.Offscreen {
						t.Fatal("collapsing the menu hid primary navigation", rail)
					}
					after, ok := tester.Find("Page content")
					available := float32(sample.width - presentation.ActivityBarWidth - 2*shellContentPaddingX)
					wantWidth := min(float32(shellContentMaxWidth), available)
					left := float32(presentation.ActivityBarWidth+shellContentPaddingX) + (available-wantWidth)/2
					if sample.route == "/farm" {
						wantWidth, left = available, presentation.ActivityBarWidth+shellContentPaddingX
					}
					if compact {
						wantWidth, left = float32(sample.width-2*shellContentPaddingX), shellContentPaddingX
					}
					if !ok || math.Abs(float64(after.X-left)) > 1 || math.Abs(float64(after.W-wantWidth)) > 1 {
						t.Fatalf("content did not reclaim menu space: %+v, want x=%.1f width=%.1f", after, left, wantWidth)
					}
					if measuredWidth != min(float32(shellContentMaxWidth), available) {
						t.Fatal("responsive content still measures the expanded sidebar", measuredWidth)
					}
					if compact && after.H <= before.H {
						t.Fatal("compact settings did not reclaim the menu's height", before, after)
					}
					if toggle, ok := tester.AccessByRole(ui.RoleButton, expandLabel); !ok || toggle.Expanded || toggle.Offscreen {
						t.Fatal("collapsed menu cannot be expanded", toggle)
					}
					if err := tester.Click(expandLabel); err != nil {
						t.Fatal(err)
					}
					restored, ok := tester.Find("Page content")
					if !ok || restored != before {
						t.Fatal("expanding the menu did not restore the original layout", before, restored)
					}
					if menu, ok := tester.AccessByRole(ui.RoleNavigation, menuLabel); !ok || menu.Offscreen {
						t.Fatal("secondary navigation was not restored", menu)
					}
				})
			}
		}
	}
}

func TestShellSecondaryMenuTogglePreservesWindowStateAndKeyboardFocus(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			route, draft, mounts := "/jobs/new", "Synthetic draft", 0
			collapseLabel := presentation.Message(locale, "shell.collapseMenu")
			expandLabel := presentation.Message(locale, "shell.expandMenu")
			tester := ui.NewTester(func(c *ui.Context) {
				// Match Workspace.View, which creates a new Shell for each frame.
				shell := &Shell{Locale: locale, Route: route, Preferences: domain.DefaultUIPreferences()}
				shell.Navigate = func(path string) { route = path }
				shell.Content = func(c *ui.Context) {
					content := ui.Column(c.Key("synthetic-page")).Fill()
					ui.Local(content, "draft-view", func() bool { mounts++; return true })
					content.Children(func() { ui.TextInput(c, &draft).Label("Synthetic draft") })
				}
				shell.View(c)
			}, 1100, 760)
			if err := tester.Click("Synthetic draft"); err != nil {
				t.Fatal(err)
			}
			tester.Type(" updated")
			wantDraft := draft
			if wantDraft == "Synthetic draft" {
				t.Fatal("draft input did not accept typing")
			}
			if err := tester.Click(collapseLabel); err != nil {
				t.Fatal(err)
			}
			if route != "/jobs/new" || mounts != 1 || !tester.Focused(expandLabel) {
				t.Fatal("collapsing reset the current page, its state or button focus", route, mounts)
			}
			tester.Key(0, ui.KeyEnter)
			if _, ok := tester.AccessByRole(ui.RoleButton, collapseLabel); !ok || !tester.Focused(collapseLabel) {
				t.Fatal("keyboard cannot expand the secondary menu")
			}
			tester.Key(0, ui.KeySpace)
			if _, ok := tester.AccessByRole(ui.RoleButton, expandLabel); !ok {
				t.Fatal("keyboard cannot collapse the secondary menu")
			}
			if err := tester.Click(localized(locale, "资源", "Resources")); err != nil || route != "/models" {
				t.Fatal("primary navigation stopped working while collapsed", route, err)
			}
			tester.SetSize(1440, 900)
			if _, ok := tester.AccessByRole(ui.RoleButton, expandLabel); !ok || mounts != 1 || draft != wantDraft {
				t.Fatal("navigation or resizing reset the window's menu state or draft", mounts, draft)
			}
		})
	}
}

func TestShellSecondaryMenuToggleVisualStates(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-dark-%v", locale, dark), func(t *testing.T) {
				dashboard := NewDashboardView(&client.DashboardController{Overview: dashboardFixture()}, locale)
				shell := &Shell{Locale: locale, Route: "/", Source: "local", Version: presentation.Version, Preferences: domain.DefaultUIPreferences(), Content: dashboard.View}
				shell.Navigate = func(path string) { shell.Route = path }
				tester := ui.NewTesterWith(shell.View, 1100, 900, ui.TesterOptions{Dark: dark})
				tester.SetTitleBar(ui.TitleBar{Height: 60, Left: 96})
				name := fmt.Sprintf("%s-dark-%v", locale, dark)
				captureParity(t, tester, "secondary-menu-expanded-"+name)
				if err := tester.Click(shell.msg("shell.collapseMenu")); err != nil {
					t.Fatal(err)
				}
				captureParity(t, tester, "secondary-menu-collapsed-"+name)
				view := settingsParityView(locale, "appearance")
				shell.Route, shell.Sidebar, shell.Content = "/settings", view.Sidebar, view.View
				tester.Frame()
				captureParity(t, tester, "secondary-settings-collapsed-"+name)
			})
		}
	}
}
