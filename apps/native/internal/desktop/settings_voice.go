package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/domain"
)

func (v *SettingsView) voice(c *ui.Context) {
	live := v.Runtime.Live
	live.EnsureOptions()

	settingsGroup(c, v.msg("voiceInputGroup"), func() {
		if live.Error != nil {
			ui.Text(c, liveError(v.Locale, live.Error, "")).FillWidth().Padding(0, 14).FontSize(12).TextColor(c.Theme().Danger).Role(ui.RoleAlert).Selectable()
		} else if !live.OptionsLoaded() {
			ui.Text(c, localized(v.Locale, "正在读取麦克风…", "Loading microphones…")).FillWidth().Padding(0, 14).FontSize(12).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus).Busy(live.Busy)
		}
		v.row(c, "voiceInputDevice", "voiceInputDeviceHint", func() {
			ids := []string{""}
			labels := []string{v.msg("voiceInputDevicePlaceholder")}
			for index, device := range live.Options.Devices {
				ids = append(ids, device.ID)
				labels = append(labels, fmt.Sprintf("%s (%d)", device.Name, index+1))
			}

			selectedID := v.Client.Draft.LiveDeviceID
			if selectedID == "" {
				selectedID = live.Request.DeviceID
			}
			selected := labels[0]
			for index, id := range ids {
				if id == selectedID {
					selected = labels[index]
				}
			}

			if live.OptionsLoaded() && live.Error == nil {
				if selectedID != "" && selected == labels[0] {
					selectedID = ""
					selected = labels[0]
					if v.Client.Draft.LiveDeviceID != "" {
						v.Client.Draft.LiveDeviceID = ""
						v.Client.Save()
					}
				}
			}

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if selectControl(c, &selected, labels).Grow(1).MinWidth(160).Label(v.msg("voiceInputDevice")).Disabled(!live.OptionsLoaded() || live.Error != nil || live.Busy).Changed() {
					for index, label := range labels {
						if selected == label {
							v.Client.Draft.LiveDeviceID = ids[index]
							v.Client.Save()
							break
						}
					}
				}
				if ui.Button(c, v.msg("voiceRefreshDevices")).MinHeight(34).Disabled(live.Busy || domain.LiveActive(live.Snapshot.Session.State)).Clicked() {
					live.RefreshOptions()
				}
			})
		})
	})

	settingsGroup(c, v.msg("voiceTranslationGroup"), func() {
		v.row(c, "voiceTranslate", "voiceTranslateHint", func() {
			if ui.Checkbox(c, &v.Client.Draft.LiveTranslate, v.msg("voiceTranslate")).Changed() {
				v.Client.Save()
			}
		})
		v.row(c, "voiceLanguages", "voiceLanguagesHint", func() {
			ui.Text(c, v.msg("voiceChineseEnglishAuto")).FontSize(12).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
		})
	})

	v.relatedSettings(c, "speechModels", "accessPermissions")

	if !v.Client.Draft.LiveTranslate {
		ui.Text(c, v.msg("voiceTranslationOffHint")).FillWidth().Padding(0, 14).FontSize(12).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
	}
	if v.Client.Busy {
		ui.Text(c, v.msg("voiceSaving")).FillWidth().Padding(0, 14).FontSize(12).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus).Busy(true)
	}
	if domain.LiveActive(live.Snapshot.Session.State) {
		ui.Text(c, v.msg("voiceChangesNextSession")).FillWidth().Padding(0, 14).FontSize(12).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
	}
}
