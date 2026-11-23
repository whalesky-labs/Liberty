package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
)

type SettingsView struct {
	Client            *client.SettingsController
	Runtime           *RuntimeView
	Remote            *client.RemoteController
	Diagnostics       *client.DiagnosticsController
	DiagnosticFiles   client.DiagnosticFiles
	ConfirmTokenClear func()
	Locale            string
	ActiveSource      func() string
	Back              func()
	section, search   string
}

func (v *SettingsView) msg(key string) string { return presentation.Message(v.Locale, "settings."+key) }
func (v *SettingsView) Sidebar(c *ui.Context) {
	if v.section == "" {
		v.section = "appearance"
	}
	settingsSidebar(c, v.Locale, v.section, &v.search, v.Back, func(section string) bool {
		return v.Client != nil && (section != "remoteCompatibility" || v.Remote != nil) && (section != "diagnostics" || v.Diagnostics != nil) && (section != "localRuntime" || v.Runtime != nil && v.Runtime.Client != nil)
	}, func(section string) {
		if v.Client == nil {
			return
		}
		if v.Client.Dirty() {
			v.Client.Save()
		} else {
			v.Client.Load()
		}
		v.section = section
	})
}
func (v *SettingsView) View(c *ui.Context) {
	if v.section == "" {
		v.section = "appearance"
	}
	if v.section == "localRuntime" {
		if v.Runtime == nil || v.Runtime.Client == nil {
			v.unavailable(c)
			return
		}
		v.Runtime.View(c)
		return
	}
	if v.Client == nil || v.section == "remoteCompatibility" && v.Remote == nil || v.section == "diagnostics" && v.Diagnostics == nil {
		v.unavailable(c)
		return
	}
	settingsPage(c, v.Locale, v.section, func() {
		if v.Client.Error != nil {
			ui.Text(c, resourceError(v.Locale, v.Client.Error)).TextColor(c.Theme().Danger).Role(ui.RoleAlert).Selectable()
			if v.Client.CanRetry() && textButton(c, presentation.Message(v.Locale, "common.retry")).Disabled(v.Client.Busy).Clicked() {
				v.Client.Retry()
			}
		}
		if !v.Client.Loaded {
			if v.Client.Error != nil {
				return
			}
			ui.Text(c, localized(v.Locale, "正在读取…", "Loading…")).Role(ui.RoleStatus).Busy(v.Client.Busy)
			return
		}
		settingsPanel(c).Children(func() {
			switch v.section {
			case "appearance":
				v.appearance(c)
			case "themeSection":
				v.accent(c)
			case "processingDefaults":
				v.defaults(c)
			case "runtimeOverview":
				v.runtimeOverview(c)
			case "remoteCompatibility":
				v.remoteSettings(c)
			case "diagnostics":
				v.diagnostics(c)
			}
		})
	})
}

func (v *SettingsView) unavailable(c *ui.Context) {
	settingsPage(c, v.Locale, v.section, func() { v.unavailableContent(c) })
}

func (v *SettingsView) unavailableContent(c *ui.Context) {
	ui.Column(c).FillWidth().Padding(24).Gap(8).Children(func() {
		ui.Text(c, localized(v.Locale, "此设置分区暂不可用，请重新加载应用后重试。", "This settings section is unavailable. Reload the app and try again.")).FillWidth().TextColor(c.Theme().Danger).Role(ui.RoleAlert).Selectable()
		ui.Text(c, localized(v.Locale, "相关原生服务尚未连接，页面不会显示不可用的操作控件。", "The related native service is not connected, so unavailable controls are hidden.")).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
	})
}
func (v *SettingsView) row(c *ui.Context, key, hint string, control func()) {
	if hint != "" {
		hint = v.msg(hint)
	}
	settingsRow(c, v.msg(key), hint, control)
}
func (v *SettingsView) appearance(c *ui.Context) {
	preferences := v.Client.UIPreferences()
	mode := "light"
	if c.Theme().Dark {
		mode = "dark"
	}
	settingsRow(c, v.msg("themeMode"), v.msg("effectiveTheme")+": "+v.msg(mode), func() {
		ui.Row(c).FillWidth().Gap(10).Children(func() {
			for _, mode := range []string{"auto", "light", "dark"} {
				b := ui.ButtonBase(c.Key(mode)).Grow(1).Basis(0).MinWidth(0).MinHeight(88).Padding(8).Radius(8).Gap(8).Label(v.msg(mode)).Role(ui.RoleToggleButton).Checked(preferences.ThemeMode == mode).Selected(preferences.ThemeMode == mode).Background(palette(c).Subpanel).TextColor(c.Theme().Text)
				if preferences.ThemeMode == mode {
					b.Border(1, c.Theme().Accent).Background(palette(c).Subpanel.Mix(c.Theme().Accent, .08))
				} else {
					b.Border(1, palette(c).Separator)
				}
				b.Children(func() {
					ui.Column(c).Gap(8).FillWidth().Children(func() { v.themePreview(c, mode); ui.Text(c, v.msg(mode)).FontSize(12) })
				})
				if b.Clicked() {
					v.Client.SetThemeMode(mode)
				}
			}
		})
	})
	v.row(c, "glassStyle", "glassStyleHint", func() {
		ui.Row(c).FillWidth().Gap(10).Children(func() {
			for _, style := range []string{"transparent", "tinted"} {
				b := ui.ButtonBase(c.Key(style)).Grow(1).Basis(0).MinWidth(0).MinHeight(88).Padding(8).Radius(8).Label(v.msg(style)).Role(ui.RoleToggleButton).Checked(preferences.LiquidGlassStyle == style).Selected(preferences.LiquidGlassStyle == style).Background(palette(c).Subpanel).TextColor(c.Theme().Text)
				if preferences.LiquidGlassStyle == style {
					b.Border(1, c.Theme().Accent)
				} else {
					b.Border(1, palette(c).Separator)
				}
				b.Children(func() {
					ui.Column(c).FillWidth().Gap(8).Children(func() {
						v.glassPreview(c, style)
						ui.Text(c, v.msg(style)).FontSize(12)
					})
				})
				if b.Clicked() {
					v.Client.Draft.LiquidGlassStyle = style
					v.Client.Save()
				}
			}
		})
	})
	v.row(c, "locale", "", func() {
		labels := []string{v.msg("localeZh"), v.msg("localeEn")}
		selected := labels[0]
		if preferences.Locale == "en-US" {
			selected = labels[1]
		}
		if ui.Select(c, &selected, labels).Width(190).MinHeight(40).Label(v.msg("locale")).Changed() {
			locale := "zh-CN"
			if selected == labels[1] {
				locale = "en-US"
			}
			v.Client.SetLocale(locale)
		}
	})
}
func (v *SettingsView) themePreview(c *ui.Context, mode string) {
	background := ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: ui.Hex("#fcfdff")}, {Color: ui.Hex("#eef2f7"), At: 1}}}
	header, body := ui.RGBA(255, 255, 255, .84), ui.RGBA(242, 244, 247, .96)
	switch mode {
	case "auto":
		background = ui.LinearGradient{Angle: 135, Stops: []ui.GradientStop{{Color: ui.Hex("#f4f7fb")}, {Color: ui.Hex("#d8deeb"), At: .5}, {Color: ui.Hex("#171b24"), At: .5}, {Color: ui.Hex("#0d1117"), At: 1}}}
		header = ui.RGBA(255, 255, 255, .76)
	case "dark":
		background = ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: ui.Hex("#171c26")}, {Color: ui.Hex("#0d1117"), At: 1}}}
		header, body = ui.RGBA(220, 223, 230, .6), ui.RGBA(62, 64, 71, .94)
	}
	ui.Box(c).FillWidth().Height(46).Radius(5).Clip().LinearGradient(background).Children(func() {
		ui.Box(c).Absolute().Left(8).Right(8).Top(8).Height(8).Radius(8).Background(header)
		content := ui.Box(c).Absolute().Left(8).Right(8).Top(22).Bottom(8).Radius(4).Background(body)
		if mode == "auto" {
			content.LinearGradient(ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{{Color: ui.RGBA(255, 255, 255, .86)}, {Color: ui.RGBA(255, 255, 255, .86), At: .48}, {Color: ui.RGBA(39, 41, 48, .82), At: .48}, {Color: ui.RGBA(39, 41, 48, .82), At: 1}}})
		}
	})
}

func (v *SettingsView) glassPreview(c *ui.Context, style string) {
	accent := c.Theme().Accent
	start, end := ui.Hex("#eef2f6").Mix(accent, .16), ui.Hex("#dde4ec").Mix(accent, .10)
	radial, radius := ui.Hex("#ffffff").Mix(accent, .20), float32(.42)
	border, glint := ui.RGBA(206, 212, 220, .96), ui.RGBA(255, 255, 255, .92)
	header, body := ui.RGBA(255, 255, 255, .94), ui.RGBA(255, 255, 255, .42)
	if style == "tinted" {
		start, end = ui.Hex("#ffffff").Mix(accent, .12), ui.Hex("#f6f8fb").Mix(accent, .08)
		radial, radius = ui.Hex("#ffffff").Mix(accent, .24), .46
		border, glint = ui.RGBA(214, 218, 224, .82), ui.RGBA(255, 255, 255, .9)
		header, body = ui.RGBA(255, 255, 255, .96), ui.Hex("#f7f7f7")
	}
	if c.Theme().Dark {
		start, end = ui.RGBA(45, 45, 44, .82).Mix(accent, .10), ui.RGBA(51, 51, 50, .72).Mix(accent, .08)
		radial, radius = accent.Alpha(.28), .44
		border, glint = ui.RGBA(255, 255, 255, .08), ui.RGBA(255, 255, 255, .18)
		header, body = ui.RGBA(255, 255, 255, .36), ui.RGBA(255, 255, 255, .12)
		if style == "tinted" {
			start, end = ui.Hex("#2d2d2c").Mix(accent, .08), ui.Hex("#333332").Mix(accent, .06)
			radial, radius = accent.Alpha(.22), .42
			header, body = ui.RGBA(220, 223, 230, .32), ui.Hex("#3e4047")
		}
	}
	ui.Box(c).FillWidth().Height(46).Radius(5).Clip().Gradient(start, end, 140).InsetShadow(0, 0, 0, 1, border).InsetShadow(0, 1, 0, 0, glint).Children(func() {
		if !c.Theme().Dark && style == "transparent" {
			ui.Box(c).Absolute().Fill().Gradient(ui.RGBA(255, 255, 255, .66), ui.RGBA(255, 255, 255, .54), 140)
		}
		ui.Box(c).Absolute().Fill().RadialGradient(ui.RadialGradient{Center: ui.Point{X: .18, Y: .18}, Stops: []ui.GradientStop{{Color: radial}, {Color: radial.Alpha(0), At: radius}}})
		ui.Box(c).Absolute().Left(8).Right(8).Top(8).Height(8).Radius(8).Background(header)
		ui.Box(c).Absolute().Left(8).Right(8).Bottom(8).Height(18).Radius(4).Background(body)
	})
}
func (v *SettingsView) accent(c *ui.Context) {
	v.row(c, "accentColor", "", func() {
		ui.Row(c).FillWidth().Gap(8).Wrap().Justify(ui.End).Children(func() {
			for _, color := range []string{"#8f96a3", "#2f6dff", "#a65dd9", "#f062a8", "#ff6a57", "#ffb020", "#f5dd00", "#33c96f"} {
				selected := strings.EqualFold(v.Client.Draft.AccentColor, color)
				b := ui.ButtonBase(c.Key(color)).Size(38, 38).Padding(5).Center().Radius(19).Label(v.msg("colorLabels." + color)).Tooltip(v.msg("colorLabels." + color)).Role(ui.RoleToggleButton).Checked(selected).Selected(selected)
				b.Children(func() {
					dot := ui.Box(c).Size(28, 28).Radius(14).Background(ui.Hex(color)).Border(2, palette(c).Separator)
					if selected {
						dot.Border(2, c.Theme().Accent).Shadow(0, 0, 0, 2, palette(c).Panel).Shadow(0, 0, 0, 4, c.Theme().Accent)
					}
				})
				if b.Clicked() {
					v.Client.Draft.AccentColor = color
					v.Client.Save()
				}
			}
		})
	})
}
func (v *SettingsView) text(c *ui.Context, key, hint string, value *string, multiline bool) {
	v.row(c, key, hint, func() {
		var field ui.Element
		if multiline {
			field = ui.TextArea(c, value).FillWidth().Height(112)
		} else {
			field = ui.TextInput(c, value).FillWidth().MinHeight(40)
		}
		field.Label(v.msg(key))
		if field.FocusLeft() {
			v.Client.Save()
		}
	})
}
func (v *SettingsView) number(c *ui.Context, key, hint string, value *uint32, minValue, maxValue, step float64) {
	v.row(c, key, hint, func() {
		number := float64(*value)
		field := ui.NumberInput(c, &number, minValue, maxValue, step).Width(190).MinHeight(40).Label(v.msg(key))
		if field.Changed() {
			*value = uint32(number)
		}
		if field.FocusLeft() {
			v.Client.Save()
		}
	})
}
func (v *SettingsView) defaults(c *ui.Context) {
	d := &v.Client.Draft
	v.text(c, "defaultHotwords", "defaultHotwordsHint", &d.DefaultHotwords, true)
	v.text(c, "defaultSummaryTemplate", "defaultSummaryTemplateHint", &d.SummaryTemplate, false)
	v.number(c, "concurrency", "concurrencyHint", &d.Concurrency, 1, 8, 1)
	v.row(c, "localAsrDevice", "localAsrDeviceHint", func() {
		ids := []string{"auto", "cpu", "mps", "cuda"}
		labels := []string{v.msg("localAsrDeviceAuto"), v.msg("localAsrDeviceCpu"), v.msg("localAsrDeviceMps"), v.msg("localAsrDeviceCuda")}
		selected := labels[0]
		for i, id := range ids {
			if id == d.Device {
				selected = labels[i]
			}
		}
		if ui.Select(c, &selected, labels).Width(190).MinHeight(40).Label(v.msg("localAsrDevice")).Changed() {
			for i, label := range labels {
				if label == selected {
					d.Device = ids[i]
				}
			}
			v.Client.Save()
		}
	})
	v.number(c, "localAsrThreads", "localAsrThreadsHint", &d.Threads, 0, 32, 1)
	v.number(c, "localAsrBatchSizeSeconds", "localAsrBatchSizeSecondsHint", &d.BatchSeconds, 30, 1200, 30)
}
