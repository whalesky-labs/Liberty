package presentation

import (
	"math"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo/ui"
)

var macColors = [2]func() SystemColors{
	sync.OnceValue(func() SystemColors { return readSystemColors(false) }),
	sync.OnceValue(func() SystemColors { return readSystemColors(true) }),
}

func systemColors(dark bool) SystemColors {
	if dark {
		return macColors[1]()
	}
	return macColors[0]()
}

func readSystemColors(dark bool) SystemColors {
	colors := fallbackSystemColors(dark)
	// NSAppearance's current drawing appearance is thread-local. Resolving an
	// explicit Aqua/Dark Aqua pair must not change the app or another window.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		return colors
	}
	send := func(object objc.ID, selector string, args ...any) objc.ID {
		return object.Send(objc.RegisterName(selector), args...)
	}
	pool := send(send(objc.ID(objc.GetClass("NSAutoreleasePool")), "alloc"), "init")
	defer send(pool, "drain")
	appearanceClass := objc.ID(objc.GetClass("NSAppearance"))
	previous := send(appearanceClass, "currentAppearance")
	defer send(appearanceClass, "setCurrentAppearance:", previous)
	name := "NSAppearanceNameAqua"
	if dark {
		name = "NSAppearanceNameDarkAqua"
	}
	stringName := send(objc.ID(objc.GetClass("NSString")), "stringWithUTF8String:", name)
	appearance := send(appearanceClass, "appearanceNamed:", stringName)
	if appearance == 0 {
		return colors
	}
	send(appearanceClass, "setCurrentAppearance:", appearance)
	colorClass := objc.ID(objc.GetClass("NSColor"))
	colorSpace := send(objc.ID(objc.GetClass("NSColorSpace")), "sRGBColorSpace")
	for _, entry := range []struct {
		selector string
		color    *ui.Color
	}{
		{"windowBackgroundColor", &colors.Window}, {"textBackgroundColor", &colors.TextBackground},
		{"labelColor", &colors.Label}, {"secondaryLabelColor", &colors.SecondaryLabel},
		{"separatorColor", &colors.Separator}, {"unemphasizedSelectedContentBackgroundColor", &colors.Selection},
		{"tertiarySystemFillColor", &colors.Fill}, {"quaternarySystemFillColor", &colors.SubtleFill},
		{"systemRedColor", &colors.Red}, {"systemOrangeColor", &colors.Orange}, {"systemGreenColor", &colors.Green},
	} {
		selector := objc.RegisterName(entry.selector)
		// System fill colors were added after the oldest supported macOS.
		if !objc.Send[bool](colorClass, objc.RegisterName("respondsToSelector:"), selector) {
			continue
		}
		color := send(colorClass.Send(selector), "colorUsingColorSpace:", colorSpace)
		if color == 0 {
			continue
		}
		component := func(selector string) uint8 {
			value := objc.Send[float64](color, objc.RegisterName(selector))
			return uint8(math.Round(max(0, min(1, value)) * 255))
		}
		*entry.color = ui.Color{R: component("redComponent"), G: component("greenComponent"), B: component("blueComponent"), A: component("alphaComponent")}
	}
	return colors
}
