package desktop

import (
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
	"liberty.local/liberty/internal/desktop/presentation"
)

var (
	setWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	// Window construction and theme callbacks run on MyGo's main thread.
	chromeWindows = make(map[*mygo.Window]struct{})
)

// MyGo already sets the immersive-dark hint. An explicit caption/text color
// also keeps a framed, acrylic window aligned with Liberty's neutral palette,
// including when the system's title-bar accent setting differs from the app.
func applyWindowChrome(window *mygo.Window) {
	if window == nil || setWindowAttribute.Find() != nil {
		return
	}
	handle := window.NativeHandle()
	if handle == 0 {
		return
	}
	appearance := nativeTitlebarAppearance(mygo.Theme.IsDark(), mygo.Theme.Preferences().HighContrast)
	for _, attribute := range []struct{ id, value uint32 }{
		{20, appearance.dark},    // DWMWA_USE_IMMERSIVE_DARK_MODE
		{35, appearance.caption}, // DWMWA_CAPTION_COLOR (Windows 11+)
		{36, appearance.text},    // DWMWA_TEXT_COLOR (Windows 11+)
	} {
		// Older Windows versions reject unsupported color attributes. Retain
		// MyGo's native frame and immersive-dark fallback on those systems.
		setWindowAttribute.Call(handle, uintptr(attribute.id), uintptr(unsafe.Pointer(&attribute.value)), 4)
	}
}

func syncWindowChrome() {
	for window := range chromeWindows {
		applyWindowChrome(window)
	}
}

func configureWindowChrome(window *mygo.Window, options mygo.WindowOptions) {
	// Also supplies icons to development/preview builds without PE resources.
	_ = window.SetIcon(presentation.ApplicationIconPNG)
	if !usesNativeCaption(options) {
		return
	}
	chromeWindows[window] = struct{}{}
	applyWindowChrome(window)
	stop := mygo.Theme.OnUpdated(func() { applyWindowChrome(window) })
	window.OnClosed(func() {
		stop()
		delete(chromeWindows, window)
	})
}
