package desktop

import (
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
	"testing"
)

func TestRemoteDiagnosticSettingsRetainOriginalFieldsAndBilingualLabels(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, section := range []string{"runtimeOverview", "remoteCompatibility", "diagnostics"} {
			v := &SettingsView{Locale: locale, section: section, Client: &client.SettingsController{Loaded: true, Draft: domain.DefaultSettings()}, Remote: &client.RemoteController{}, Diagnostics: &client.DiagnosticsController{Report: &domain.DiagnosticReport{AppVersion: "1.1.24", SchemaVersion: 10, RuntimeStatus: "ready", SupportedPlatforms: domain.DiagnosticPlatforms()}}}
			tt := ui.NewTester(v.View, 1180, 1000)
			if !tt.HasText(v.msg(section)) {
				t.Fatal("missing section", section, locale)
			}
			if section == "remoteCompatibility" {
				for _, key := range []string{"backendUrl", "apiToken", "remoteStatus", "remoteCheck"} {
					if !tt.HasText(v.msg(key)) {
						t.Fatal("missing remote field", key, locale)
					}
				}
			}
			if section == "diagnostics" {
				if !tt.HasText("darwin-aarch64") || !tt.HasText(localized(locale, "原生界面（不适用）", "Native UI (not applicable)")) {
					t.Fatal("diagnostics native projection missing", locale)
				}
			}
		}
	}
}
