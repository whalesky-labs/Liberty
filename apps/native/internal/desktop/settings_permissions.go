package desktop

import "github.com/egoist/mygo/ui"

// Only fixed system-setting destinations are exposed. Opening these pages
// neither grants access nor proves that a particular file/device is usable.
func accessPermissionURLs(platform string) (disk, microphone string) {
	switch platform {
	case "darwin":
		return "x-apple.systempreferences:com.apple.preference.security?Privacy_FilesAndFolders",
			"x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone"
	case "windows":
		return "ms-settings:privacy-broadfilesystemaccess", "ms-settings:privacy-microphone"
	default:
		return "", ""
	}
}

func (v *SettingsView) accessPermissions(c *ui.Context, platform string) {
	diskURL, microphoneURL := accessPermissionURLs(platform)
	hintSuffix := "OtherHint"
	if platform == "darwin" {
		hintSuffix = "MacHint"
	} else if platform == "windows" {
		hintSuffix = "WindowsHint"
	}
	for _, permission := range []struct{ key, action, url string }{
		{"diskPermission", "openDiskPermissions", diskURL},
		{"microphonePermission", "openMicrophonePermissions", microphoneURL},
	} {
		hint := v.msg(permission.key + hintSuffix)
		settingsRow(c, v.msg(permission.key), hint, func() {
			if permission.url == "" {
				ui.Text(c, v.msg("permissionSettingsUnavailable")).FontSize(12).TextColor(c.Theme().TextMuted)
				return
			}
			button := ui.Button(c, v.msg("requestPermission")).Label(v.msg(permission.action)).Description(hint).Padding(4, 10).MinHeight(28).Radius(7)
			if !button.Hovered() && !button.Pressed() {
				button.Background(palette(c).Subpanel)
			}
			if button.Clicked() {
				v.permissionSettingsFailed = false
				c.OpenURLThen(permission.url, func(err error) { v.permissionSettingsFailed = err != nil })
			}
		})
	}
	if v.permissionSettingsFailed {
		ui.Text(c, v.msg("permissionSettingsOpenFailed")).FillWidth().Padding(12, 0).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
	}
}
