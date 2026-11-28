package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/domain"
)

func TestContentScrollbarReachesRightEdge(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		contentScroll(c).Fill().Children(func() {
			ui.Box(c).FillWidth().Height(600).Background(ui.Hex("#b04060"))
		})
	}, 200, 200)
	before := tester.Image()
	tester.Move(100, 100)
	after := tester.Image()

	visibleAtEdge := false
	for y := 0; y < after.Bounds().Dy(); y++ {
		x := after.Bounds().Dx() - 1
		if before.RGBAAt(x, y) != after.RGBAAt(x, y) {
			visibleAtEdge = true
			break
		}
	}
	if !visibleAtEdge {
		t.Fatal("scrollbar thumb did not touch the viewport's right edge")
	}
}

func TestSettingsContentScrollbarReachesWindowEdge(t *testing.T) {
	const width, height = 1100, 600
	shell := Shell{
		Locale: "en-US", Route: "/settings", Preferences: domain.DefaultUIPreferences(),
		Sidebar: settingsParityView("en-US", "appearance").Sidebar,
		Content: func(c *ui.Context) {
			settingsPage(c, "en-US", "appearance", func() {
				ui.Box(c).FillWidth().Height(1200)
			})
		},
	}
	tester := ui.NewTester(shell.View, width, height)
	before := tester.Image()
	tester.Move(width-4, height/2)
	after := tester.Image()

	for y := 0; y < after.Bounds().Dy(); y++ {
		x := width - 1
		if before.RGBAAt(x, y) != after.RGBAAt(x, y) {
			return
		}
	}
	t.Fatal("settings scrollbar thumb remained inset from the window's right edge")
}
