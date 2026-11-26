package desktop

import (
	"fmt"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func resultWindowTitle(locale string) string {
	return localized(locale, "Liberty · 结果工作台", "Liberty · Result workbench")
}

// ApplicationMenu uses MyGo's native close role so keyboard closure follows
// the same unsaved-editor and settings guards as the window close button.
func ApplicationMenu(locale string) *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Role: mygo.RoleFileMenu, Label: localized(locale, "文件", "File"), Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleClose, Label: presentation.Message(locale, "common.closeWindow")},
		}},
		{Role: mygo.RoleEditMenu, Label: localized(locale, "编辑", "Edit")},
		{Role: mygo.RoleWindowMenu, Label: localized(locale, "窗口", "Window")},
	})
}

func mainWindowOptions(content func(*ui.Context)) mygo.WindowOptions {
	return mainWindowOptionsForPlatform(runtime.GOOS, content)
}

func mainWindowOptionsForPlatform(platform string, content func(*ui.Context)) mygo.WindowOptions {
	options := mygo.WindowOptions{
		Title: mygo.App.Name(), Width: presentation.WindowWidth, Height: presentation.WindowHeight,
		MinWidth: presentation.WindowMinWidth, MinHeight: presentation.WindowMinHeight,
		TitleBarStyle: mygo.TitleBarDefault,
		Content:       ui.View(content),
	}
	switch platform {
	case "darwin":
		options.Vibrancy = mygo.VibrancyGlass
		options.TitleBarStyle = mygo.TitleBarHiddenInset
		options.TrafficLightPosition = &mygo.Point{X: presentation.TrafficLightX, Y: presentation.TrafficLightY}
	case "windows":
		// Windows can change a material later only if created with one.
		// MyGo uses Acrylic on Windows 11 and reports no material on older OSes.
		options.Vibrancy = mygo.VibrancyGlass
		options.TitleBarStyle = mygo.TitleBarHidden
		options.TitleBarHeight = presentation.TitlebarHeight
	}
	return options
}

func windowGlass(preferences domain.UIPreferences, systemDark bool) (mygo.Vibrancy, string) {
	colors := presentation.PreferenceColors(systemDark, preferences)
	material := presentation.LiquidGlass(preferences.LiquidGlassStyle, colors.Window)
	if material.Tint.A == 0 {
		return mygo.VibrancyGlass, ""
	}
	tint := material.Tint
	return mygo.VibrancyGlass, fmt.Sprintf("rgba(%d, %d, %d, %.4f)", tint.R, tint.G, tint.B, float64(tint.A)/255)
}

func applyWindowGlass(window *mygo.Window, preferences domain.UIPreferences) {
	if window == nil {
		return
	}
	material, tint := windowGlass(preferences, mygo.Theme.IsDark())
	window.SetVibrancy(material)
	// The system material supplies the frost; the optional tint is a neutral
	// semantic window color, independent of the user's accent color.
	_ = window.SetVibrancyTint(tint)
}
