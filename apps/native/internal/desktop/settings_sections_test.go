package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSettingsControlsHaveOnePrimaryHome(t *testing.T) {
	controls := map[string]struct {
		section string
		role    ui.Role
	}{
		"accentColor":              {"appearance", ui.RoleMenuButton},
		"locale":                   {"languageDisplay", ui.RolePopUpButton},
		"performanceIndicators":    {"languageDisplay", ui.RoleSwitch},
		"defaultHotwords":          {"processingDefaults", ui.RoleTextField},
		"defaultSummaryTemplate":   {"processingDefaults", ui.RoleTextField},
		"concurrency":              {"performance", ui.RoleTextField},
		"localAsrDevice":           {"performance", ui.RolePopUpButton},
		"localAsrThreads":          {"performance", ui.RoleTextField},
		"localAsrBatchSizeSeconds": {"performance", ui.RoleTextField},
		"voiceInputDevice":         {"voice", ui.RolePopUpButton},
		"voiceTranslate":           {"voice", ui.RoleCheckBox},
		"runtimeDownloadSource":    {"runtimeDownloads", ui.RolePopUpButton},
		"runtimeInstallLog":        {"runtimeDownloads", ui.RoleButton},
		"runtimeMode":              {"runtimeOverview", ui.RolePopUpButton},
		"backendUrl":               {"remoteCompatibility", ui.RoleTextField},
		"apiToken":                 {"remoteCompatibility", ui.RoleTextField},
		"updateChannel":            {"about", ui.RolePopUpButton},
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		seen := map[string]int{}
		for _, group := range settingsNavigation {
			for _, item := range group.items {
				view := settingsParityView(locale, item.key)
				tester := ui.NewTester(view.View, 1100, 2200)
				for key, expected := range controls {
					if _, ok := tester.AccessByRole(expected.role, view.msg(key)); ok {
						seen[key]++
						if expected.section != item.key {
							t.Fatalf("%s unexpectedly rendered in %s", key, item.key)
						}
					}
				}
			}
		}
		for key := range controls {
			if seen[key] != 1 {
				t.Fatalf("%s has %d primary homes", key, seen[key])
			}
		}
	}
}

func TestSettingsSearchFindsMovedControlsAndRelatedLinksPreserveDrafts(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := settingsParityView(locale, "voice")
		for key, section := range map[string]string{"accentColor": "appearance", "localAsrThreads": "performance", "runtimeInstallLog": "runtimeDownloads", "liveASRModel": "speechModels"} {
			view.search = view.msg(key)
			tester := ui.NewTester(view.Sidebar, 1100, 1200)
			if control, ok := tester.AccessByRole(ui.RoleButton, view.msg(section)); !ok || control.Disabled {
				t.Fatal("configuration search lost its destination", key, section, control)
			}
		}
		fixture := newSettingsCloseFixture(t)
		fixture.load(t)
		view.Client = fixture.controller
		view.Client.Draft.DefaultHotwords = "Synthetic updated hotword"
		destination := ""
		view.NavigateSection = func(section string) { destination = section }
		tester := ui.NewTester(view.View, 1100, 1200)
		if err := tester.Click(localized(locale, "打开", "Open") + " " + view.msg("speechModels")); err != nil {
			t.Fatal(err)
		}
		if destination != "speechModels" || !view.Client.Busy || view.Client.Draft.DefaultHotwords != "Synthetic updated hotword" {
			t.Fatal("related navigation lost the draft or skipped save", destination)
		}
		call := receiveSettingsClose(t, fixture.service.saves)
		fixture.publish(t, call)
		tester.Frame()
		if view.Client.Dirty() {
			t.Fatal("draft did not settle after related navigation")
		}
	}
}
