package desktop

import (
	"context"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func (w *Workspace) AttachResources(service *application.Resources) {
	w.resourceService = service
	w.editorViews = make(map[application.ResourceKind]*ResourceEditorView)
	w.editors = make(map[application.ResourceKind]*mygo.Window)
	w.openingEditor = make(map[application.ResourceKind]bool)
	w.editorIDs = make(map[application.ResourceKind]string)
	w.pendingEditors = make(map[application.ResourceKind]string)
	controller := client.NewResources(service, mygo.RunOnMain, func() {
		if !w.closed {
			w.main.Invalidate()
		}
	})
	w.resources = &ResourcesView{Client: controller, Locale: w.preferences.Locale, Kind: application.Models, Open: w.openResource, Files: &NativeMemberFiles{Parent: w.main, Service: service}, ContentWidth: shellContentWidth}
	w.resources.Confirm = func(kind application.ResourceKind, name string) func(context.Context) (bool, error) {
		return nativeResourceConfirm(w.main, w.preferences.Locale, presentation.Message(w.preferences.Locale, string(kind)+".deleteTitle"), presentation.Format(w.preferences.Locale, string(kind)+".deleteConfirm", map[string]any{"name": name}))
	}
	w.main.OnFocus(func() {
		if !w.closed && (w.tab == "/models" || w.tab == "/templates" || w.tab == "/members") {
			controller.Refresh(w.resources.Kind)
		}
	})
}
func (w *Workspace) AttachSettings(service client.SettingsService, startup ...client.StartupPreferences) {
	controller := client.NewSettings(service, mygo.RunOnMain, func() {
		if !w.closed {
			w.main.Invalidate()
		}
	}, w.applyPreferences, startup...)
	controller.OnSaved(w.applySavedSettings)
	w.settings = &SettingsView{Client: controller, Runtime: w.runtime, Locale: w.preferences.Locale, Back: w.returnToApp, ActiveSource: func() string {
		if w.view == nil {
			return ""
		}
		return w.view.Source
	}}
	controller.Load()
}
func (w *Workspace) applyPreferences(preferences domain.UIPreferences) {
	if w.closed {
		return
	}
	if w.preferences.Locale != preferences.Locale {
		mygo.App.SetMenu(ApplicationMenu(preferences.Locale))
	}
	appearanceChanged := w.preferences.LiquidGlassStyle != preferences.LiquidGlassStyle || w.preferences.ThemeMode != preferences.ThemeMode
	w.preferences = preferences
	switch preferences.ThemeMode {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
	if appearanceChanged {
		applyWindowGlass(w.main, preferences)
	}
	w.view.Locale = preferences.Locale
	w.dashboard.Locale = preferences.Locale
	if w.pet != nil {
		w.pet.Locale = preferences.Locale
		w.refreshStoreItem()
		w.pet.Client.ObserveAppearance(mygo.Theme.IsDark(), time.Now())
	}
	if w.workMarket != nil {
		w.workMarket.Locale = preferences.Locale
	}
	if w.farm != nil {
		w.farm.Locale = preferences.Locale
	}
	if w.workGame != nil {
		w.workGame.Locale = preferences.Locale
	}
	if w.newJob != nil {
		w.newJob.Locale = preferences.Locale
	}
	if w.runtime != nil {
		w.runtime.Locale = preferences.Locale
	}
	if w.settings != nil {
		w.settings.Locale = preferences.Locale
	}
	if w.resources != nil {
		w.resources.Locale = preferences.Locale
	}
	if w.resultView != nil {
		w.resultView.Preferences = preferences
		w.resultView.Locale = preferences.Locale
		w.result.SetTitle(resultWindowTitle(preferences.Locale))
		w.result.Invalidate()
	}
	if w.summaryView != nil {
		w.summaryView.Preferences = preferences
		w.summaryView.Locale = preferences.Locale
		w.summaryWindow.SetTitle(presentation.Format(preferences.Locale, "windows.aiSummaryTitle", map[string]any{"title": w.summaryTitle}))
		w.summaryWindow.Invalidate()
	}
	for kind, view := range w.editorViews {
		// Editors and independent readers share the same appearance preference.
		view.Preferences = preferences
		view.Locale = preferences.Locale
		if window := w.editors[kind]; window != nil {
			window.SetTitle(resourceEditorTitle(kind, view.Locale, view.Client.Draft.ID()))
			window.Invalidate()
		}
	}
	w.main.Invalidate()
	if w.notesView != nil {
		w.notesView.Preferences = preferences
		w.notes.SetTitle(presentation.Format(preferences.Locale, "windows.meetingNotesTitle", map[string]any{"title": w.notesTitle}))
		w.notes.Invalidate()
	}
}

func (w *Workspace) openResource(kind application.ResourceKind, id string) {
	if w.closed || w.openingEditor[kind] {
		return
	}
	if existing := w.editors[kind]; existing != nil {
		if w.editorIDs[kind] == id {
			existing.Focus()
			return
		}
		w.pendingEditors[kind] = id
		existing.Close()
		return
	}
	w.openingEditor[kind] = true
	go func() {
		grant, draft, err := w.resourceService.OpenEditor(context.Background(), kind, id)
		mygo.RunOnMain(func() {
			w.openingEditor[kind] = false
			if w.closed {
				if grant != nil {
					grant.Close()
				}
				return
			}
			if err != nil {
				w.resources.Client.Error = err
				w.main.Invalidate()
				return
			}
			locale := w.preferences.Locale
			var window *mygo.Window
			var view *ResourceEditorView
			var controller *client.ResourceEditor
			controller = client.NewResourceEditor(grant, draft, mygo.RunOnMain, func() {
				if window != nil {
					w.editorIDs[kind] = controller.Draft.ID()
					window.SetTitle(resourceEditorTitle(kind, view.Locale, controller.Draft.ID()))
					window.Invalidate()
				}
			}, func() {
				w.resources.Client.Refresh(kind)
			})
			view = &ResourceEditorView{Client: controller, Locale: locale, Preferences: w.preferences}
			view.Confirm = func(title, message string) func(context.Context) (bool, error) {
				return nativeResourceConfirm(window, view.Locale, title, message)
			}
			options := resourceEditorOptions(kind, locale, id)
			options.Content = ui.View(view.View)
			window = mygo.NewWindow(options)
			w.editorViews[kind] = view
			w.editors[kind] = window
			w.editorIDs[kind] = id
			guard := &closeGuard{check: func() closeCheck { return resourceCloseCheck(controller) }, focus: window.Focus}
			w.closing.register(guard)
			window.OnClose(func(event *mygo.CloseEvent) {
				if !w.closing.allow(guard, w.closeConfirmation(window, false), window.Close, func() { delete(w.pendingEditors, kind) }) {
					event.PreventDefault()
				}
			})
			window.OnClosed(func() {
				w.closing.remove(guard)
				controller.Close()
				if w.editors[kind] == window {
					delete(w.editorViews, kind)
					delete(w.editors, kind)
					delete(w.editorIDs, kind)
				}
				if next, ok := w.pendingEditors[kind]; ok && !w.closed {
					delete(w.pendingEditors, kind)
					w.openResource(kind, next)
				}
			})
		})
	}()
}

func resourceEditorTitle(kind application.ResourceKind, locale, id string) string {
	key := "editorNewTitle"
	if id != "" {
		key = "editorEditTitle"
	}
	return presentation.Message(locale, string(kind)+"."+key)
}

func resourceEditorOptions(kind application.ResourceKind, locale, id string) mygo.WindowOptions {
	options := mygo.WindowOptions{Title: resourceEditorTitle(kind, locale, id), Width: 880, Height: 760, MinWidth: 760, MinHeight: 680}
	switch kind {
	case application.Templates:
		options.Width, options.Height, options.MinWidth, options.MinHeight = 960, 820, 820, 720
	case application.Members:
		options.Width, options.Height, options.MinWidth, options.MinHeight = 760, 680, 640, 560
	}
	return options
}
