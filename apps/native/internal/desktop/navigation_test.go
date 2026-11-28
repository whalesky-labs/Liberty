package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestShellHistoryControlsSupportKeyboardAndCollapsedChrome(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, nativeBar := range []ui.TitleBar{{Height: 60, Left: 96}, {Height: 42, Right: 138}} {
			shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), CanBack: true}
			backCalls, forwardCalls := 0, 0
			shell.Back = func() { backCalls++; shell.CanForward = true }
			shell.Forward = func() { forwardCalls++ }
			tester := ui.NewTester(shell.View, 1100, 900)
			tester.SetTitleBar(nativeBar)
			back, forward := shell.msg("shell.back"), shell.msg("shell.forward")
			assertDisabled(t, tester, forward, true)
			if err := tester.Click(back); err != nil {
				t.Fatal(err)
			}
			tester.Key(0, ui.KeyEnter)
			tester.Key(0, ui.KeyTab)
			if !tester.Focused(forward) {
				t.Fatal("keyboard focus did not reach the next history control")
			}
			tester.Key(0, ui.KeySpace)
			if backCalls != 2 || forwardCalls != 1 {
				t.Fatal("history controls did not support keyboard activation", backCalls, forwardCalls)
			}
			if err := tester.Click(shell.msg("shell.collapseMenu")); err != nil {
				t.Fatal(err)
			}
			previous, _ := tester.Find(shell.msg("shell.expandMenu"))
			if previous.X < nativeBar.Left+16 {
				t.Fatal("collapsed toggle overlaps native window controls", previous)
			}
			for _, label := range []string{back, forward} {
				button, ok := tester.AccessByRole(ui.RoleButton, label)
				if !ok || button.Offscreen || button.Disabled || button.Bounds.X < previous.X+previous.W {
					t.Fatal("collapsed history controls overlap or disappear", button)
				}
				previous = button.Bounds
			}
			cpu, ok := tester.Find(shell.msg("shell.cpu"))
			if !ok || cpu.X < previous.X+previous.W {
				t.Fatal("collapsed history controls overlap the content metrics", previous, cpu)
			}
		}
	}
}

func TestWorkspaceHistoryButtonsRestoreQueueFiltersAndSettingsSections(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			workspace, _, callbacks := newWorkspaceJobs(t, locale)
			fixture := newSettingsCloseFixture(t)
			fixture.load(t)
			workspace.settings = settingsParityView(locale, "appearance")
			workspace.settings.Client = fixture.controller
			workspace.settings.NavigateSection = workspace.selectSettingsSection
			workspace.navigate("/jobs?status=failed")
			awaitWorkspaceJobs(t, callbacks, func() bool { return !workspace.controller.LoadingJobs })
			workspace.navigate("/settings")
			tester := ui.NewTester(workspace.View, 1100, 900)
			back, forward := presentation.Message(locale, "shell.back"), presentation.Message(locale, "shell.forward")
			assertDisabled(t, tester, forward, true)
			if err := tester.Click(workspace.settings.msg("languageDisplay")); err != nil {
				t.Fatal(err)
			}
			for _, want := range []client.NavigationEntry{
				{Path: "/settings", SettingsSection: "appearance"},
				{Path: "/jobs?status=failed"},
			} {
				if err := tester.Click(back); err != nil || workspace.navigationEntry() != want {
					t.Fatal("back did not restore its page", workspace.navigationEntry(), want, err)
				}
			}
			awaitWorkspaceJobs(t, callbacks, func() bool { return !workspace.controller.LoadingJobs })
			for _, section := range []string{"appearance", "languageDisplay"} {
				if err := tester.Click(forward); err != nil || workspace.tab != "/settings" || workspace.settings.section != section {
					t.Fatal("forward did not restore its settings section", workspace.tab, workspace.settings.section, err)
				}
			}
			if err := tester.Click(back); err != nil {
				t.Fatal(err)
			}
			if err := tester.Click(workspace.settings.msg("accessPermissions")); err != nil {
				t.Fatal(err)
			}
			assertDisabled(t, tester, forward, true)
		})
	}
}

func TestWorkspaceHistoryReopensSourceBoundDetails(t *testing.T) {
	workspace, _, callbacks := newWorkspaceJobs(t, "en-US")
	for _, source := range []string{"local", "remote"} {
		workspace.openDetailFromSource("same-id", source)
		awaitWorkspaceJobs(t, callbacks, func() bool { return workspace.detail != nil && workspace.detail.Client.Result != nil })
	}
	workspace.navigate("/settings")
	workspace.view.Source = "local"
	for _, source := range []string{"remote", "local"} {
		workspace.travelHistory(-1)
		awaitWorkspaceJobs(t, callbacks, func() bool { return workspace.detail != nil && workspace.detail.Client.Result != nil })
		if workspace.detail.Source != source || workspace.detail.Client.Result.Job.Source != source || workspace.view.Source != "local" {
			t.Fatal("history rebound a detail to the current processing mode", source, workspace.detail.Source)
		}
	}
	workspace.travelHistory(1)
	awaitWorkspaceJobs(t, callbacks, func() bool { return workspace.detail != nil && workspace.detail.Client.Result != nil })
	if workspace.detail.Source != "remote" {
		t.Fatal("forward lost the same-ID remote detail")
	}
	workspace.travelHistory(1)
	if workspace.tab != "/settings" || workspace.detail != nil {
		t.Fatal("leaving a restored detail did not release its controller")
	}
}

func TestWorkspaceHistorySkipsUnavailableCreationAndPreservesDraft(t *testing.T) {
	workspace, _, callbacks := newWorkspaceJobs(t, "en-US")
	workspace.newJob.Client.Form.Title = "Synthetic draft"
	workspace.navigate("/jobs?status=completed")
	awaitWorkspaceJobs(t, callbacks, func() bool { return !workspace.controller.LoadingJobs })
	workspace.navigate("/jobs/new")
	workspace.navigate("/settings")
	workspace.view.Source = "remote"
	workspace.travelHistory(-1)
	if workspace.tab != "/jobs" || workspace.view.status != "completed" || workspace.newJob.Client.Form.Title != "Synthetic draft" {
		t.Fatal("history entered unavailable creation or lost its retained draft")
	}
}
