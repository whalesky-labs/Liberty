package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

type companionTestWindow struct{ x, y int }

func (window *companionTestWindow) Position() (int, int) { return window.x, window.y }
func (window *companionTestWindow) SetPosition(x, y int) { window.x, window.y = x, y }

func TestCompanionDragUsesEventCoordinatesAndKeepsCaptureAcrossPrompts(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			window := &companionTestWindow{x: -900, y: 125}
			var saved []mygo.Point
			taps := 0
			pointer := companionPointer{window: window, remember: func(p mygo.Point) { saved = append(saved, p) }, tap: func() { taps++ }}
			view := &PetView{Client: &client.PetController{}, Locale: locale}
			var until time.Time
			tt := ui.NewTester(func(c *ui.Context) { view.companion(c, "Prompt", until, nil, pointer.input) }, companionWidth, companionHeight)
			tt.Press(150, 140)
			tt.Move(152, 141)
			if window.x != -900 || window.y != 125 {
				t.Fatal("a small pointer jitter moved the window")
			}
			until = tt.Now().Add(time.Second)
			tt.Frame()
			tt.Move(170, 150)
			if window.x != -880 || window.y != 135 {
				t.Fatal("drag lost capture when prompt appeared", window)
			}
			// The window has moved: the next event is relative to its new origin.
			tt.Move(155, 145)
			if window.x != -875 || window.y != 140 {
				t.Fatal("continued drag drifted from the original grab point", window)
			}
			tt.Advance(2 * time.Second)
			tt.Move(-10, -20)
			if window.x != -1035 || window.y != -20 {
				t.Fatal("drag lost capture outside the sprite or when prompt expired", window)
			}
			if len(saved) != 0 || taps != 0 {
				t.Fatal("drag committed before release")
			}
			// Release at the original grab point after the window caught up.
			tt.Release(150, 140)
			if len(saved) != 1 || saved[0] != (mygo.Point{X: -1035, Y: -20}) || taps != 0 || pointer.pressed || pointer.dragging {
				t.Fatal("drag did not save only its final position", saved, taps)
			}
		})
	}
}

func TestCompanionTapCancelAndSecondaryButton(t *testing.T) {
	window := &companionTestWindow{}
	taps, saves := 0, 0
	pointer := companionPointer{window: window, tap: func() { taps++ }, remember: func(mygo.Point) { saves++ }}
	view := &PetView{Client: &client.PetController{}, Locale: "en-US"}
	tt := ui.NewTester(func(c *ui.Context) { view.companion(c, "", time.Time{}, nil, pointer.input) }, companionWidth, companionHeight)
	tt.PressButton(1, 150, 140)
	tt.Move(160, 150)
	tt.ReleaseButton(1, 160, 150)
	if pointer.pressed || window.x != 0 || window.y != 0 || taps != 0 || saves != 0 {
		t.Fatal("secondary button began a drag or tap")
	}
	tt.Press(150, 140)
	tt.Move(152, 141)
	tt.Release(152, 141)
	if taps != 1 || saves != 0 {
		t.Fatal("tap was treated as drag")
	}
	for _, drag := range []bool{false, true} {
		tt.Press(150, 140)
		if drag {
			tt.Move(170, 150)
		}
		tt.CancelPress()
		if pointer.pressed || pointer.dragging || taps != 1 || saves != 0 {
			t.Fatal("cancelled gesture remained captured or committed")
		}
	}
	// Extra pets have no position writer; dragging them must still move them.
	pointer.remember = nil
	tt.Press(150, 140)
	tt.Move(160, 150)
	tt.Release(150, 140)
	if window.x != 30 || window.y != 20 || taps != 1 {
		t.Fatal("extra companion drag failed", window, taps)
	}
}

func TestCompanionCanvasIsTransparentInBothThemes(t *testing.T) {
	view := &PetView{Client: &client.PetController{}, Locale: "en-US"}
	for _, dark := range []bool{false, true} {
		tt := ui.NewTesterWith(func(c *ui.Context) {
			view.companion(c, "", time.Time{}, nil, func(ui.InputEvent) bool { return false })
		}, companionWidth, companionHeight, ui.TesterOptions{Dark: dark})
		image := tt.Image()
		for _, point := range []mygo.Point{{X: 0, Y: 0}, {X: 20, Y: 100}, {X: 319, Y: 219}} {
			if pixel := image.RGBAAt(point.X, point.Y); pixel.A != 0 {
				t.Fatalf("dark=%t: canvas is opaque at %+v: %+v", dark, point, pixel)
			}
		}
		visible := false
		for y := companionSprite.Y; y < companionSprite.Y+companionSprite.Height; y++ {
			for x := companionSprite.X; x < companionSprite.X+companionSprite.Width; x++ {
				visible = visible || image.RGBAAt(x, y).A > 0
			}
		}
		if !visible {
			t.Fatal("transparent canvas also hid the companion")
		}
	}
}

func TestCompanionPositionRestoresOnLeftDisplayAndRecoversAfterDisconnect(t *testing.T) {
	left := mygo.Display{WorkArea: mygo.Rectangle{X: -1600, Y: 30, Width: 1600, Height: 870}, ScaleFactor: 1}
	main := mygo.Display{Primary: true, WorkArea: mygo.Rectangle{X: 0, Y: 24, Width: 1440, Height: 850}, ScaleFactor: 2}
	x, y := -900.0, 125.0
	settings := domain.PetSettings{LastWindowX: &x, LastWindowY: &y}
	if got := companionPosition(settings, []mygo.Display{main, left}, 0); got != (mygo.Point{X: -900, Y: 125}) {
		t.Fatal("valid saved position was discarded", got)
	}
	primary := companionPosition(settings, []mygo.Display{main}, 0)
	if primary.X != 1096 || primary.Y != 630 {
		t.Fatal("offscreen position retained", primary)
	}
	extra := companionPosition(settings, []mygo.Display{main, left}, 1)
	if extra.X != primary.X-168 || extra.Y != primary.Y {
		t.Fatal("extra window overrode primary position", extra)
	}
}
