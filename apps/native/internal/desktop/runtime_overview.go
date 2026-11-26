package desktop

import (
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func (v *SettingsView) runtimeOverview(c *ui.Context) {
	v.row(c, "runtimeMode", "runtimeModeHint", func() {
		local, remote := presentation.Message(v.Locale, "shell.localMode"), presentation.Message(v.Locale, "shell.remoteMode")
		draftMode := v.Client.Draft.ProcessingMode
		if draftMode != "remote" {
			draftMode = "local"
		}
		activeMode := ""
		if v.ActiveSource != nil {
			activeMode = v.ActiveSource()
		}
		if activeMode != "remote" && activeMode != "local" {
			activeMode = draftMode
		}
		selected := local
		if draftMode == "remote" {
			selected = remote
		}
		status := ""
		if draftMode != activeMode {
			activeLabel := local
			if activeMode == "remote" {
				activeLabel = remote
			}
			statusKey := "runtimeModePending"
			if v.Client.SaveFailed() {
				statusKey = "runtimeModeSaveFailed"
			} else if v.Client.Busy {
				statusKey = "runtimeModeSaving"
			}
			status = presentation.Format(v.Locale, "settings."+statusKey, map[string]any{"mode": activeLabel})
		} else if activeMode == "remote" {
			status = v.msg(remoteStatusKey(v.Remote))
		} else {
			ready := v.Runtime != nil && v.Runtime.Client != nil && v.Runtime.Client.Error == nil && v.Runtime.Client.PanelAvailable() && v.Runtime.Client.Panel.Overview.ShellReady
			status = v.msg("waitingLocalConfig")
			if ready {
				status = v.msg("localDatabaseReady")
			}
		}
		if ui.Select(c, &selected, []string{local, remote}).Width(190).MinHeight(40).Label(v.msg("runtimeMode")).Description(status).Tooltip(status).Changed() {
			v.Client.Draft.ProcessingMode = "local"
			if selected == remote {
				v.Client.Draft.ProcessingMode = "remote"
			}
			v.Client.Save()
		}
	})
}
