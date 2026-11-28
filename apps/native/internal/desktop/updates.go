package desktop

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func (w *Workspace) AttachUpdates(service client.UpdateService) {
	w.updates.Close()
	w.updates = client.NewUpdates(service, presentation.Version, mygo.RunOnMain, func() {
		w.syncUpdateWindow()
		if !w.closed && w.main != nil {
			w.main.Invalidate()
		}
	})
	if w.settings != nil {
		w.settings.Updates = w.updates
		w.settings.CheckUpdates = w.checkUpdates
	}
}

type updateNativeWindow interface {
	Show()
	Focus()
	Close()
	Invalidate()
	SetTitle(string)
	SetContentSize(int, int)
	OnClosed(func()) func()
}

type updateSize struct{ width, height int }

func updateWindowContentSize(controller *client.UpdateController) updateSize {
	if controller.Result != nil && controller.Result.Available && controller.Result.Latest != nil {
		return updateSize{640, 560}
	}
	if controller.Error != nil {
		return updateSize{440, 260}
	}
	if controller.Busy {
		return updateSize{400, 150}
	}
	return updateSize{400, 200}
}

func updateWindowOptions(parent *mygo.Window, locale string, size updateSize, content func(*ui.Context)) mygo.WindowOptions {
	return mygo.WindowOptions{
		Title: presentation.Message(locale, "settings.softwareUpdate"), Parent: parent,
		Width: size.width, Height: size.height, UseContentSize: true,
		TitleBarStyle: mygo.TitleBarDefault, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		SkipTaskbar: true, AutoHideMenuBar: true, Content: ui.View(content),
	}
}

func (w *Workspace) checkUpdates() {
	if w.closed || w.updates == nil {
		return
	}
	if w.main != nil {
		w.main.Show()
	}
	if w.updateWindow != nil {
		w.updateWindow.Show()
		w.updateWindow.Focus()
		return
	}
	w.updates.Check()
}

func (w *Workspace) syncUpdateWindow() {
	if w.updates == nil {
		return
	}
	if w.closed || !w.updates.Open {
		if w.updateWindow != nil {
			w.updateWindow.Close()
		}
		return
	}
	size := updateWindowContentSize(w.updates)
	if w.updateWindow == nil {
		makeWindow := w.makeUpdateWindow
		if makeWindow == nil {
			makeWindow = func(options mygo.WindowOptions) updateNativeWindow { return newDesktopWindow(options) }
		}
		window := makeWindow(updateWindowOptions(w.main, w.preferences.Locale, size, func(c *ui.Context) {
			p := presentation.PreferenceColors(c.Theme().Dark, w.preferences)
			ui.Themed(c, &p.Theme).Fill().Children(func() { updateWindowContent(c, w.updates, w.preferences.Locale) })
		}))
		w.updateWindow, w.updateWindowSize = window, size
		window.OnClosed(func() {
			if w.updateWindow != window {
				return
			}
			w.updateWindow = nil
			if w.updates.Open {
				w.updates.Dismiss()
			}
			if !w.closed && w.main != nil {
				w.main.Focus()
				w.main.Invalidate()
			}
		})
		window.Show()
		window.Focus()
	}
	if w.updateWindowSize != size {
		w.updateWindow.SetContentSize(size.width, size.height)
		w.updateWindowSize = size
	}
	w.updateWindow.SetTitle(presentation.Message(w.preferences.Locale, "settings.softwareUpdate"))
	w.updateWindow.Invalidate()
}

var updateApplicationIcon, _ = ui.DecodeImage(presentation.ApplicationIconPNG)

func updateWindowContent(c *ui.Context, controller *client.UpdateController, locale string) {
	if controller == nil || !controller.Open {
		return
	}
	message := func(key string) string { return presentation.Message(locale, "settings."+key) }
	available := controller.Result != nil && controller.Result.Available && controller.Result.Latest != nil
	if c.Shortcut(0, ui.KeyEscape) {
		controller.Dismiss()
		return
	}
	ui.Column(c).Fill().Padding(20).Gap(18).Role(ui.RoleDialog).Label(message("softwareUpdate")).Children(func() {
		ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Start).Children(func() {
			ui.Image(c, updateApplicationIcon).Size(56, 56).Shrink(0).Role(ui.RoleNone)
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(8).Children(func() {
				title := "updateCurrent"
				switch {
				case controller.Busy:
					title = "updateChecking"
				case controller.Error != nil:
					title = "updateFailed"
				case available:
					title = "updateAvailable"
				case controller.Result == nil || controller.Result.Latest == nil:
					title = "updateNoReleases"
				}
				ui.Text(c, message(title)).FillWidth().FontSize(16).Bold().Role(ui.RoleHeading).Level(2)
				switch {
				case controller.Busy:
					ui.Progress(c, -1).FillWidth().Label(message("updateChecking"))
				case controller.Error != nil:
					key := "updateFailedHint"
					if errors.Is(controller.Error, domain.ErrUpdateRateLimit) {
						key = "updateRateLimited"
					}
					if errors.Is(controller.Error, context.DeadlineExceeded) {
						key = "updateTimedOut"
					}
					if errors.Is(controller.Error, domain.ErrUpdateVersion) {
						key = "updateInvalidVersion"
					}
					ui.Text(c, message(key)).FillWidth().Role(ui.RoleAlert)
					if errors.Is(controller.Error, domain.ErrUpdateRateLimit) && !controller.RetryAt.IsZero() {
						ui.Text(c, presentation.Format(locale, "settings.updateRetryAt", map[string]any{"time": controller.RetryAt.Local().Format("15:04:05")})).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
					}
				case available:
					release := controller.Result.Latest
					ui.Text(c, presentation.Format(locale, "settings.updateVersionAvailable", map[string]any{"version": strings.TrimPrefix(release.Tag, "v"), "current": controller.Result.CurrentVersion})).FillWidth()
					if !release.PublishedAt.IsZero() {
						ui.Text(c, presentation.Format(locale, "settings.updatePublishedAt", map[string]any{"date": release.PublishedAt.Local().Format("2006-01-02")})).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
					}
				default:
					key := "updateCurrentHint"
					if controller.Result == nil || controller.Result.Latest == nil {
						key = "updateNoReleasesHint"
					}
					channel := message("stableChannel")
					if controller.Channel == domain.PreviewUpdates {
						channel = message("previewChannel")
					}
					ui.Text(c, presentation.Format(locale, "settings."+key, map[string]any{"version": presentation.Version, "channel": channel})).FillWidth().Role(ui.RoleStatus)
				}
				if controller.Result != nil && controller.Result.Cached {
					ui.Text(c, presentation.Format(locale, "settings.updateCached", map[string]any{"time": controller.Result.CheckedAt.Local().Format("15:04:05")})).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
				}
			})
		})
		if available {
			ui.Column(c).FillWidth().Grow(1).MinHeight(0).Gap(10).Children(func() {
				ui.Text(c, message("updateReleaseNotes")).FontSize(14).Bold().Role(ui.RoleHeading).Level(3)
				contentScroll(c.Key("release-notes")).FillWidth().Grow(1).MinHeight(0).Padding(12).Radius(8).Background(palette(c).Panel).Border(1, palette(c).Separator).Label(message("updateReleaseNotes")).Focusable().Children(func() {
					ui.Column(c).FillWidth().Gap(10).Children(func() { releaseNotes(c, controller.Result.Latest.Notes, message("updateNoNotes")) })
				})
			})
		} else {
			ui.Box(c).Grow(1)
		}
		ui.Row(c).FillWidth().Shrink(0).Gap(8).Justify(ui.End).Children(func() {
			switch {
			case controller.Busy:
				if ui.Button(c, presentation.Message(locale, "common.cancel")).MinWidth(90).AutoFocus().Clicked() {
					controller.Dismiss()
				}
			case controller.Error != nil:
				if ui.Button(c, presentation.Message(locale, "common.cancel")).AutoFocus().Clicked() {
					controller.Dismiss()
				}
				if ui.PrimaryButton(c, presentation.Message(locale, "common.retry")).Disabled(!controller.CanRetry()).Clicked() {
					controller.Check()
				}
			case available:
				if ui.Button(c, message("updateLater")).Clicked() {
					controller.Dismiss()
				}
				if ui.PrimaryButton(c, message("updateDownload")).AutoFocus().Clicked() {
					if target := controller.Result.Latest.URL(); target != "" {
						c.OpenURL(target)
					}
				}
			default:
				if ui.PrimaryButton(c, message("updateOK")).MinWidth(90).AutoFocus().Clicked() {
					controller.Dismiss()
				}
			}
		})
	})
}

var releaseHTMLComments = regexp.MustCompile(`(?s)<!--.*?-->`)

// Release notes stay local, selectable text. Render basic Markdown headings
// and lists without loading remote images, executing HTML or activating links.
func releaseNotes(c *ui.Context, notes, empty string) {
	notes = strings.TrimSpace(releaseHTMLComments.ReplaceAllString(strings.ReplaceAll(notes, "\r\n", "\n"), ""))
	if notes == "" {
		ui.Text(c, empty).FillWidth().TextColor(c.Theme().TextMuted)
		return
	}
	for _, paragraph := range strings.Split(notes, "\n\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		level := len(paragraph) - len(strings.TrimLeft(paragraph, "#"))
		if level >= 1 && level <= 6 && len(paragraph) > level && paragraph[level] == ' ' && !strings.Contains(paragraph, "\n") {
			ui.Text(c, strings.TrimSpace(paragraph[level:])).FillWidth().FontSize(15).Bold().Role(ui.RoleHeading).Level(min(level+1, 6)).Selectable()
		} else {
			paragraph = strings.TrimPrefix(strings.ReplaceAll("\n"+paragraph, "\n- ", "\n• "), "\n")
			ui.Text(c, paragraph).FillWidth().FontSize(13).Selectable()
		}
	}
}
