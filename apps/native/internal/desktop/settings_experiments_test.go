package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func TestSettingsExperimentsToggleIndicatorsAndKeepChart(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := &SettingsView{Locale: locale}
			shell := Shell{Locale: locale, Route: "/settings", Sidebar: view.Sidebar, Content: view.View, Metrics: &domain.ProcessMetrics{MemoryBytes: 345 << 20, SampledAt: time.Now()}}
			service := settingsParityService{saved: make(chan domain.Settings, 2)}
			callbacks := make(chan func(), 8)
			view.Client = client.NewSettings(service, func(fn func()) { callbacks <- fn }, func() {}, func(p domain.UIPreferences) { shell.Preferences = p })
			defer view.Client.Close()
			tester := ui.NewTesterWith(shell.View, 1440, 960, ui.TesterOptions{Dark: true})
			if err := tester.Click(view.msg("languageDisplay")); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tester)
			if !tester.HasText("345 MiB") {
				t.Fatal("header memory indicator missing")
			}
			for _, hidden := range []bool{true, false} {
				sw, ok := tester.AccessByRole(ui.RoleSwitch, view.msg("performanceIndicators"))
				if !ok || sw.Checked != hidden {
					t.Fatal("unexpected switch state", sw)
				}
				tester.ClickAt(sw.Bounds.X+sw.Bounds.W/2, sw.Bounds.Y+sw.Bounds.H/2)
				flushView(t, callbacks, tester)
				if saved := <-service.saved; saved.HidePerformanceIndicators != hidden || tester.HasText("345 MiB") == hidden {
					t.Fatal("header and preference did not follow switch")
				}

			}
			navigation, _ := tester.AccessByRole(ui.RoleNavigation, view.msg("navigationLabel"))
			tester.Scroll(navigation.Bounds.X+100, navigation.Bounds.Y+100, 0, 600)
			tester.Frame()
			if err := tester.Click(view.msg("experiments")); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tester)
			if chart, ok := tester.AccessByRole(ui.RoleGroup, view.msg("memoryUsage")); !ok || chart.Offscreen || chart.Bounds.H < 200 {
				t.Fatal("memory chart missing", chart)
			}
			captureParity(t, tester, "settings-experiments-"+locale)
		})
	}
}

func TestMemoryTreemapUsesSampledArea(t *testing.T) {
	tiles := []memoryTile{{name: "A", bytes: 3 << 20}, {name: "B", bytes: 1 << 20}}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Size(600, 200).Children(func() {
			memoryTreemap(c, tiles, ui.Rect{W: 100, H: 100}, 600, 200)
		})
	}, 800, 400)
	for _, value := range []uint64{3 << 20, 1 << 20} {
		tiles[0].bytes = value
		tester.Frame()
		a, okA := tester.AccessByRole(ui.RoleGroup, "A · "+tiles[0].size())
		b, okB := tester.AccessByRole(ui.RoleGroup, "B · "+tiles[1].size())
		wantWidth := float32(600 * value / (value + tiles[1].bytes))
		if !okA || !okB || a.Bounds.H < 195 || b.Bounds.H < 195 || a.Bounds.W < wantWidth-4 || a.Bounds.W > wantWidth || b.Bounds.X < a.Bounds.X+a.Bounds.W || a.Bounds.W+b.Bounds.W < 592 {
			t.Fatal("tiles must fill the chart in proportion to their bytes", a.Bounds, b.Bounds, wantWidth)
		}
	}
}
