package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func settingsParityView(locale, section string) *SettingsView {
	panel := &domain.RuntimePanel{
		Overview: domain.RuntimeOverview{
			Status: "missing", Python: domain.NewRuntimeComponent("python", "managed", ""),
			FFmpeg: domain.NewRuntimeComponent("ffmpeg", "managed", ""), Models: domain.NewRuntimeComponent("model", "managed", ""),
		},
		Sources: []domain.RuntimeSourceOption{{ID: "official", Name: "Official"}},
	}
	return &SettingsView{
		Locale: locale, section: section, Back: func() {},
		Client:      &client.SettingsController{Loaded: true, Draft: domain.DefaultSettings()},
		Remote:      &client.RemoteController{},
		Runtime:     &RuntimeView{Locale: locale, Client: &client.RuntimeController{Panel: panel}},
		Diagnostics: &client.DiagnosticsController{Report: &domain.DiagnosticReport{AppVersion: presentation.Version, SchemaVersion: 10, RuntimeStatus: "missing", SupportedPlatforms: domain.DiagnosticPlatforms()}},
	}
}

type settingsEdgeTarget struct {
	role ui.Role
	name string
}

func settingsTrailingTargets(view *SettingsView, locale, section string) []settingsEdgeTarget {
	switch section {
	case "appearance":
		return []settingsEdgeTarget{
			{ui.RoleToggleButton, view.msg("dark")},
			{ui.RoleToggleButton, view.msg("tinted")},
			{ui.RolePopUpButton, view.msg("locale")},
		}
	case "themeSection":
		return []settingsEdgeTarget{{ui.RoleToggleButton, view.msg("colorLabels.#33c96f")}}
	case "processingDefaults":
		return []settingsEdgeTarget{
			{ui.RoleTextField, view.msg("defaultHotwords")},
			{ui.RoleTextField, view.msg("defaultSummaryTemplate")},
			{ui.RolePopUpButton, view.msg("localAsrDevice")},
		}
	case "runtimeOverview":
		return []settingsEdgeTarget{
			{ui.RolePopUpButton, view.msg("runtimeMode")},
			{ui.RoleText, presentation.Message(locale, "shell.localMode") + " · " + view.msg("waitingLocalConfig")},
		}
	case "localRuntime":
		return []settingsEdgeTarget{
			{ui.RolePopUpButton, view.msg("runtimeDownloadSource")},
			{ui.RoleButton, view.msg("runtimeInstallAction")},
			{ui.RoleButton, view.msg("runtimeResourcePython") + " · " + view.msg("runtimeResourceDownload")},
			{ui.RoleButton, view.msg("runtimeResourceFfmpeg") + " · " + view.msg("runtimeResourceDownload")},
			{ui.RoleButton, view.msg("runtimeResourceFunasr") + " · " + view.msg("runtimeResourceDownload")},
			{ui.RoleStatus, view.msg("runtimeStatusMissing")},
		}
	case "remoteCompatibility":
		return []settingsEdgeTarget{
			{ui.RoleTextField, view.msg("backendUrl")},
			{ui.RoleTextField, view.msg("apiToken")},
			{ui.RoleText, view.msg("apiTokenNotConfigured")},
		}
	case "diagnostics":
		return []settingsEdgeTarget{
			{ui.RoleButton, localized(locale, "刷新", "Refresh")},
			{ui.RoleText, presentation.Version},
		}
	default:
		return nil
	}
}

func TestSettingsAllSectionsKeepLegacyHierarchyAndResponsiveFields(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1440, 1100, 760} {
			for _, section := range []string{"appearance", "themeSection", "runtimeOverview", "processingDefaults", "localRuntime", "remoteCompatibility", "diagnostics"} {
				t.Run(fmt.Sprintf("%s-%d-%s", locale, width, section), func(t *testing.T) {
					view := settingsParityView(locale, section)
					shell := Shell{Locale: locale, Route: "/settings", Preferences: domain.DefaultUIPreferences(), Sidebar: view.Sidebar, Content: view.View}
					tester := ui.NewTesterWith(shell.View, width, 2200, ui.TesterOptions{Dark: locale == "en-US"})
					heading, ok := tester.AccessByRole(ui.RoleHeading, view.msg(section))
					if !ok || heading.Level != 2 || heading.Offscreen || !tester.HasText(view.msg(settingsHintKey(section))) {
						t.Fatal("settings lost its heading or section description", heading)
					}
					sideHeading, ok := tester.AccessByRole(ui.RoleHeading, view.msg("pageTitle"))
					if !ok || sideHeading.Level != 1 || sideHeading.Offscreen {
						t.Fatal("settings sidebar title missing", sideHeading)
					}
					if width > 760 && heading.Bounds.X < 248 || width <= 760 && heading.Bounds.Y < sideHeading.Bounds.Y+sideHeading.Bounds.H {
						t.Fatal("settings sidebar ignored its breakpoint", heading, sideHeading)
					}
					for _, node := range tester.AccessAll() {
						if node.Role != ui.RoleTextField && node.Role != ui.RolePopUpButton && node.Role != ui.RoleToggleButton {
							continue
						}
						if node.Offscreen || node.Bounds.W < 20 || node.Bounds.X < 0 || node.Bounds.X+node.Bounds.W > float32(width) {
							t.Fatalf("settings control clipped: %+v", node)
						}
						if node.Role == ui.RoleToggleButton && section != "themeSection" && (node.Name == view.msg("auto") || node.Name == view.msg("light") || node.Name == view.msg("dark") || node.Name == view.msg("transparent") || node.Name == view.msg("tinted")) && node.Bounds.H < 80 {
							t.Fatal("appearance preview lost its card size", node)
						}
						if node.Role == ui.RolePopUpButton && node.Bounds.H < 39.9 {
							t.Fatal("settings selector lost its control height", node)
						}
					}
					if section == "processingDefaults" {
						label, _ := tester.AccessByRole(ui.RoleText, view.msg("defaultHotwords"))
						field, _ := tester.AccessByRole(ui.RoleTextField, view.msg("defaultHotwords"))
						if width <= 1180 && field.Bounds.Y < label.Bounds.Y+label.Bounds.H || width > 1180 && field.Bounds.X <= label.Bounds.X+label.Bounds.W {
							t.Fatalf("settings fields ignored stacked breakpoint: label=%+v field=%+v", label, field)
						}
					}
					tester.SetSize(width, 960)
					tester.Frame()
					if section == "appearance" || section == "themeSection" {
						footer, ok := tester.AccessByRole(ui.RoleText, view.msg("copyright"))
						if !ok || footer.Offscreen || footer.Bounds.Y < 820 || footer.Bounds.Y+footer.Bounds.H > 930 {
							t.Fatal("short settings pages lost their bottom footer", footer)
						}
					}
					captureParity(t, tester, fmt.Sprintf("settings-%s-%s-%d", section, locale, width))
				})
			}
		}
	}
}

func TestSettingsTrailingControlsAlignAcrossEverySection(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1440, 1100, 760} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				view := settingsParityView(locale, "appearance")
				shell := Shell{Locale: locale, Route: "/settings", Preferences: domain.DefaultUIPreferences(), Sidebar: view.Sidebar, Content: view.View}
				appearanceTester := ui.NewTesterWith(shell.View, width, 2200, ui.TesterOptions{Dark: locale == "en-US"})
				localeField, ok := appearanceTester.AccessByRole(ui.RolePopUpButton, view.msg("locale"))
				if !ok {
					t.Fatal("appearance language selector missing")
				}
				appearanceEdge := localeField.Bounds.X + localeField.Bounds.W

				for _, section := range []string{"appearance", "themeSection", "runtimeOverview", "processingDefaults", "localRuntime", "remoteCompatibility", "diagnostics"} {
					t.Run(section, func(t *testing.T) {
						sectionView := settingsParityView(locale, section)
						sectionShell := Shell{Locale: locale, Route: "/settings", Preferences: domain.DefaultUIPreferences(), Sidebar: sectionView.Sidebar, Content: sectionView.View}
						tester := ui.NewTesterWith(sectionShell.View, width, 2200, ui.TesterOptions{Dark: locale == "en-US"})
						targets := settingsTrailingTargets(sectionView, locale, section)
						controls := make([]ui.AccessInfo, 0, len(targets)+2)
						for _, target := range targets {
							control, ok := tester.AccessByRole(target.role, target.name)
							if !ok || control.Offscreen {
								t.Fatalf("settings control missing for trailing-edge check: %s (%v)", target.name, control)
							}
							controls = append(controls, control)
						}
						for index := 1; index < len(controls); index++ {
							first, control := controls[0], controls[index]
							if math.Abs(float64(control.Bounds.X+control.Bounds.W-first.Bounds.X-first.Bounds.W)) > 1 {
								t.Fatalf("%s controls do not share their right edge: %+v and %+v", section, first, control)
							}
						}
						if section == "themeSection" {
							colorPicker := controls[0]
							if math.Abs(float64(colorPicker.Bounds.X+colorPicker.Bounds.W-appearanceEdge)) > 1 {
								t.Fatalf("theme color picker does not end with the other settings controls: %+v language=%+v", colorPicker, localeField)
							}
						}
					})
				}
			})
		}
	}
}

func TestSettingsSidebarFiltersDescriptionsAndAnnouncesEmptyResults(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := settingsParityView(locale, "appearance")
		shell := Shell{Locale: locale, Route: "/settings", Preferences: domain.DefaultUIPreferences(), Sidebar: view.Sidebar, Content: view.View}
		tester := ui.NewTester(shell.View, 1100, 960)
		active, ok := tester.AccessByRole(ui.RoleButton, view.msg("appearance"))
		if !ok || active.Current != ui.CurrentPage || active.Description != view.msg("appearanceHint") {
			t.Fatal("settings navigation lost its current page or description", active)
		}
		if err := tester.Click(view.msg("searchPlaceholder")); err != nil {
			t.Fatal(err)
		}
		tester.Type("  pYtHoN  ")
		if view.search != "  pYtHoN  " {
			t.Fatal("settings search did not accept keyboard input")
		}
		for _, section := range []string{"appearance", "themeSection", "runtimeOverview", "processingDefaults", "remoteCompatibility", "diagnostics"} {
			if _, ok := tester.AccessByRole(ui.RoleButton, view.msg(section)); ok {
				t.Fatal("search retained a navigation item without a match", section)
			}
		}
		match, ok := tester.AccessByRole(ui.RoleButton, view.msg("localRuntime"))
		if !ok || match.Disabled || match.Offscreen || !strings.Contains(match.Description, "Python") {
			t.Fatal("settings description search lost its result", match)
		}
		view.search = "no-synthetic-setting-matches"
		tester.Frame()
		status, ok := tester.AccessByRole(ui.RoleStatus, view.msg("searchEmpty"))
		if !ok || status.Offscreen {
			t.Fatal("empty search lacks a live status", status)
		}
		for _, group := range settingsNavigation {
			for _, node := range tester.AccessAll() {
				if node.Role == ui.RoleText && node.Name == view.msg(group.key) && node.Bounds.X < 248 {
					t.Fatal("empty search retained an empty navigation group", group.key)
				}
			}
		}
	}
}

func TestSettingsCompactNavigationScrollsAndKeepsMainContentUsable(t *testing.T) {
	view := settingsParityView("en-US", "appearance")
	shell := Shell{Locale: view.Locale, Route: "/settings", Preferences: domain.DefaultUIPreferences(), Sidebar: view.Sidebar, Content: view.View}
	tester := ui.NewTester(shell.View, 760, 960)
	navigation, ok := tester.AccessByRole(ui.RoleNavigation, view.msg("navigationLabel"))
	if !ok || navigation.Bounds.H > 50 || navigation.Bounds.W < 700 {
		t.Fatal("compact settings navigation did not become a horizontal strip", navigation)
	}
	tester.Scroll(navigation.Bounds.X+200, navigation.Bounds.Y+15, 2000, 0)
	tester.Frame()
	last, ok := tester.AccessByRole(ui.RoleButton, view.msg("diagnostics"))
	if !ok || last.Offscreen || last.Bounds.W < 70 || last.Bounds.X+last.Bounds.W > 760 {
		t.Fatal("last compact settings link remains unreachable", last)
	}
	heading, _ := tester.AccessByRole(ui.RoleHeading, view.msg("appearance"))
	if heading.Bounds.Y < navigation.Bounds.Y+navigation.Bounds.H || heading.Bounds.Y > 340 {
		t.Fatal("compact navigation covered or displaced settings content", heading)
	}
	view.Runtime = nil
	tester.SetSize(1100, 960)
	tester.Frame()
	runtime, _ := tester.AccessByRole(ui.RoleButton, view.msg("localRuntime"))
	if !runtime.Disabled {
		t.Fatal("missing runtime controller exposed a working settings route")
	}
}

func TestSettingsMissingAuxiliaryControllersRenderAccessibleUnavailableState(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, scenario := range []struct {
			name, section string
			remove        func(*SettingsView)
		}{
			{"runtime-view", "localRuntime", func(view *SettingsView) { view.Runtime = nil }},
			{"runtime-controller", "localRuntime", func(view *SettingsView) { view.Runtime.Client = nil }},
			{"remote-controller", "remoteCompatibility", func(view *SettingsView) { view.Remote = nil }},
			{"diagnostics-controller", "diagnostics", func(view *SettingsView) { view.Diagnostics = nil }},
			{"settings-controller", "appearance", func(view *SettingsView) { view.Client = nil }},
		} {
			t.Run(locale+"/"+scenario.name, func(t *testing.T) {
				view := settingsParityView(locale, scenario.section)
				scenario.remove(view)
				tester := ui.NewTester(view.View, 1100, 960)
				message := localized(locale, "此设置分区暂不可用，请重新加载应用后重试。", "This settings section is unavailable. Reload the app and try again.")
				alert, ok := tester.AccessByRole(ui.RoleAlert, message)
				if !ok || alert.Offscreen {
					t.Fatal("missing settings dependency did not produce an accessible alert", alert)
				}
				if !tester.HasText(localized(locale, "相关原生服务尚未连接，页面不会显示不可用的操作控件。", "The related native service is not connected, so unavailable controls are hidden.")) {
					t.Fatal("missing settings dependency lost its explanatory text")
				}
				for _, control := range tester.AccessAll() {
					switch control.Role {
					case ui.RoleButton, ui.RolePopUpButton, ui.RoleTextField, ui.RoleToggleButton:
						t.Fatal("unavailable settings section exposed an interactive control", control)
					}
				}
				if scenario.name == "settings-controller" {
					sidebar := ui.NewTester(view.Sidebar, 1100, 960)
					for _, section := range []string{"appearance", "themeSection", "runtimeOverview", "processingDefaults", "localRuntime", "remoteCompatibility", "diagnostics"} {
						link, ok := sidebar.AccessByRole(ui.RoleButton, view.msg(section))
						if !ok || !link.Disabled {
							t.Fatal("settings navigation remained actionable without its controller", section, link)
						}
					}
				}
			})
		}
	}
}

func TestSettingsRuntimeOverviewDoesNotRequireRemoteController(t *testing.T) {
	view := settingsParityView("en-US", "runtimeOverview")
	view.Remote = nil
	tester := ui.NewTester(view.View, 1100, 960)
	if _, ok := tester.AccessByRole(ui.RolePopUpButton, view.msg("runtimeMode")); !ok {
		t.Fatal("runtime overview disappeared when the optional remote controller was absent")
	}

	view.section = "appearance"
	tester = ui.NewTester(view.Sidebar, 1100, 960)
	runtimeOverview, ok := tester.AccessByRole(ui.RoleButton, view.msg("runtimeOverview"))
	if !ok || runtimeOverview.Disabled {
		t.Fatal("runtime overview was incorrectly disabled without a remote controller", runtimeOverview)
	}
	remote, ok := tester.AccessByRole(ui.RoleButton, view.msg("remoteCompatibility"))
	if !ok || !remote.Disabled {
		t.Fatal("remote compatibility route did not reflect its missing controller", remote)
	}
}

type settingsParityService struct{ saved chan domain.Settings }

func (service settingsParityService) Settings(context.Context) (domain.Settings, error) {
	return domain.DefaultSettings(), nil
}
func (service settingsParityService) SaveSettings(_ context.Context, value domain.Settings, _ domain.CredentialUpdate) (domain.Settings, application.MutationOutcome, error) {
	service.saved <- value
	return value, application.MutationOutcome{}, nil
}

func TestSettingsKeyboardChoicePersistsAndExposesPressedState(t *testing.T) {
	callbacks := make(chan func(), 16)
	service := settingsParityService{saved: make(chan domain.Settings, 4)}
	controller := client.NewSettings(service, func(callback func()) { callbacks <- callback }, func() {}, func(domain.UIPreferences) {})
	defer controller.Close()
	view := &SettingsView{Locale: "en-US", Client: controller, section: "appearance"}
	tester := ui.NewTester(view.View, 1100, 960)
	controller.Load()
	flushView(t, callbacks, tester)
	if err := tester.Click(view.msg("auto")); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyTab)
	if !tester.Focused(view.msg("light")) {
		t.Fatal("appearance previews lost their visual keyboard order")
	}
	tester.Key(0, ui.KeyEnter)
	flushView(t, callbacks, tester)
	if value := <-service.saved; value.ThemeMode != "light" {
		t.Fatal("keyboard theme choice was not saved", value.ThemeMode)
	}
	choice, ok := tester.AccessByRole(ui.RoleToggleButton, view.msg("light"))
	if !ok || !choice.Checked {
		t.Fatal("selected theme lacks its pressed state", choice)
	}
	for _, mode := range []string{"auto", "dark"} {
		other, ok := tester.AccessByRole(ui.RoleToggleButton, view.msg(mode))
		if !ok || other.Checked {
			t.Fatal("theme choice no longer has a single pressed option", other)
		}
	}
	view.section = "themeSection"
	controller.Draft.AccentColor = "#A65DD9"
	tester.Frame()
	color, ok := tester.AccessByRole(ui.RoleToggleButton, view.msg("colorLabels.#a65dd9"))
	if !ok || !color.Checked || math.Abs(float64(color.Bounds.W-38)) > .1 {
		t.Fatal("accent color selection is case-sensitive or lost its hit target", color)
	}
}

func TestSettingsAppearanceShowsSessionOverridesAndPersistsExplicitChoices(t *testing.T) {
	callbacks := make(chan func(), 8)
	service := settingsParityService{saved: make(chan domain.Settings, 4)}
	startup := client.StartupPreferences{Locale: "en-US", ThemeMode: "dark"}
	view := &SettingsView{Locale: "en-US", section: "appearance"}
	controller := client.NewSettings(service, func(callback func()) { callbacks <- callback }, func() {}, func(preferences domain.UIPreferences) {
		view.Locale = preferences.Locale
	}, startup)
	defer controller.Close()
	view.Client = controller
	tester := ui.NewTester(view.View, 1100, 960)
	controller.Load()
	flushView(t, callbacks, tester)
	assertPreferences := func(mode, locale string) {
		t.Helper()
		choice, ok := tester.AccessByRole(ui.RoleToggleButton, view.msg(mode))
		if !ok || !choice.Checked {
			t.Fatal("appearance did not expose the effective session theme", choice)
		}
		language, ok := tester.AccessByRole(ui.RolePopUpButton, view.msg("locale"))
		if !ok || language.Value != view.msg(locale) {
			t.Fatal("appearance did not expose the effective session language", language)
		}
	}
	assertPreferences("dark", "localeEn")
	if controller.Dirty() || len(service.saved) != 0 {
		t.Fatal("loading session overrides wrote settings")
	}
	if err := tester.Click(view.msg("auto")); err != nil {
		t.Fatal(err)
	}
	flushView(t, callbacks, tester)
	if value := <-service.saved; value.ThemeMode != "auto" || value.Locale != "zh-CN" {
		t.Fatal("explicit theme choice saved the unrelated language override", value.UIPreferences)
	}
	assertPreferences("auto", "localeEn")
	language, _ := tester.AccessByRole(ui.RolePopUpButton, view.msg("locale"))
	tester.ClickAt(language.Bounds.X+language.Bounds.W/2, language.Bounds.Y+language.Bounds.H/2)
	if err := tester.Click(view.msg("localeZh")); err != nil {
		t.Fatal(err)
	}
	flushView(t, callbacks, tester)
	if value := <-service.saved; value.ThemeMode != "auto" || value.Locale != "zh-CN" {
		t.Fatal("explicit language choice did not release its session override", value.UIPreferences)
	}
	if view.Locale != "zh-CN" || controller.Dirty() {
		t.Fatal("explicit stored-value choice did not settle")
	}
	assertPreferences("auto", "localeZh")
}

func TestSettingsRuntimeOverviewDoesNotReportStaleReadyStatus(t *testing.T) {
	view := settingsParityView("en-US", "runtimeOverview")
	view.Runtime.Client.Panel.Overview.ShellReady = true
	tester := ui.NewTester(view.View, 1100, 960)
	if !tester.HasText(view.msg("localDatabaseReady")) {
		t.Fatal("verified runtime readiness missing")
	}
	view.Runtime.Client.Error = errors.New("synthetic runtime refresh failed")
	tester.Frame()
	if tester.HasText(view.msg("localDatabaseReady")) || !tester.HasText(view.msg("waitingLocalConfig")) {
		t.Fatal("failed runtime refresh retained a ready label")
	}
}

func TestSettingsRuntimeOverviewFollowsSelectedProcessingModeAndRemoteState(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, scenario := range []struct {
			name         string
			capabilities *domain.RemoteCapabilities
			err          error
			checking     bool
			want         string
		}{
			{name: "idle", want: "remoteStatusIdle"},
			{name: "checking", checking: true, want: "remoteStatusChecking"},
			{name: "ready", capabilities: &domain.RemoteCapabilities{ServiceVersion: "1"}, want: "remoteStatusReady"},
			{name: "unavailable", err: errors.New("synthetic remote failure"), want: "remoteStatusUnavailable"},
			{name: "checking takes precedence", capabilities: &domain.RemoteCapabilities{ServiceVersion: "1"}, checking: true, want: "remoteStatusChecking"},
			{name: "checking supersedes failure", err: errors.New("synthetic remote failure"), checking: true, want: "remoteStatusChecking"},
		} {
			t.Run(locale+"/"+scenario.name, func(t *testing.T) {
				view := settingsParityView(locale, "runtimeOverview")
				view.Runtime.Client.Panel.Overview.ShellReady = true
				view.Client.Draft.ProcessingMode = "remote"
				view.Remote.Capabilities = scenario.capabilities
				view.Remote.Error = scenario.err
				view.Remote.Checking = scenario.checking

				tester := ui.NewTester(view.View, 1100, 960)
				if !tester.HasText(view.msg(scenario.want)) {
					t.Fatalf("remote runtime status missing: want %q", view.msg(scenario.want))
				}
				if tester.HasText(view.msg("localDatabaseReady")) || tester.HasText(view.msg("waitingLocalConfig")) {
					t.Fatal("remote mode displayed local runtime status")
				}

				view.Client.Draft.ProcessingMode = "local"
				tester.Frame()
				if !tester.HasText(view.msg("localDatabaseReady")) || tester.HasText(view.msg(scenario.want)) {
					t.Fatal("switching back to local mode retained remote status")
				}

				view.Runtime.Client.Error = errors.New("synthetic local runtime failure")
				tester.Frame()
				if tester.HasText(view.msg("localDatabaseReady")) || !tester.HasText(view.msg("waitingLocalConfig")) {
					t.Fatal("local mode did not restore its runtime status after refresh failure")
				}
			})
		}
	}
}

func TestSettingsRuntimeModeChoicePersistsBothDirections(t *testing.T) {
	callbacks := make(chan func(), 8)
	service := settingsParityService{saved: make(chan domain.Settings, 4)}
	controller := client.NewSettings(service, func(callback func()) { callbacks <- callback }, func() {}, func(domain.UIPreferences) {})
	defer controller.Close()
	view := settingsParityView("en-US", "runtimeOverview")
	view.Client = controller
	tester := ui.NewTester(view.View, 1100, 960)
	controller.Load()
	flushView(t, callbacks, tester)

	selectMode := func(key ui.Key, expected string) {
		t.Helper()
		control, ok := tester.AccessByRole(ui.RolePopUpButton, view.msg("runtimeMode"))
		if !ok || control.Disabled {
			t.Fatalf("runtime mode selector is unavailable: %+v", control)
		}
		tester.ClickAt(control.Bounds.X+control.Bounds.W/2, control.Bounds.Y+control.Bounds.H/2)
		tester.Key(0, key)
		tester.Key(0, ui.KeyEnter)
		flushView(t, callbacks, tester)
		saved := <-service.saved
		if saved.ProcessingMode != expected || controller.Draft.ProcessingMode != expected || controller.Dirty() {
			t.Fatalf("runtime mode did not settle after save: saved=%q draft=%q dirty=%v", saved.ProcessingMode, controller.Draft.ProcessingMode, controller.Dirty())
		}
	}

	selectMode(ui.KeyEnd, "remote")
	selectMode(ui.KeyHome, "local")
}
