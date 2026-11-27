package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func TestSettingsGitHubUpdateLink(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			tester := ui.NewTesterWith(func(c *ui.Context) {
				settingsPage(c, locale, "diagnostics", func() {})
			}, 1100, 960, ui.TesterOptions{})
			label := presentation.Message(locale, "settings.downloadUpdate")
			link, ok := tester.AccessByRole(ui.RoleLink, label)
			if !ok || link.Offscreen {
				t.Fatal("missing accessible GitHub update link", link)
			}
			if err := tester.Click(label); err != nil {
				t.Fatal(err)
			}
			opened := tester.OpenedURLs()
			if len(opened) != 1 || opened[0] != "https://github.com/whalesky-labs/Liberty/releases" {
				t.Fatal("update link must include prereleases and use the canonical GitHub repository", opened)
			}
			captureParity(t, tester, "settings-github-update-"+locale)
		})
	}
}
