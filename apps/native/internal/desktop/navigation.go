package desktop

import (
	"strings"

	"liberty.local/liberty/internal/client"
)

func (w *Workspace) navigationEntry() client.NavigationEntry {
	entry := client.NavigationEntry{Path: w.tab}
	if entry.Path == "" {
		entry.Path = "/"
	}
	if w.tab == "/jobs" && w.view != nil && w.view.status != "" && w.view.status != "all" {
		entry.Path += "?status=" + w.view.status
	}
	if w.detail != nil && w.detail.Client != nil && w.tab == "/jobs/"+w.detail.Client.SelectedID {
		entry.DetailSource = w.detail.Source
	}
	if w.tab == "/settings" {
		entry.SettingsSection = "localRuntime"
		if w.settings != nil {
			entry.SettingsSection = w.settings.section
			if entry.SettingsSection == "" {
				entry.SettingsSection = "appearance"
			}
		}
	}
	return entry
}

func (w *Workspace) canRevisit(entry client.NavigationEntry) bool {
	if w.closed {
		return false
	}
	if entry.DetailSource != "" {
		id, detail := strings.CutPrefix(entry.Path, "/jobs/")
		return detail && id != "" && id != "new" && !strings.ContainsAny(id, "/?") && w.meetings != nil &&
			(entry.DetailSource == "local" || entry.DetailSource == "remote")
	}
	return w.canNavigate(entry.Path)
}

func (w *Workspace) travelHistory(direction int) {
	if w.closed {
		return
	}
	w.navigation.ReplaceCurrent(w.navigationEntry())
	entry, ok := w.navigation.Travel(direction, w.canRevisit)
	if !ok {
		return
	}
	if entry.DetailSource != "" {
		w.showDetailFromSource(strings.TrimPrefix(entry.Path, "/jobs/"), entry.DetailSource)
	} else {
		w.navigateTo(entry.Path)
		if entry.Path == "/settings" && w.settings != nil {
			w.settings.section = entry.SettingsSection
			if w.settings.section == "themeSection" {
				w.settings.section = "appearance"
			}
		}
	}
	if w.main != nil {
		w.main.Invalidate()
	}
}

func (w *Workspace) selectSettingsSection(section string) {
	if w.settings == nil || section == w.settings.section {
		return
	}
	w.navigation.ReplaceCurrent(w.navigationEntry())
	w.settings.section = section
	w.navigation.Visit(w.navigationEntry())
}
