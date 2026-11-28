package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestNativeSwitchabilityAuditRendersLocalAndRemoteModeChanges(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Source: "local"}
			tester := ui.NewTester(shell.View, presentation.WindowMinWidth, presentation.WindowMinHeight)

			localMode := presentation.Message(locale, "shell.localMode")
			localReady := presentation.Message(locale, "shell.localReady")
			remoteMode := presentation.Message(locale, "shell.remoteMode")
			remoteReady := presentation.Message(locale, "shell.remoteReady")
			if !tester.HasText(localMode) || tester.HasText(remoteMode) || tester.HasText(localReady) || tester.HasText(remoteReady) {
				t.Fatal("initial local mode did not expose the expected unready state")
			}

			shell.SourceReady = true
			tester.Frame()
			if !tester.HasText(localReady) || tester.HasText(remoteReady) {
				t.Fatal("local readiness did not become visible without changing the active mode")
			}

			shell.Source, shell.SourceReady = "remote", false
			tester.Frame()
			if !tester.HasText(remoteMode) || tester.HasText(localMode) || tester.HasText(remoteReady) || tester.HasText(localReady) {
				t.Fatal("remote mode switch did not replace the visible local status")
			}

			shell.SourceReady = true
			tester.Frame()
			if !tester.HasText(remoteReady) || tester.HasText(localReady) {
				t.Fatal("remote readiness did not become visible after capability recovery")
			}
		})
	}
}

func TestNativeSwitchabilityAuditWorkspaceModeRefreshChangesRenderedSnapshot(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			service := &workspaceModeService{queues: make(chan domain.QueueQuery, 4), dashboards: make(chan string, 4), source: "local"}
			callbacks := make(chan func(), 8)
			dispatch := func(callback func()) { callbacks <- callback }
			meetings := client.New(service, dispatch, func() {})
			dashboardClient := client.NewDashboard(service, dispatch, func() {})
			t.Cleanup(meetings.Close)
			t.Cleanup(dashboardClient.Close)

			view := NewMeetings(meetings, locale)
			view.Source = "local"
			dashboard := NewDashboardView(dashboardClient, locale)
			dashboardClient.Overview = &domain.DashboardOverview{Source: "local"}
			workspace := &Workspace{
				controller: meetings,
				view:       view,
				dashboard:  dashboard,
				tab:        "/",
				preferences: func() domain.UIPreferences {
					preferences := domain.DefaultUIPreferences()
					preferences.Locale = locale
					return preferences
				}(),
			}
			tester := ui.NewTester(workspace.View, presentation.WindowMinWidth, presentation.WindowMinHeight)
			if !tester.HasText(presentation.Message(locale, "shell.localMode")) || tester.HasText(localized(locale, "远端任务快照", "Remote snapshot")) {
				t.Fatal("workspace did not initially render the local source")
			}

			service.source = "remote"
			workspace.applyProcessingMode("remote")
			drainWorkspaceModeRefresh(t, callbacks, service, "remote")
			tester.Frame()
			if !tester.HasText(presentation.Message(locale, "shell.remoteMode")) || tester.HasText(presentation.Message(locale, "shell.localMode")) || !tester.HasText(localized(locale, "远端任务快照", "Remote snapshot")) || dashboardClient.Overview.Source != "remote" {
				t.Fatal("remote mode did not update the rendered shell and dashboard snapshot")
			}

			service.source = "local"
			workspace.applyProcessingMode("local")
			drainWorkspaceModeRefresh(t, callbacks, service, "local")
			tester.Frame()
			if !tester.HasText(presentation.Message(locale, "shell.localMode")) || tester.HasText(presentation.Message(locale, "shell.remoteMode")) || tester.HasText(localized(locale, "远端任务快照", "Remote snapshot")) || dashboardClient.Overview.Source != "local" {
				t.Fatal("switching back to local did not replace the rendered shell and dashboard snapshot")
			}
		})
	}
}

func TestSettingsSwitchabilityAuditBackButtonReturnsToApplication(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 4)
			service := settingsParityService{saved: make(chan domain.Settings, 1)}
			settingsController := client.NewSettings(service, func(callback func()) { callbacks <- callback }, func() {}, func(domain.UIPreferences) {})
			t.Cleanup(settingsController.Close)
			settingsController.Load()
			(<-callbacks)()
			if !settingsController.Loaded || settingsController.Dirty() {
				t.Fatal("settings audit fixture did not load a clean persisted snapshot")
			}
			view := settingsParityView(locale, "appearance")
			view.Client = settingsController
			controller := &client.Controller{ResultController: &client.ResultController{}}
			dashboard := NewDashboardView(&client.DashboardController{Overview: dashboardFixture()}, locale)
			workspace := &Workspace{
				controller:  controller,
				view:        NewMeetings(controller, locale),
				dashboard:   dashboard,
				settings:    view,
				runtime:     &RuntimeView{Client: &client.RuntimeController{Panel: &domain.RuntimePanel{}}},
				tab:         "/settings",
				lastAppPath: "/",
				preferences: func() domain.UIPreferences {
					preferences := domain.DefaultUIPreferences()
					preferences.Locale = locale
					return preferences
				}(),
			}
			view.Back = workspace.returnToApp
			dashboard.Navigate, dashboard.CanNavigate = workspace.navigate, workspace.canNavigate
			tester := ui.NewTester(workspace.View, presentation.WindowWidth, presentation.WindowHeight)
			backLabel := view.msg("backToApp")
			back, ok := tester.AccessByRole(ui.RoleButton, backLabel)
			if !ok || back.Disabled || back.Offscreen {
				t.Fatalf("settings back control is not visibly actionable: %+v", back)
			}
			if err := tester.Click(backLabel); err != nil {
				t.Fatal(err)
			}
			tester.Frame()
			if workspace.tab != "/" || !tester.HasText(localized(locale, "会议工作台", "Meeting workspace")) || tester.HasText(view.msg("appearance")) {
				t.Fatal("settings back control did not visibly return to the application dashboard")
			}
		})
	}
}

func TestSettingsSwitchabilityAuditModeChoiceIsVisibleAfterCommit(t *testing.T) {
	callbacks := make(chan func(), 2)
	service := settingsParityService{saved: make(chan domain.Settings, 1)}
	controller := client.NewSettings(service, func(callback func()) { callbacks <- callback }, func() {}, func(domain.UIPreferences) {})
	defer controller.Close()

	view := settingsParityView("en-US", "runtimeOverview")
	view.Client = controller
	tester := ui.NewTester(view.View, 1100, 960)
	controller.Load()
	flushView(t, callbacks, tester)

	selector, ok := tester.AccessByRole(ui.RolePopUpButton, view.msg("runtimeMode"))
	if !ok || selector.Disabled {
		t.Fatalf("runtime mode selector is not actionable: %+v", selector)
	}
	tester.ClickAt(selector.Bounds.X+selector.Bounds.W/2, selector.Bounds.Y+selector.Bounds.H/2)
	tester.Key(0, ui.KeyEnd)
	tester.Key(0, ui.KeyEnter)
	flushView(t, callbacks, tester)
	saved := <-service.saved
	if saved.ProcessingMode != "remote" {
		t.Fatalf("settings did not commit the remote mode: %q", saved.ProcessingMode)
	}
	tester.Frame()
	selector, _ = tester.AccessByRole(ui.RolePopUpButton, view.msg("runtimeMode"))
	if selector.Value != presentation.Message(view.Locale, "shell.remoteMode") || selector.Description != view.msg("remoteStatusIdle") {
		t.Fatal("committed remote mode did not replace the selected mode and its status")
	}
}
