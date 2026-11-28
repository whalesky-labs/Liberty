package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
)

func remoteStatusKey(remote *client.RemoteController) string {
	if remote == nil {
		return "remoteStatusIdle"
	}
	key := "remoteStatusIdle"
	if remote.Capabilities != nil {
		key = "remoteStatusReady"
	}
	if remote.Error != nil {
		key = "remoteStatusUnavailable"
	}
	if remote.Checking {
		key = "remoteStatusChecking"
	}
	return key
}

func (v *SettingsView) remoteSettings(c *ui.Context) {
	settingsGroup(c, v.msg("connectionGroup"), func() {
		v.row(c, "backendUrl", "backendUrlHint", func() {
			field := ui.TextInput(c, &v.Client.Draft.BackendURL).FillWidth().Label(v.msg("backendUrl")).Placeholder(v.msg("backendUrlPlaceholder"))
			if field.Changed() {
				v.Remote.Reset()
			}
			if field.FocusLeft() {
				v.Client.Save()
			}
		})
		v.row(c, "apiToken", "apiTokenHint", func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				status := v.msg("apiTokenNotConfigured")
				placeholder := v.msg("apiTokenPlaceholder")
				if v.Client.Draft.TokenConfigured {
					status = v.msg("apiTokenConfigured")
					placeholder = status
				}
				field := ui.TextInput(c, &v.Client.Password).Password().Grow(1).MinWidth(80).Label(v.msg("apiToken")).Description(status).Tooltip(status).Placeholder(placeholder)
				if field.Changed() {
					v.Remote.Reset()
				}
				if field.FocusLeft() {
					v.Client.Save()
				}
				if v.Client.Draft.TokenConfigured && ui.Button(c, v.msg("apiTokenClear")).Disabled(v.Client.Confirming).TextColor(c.Theme().Danger).Clicked() && v.ConfirmTokenClear != nil {
					v.ConfirmTokenClear()
				}
			})
		})
	})
	settingsGroup(c, v.msg("connectionTestGroup"), func() {
		hint := v.msg("backendUrlHint")
		key := remoteStatusKey(v.Remote)
		if v.Remote.Capabilities != nil {
			hint = presentation.Format(v.Locale, "settings.remoteServiceVersion", map[string]any{"version": v.Remote.Capabilities.ServiceVersion})
		}
		if v.Remote.Error != nil {
			hint = localized(v.Locale, "远端服务不可用，请检查地址、Token 和服务能力。", "Remote service unavailable. Check the address, token and capabilities.")
		}
		settingsRow(c, v.msg("remoteStatus"), hint, func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c.Key("remote-status"), v.msg(key)).FontSize(12).Role(ui.RoleStatus)
				if ui.Button(c, v.msg("remoteCheck")).Disabled(v.Remote.Checking || strings.TrimSpace(v.Client.Draft.BackendURL) == "").Clicked() {
					v.Client.SaveThen(v.Remote.Check)
				}
			})
		})
	})
}
