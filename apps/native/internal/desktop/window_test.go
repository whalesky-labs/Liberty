package desktop

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestShellCentersContentWhenResized(t *testing.T) {
	for _, route := range []string{"/jobs", "/settings", "/farm"} {
		for _, scroll := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/scroll=%v", route, scroll), func(t *testing.T) {
				shell := Shell{
					Locale: "en-US", Route: route, Preferences: domain.DefaultUIPreferences(),
					Content: func(c *ui.Context) {
						var root ui.Element
						if scroll {
							root = ui.Scroll(c).Fill()
						} else {
							root = ui.Column(c).Fill()
						}
						root.Children(func() {
							ui.Row(c).FillWidth().Height(80).Label("Page content")
						})
					},
				}
				if route == "/settings" {
					shell.Sidebar = settingsParityView("en-US", "appearance").Sidebar
				}
				tester := ui.NewTester(shell.View, 1100, 960)
				for _, width := range []int{1100, 1440, 1920, 2560, 1100} {
					tester.SetSize(width, 960)
					content, ok := tester.Find("Page content")
					available := float32(width - presentation.SidebarWidth - 24)
					wantWidth := min(float32(1180), available)
					if route == "/farm" {
						wantWidth = available
					}
					left := float32(presentation.SidebarWidth+12) + (available-wantWidth)/2
					if !ok || math.Abs(float64(content.W-wantWidth)) > 1 || math.Abs(float64(content.X-left)) > 1 || content.H < 79 {
						t.Fatalf("page content did not stay centered at width %d: %+v, expected x=%.1f width=%.1f", width, content, left, wantWidth)
					}
				}
				if route == "/settings" {
					tester.SetSize(760, 960)
					content, ok := tester.Find("Page content")
					if !ok || math.Abs(float64(content.X-12)) > 1 || math.Abs(float64(content.W-736)) > 1 {
						t.Fatal("compact settings lost its available content width", content)
					}
				}
			})
		}
	}
}

func TestSettingsBackReturnsToPreviousApplicationPage(t *testing.T) {
	for _, path := range []string{"/", "/work-market", "/farm", "/work-game/factory", "/members", "/pet-store", "/jobs/synthetic-id"} {
		t.Run(path, func(t *testing.T) {
			workspace := Workspace{tab: path, view: &Meetings{Source: "local"}, runtime: &RuntimeView{search: "old runtime search"}, settings: &SettingsView{Client: &client.SettingsController{}, search: "old settings search"}}
			workspace.navigate("/settings")
			workspace.navigate("/settings")
			workspace.returnToApp()
			if workspace.tab != path || workspace.settings.search != "" || workspace.runtime.search != "" {
				t.Fatalf("settings return lost page or search reset: page=%s settings=%s runtime=%s", workspace.tab, workspace.settings.search, workspace.runtime.search)
			}
		})
	}
	workspace := Workspace{tab: "/jobs", view: &Meetings{status: "completed"}}
	workspace.navigate("/settings")
	if workspace.lastAppPath != "/jobs?status=completed" {
		t.Fatal("settings discarded the completed-results queue filter", workspace.lastAppPath)
	}
	workspace = Workspace{tab: "/jobs/new", view: &Meetings{Source: "local"}}
	workspace.navigate("/settings")
	workspace.view.Source = "remote"
	workspace.returnToApp()
	if workspace.tab != "/" {
		t.Fatal("settings returned to unavailable remote upload", workspace.tab)
	}
}

func TestShellLeavesRoomForNativeTitlebarControls(t *testing.T) {
	platforms := []struct {
		name string
		bar  ui.TitleBar
	}{
		{name: "macos", bar: ui.TitleBar{Height: 60, Left: 96}},
		{name: "windows", bar: ui.TitleBar{Height: presentation.TitlebarHeight, Right: 138}},
	}
	sizes := []struct {
		name          string
		width, height int
	}{{"minimum", presentation.WindowMinWidth, presentation.WindowMinHeight}, {"default", presentation.WindowWidth, presentation.WindowHeight}}
	for _, platform := range platforms {
		for _, size := range sizes {
			t.Run(platform.name+"/"+size.name, func(t *testing.T) {
				shell := &Shell{Locale: "en-US", Preferences: domain.DefaultUIPreferences(), Metrics: &domain.ProcessMetrics{MemoryBytes: 123 * 1024 * 1024, SampledAt: time.Now()}}
				tester := ui.NewTester(shell.View, size.width, size.height)
				tester.SetTitleBar(platform.bar)

				leftInset := max(float32(16), float32(platform.bar.Left+16))
				rightInset := max(float32(16), float32(platform.bar.Right+12))
				barHeight := max(float32(presentation.TitlebarHeight), float32(platform.bar.Height))
				toggle, ok := tester.Find(shell.msg("shell.collapseMenu"))
				if !ok || toggle.X < leftInset || toggle.Y < 0 || toggle.Y+toggle.H > barHeight {
					t.Fatalf("menu toggle overlaps native controls or escapes the titlebar: %+v, found %v", toggle, ok)
				}
				metric, ok := tester.Find(presentation.Message("en-US", "shell.cpu"))
				if !ok || metric.X < toggle.X+toggle.W {
					t.Fatalf("titlebar metrics overlap the menu toggle: %+v, found %v", metric, ok)
				}
				memory, ok := tester.Find("123 MiB")
				if !ok {
					t.Fatal("memory metric is missing")
				}
				pet, ok := tester.Find(presentation.Message("en-US", "shell.petDisabled"))
				if !ok || memory.X+memory.W > pet.X {
					t.Fatalf("titlebar metrics overlap actions: memory=%+v pet=%+v found=%v", memory, pet, ok)
				}
				github, ok := tester.Find(presentation.Message("en-US", "shell.github"))
				if !ok || github.X+github.W > float32(size.width)-rightInset {
					t.Fatalf("titlebar actions overlap native controls: %+v, found %v", github, ok)
				}
				if metric.Y < 0 || metric.Y+metric.H > barHeight || github.Y < 0 || github.Y+github.H > barHeight {
					t.Fatalf("titlebar content escaped the titlebar: metric=%+v github=%+v", metric, github)
				}
			})
		}
	}
}

func TestShellMetricsShowFreshSamplesAndNeverEstimateGraphics(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		cpu := 250.5
		shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences()}
		tester := ui.NewTester(shell.View, 1440, 960)
		if tester.HasText(presentation.Message(locale, "shell.graphics")) || tester.HasText("0.0%") {
			t.Fatal("unavailable metrics were presented as measured usage")
		}
		shell.Metrics = &domain.ProcessMetrics{CPUPercent: &cpu, MemoryBytes: 379 << 20, MemoryKind: domain.ProcessMemoryFootprint, SampledAt: time.Now()}
		tester.Frame()
		if !tester.HasText("250.5%") || !tester.HasText("379 MiB") {
			t.Fatal("live process metrics were not rendered")
		}
		shell.Metrics.SampledAt = time.Now().Add(-4 * time.Second)
		tester.Frame()
		if tester.HasText("250.5%") || tester.HasText("379 MiB") {
			t.Fatal("stale sample remained visible")
		}
		shell.Metrics = &domain.ProcessMetrics{MemoryBytes: 280 << 20, MemoryKind: domain.ProcessMemoryWorkingSet, SampledAt: time.Now()}
		tester.Frame()
		if !tester.HasText("280 MiB") || tester.HasText("0.0%") {
			t.Fatal("first sample invented CPU or hid measured memory")
		}
	}
}

func TestShellModeReadFailureOffersRetryInBothLocales(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			calls := 0
			shell := &Shell{
				Locale:         locale,
				Preferences:    domain.DefaultUIPreferences(),
				StatusMessage:  presentation.Message(locale, "shell.modeReadFailed"),
				StatusTone:     "error",
				ModeReadFailed: true,
				RetryMode:      func() { calls++ },
			}
			tester := ui.NewTester(shell.View, 1100, 760)
			if _, ok := tester.AccessByRole(ui.RoleStatus, shell.StatusMessage); !ok {
				t.Fatal("mode read error is not exposed as an accessible status")
			}
			label := presentation.Message(locale, "common.retry")
			retry, ok := tester.AccessByRole(ui.RoleButton, label)
			if !ok || retry.Disabled {
				t.Fatalf("mode retry is not available to %s users: %+v", locale, retry)
			}
			tester.ClickAt(retry.Bounds.X+retry.Bounds.W/2, retry.Bounds.Y+retry.Bounds.H/2)
			if calls != 1 {
				t.Fatal("retry button did not invoke its action")
			}

			shell.ModeReadBusy = true
			tester.Frame()
			retry, ok = tester.AccessByRole(ui.RoleButton, label)
			if !ok || !retry.Disabled {
				t.Fatalf("mode retry remained enabled during an in-flight read: %+v", retry)
			}
		})
	}
}

func TestMainWindowGeometryAndPlatformChrome(t *testing.T) {
	platforms := []struct {
		name                 string
		mainStyle            mygo.TitleBarStyle
		mainTitlebarHeight   int
		workspaceStyle       mygo.TitleBarStyle
		workspaceTitlebar    int
		trafficLightPosition *mygo.Point
	}{
		{name: "darwin", mainStyle: mygo.TitleBarHiddenInset, workspaceStyle: mygo.TitleBarHiddenInset, trafficLightPosition: &mygo.Point{X: presentation.TrafficLightX, Y: presentation.TrafficLightY}},
		{name: "windows", mainStyle: mygo.TitleBarHidden, mainTitlebarHeight: presentation.TitlebarHeight, workspaceStyle: mygo.TitleBarDefault},
		{name: "linux", mainStyle: mygo.TitleBarDefault, workspaceStyle: mygo.TitleBarDefault},
	}
	for _, platform := range platforms {
		t.Run(platform.name, func(t *testing.T) {
			main := mainWindowOptionsForPlatform(platform.name, func(*ui.Context) {})
			assertWindowGeometry(t, main)
			if main.TitleBarStyle != platform.mainStyle || main.TitleBarHeight != platform.mainTitlebarHeight {
				t.Fatalf("unexpected %s main window titlebar: %+v", platform.name, main)
			}
			assertTrafficLightPosition(t, main.TrafficLightPosition, platform.trafficLightPosition)

			workspace := workspaceWindowOptionsForPlatform(platform.name, domain.DefaultUIPreferences(), func(*ui.Context) {})
			assertWindowGeometry(t, workspace)
			if workspace.TitleBarStyle != platform.workspaceStyle || workspace.TitleBarHeight != platform.workspaceTitlebar {
				t.Fatalf("unexpected %s workspace titlebar: %+v", platform.name, workspace)
			}
			assertTrafficLightPosition(t, workspace.TrafficLightPosition, platform.trafficLightPosition)
		})
	}
}

func assertWindowGeometry(t *testing.T, options mygo.WindowOptions) {
	t.Helper()
	if options.Width != presentation.WindowWidth || options.Height != presentation.WindowHeight || options.MinWidth != presentation.WindowMinWidth || options.MinHeight != presentation.WindowMinHeight || options.DisableResize {
		t.Fatalf("unexpected native window geometry or resizability: %+v", options)
	}
}

func assertTrafficLightPosition(t *testing.T, actual, expected *mygo.Point) {
	t.Helper()
	if expected == nil {
		if actual != nil {
			t.Fatalf("unexpected traffic-light position: %+v", actual)
		}
		return
	}
	if actual == nil || *actual != *expected {
		t.Fatalf("unexpected traffic-light position: %+v", actual)
	}
}
