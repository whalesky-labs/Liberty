package desktop

import (
	"fmt"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestShellContentViewportReachesRightEdgeWhenResized(t *testing.T) {
	for _, route := range []string{"/jobs", "/settings", "/live", "/farm"} {
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
					rightPadding := 0
					if route == "/live" {
						rightPadding = shellContentPaddingX
					}
					available := float32(width - presentation.SidebarWidth - shellContentPaddingX - rightPadding)
					left := float32(presentation.SidebarWidth + shellContentPaddingX)
					if !ok || math.Abs(float64(content.W-available)) > 1 || math.Abs(float64(content.X-left)) > 1 || content.H < 79 {
						t.Fatalf("page viewport did not reach the content edge at width %d: %+v, expected x=%.1f width=%.1f", width, content, left, available)
					}
				}
				if route == "/settings" {
					tester.SetSize(760, 960)
					content, ok := tester.Find("Page content")
					if !ok || math.Abs(float64(content.X-12)) > 1 || math.Abs(float64(content.W-748)) > 1 {
						t.Fatal("compact settings lost its available content width", content)
					}
				}
			})
		}
	}
}

func TestApplicationMenuProvidesWorkspaceActionsAndNativeMenus(t *testing.T) {
	called := map[string]int{}
	var openedURLs []string
	menu := ApplicationMenu("zh-CN", ApplicationMenuActions{
		NewTaskEnabled: true,
		NewTask:        func() { called["new"]++ },
		OpenQueue:      func() { called["queue"]++ },
		OpenSettings:   func() { called["settings"]++ },
		CheckUpdates:   func() { called["updates"]++ },
		OpenURL:        func(url string) { openedURLs = append(openedURLs, url) },
	})
	items := menu.Items()
	wantHelpRole := mygo.MenuRole("")
	if runtime.GOOS == "darwin" {
		wantHelpRole = mygo.RoleHelp
	}
	if len(items) != 5 || items[0].Role != mygo.RoleAppMenu || items[1].Label != "文件" || items[2].Label != "编辑" || items[3].Label != "窗口" || items[4].Label != presentation.Message("zh-CN", "menu.help") || items[4].Role != wantHelpRole {
		t.Fatalf("unexpected application menu layout: %+v", items)
	}
	file := items[1].Submenu
	wantFileLength := 5
	if runtime.GOOS == "darwin" {
		wantFileLength = 4
	}
	if len(file) != wantFileLength || file[0].Label != presentation.Message("zh-CN", "nav.newJob") || file[0].Accelerator != "CmdOrCtrl+N" || file[0].Disabled || file[1].Label != presentation.Message("zh-CN", "nav.jobs") {
		t.Fatalf("unexpected File menu: %+v", file)
	}
	if runtime.GOOS == "darwin" {
		if file[2].Type != mygo.MenuItemSeparator || file[3].Role != mygo.RoleClose {
			t.Fatalf("macOS File menu should keep Settings in the application menu: %+v", file)
		}
	} else if file[2].Label != presentation.Message("zh-CN", "nav.settings") || file[2].Accelerator != "CmdOrCtrl+," || file[3].Type != mygo.MenuItemSeparator || file[4].Role != mygo.RoleClose {
		t.Fatalf("non-macOS File menu should retain Settings: %+v", file)
	}
	for index, key := range []string{"new", "queue"} {
		if file[index].Click == nil {
			t.Fatalf("menu action %q is not connected", key)
		}
		file[index].Click(file[index], nil)
		if called[key] != 1 {
			t.Fatalf("menu action %q was not invoked", key)
		}
	}
	if len(items[2].Submenu) < 6 || items[2].Submenu[0].Role != mygo.RoleUndo || items[3].Submenu[0].Role != mygo.RoleMinimize {
		t.Fatal("Edit and Window menus must keep MyGo's native role actions")
	}
	applicationMenu := items[0].Submenu
	if len(applicationMenu) != 11 || applicationMenu[0].Role != mygo.RoleAbout || applicationMenu[0].Label != presentation.Format("zh-CN", "menu.about", map[string]any{"app": mygo.App.Name()}) || applicationMenu[1].Label != "设置" || applicationMenu[1].Accelerator != "CmdOrCtrl+," || applicationMenu[2].Label != "检查更新…" || applicationMenu[3].Type != mygo.MenuItemSeparator || applicationMenu[4].Role != mygo.RoleServices || applicationMenu[4].Label != "服务" || applicationMenu[4].Submenu == nil || applicationMenu[5].Type != mygo.MenuItemSeparator || applicationMenu[6].Role != mygo.RoleHide || applicationMenu[6].Label != presentation.Format("zh-CN", "menu.hide", map[string]any{"app": mygo.App.Name()}) || applicationMenu[7].Role != mygo.RoleHideOthers || applicationMenu[7].Label != "隐藏其他应用" || applicationMenu[8].Role != mygo.RoleUnhide || applicationMenu[8].Label != "显示全部" || applicationMenu[9].Type != mygo.MenuItemSeparator || applicationMenu[10].Role != mygo.RoleQuit || applicationMenu[10].Label != presentation.Format("zh-CN", "menu.quit", map[string]any{"app": mygo.App.Name()}) {
		t.Fatalf("application menu did not use localized native actions: %+v", applicationMenu)
	}
	settingsItem := applicationMenu[1]
	if runtime.GOOS != "darwin" {
		settingsItem = file[2]
	}
	if settingsItem.Click == nil {
		t.Fatal("Settings action is not connected")
	}
	settingsItem.Click(settingsItem, nil)
	if called["settings"] != 1 {
		t.Fatal("Settings action was not invoked")
	}
	if applicationMenu[2].Click == nil {
		t.Fatal("application menu check-updates action is not connected")
	}
	applicationMenu[2].Click(applicationMenu[2], nil)
	if called["updates"] != 1 {
		t.Fatal("application menu check-updates action was not invoked", called)
	}
	help := items[4].Submenu
	if len(help) != 4 || help[0].Label != presentation.Message("zh-CN", "menu.documentation") || help[1].Label != presentation.Message("zh-CN", "menu.reportIssue") || help[2].Type != mygo.MenuItemSeparator || help[3].Label != presentation.Message("zh-CN", "menu.checkUpdates") {
		t.Fatalf("unexpected Help menu: %+v", help)
	}
	for _, item := range []*mygo.MenuItem{help[0], help[1], help[3]} {
		if item.Click == nil {
			t.Fatalf("help action %q is not connected", item.Label)
		}
		item.Click(item, nil)
	}
	wantURLs := []string{presentation.DocumentationURL, presentation.IssuesURL}
	if fmt.Sprint(openedURLs) != fmt.Sprint(wantURLs) {
		t.Fatalf("Help menu opened unexpected URLs: got %v, want %v", openedURLs, wantURLs)
	}
	if called["updates"] != 2 {
		t.Fatal("update menu must invoke the in-app checker", called)
	}
	english := ApplicationMenu("en-US").Items()[4]
	if english.Label != "Help" || english.Submenu[0].Label != "Documentation" || english.Submenu[1].Label != "Report an Issue" || english.Submenu[3].Label != "Check for Updates…" {
		t.Fatalf("Help menu did not localize to English: %+v", english)
	}
	englishApplication := ApplicationMenu("en-US").Items()[0].Submenu
	if englishApplication[0].Label != presentation.Format("en-US", "menu.about", map[string]any{"app": mygo.App.Name()}) || englishApplication[1].Label != "Settings" || englishApplication[2].Label != "Check for Updates…" || englishApplication[4].Label != "Services" || englishApplication[6].Label != presentation.Format("en-US", "menu.hide", map[string]any{"app": mygo.App.Name()}) || englishApplication[7].Label != "Hide Others" || englishApplication[8].Label != "Show All" || englishApplication[10].Label != presentation.Format("en-US", "menu.quit", map[string]any{"app": mygo.App.Name()}) {
		t.Fatalf("application menu did not localize to English: %+v", englishApplication)
	}
}

func TestApplicationMenuItemsUseSharedIcons(t *testing.T) {
	menu := ApplicationMenu("zh-CN", ApplicationMenuActions{})
	items := menu.Items()
	app := items[0].Submenu
	file := items[1].Submenu
	help := items[4].Submenu

	for name, item := range map[string]*mygo.MenuItem{
		"about": app[0], "gear": app[1], "refresh": app[2], "services": app[4],
		"hideApp": app[6], "hideOthers": app[7], "showAll": app[8], "quit": app[10],
		"plus": file[0], "tray": file[1], "close": file[len(file)-1],
		"documentation": help[0], "reportIssue": help[1],
	} {
		if item.Icon == nil || item.Icon != menuIcon(name) {
			t.Errorf("menu item %q does not use its shared icon", item.Label)
		}
	}
	if fileSettings := fileSettingsMenuItem(file); fileSettings != nil && fileSettings.Icon != menuIcon("gear") {
		t.Fatalf("File Settings does not use the settings icon: %+v", fileSettings)
	}
	for _, item := range []*mygo.MenuItem{app[3], app[5], app[9], help[2]} {
		if item.Type != mygo.MenuItemSeparator || item.Icon != nil {
			t.Fatalf("separator should not have an icon: %+v", item)
		}
	}
	if help[3].Icon != menuIcon("refresh") {
		t.Fatal("Help menu update action does not use the update icon")
	}
}

func fileSettingsMenuItem(items []*mygo.MenuItem) *mygo.MenuItem {
	for _, item := range items {
		if item.Label == presentation.Message("zh-CN", "menu.settings") {
			return item
		}
	}
	return nil
}

func TestApplicationMenuDisablesUnavailableActions(t *testing.T) {
	menu := ApplicationMenu("en-US", ApplicationMenuActions{
		NewTaskEnabled: false,
		NewTask:        func() {},
	})
	file := menu.Items()[1].Submenu
	update := menu.Items()[4].Submenu[3]
	if !update.Disabled || update.Click != nil {
		t.Fatal("unavailable update service must not open a website")
	}
	applicationItems := menu.Items()[0].Submenu
	appSettings := applicationItems[1]
	appUpdate := applicationItems[2]
	if !appSettings.Disabled || appSettings.Click != nil {
		t.Fatal("unavailable Settings action should be disabled", appSettings)
	}
	if runtime.GOOS != "darwin" {
		fileSettings := menu.Items()[1].Submenu[2]
		if !fileSettings.Disabled || fileSettings.Click != nil {
			t.Fatal("non-macOS Settings action should be disabled when unavailable", fileSettings)
		}
	}
	if !appUpdate.Disabled || appUpdate.Click != nil {
		t.Fatal("unavailable app-menu update action must be disabled", appUpdate)
	}
	if !file[0].Disabled || !file[1].Disabled || file[1].Click != nil {
		t.Fatal("unavailable new-job and queue actions should be disabled", file[:2])
	}
	if runtime.GOOS == "darwin" {
		if file[2].Type != mygo.MenuItemSeparator {
			t.Fatal("macOS File menu should not duplicate Settings", file)
		}
	} else if !file[2].Disabled || file[2].Click != nil {
		t.Fatal("unavailable Settings navigation should be disabled", file[2])
	}
}

func TestApplicationHelpMenuRoleKeepsHelpVisibleAcrossPlatforms(t *testing.T) {
	for platform, want := range map[string]mygo.MenuRole{
		"darwin":  mygo.RoleHelp,
		"linux":   "",
		"windows": "",
	} {
		if got := applicationHelpMenuRole(platform); got != want {
			t.Fatalf("unexpected Help role on %s: got %q, want %q", platform, got, want)
		}
	}
}

func TestWorkspaceMenuActionsFollowCurrentModeAndCapabilities(t *testing.T) {
	workspace := &Workspace{view: &Meetings{Source: "remote"}, newJob: &NewJobView{}}
	remote := workspace.applicationMenuActions()
	if remote.NewTaskEnabled || remote.NewTask != nil || remote.OpenQueue == nil || remote.OpenSettings != nil {
		t.Fatal("remote mode must suppress local upload and unavailable settings", remote)
	}
	workspace.view.Source = "local"
	workspace.runtime = &RuntimeView{}
	local := workspace.applicationMenuActions()
	if !local.NewTaskEnabled || local.NewTask == nil || local.OpenQueue == nil || local.OpenSettings == nil {
		t.Fatal("local mode must expose navigation supported by the workspace", local)
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
				if !ok || metric.X < presentation.SidebarWidth {
					t.Fatalf("metrics did not move into the content header: %+v, found %v", metric, ok)
				}
				previous := toggle
				for _, key := range []string{"back", "forward"} {
					button, found := tester.AccessByRole(ui.RoleButton, shell.msg("shell."+key))
					if !found || !button.Disabled || button.Offscreen || button.Bounds.X < previous.X+previous.W || button.Bounds.X+button.Bounds.W > presentation.SidebarWidth || button.Bounds.Y < 0 || button.Bounds.Y+button.Bounds.H > barHeight {
						t.Fatalf("history control overlaps native controls or the content header: %+v", button)
					}
					previous = button.Bounds
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
