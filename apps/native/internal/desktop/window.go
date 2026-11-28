package desktop

import (
	"fmt"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

var nativeMenuIcons = loadNativeMenuIcons()

func resultWindowTitle(locale string) string {
	return localized(locale, "Liberty · 结果工作台", "Liberty · Result workbench")
}

// ApplicationMenuActions connect app-menu navigation and external links to handlers.
// NewTaskEnabled follows the active processing mode so the menu does not offer
// local-only upload while the workspace is using a remote service.
type ApplicationMenuActions struct {
	NewTaskEnabled bool
	NewTask        func()
	OpenQueue      func()
	OpenSettings   func()
	CheckUpdates   func()
	OpenURL        func(string)
}

// ApplicationMenu uses native edit/window roles and MyGo's native close role
// so standard shortcuts and existing close guards remain in effect.
func ApplicationMenu(locale string, actionSets ...ApplicationMenuActions) *mygo.Menu {
	actions := ApplicationMenuActions{}
	if len(actionSets) > 0 {
		actions = actionSets[0]
	}
	fileItems := make([]*mygo.MenuItem, 0, 5)
	if len(actionSets) > 0 {
		fileItems = append(fileItems,
			&mygo.MenuItem{
				Label: presentation.Message(locale, "nav.newJob"), Accelerator: "CmdOrCtrl+N",
				Icon: menuIcon("plus"), Disabled: !actions.NewTaskEnabled || actions.NewTask == nil, Click: menuAction(actions.NewTask),
			},
			&mygo.MenuItem{Label: presentation.Message(locale, "nav.jobs"), Icon: menuIcon("tray"), Disabled: actions.OpenQueue == nil, Click: menuAction(actions.OpenQueue)},
		)
		if runtime.GOOS != "darwin" {
			fileItems = append(fileItems, applicationSettingsMenuItem(locale, actions.OpenSettings), mygo.Separator())
		} else {
			fileItems = append(fileItems, mygo.Separator())
		}
	}
	fileItems = append(fileItems, &mygo.MenuItem{Role: mygo.RoleClose, Label: presentation.Message(locale, "common.closeWindow"), Icon: menuIcon("close")})
	helpRole := applicationHelpMenuRole(runtime.GOOS)
	applicationName := mygo.App.Name()
	applicationItems := []*mygo.MenuItem{
		{Role: mygo.RoleAbout, Label: presentation.Format(locale, "menu.about", map[string]any{"app": applicationName}), Icon: menuIcon("about")},
		applicationSettingsMenuItem(locale, actions.OpenSettings),
		{Label: presentation.Message(locale, "menu.checkUpdates"), Icon: menuIcon("refresh"), Disabled: actions.CheckUpdates == nil, Click: menuAction(actions.CheckUpdates)},
		mygo.Separator(),
		{Role: mygo.RoleServices, Label: presentation.Message(locale, "menu.services"), Icon: menuIcon("services"), Submenu: []*mygo.MenuItem{}},
		mygo.Separator(),
		{Role: mygo.RoleHide, Label: presentation.Format(locale, "menu.hide", map[string]any{"app": applicationName}), Icon: menuIcon("hideApp")},
		{Role: mygo.RoleHideOthers, Label: presentation.Message(locale, "menu.hideOthers"), Icon: menuIcon("hideOthers")},
		{Role: mygo.RoleUnhide, Label: presentation.Message(locale, "menu.showAll"), Icon: menuIcon("showAll")},
		mygo.Separator(),
		{Role: mygo.RoleQuit, Label: presentation.Format(locale, "menu.quit", map[string]any{"app": applicationName}), Icon: menuIcon("quit")},
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: applicationItems},
		{Role: mygo.RoleFileMenu, Label: localized(locale, "文件", "File"), Submenu: fileItems},
		{Role: mygo.RoleEditMenu, Label: localized(locale, "编辑", "Edit")},
		{Role: mygo.RoleWindowMenu, Label: localized(locale, "窗口", "Window")},
		{Role: helpRole, Label: presentation.Message(locale, "menu.help"), Submenu: []*mygo.MenuItem{
			{Label: presentation.Message(locale, "menu.documentation"), Icon: menuIcon("documentation"), Click: menuURLAction(actions.OpenURL, presentation.DocumentationURL)},
			{Label: presentation.Message(locale, "menu.reportIssue"), Icon: menuIcon("reportIssue"), Click: menuURLAction(actions.OpenURL, presentation.IssuesURL)},
			mygo.Separator(),
			{Label: presentation.Message(locale, "menu.checkUpdates"), Icon: menuIcon("refresh"), Disabled: actions.CheckUpdates == nil, Click: menuAction(actions.CheckUpdates)},
		}},
	})
}

func applicationSettingsMenuItem(locale string, action func()) *mygo.MenuItem {
	return &mygo.MenuItem{
		Label: presentation.Message(locale, "menu.settings"), Accelerator: "CmdOrCtrl+,",
		Icon: menuIcon("gear"), Disabled: action == nil, Click: menuAction(action),
	}
}

func loadNativeMenuIcons() map[string]*mygo.MenuIcon {
	icons := make(map[string]*mygo.MenuIcon, 13)
	for _, name := range []string{"about", "gear", "refresh", "services", "hideApp", "hideOthers", "showAll", "quit", "plus", "tray", "close", "documentation", "reportIssue"} {
		svg := presentation.Icon(name)
		icon, err := mygo.NewMenuIconSVG(svg, true)
		if err != nil {
			panic(fmt.Sprintf("create native menu icon %q: %v", name, err))
		}
		icons[name] = icon
	}
	return icons
}

func menuIcon(name string) *mygo.MenuIcon {
	icon := nativeMenuIcons[name]
	if icon == nil {
		panic("missing native menu icon: " + name)
	}
	return icon
}

func menuAction(action func()) func(*mygo.MenuItem, *mygo.Window) {
	if action == nil {
		return nil
	}
	return func(*mygo.MenuItem, *mygo.Window) { action() }
}

func menuURLAction(openURL func(string), target string) func(*mygo.MenuItem, *mygo.Window) {
	return func(*mygo.MenuItem, *mygo.Window) {
		if openURL != nil {
			openURL(target)
			return
		}
		_ = mygo.Shell.OpenExternal(target)
	}
}

func applicationHelpMenuRole(platform string) mygo.MenuRole {
	if platform == "darwin" {
		return mygo.RoleHelp
	}
	return ""
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
