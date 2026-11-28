package desktop

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type updateViewService func(context.Context, domain.UpdateChannel) (domain.UpdateResult, error)

func (f updateViewService) Check(ctx context.Context, channel domain.UpdateChannel) (domain.UpdateResult, error) {
	return f(ctx, channel)
}

type fakeUpdateWindow struct {
	title                               string
	size                                updateSize
	shown, focused, invalidated, closed int
	onClosed                            func()
}

func (w *fakeUpdateWindow) Show()                            { w.shown++ }
func (w *fakeUpdateWindow) Focus()                           { w.focused++ }
func (w *fakeUpdateWindow) Invalidate()                      { w.invalidated++ }
func (w *fakeUpdateWindow) SetTitle(title string)            { w.title = title }
func (w *fakeUpdateWindow) SetContentSize(width, height int) { w.size = updateSize{width, height} }
func (w *fakeUpdateWindow) OnClosed(f func()) func()         { w.onClosed = f; return func() {} }
func (w *fakeUpdateWindow) Close() {
	w.closed++
	if w.onClosed != nil {
		w.onClosed()
	}
}

func TestUpdateNativeWindowLifecycleAndOptions(t *testing.T) {
	callbacks := make(chan func(), 6)
	started := make(chan context.Context, 6)
	replies := make(chan domain.UpdateResult, 6)
	service := updateViewService(func(ctx context.Context, channel domain.UpdateChannel) (domain.UpdateResult, error) {
		started <- ctx
		select {
		case result := <-replies:
			return result, nil
		case <-ctx.Done():
			return domain.UpdateResult{}, ctx.Err()
		}
	})
	w := &Workspace{preferences: domain.DefaultUIPreferences(), view: &Meetings{Source: "local"}}
	var created []*fakeUpdateWindow
	w.makeUpdateWindow = func(options mygo.WindowOptions) updateNativeWindow {
		if options.TitleBarStyle != mygo.TitleBarDefault || options.Frameless || options.Modal || !options.UseContentSize || options.Content == nil || options.DisableClose || options.Width != 400 || options.Height != 150 {
			t.Fatal("update does not create an independent native titled window", options)
		}
		window := &fakeUpdateWindow{}
		created = append(created, window)
		return window
	}
	w.updates = client.NewUpdates(service, presentation.Version, func(f func()) { callbacks <- f }, w.syncUpdateWindow)
	defer w.updates.Close()
	menu := ApplicationMenu("zh-CN", w.applicationMenuActions())
	menu.Items()[4].Submenu[3].Click(nil, nil)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("check not started")
	}
	w.checkUpdates()
	if len(created) != 1 || created[0].focused != 2 || !w.updates.Busy {
		t.Fatal("duplicate menu entry created windows or checks")
	}
	replies <- domain.UpdateResult{CurrentVersion: presentation.Version, Available: true, Latest: &domain.Release{Tag: "v2.0.0-alpha.4", Notes: "Release notes"}}
	select {
	case f := <-callbacks:
		f()
	case <-time.After(3 * time.Second):
		t.Fatal("check not completed")
	}
	if len(created) != 1 || created[0].size != (updateSize{640, 560}) {
		t.Fatal("new version did not resize the same native window")
	}
	created[0].Close()
	if w.updateWindow != nil || w.updates.Open {
		t.Fatal("system close did not dismiss update state")
	}
	w.checkUpdates()
	var active context.Context
	select {
	case active = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("reopen not started")
	}
	if len(created) != 2 {
		t.Fatal("closed window was reused")
	}
	created[1].Close()
	if active.Err() == nil {
		t.Fatal("native close did not cancel request")
	}
	select {
	case f := <-callbacks:
		f()
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled request not completed")
	}
	if w.updateWindow != nil || w.updates.Open || len(created) != 2 {
		t.Fatal("late response reopened the native window")
	}
	w.closed = true
	w.checkUpdates()
	if w.updates.Open {
		t.Fatal("closed workspace opened updates")
	}
}

func TestSettingsAboutFooterAuthorLink(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			tester := ui.NewTesterWith(func(c *ui.Context) {
				settingsAboutPage(c, locale, "about", func() {})
			}, 1100, 960, ui.TesterOptions{})
			authorLabel := presentation.Message(locale, "settings.authorGithub")
			authorLink, ok := tester.AccessByRole(ui.RoleLink, authorLabel)
			if !ok || authorLink.Offscreen || authorLink.Bounds.H > 24 {
				t.Fatal("author credit should remain a compact text link", authorLink)
			}
			if err := tester.Click(authorLabel); err != nil {
				t.Fatal(err)
			}
			opened := tester.OpenedURLs()
			if len(opened) != 1 || opened[0] != "https://github.com/westng/Liberty" {
				t.Fatal("author link must open the GitHub repository", opened)
			}

			if tester.HasText(presentation.Message(locale, "settings.downloadUpdate")) {
				t.Fatal("About footer must not offer a separate GitHub update download")
			}
			captureParity(t, tester, "settings-about-footer-"+locale)
		})
	}
}

func TestSettingsAboutNavigationAndUpdateChannel(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := settingsParityView(locale, "diagnostics")
			view.Client = nil
			view.BuildChannel = "candidate"
			callbacks := make(chan func(), 4)
			channels := make(chan domain.UpdateChannel, 4)
			view.Updates = client.NewUpdates(updateViewService(func(ctx context.Context, channel domain.UpdateChannel) (domain.UpdateResult, error) {
				channels <- channel
				return domain.UpdateResult{CurrentVersion: presentation.Version, Channel: channel, Latest: &domain.Release{Tag: "v" + presentation.Version}}, nil
			}), presentation.Version, func(f func()) { callbacks <- f }, func() {})
			defer view.Updates.Close()
			view.CheckUpdates = view.Updates.Check
			shell := Shell{Locale: locale, Route: "/settings", Sidebar: view.Sidebar, Content: view.View}
			tester := ui.NewTester(shell.View, 1100, 960)
			for _, key := range []string{"applicationInfo", "updateSettings"} {
				if _, ok := tester.AccessByRole(ui.RoleHeading, view.msg(key)); ok {
					t.Fatal("About group appeared in engineering diagnostics", key)
				}
			}
			for _, group := range settingsNavigation {
				for _, item := range group.items {
					if item.key == "about" && group.key != "systemGroup" {
						t.Fatal("About must be in the System group", group.key)
					}
				}
			}
			if err := tester.Click(view.msg("about")); err != nil {
				t.Fatal(err)
			}
			if _, ok := tester.AccessByRole(ui.RoleHeading, view.msg("about")); !ok {
				t.Fatal("About menu did not open its page")
			}

			for _, key := range []string{"applicationInfo", "applicationVersion", "buildType", "license", "updateSettings", "updateChannel", "currentVersion", "checkUpdates"} {
				if !tester.HasText(view.msg(key)) {
					t.Fatal("About page is missing application or update information", locale, key)
				}
			}
			if !tester.HasText(presentation.Version) || !tester.HasText("MIT") || !tester.HasText(localized(locale, "迁移候选版", "Migration Candidate")) {
				t.Fatal("application details do not reflect the native build", locale)
			}
			if err := tester.Click(view.msg("checkUpdates")); err != nil {
				t.Fatal(err)
			}
			if len(tester.OpenedURLs()) != 0 || !view.Updates.Busy || tester.HasText(view.msg("updateChecking")) {
				t.Fatal("check must invoke the window action without painting a dialog in Settings")
			}
			flushView(t, callbacks, tester)
			if got := <-channels; got != domain.DefaultUpdateChannel(presentation.Version) {
				t.Fatal("incorrect default channel", got)
			}
			if view.Updates.Result == nil {
				t.Fatal("missing current-version result")
			}
			view.Updates.Dismiss()
			tester.Frame()
			if !tester.Focused(view.msg("checkUpdates")) {
				t.Fatal("closing the result did not restore check-button focus")
			}

			selector, ok := tester.AccessByRole(ui.RolePopUpButton, "")
			if !ok {
				t.Fatal("update channel selector is not accessible")
			}
			tester.ClickAt(selector.Bounds.X+selector.Bounds.W/2, selector.Bounds.Y+selector.Bounds.H/2)
			if _, ok := tester.AccessByRole(ui.RoleOption, view.msg("stableChannel")); !ok {
				t.Fatal("stable update channel is missing from the selector")
			}
			if err := tester.Click(view.msg("stableChannel")); err != nil {
				t.Fatal(err)
			}
			if view.Updates.Channel != domain.StableUpdates {
				t.Fatal("stable channel selection was not retained", view.Updates.Channel)
			}
			if err := tester.Click(view.msg("checkUpdates")); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tester)
			if got := <-channels; got != domain.StableUpdates {
				t.Fatal("selected channel was ignored", got)
			}
			if len(tester.OpenedURLs()) != 0 {
				t.Fatal("check opened a browser", tester.OpenedURLs())
			}
		})
	}
}

func TestUpdateDialogStatesAndReleaseNotes(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, state := range []string{"checking", "current", "available", "failed", "empty", "limited", "cached"} {
				t.Run(fmt.Sprintf("%s/%v/%s", locale, dark, state), func(t *testing.T) {
					controller := client.NewUpdates(nil, presentation.Version, func(func()) {}, nil)
					defer controller.Close()
					controller.Open = true
					result := domain.UpdateResult{CurrentVersion: presentation.Version, Channel: domain.PreviewUpdates, Latest: &domain.Release{Tag: "v2.0.0-alpha.4", PublishedAt: time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC), Notes: "<!-- hidden-publication-metadata -->\n\n## 中文\n\n- 修复更新检查\n\n### English\n\n- Fixed update checks\n\n" + strings.Repeat("More release details.\n\n", 40) + "End of release notes."}}
					key, button := "updateCurrent", "updateOK"
					switch state {
					case "checking":
						controller.Busy = true
						key = "updateChecking"
						button = ""
					case "current":
						controller.Result = &result
					case "available":
						result.Available = true
						controller.Result = &result
						key = "updateAvailable"
						button = "updateDownload"
					case "failed":
						controller.Error = domain.ErrUpdateCheck
						key = "updateFailed"
						button = ""
					case "empty":
						controller.Result = &domain.UpdateResult{CurrentVersion: presentation.Version}
						key = "updateNoReleases"
					case "limited":
						controller.Error = domain.ErrUpdateRateLimit
						controller.RetryAt = time.Now().Add(time.Hour)
						key = "updateFailed"
						button = ""
					case "cached":
						result.Cached = true
						result.CheckedAt = time.Now().Add(-time.Minute)
						controller.Result = &result
					}
					msg := func(k string) string { return presentation.Message(locale, "settings."+k) }
					p := presentation.Colors(dark, ui.Hex("#007AFF"))
					size := updateWindowContentSize(controller)
					tester := ui.NewTesterWith(func(c *ui.Context) {
						ui.Themed(c, &p.Theme).Fill().Children(func() { updateWindowContent(c, controller, locale) })
					}, size.width, size.height, ui.TesterOptions{})
					if dialog, ok := tester.AccessByRole(ui.RoleDialog, msg("softwareUpdate")); !ok || dialog.Offscreen {
						t.Fatal("missing named modal", dialog)
					}
					if !tester.HasText(msg(key)) {
						t.Fatal("missing state", key)
					}
					if button != "" {
						b, ok := tester.AccessByRole(ui.RoleButton, msg(button))
						if !ok || b.Offscreen || b.Bounds.Y+b.Bounds.H > float32(size.height) {
							t.Fatal("dialog action out of bounds", b)
						}
					}
					if state == "checking" {
						if _, ok := tester.AccessByRole(ui.RoleProgress, msg("updateChecking")); !ok {
							t.Fatal("missing accessible progress")
						}
					}
					if state == "limited" {
						retry, ok := tester.AccessByRole(ui.RoleButton, presentation.Message(locale, "common.retry"))
						if !ok || !retry.Disabled || retry.Offscreen || retry.Bounds.Y+retry.Bounds.H > float32(size.height) || !tester.HasText(msg("updateRateLimited")) || !tester.HasText(controller.RetryAt.Local().Format("15:04:05")) {
							t.Fatal("limit message/deadline/retry state missing", retry)
						}
					}
					if state == "available" {
						if !tester.HasText("修复更新检查") || !tester.HasText("Fixed update checks") || tester.HasText("hidden-publication-metadata") {
							t.Fatal("release notes missing or include hidden metadata")
						}
						if len(tester.OpenedURLs()) != 0 {
							t.Fatal("notes opened remote content")
						}
					}
					captureParity(t, tester, fmt.Sprintf("update-%s-%t-%s", locale, dark, state))
					if state == "available" {
						area, ok := tester.AccessByRole(ui.RoleScroll, msg("updateReleaseNotes"))
						if !ok {
							t.Fatal("release notes are not scrollable")
						}
						tester.Scroll(area.Bounds.X+area.Bounds.W/2, area.Bounds.Y+area.Bounds.H/2, 0, 10000)
						last, ok := tester.AccessByRole(ui.RoleText, "End of release notes.")
						if !ok || last.Offscreen || last.Bounds.Y < area.Bounds.Y || last.Bounds.Y+last.Bounds.H > area.Bounds.Y+area.Bounds.H {
							t.Fatal("end of release notes is inaccessible", last, area)
						}
						if err := tester.Click(msg("updateDownload")); err != nil {
							t.Fatal(err)
						}
						if urls := tester.OpenedURLs(); len(urls) != 1 || urls[0] != presentation.ReleasesURL+"/tag/v2.0.0-alpha.4" {
							t.Fatal("download did not target the checked release", urls)
						}
					}
					tester.Key(0, ui.KeyEscape)
					if controller.Open || controller.Busy {
						t.Fatal("escape did not dismiss and cancel")
					}
				})
			}
		}
	}
}

func TestUpdateDialogCancellationRetryAndMenuEntry(t *testing.T) {
	callbacks := make(chan func(), 4)
	started := make(chan context.Context, 4)
	calls := 0
	service := updateViewService(func(ctx context.Context, channel domain.UpdateChannel) (domain.UpdateResult, error) {
		started <- ctx
		<-ctx.Done()
		return domain.UpdateResult{}, ctx.Err()
	})
	c := client.NewUpdates(service, presentation.Version, func(f func()) { callbacks <- f }, func() { calls++ })
	defer c.Close()
	w := Workspace{updates: c, view: &Meetings{Source: "local"}}
	w.makeUpdateWindow = func(mygo.WindowOptions) updateNativeWindow { return &fakeUpdateWindow{} }
	w.preferences = domain.DefaultUIPreferences()
	menu := ApplicationMenu("en-US", w.applicationMenuActions())
	check := menu.Items()[4].Submenu[3]
	check.Click(check, nil)
	var active context.Context
	select {
	case active = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("menu did not start checking")
	}
	tester := ui.NewTester(func(context *ui.Context) { updateWindowContent(context, c, "en-US") }, 1100, 760)
	if !tester.Focused("Cancel") {
		t.Fatal("checking dialog should focus cancel")
	}
	if err := tester.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if active.Err() == nil || c.Open || c.Busy {
		t.Fatal("cancel did not abort request")
	}
	flushView(t, callbacks, tester)
	if c.Open {
		t.Fatal("cancelled response reopened dialog")
	}
	c.Open = true
	c.Error = domain.ErrUpdateRateLimit
	tester.Frame()
	if !tester.HasText(presentation.Message("en-US", "settings.updateRateLimited")) {
		t.Fatal("rate limit hint missing")
	}
	if err := tester.Click(presentation.Message("en-US", "common.retry")); err != nil {
		t.Fatal(err)
	}
	select {
	case active = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not start checking")
	}
	if !c.Busy || c.Error != nil || calls == 0 {
		t.Fatal("retry state missing")
	}
	c.Close()
	flushView(t, callbacks, tester)
	if active.Err() == nil || c.Open {
		t.Fatal("shutdown did not cancel")
	}
}
