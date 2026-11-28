package desktop

import (
	"errors"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func TestSettingsAccessPermissionsNavigationSurvivesUnavailableProfile(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, controller := range []*client.SettingsController{nil, {Error: errors.New("synthetic settings failure")}, {Loaded: true}} {
			view := &SettingsView{Locale: locale, Client: controller}
			shell := Shell{Locale: locale, Route: "/settings", Preferences: domain.DefaultUIPreferences(), Sidebar: view.Sidebar, Content: view.View}
			tester := ui.NewTester(shell.View, 1100, 960)
			link, ok := tester.AccessByRole(ui.RoleButton, view.msg("accessPermissions"))
			if !ok || link.Disabled || link.Offscreen {
				t.Fatal("permission recovery navigation is unavailable", link)
			}
			if err := tester.Click(view.msg("accessPermissions")); err != nil {
				t.Fatal(err)
			}
			heading, ok := tester.AccessByRole(ui.RoleHeading, view.msg("accessPermissions"))
			if view.section != "accessPermissions" || !ok || heading.Offscreen {
				t.Fatal("permission settings depend on loading profile settings", heading)
			}
			if len(tester.OpenedURLs()) != 0 {
				t.Fatal("visiting permissions must not open system settings automatically")
			}
			view.search = map[string]string{"zh-CN": "麦克风", "en-US": "microphone"}[locale]
			tester.Frame()
			if _, ok := tester.AccessByRole(ui.RoleButton, view.msg("accessPermissions")); !ok {
				t.Fatal("permission purposes are not searchable")
			}
		}
	}
	for _, group := range settingsNavigation {
		for _, item := range group.items {
			if item.key == "accessPermissions" && group.key != "connectionsGroup" {
				t.Fatal("permissions must be under the Connections & Access group")
			}
		}
	}
}

func TestSettingsAccessPermissionsUsePlatformDestinationsAndAccessibleControls(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, platform := range []string{"darwin", "windows"} {
			for _, dark := range []bool{false, true} {
				for _, width := range []int{1100, 760} {
					name := fmt.Sprintf("%s-%s-%v-%d", platform, locale, dark, width)
					t.Run(name, func(t *testing.T) {
						view := settingsParityView(locale, "accessPermissions")
						shell := Shell{Locale: locale, Route: "/settings", Preferences: domain.DefaultUIPreferences(), Sidebar: view.Sidebar, Content: func(c *ui.Context) {
							settingsPage(c, locale, "accessPermissions", func() { settingsPanel(c, func() { view.accessPermissions(c, platform) }) })
						}}
						tester := ui.NewTesterWith(shell.View, width, 960, ui.TesterOptions{Dark: dark})
						if !tester.HasText(view.msg("requestPermission")) {
							t.Fatal("permission actions must show the shared request label")
						}
						want := []string{"ms-settings:privacy-broadfilesystemaccess", "ms-settings:privacy-microphone"}
						if platform == "darwin" {
							want = []string{"x-apple.systempreferences:com.apple.preference.security?Privacy_FilesAndFolders", "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone"}
						}
						for index, key := range []string{"openDiskPermissions", "openMicrophonePermissions"} {
							button, ok := tester.AccessByRole(ui.RoleButton, view.msg(key))
							if !ok || button.Disabled || button.Offscreen || button.Description == "" || button.Bounds.X < 0 || button.Bounds.X+button.Bounds.W > float32(width) {
								t.Fatal("system permission action is clipped or lacks its purpose", button)
							}
							if err := tester.Click(view.msg(key)); err != nil {
								t.Fatal(err)
							}
							opened := tester.OpenedURLs()
							if len(opened) != index+1 || opened[index] != want[index] {
								t.Fatal("wrong system privacy destination", opened)
							}
						}
						heading, _ := tester.AccessByRole(ui.RoleHeading, view.msg("accessPermissions"))
						for _, node := range tester.AccessAll() {
							if node.Bounds.Y > heading.Bounds.Y && (node.Role == ui.RoleToggleButton || node.Role == ui.RoleCheckBox || node.Role == ui.RoleSwitch) {
								t.Fatal("app must not imitate an operating-system permission toggle", node)
							}
						}
						captureParity(t, tester, "settings-permissions-"+name)
					})
				}
			}
		}
	}
}

func TestSettingsAccessPermissionsReportOpenFailureAndAllowRetry(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := &SettingsView{Locale: locale}
		tester := ui.NewTester(func(c *ui.Context) { settingsPanel(c, func() { view.accessPermissions(c, "windows") }) }, 1100, 960)
		tester.FailOpenURL(errors.New("synthetic system opener failure"))
		if err := tester.Click(view.msg("openDiskPermissions")); err != nil {
			t.Fatal(err)
		}
		if alert, ok := tester.AccessByRole(ui.RoleAlert, view.msg("permissionSettingsOpenFailed")); !ok || alert.Offscreen {
			t.Fatal("failed system launch lacks accessible manual recovery", alert)
		}
		tester.FailOpenURL(nil)
		if err := tester.Click(view.msg("openDiskPermissions")); err != nil {
			t.Fatal(err)
		}
		if view.permissionSettingsFailed || tester.HasText(view.msg("permissionSettingsOpenFailed")) {
			t.Fatal("successful retry retained a stale failure")
		}
	}
}

func TestSettingsAccessPermissionsUnsupportedPlatformHasManualGuidance(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := &SettingsView{Locale: locale}
		tester := ui.NewTester(func(c *ui.Context) { settingsPanel(c, func() { view.accessPermissions(c, "unsupported") }) }, 1100, 960)
		for _, key := range []string{"diskPermissionOtherHint", "microphonePermissionOtherHint", "permissionSettingsUnavailable"} {
			if !tester.HasText(view.msg(key)) {
				t.Fatal("unsupported platform lost manual permission instructions", key)
			}
		}
		for _, key := range []string{"openDiskPermissions", "openMicrophonePermissions"} {
			if _, ok := tester.AccessByRole(ui.RoleButton, view.msg(key)); ok {
				t.Fatal("unsupported platform exposed a broken settings action")
			}
		}
	}
}
