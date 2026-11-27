package desktop

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func TestWindowChromePreservesCompanionAndHiddenCaptions(t *testing.T) {
	for _, options := range []mygo.WindowOptions{{}, {TitleBarStyle: mygo.TitleBarDefault}} {
		if !usesNativeCaption(options) {
			t.Fatal("native caption was excluded", options)
		}
	}
	for _, options := range []mygo.WindowOptions{{Frameless: true}, {TitleBarStyle: mygo.TitleBarHidden}, {TitleBarStyle: mygo.TitleBarHiddenInset}} {
		if usesNativeCaption(options) {
			t.Fatal("custom caption must retain MyGo's frame suppression", options)
		}
	}
}

func TestNativeTitlebarMatchesBothApplicationThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		actual := nativeTitlebarAppearance(dark, false)
		palette := presentation.Colors(dark, ui.Color{})
		if actual.caption != windowColorRef(palette.Window) || actual.text != windowColorRef(palette.Theme.Text) || (actual.dark == 1) != dark {
			t.Fatalf("titlebar differs from application dark=%v: %+v", dark, actual)
		}
		if contrast := nativeTitlebarAppearance(dark, true); contrast.dark != 0 || contrast.caption != 0xffffffff || contrast.text != 0xffffffff {
			t.Fatalf("titlebar overrides the Windows contrast theme: %+v", contrast)
		}
	}
	if light, dark := nativeTitlebarAppearance(false, false), nativeTitlebarAppearance(true, false); light.caption == dark.caption || light.text == dark.text {
		t.Fatal("light/dark transition did not change native titlebar colors")
	}
	if windowColorRef(ui.RGB(0x12, 0x34, 0x56)) != 0x563412 {
		t.Fatal("titlebar colors must use Windows COLORREF byte order")
	}
}
