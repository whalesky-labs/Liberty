package desktop

import (
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

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
		options.TitleBarStyle = mygo.TitleBarHiddenInset
		options.TrafficLightPosition = &mygo.Point{X: presentation.TrafficLightX, Y: presentation.TrafficLightY}
	case "windows":
		options.TitleBarStyle = mygo.TitleBarHidden
		options.TitleBarHeight = presentation.TitlebarHeight
	}
	return options
}
