package desktop

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestSettingsViewRetryUsesReadPathAndHidesLoadingAfterInitialFailure(t *testing.T) {
	fixture := newSettingsCloseFixture(t)
	view := &SettingsView{Client: fixture.controller, Locale: "en-US", section: "appearance"}
	tester := ui.NewTester(view.View, 1100, 960)

	fixture.controller.Load()
	load := receiveSettingsClose(t, fixture.service.loads)
	load.reply <- settingsCloseReply{err: errors.New("settings read failed")}
	fixture.dispatch(t)
	tester.Frame()
	alert, ok := tester.AccessByRole(ui.RoleAlert, "The operation did not complete. Please retry.")
	if !ok || alert.Offscreen {
		t.Fatal("settings read error was not accessible", alert)
	}
	if tester.HasText("Loading…") {
		t.Fatal("failed initial settings read still announced loading")
	}

	retryLabel := presentation.Message("en-US", "common.retry")
	tester.Key(0, ui.KeyTab)
	if !tester.Focused(retryLabel) {
		t.Fatal("settings retry is missing from keyboard focus order")
	}
	tester.Key(0, ui.KeyEnter)
	retried := receiveSettingsClose(t, fixture.service.loads)
	if len(fixture.service.saves) != 0 {
		t.Fatal("settings read retry issued a save")
	}
	retried.reply <- settingsCloseReply{value: domain.DefaultSettings()}
	fixture.dispatch(t)
	if fixture.controller.Error != nil || !fixture.controller.Loaded {
		t.Fatal("settings read retry did not recover")
	}
}

func TestSettingsViewRetryPreservesDraftAfterSaveFailure(t *testing.T) {
	fixture := newSettingsCloseFixture(t)
	view := &SettingsView{Client: fixture.controller, Locale: "en-US", section: "appearance"}
	tester := ui.NewTester(view.View, 1100, 960)
	fixture.load(t)
	fixture.controller.Draft.DefaultHotwords = "draft survives"
	fixture.controller.Save()
	save := receiveSettingsClose(t, fixture.service.saves)
	save.reply <- settingsCloseReply{err: errors.New("settings save failed")}
	fixture.dispatch(t)
	tester.Frame()
	if err := tester.Click(presentation.Message("en-US", "common.retry")); err != nil {
		t.Fatal(err)
	}
	retried := receiveSettingsClose(t, fixture.service.saves)
	if retried.value.DefaultHotwords != "draft survives" {
		t.Fatal("settings save retry lost the draft")
	}
	fixture.publish(t, retried)
	if fixture.controller.Error != nil || fixture.controller.Dirty() {
		t.Fatal("successful settings save retry left an error or dirty draft")
	}
}

func TestProcessingModeRemainsActiveUntilSaveRetrySucceeds(t *testing.T) {
	fixture := newSettingsCloseFixture(t)
	fixture.load(t)
	workspace := &Workspace{view: &Meetings{Locale: "en-US", Source: "local"}}
	view := settingsParityView("en-US", "runtimeOverview")
	view.Client = fixture.controller
	view.ActiveSource = func() string { return workspace.view.Source }
	fixture.controller.OnSaved(workspace.applySavedSettings)
	tester := ui.NewTester(view.View, 1100, 960)

	control, ok := tester.AccessByRole(ui.RolePopUpButton, view.msg("runtimeMode"))
	if !ok || control.Disabled {
		t.Fatal("processing mode selector is unavailable", control)
	}
	tester.ClickAt(control.Bounds.X+control.Bounds.W/2, control.Bounds.Y+control.Bounds.H/2)
	tester.Key(0, ui.KeyEnd)
	tester.Key(0, ui.KeyEnter)
	failedSave := receiveSettingsClose(t, fixture.service.saves)
	tester.Frame()
	saving := presentation.Format("en-US", "settings.runtimeModeSaving", map[string]any{"mode": presentation.Message("en-US", "shell.localMode")})
	control, _ = tester.AccessByRole(ui.RolePopUpButton, view.msg("runtimeMode"))
	if control.Description != saving || workspace.view.Source != "local" {
		t.Fatal("saving a different mode changed the active state before commit")
	}

	failedSave.reply <- settingsCloseReply{err: errors.New("settings save failed")}
	fixture.dispatch(t)
	tester.Frame()
	failed := presentation.Format("en-US", "settings.runtimeModeSaveFailed", map[string]any{"mode": presentation.Message("en-US", "shell.localMode")})
	control, _ = tester.AccessByRole(ui.RolePopUpButton, view.msg("runtimeMode"))
	if control.Description != failed || workspace.view.Source != "local" || !fixture.controller.SaveFailed() {
		t.Fatal("failed mode save was not visible or changed the active mode")
	}

	if err := tester.Click(presentation.Message("en-US", "common.retry")); err != nil {
		t.Fatal(err)
	}
	retried := receiveSettingsClose(t, fixture.service.saves)
	saved := retried.value
	saved.Revision++
	retried.reply <- settingsCloseReply{value: saved}
	fixture.dispatch(t)
	tester.Frame()
	control, _ = tester.AccessByRole(ui.RolePopUpButton, view.msg("runtimeMode"))
	if workspace.view.Source != "remote" || fixture.controller.SaveFailed() || fixture.controller.Dirty() || control.Description != view.msg("remoteStatusIdle") {
		t.Fatal("successful retry did not apply and settle the remote mode", workspace.view.Source)
	}
}

func TestProcessingModeTracksCommittedSaveWhenQueuedSaveFails(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, initialMode := range []string{"local", "remote"} {
			for _, revertMode := range []bool{false, true} {
				name := locale + "/" + initialMode + "/threads"
				if revertMode {
					name = locale + "/" + initialMode + "/revert-mode"
				}
				t.Run(name, func(t *testing.T) {
					fixture := newSettingsCloseFixture(t)
					fixture.controller.Load()
					load := receiveSettingsClose(t, fixture.service.loads)
					initial := domain.DefaultSettings()
					initial.ProcessingMode = initialMode
					load.reply <- settingsCloseReply{value: initial}
					fixture.dispatch(t)

					workspace := &Workspace{view: &Meetings{Locale: locale, Source: initialMode}}
					view := settingsParityView(locale, "runtimeOverview")
					view.Client = fixture.controller
					view.ActiveSource = func() string { return workspace.view.Source }
					fixture.controller.OnSaved(workspace.applySavedSettings)
					tester := ui.NewTester(view.View, 1100, 960)

					nextMode := "remote"
					if initialMode == "remote" {
						nextMode = "local"
					}
					fixture.controller.Draft.ProcessingMode = nextMode
					fixture.controller.Save()
					first := receiveSettingsClose(t, fixture.service.saves)
					if revertMode {
						fixture.controller.Draft.ProcessingMode = initialMode
					} else {
						fixture.controller.Draft.Threads++
					}
					continued := false
					fixture.controller.SaveThen(func() { continued = true })
					fixture.publish(t, first)
					second := receiveSettingsClose(t, fixture.service.saves)
					if workspace.view.Source != nextMode || workspace.modeRevision != 1 || continued {
						t.Fatal("workspace did not apply the committed mode while waiting for the queued save")
					}

					second.reply <- settingsCloseReply{err: errors.New("queued settings save failed")}
					fixture.dispatch(t)
					tester.Frame()
					if workspace.view.Source != nextMode || workspace.modeRevision != 1 || !fixture.controller.SaveFailed() || !fixture.controller.Dirty() || continued {
						t.Fatal("queued failure reverted committed mode, discarded the draft or resumed a failed action")
					}
					if revertMode {
						failed := presentation.Format(locale, "settings.runtimeModeSaveFailed", map[string]any{"mode": presentation.Message(locale, "shell."+nextMode+"Mode")})
						control, _ := tester.AccessByRole(ui.RolePopUpButton, view.msg("runtimeMode"))
						if control.Description != failed {
							t.Fatal("mode failure did not identify the successfully committed active mode")
						}
					}

					fixture.controller.Retry()
					retry := receiveSettingsClose(t, fixture.service.saves)
					if retry.value.ProcessingMode != second.value.ProcessingMode || retry.value.Threads != second.value.Threads {
						t.Fatal("retry did not preserve the failed queued draft")
					}
					fixture.publish(t, retry)
					if workspace.view.Source != retry.value.ProcessingMode || workspace.modeRevision != 2 || fixture.controller.Dirty() || fixture.controller.Error != nil || continued {
						t.Fatal("retry did not settle the committed mode or resumed the failed action")
					}
				})
			}
		}
	}
}
