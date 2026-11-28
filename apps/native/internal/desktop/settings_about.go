package desktop

import (
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func (v *SettingsView) about(c *ui.Context) {
	settingsGroup(c, v.msg("applicationInfo"), func() {
		settingsRow(c, v.msg("applicationVersion"), "", func() {
			ui.Text(c, presentation.Version).FontSize(12)
		})
		buildType := localized(v.Locale, "预览版", "Preview")
		if v.BuildChannel == "candidate" {
			buildType = localized(v.Locale, "迁移候选版", "Migration Candidate")
		}
		settingsRow(c, v.msg("buildType"), "", func() { ui.Text(c, buildType).FontSize(12) })
		settingsRow(c, v.msg("license"), "", func() { ui.Text(c, "MIT").FontSize(12) })
	})

	settingsGroup(c, v.msg("updateSettings"), func() {
		channel := domain.DefaultUpdateChannel(presentation.Version)
		if v.Updates != nil {
			channel = v.Updates.Channel
		}
		selected := v.msg("stableChannel")
		if channel == domain.PreviewUpdates {
			selected = v.msg("previewChannel")
		}
		settingsRow(c, v.msg("updateChannel"), "", func() {
			if selectControl(c, &selected, []string{v.msg("stableChannel"), v.msg("previewChannel")}).Width(190).Label(v.msg("updateChannel")).Disabled(v.Updates == nil || v.Updates.Open).Changed() && v.Updates != nil {
				v.Updates.Channel = domain.StableUpdates
				if selected == v.msg("previewChannel") {
					v.Updates.Channel = domain.PreviewUpdates
				}
			}
		})
		settingsRow(c, v.msg("currentVersion"), presentation.Version, func() {
			restoreFocus := ui.Local(c.Root(), "settings-update-return-focus", func() bool { return false })
			trigger := ui.Button(c, v.msg("checkUpdates")).Disabled(v.Updates == nil || v.CheckUpdates == nil)
			if trigger.Clicked() {
				*restoreFocus = true
				v.CheckUpdates()
			}
			if *restoreFocus && v.Updates != nil && !v.Updates.Open {
				trigger.Focus()
				*restoreFocus = false
			}
		})
	})
}
