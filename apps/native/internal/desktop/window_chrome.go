package desktop

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func newDesktopWindow(options mygo.WindowOptions) *mygo.Window {
	window := mygo.NewWindow(options)
	configureWindowChrome(window, options)
	return window
}

func usesNativeCaption(options mygo.WindowOptions) bool {
	return !options.Frameless && (options.TitleBarStyle == "" || options.TitleBarStyle == mygo.TitleBarDefault)
}

// Windows COLORREF stores red in the least significant byte (not RGB hex).
func windowColorRef(color ui.Color) uint32 {
	return uint32(color.R) | uint32(color.G)<<8 | uint32(color.B)<<16
}

type titlebarAppearance struct {
	dark          uint32
	caption, text uint32
}

func nativeTitlebarAppearance(dark, highContrast bool) titlebarAppearance {
	if highContrast {
		// Let Windows' contrast theme choose both colors.
		return titlebarAppearance{caption: 0xffffffff, text: 0xffffffff}
	}
	palette := presentation.Colors(dark, ui.Color{})
	appearance := titlebarAppearance{caption: windowColorRef(palette.Window), text: windowColorRef(palette.Theme.Text)}
	if dark {
		appearance.dark = 1
	}
	return appearance
}
