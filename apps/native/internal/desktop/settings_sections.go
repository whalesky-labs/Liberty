package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func runtimeSettingsSection(section string) bool {
	return section == "localRuntime" || section == "speechModels" || section == "runtimeDownloads"
}

func (v *SettingsView) sectionAvailable(section string) bool {
	if section == "deviceStorage" {
		return v.Storage != nil
	}
	if profileIndependentSection(section) {
		return true
	}
	if v.Client == nil {
		return false
	}
	switch section {
	case "remoteCompatibility":
		return v.Remote != nil
	case "diagnostics":
		return v.Diagnostics != nil
	case "localRuntime", "speechModels", "runtimeDownloads":
		return v.Runtime != nil && v.Runtime.Client != nil
	case "voice":
		return v.Runtime != nil && v.Runtime.Live != nil
	default:
		return true
	}
}

func (v *SettingsView) selectSection(section string) {
	if !v.sectionAvailable(section) {
		return
	}
	if v.Client != nil {
		if v.Client.Dirty() {
			v.Client.Save()
		} else if !profileIndependentSection(section) {
			v.Client.Load()
		}
	}
	if v.NavigateSection != nil {
		v.NavigateSection(section)
	} else {
		v.section = section
	}
}

func (v *SettingsView) relatedSettings(c *ui.Context, sections ...string) {
	settingsGroup(c, v.msg("relatedSettingsGroup"), func() {
		for _, section := range sections {
			settingsSectionLink(c, v.Locale, section, v.sectionAvailable(section), func() { v.selectSection(section) })
		}
	})
}

func settingsSectionLink(c *ui.Context, locale, section string, available bool, open func()) {
	title := presentation.Message(locale, "settings."+section)
	settingsRow(c, title, presentation.Message(locale, "settings."+settingsHintKey(section)), func() {
		if ui.Button(c, presentation.Message(locale, "settings.openSettingsSection")).Label(localized(locale, "打开", "Open") + " " + title).Disabled(!available || open == nil).Clicked() {
			open()
		}
	})
}

// Search the settings themselves as well as their page names.
func settingsSearchTerms(locale, section string) string {
	keys := map[string][]string{
		"appearance":          {"themeMode", "glassStyle", "accentColor", "customAccentColor"},
		"languageDisplay":     {"locale", "performanceIndicators"},
		"processingDefaults":  {"defaultHotwords", "defaultSummaryTemplate"},
		"performance":         {"concurrency", "localAsrDevice", "localAsrThreads", "localAsrBatchSizeSeconds"},
		"voice":               {"voiceInputDevice", "voiceTranslate", "voiceLanguages"},
		"localRuntime":        {"runtimeResourcePython", "runtimeResourceFfmpeg"},
		"speechModels":        {"runtimeResourceFunasr", "liveASRModel", "liveTranslationModels"},
		"runtimeDownloads":    {"runtimeDownloadSource", "runtimeInstallLog"},
		"runtimeOverview":     {"runtimeMode"},
		"remoteCompatibility": {"backendUrl", "apiToken", "remoteCheck"},
		"accessPermissions":   {"diskPermission", "microphonePermission"},
		"experiments":         {"memoryUsage", "memoryScope"},
		"about":               {"updateChannel", "checkUpdates", "license"},
	}
	terms := make([]string, 0, len(keys[section]))
	for _, key := range keys[section] {
		terms = append(terms, presentation.Message(locale, "settings."+key))
	}
	return strings.Join(terms, " ")
}
