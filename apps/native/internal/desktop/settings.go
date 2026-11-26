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
		settingsPanel(c, func() {
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
		ui.Row(c).Gap(14).Children(func() {
			for _, mode := range []string{"auto", "light", "dark"} {
				b := settingsPreview(c.Key(mode), v.msg(mode), preferences.ThemeMode == mode, func() ui.Element { return v.themePreview(c, mode) })
				if b.Clicked() {
					v.Client.SetThemeMode(mode)
				}
			}
		})
	})
	v.row(c, "glassStyle", "glassStyleHint", func() {
		ui.Row(c).Gap(14).Children(func() {
			for _, style := range []string{"transparent", "tinted"} {
				b := settingsPreview(c.Key(style), v.msg(style), preferences.LiquidGlassStyle == style, func() ui.Element { return v.glassPreview(c, style) })
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
		if ui.Select(c, &selected, labels).Width(160).MinHeight(40).Label(v.msg("locale")).Changed() {
			locale := "zh-CN"
			if selected == labels[1] {
				locale = "en-US"
			}
			v.Client.SetLocale(locale)
		}
	})
}

// The preview and its visible caption share one keyboard and click target.
func settingsPreview(c *ui.Context, label string, selected bool, preview func() ui.Element) ui.Element {
	button := ui.ButtonBase(c).Column().Width(100).MinHeight(78).Padding(0).Gap(6).AlignItems(ui.Center).Radius(7).Label(label).Tooltip(label).Role(ui.RoleToggleButton).Checked(selected).Selected(selected).Background(ui.Transparent)
	button.Children(func() {
		art := preview().Border(1, palette(c).Separator)
		if selected {
			art.Border(2, c.Theme().TextMuted)
		} else if button.Hovered() {
			art.Border(1, c.Theme().TextMuted)
		}
		ui.Text(c, label).FontSize(12).TextColor(c.Theme().Text).SingleLine()
	})
	return button
}

func (v *SettingsView) themePreview(c *ui.Context, mode string) ui.Element {
	return ui.Box(c).FillWidth().Height(56).Radius(7).Clip().Children(func() {
		draw := func(left, width float32, dark bool) {
			p := presentation.Colors(dark, c.Theme().Accent)
			ui.Box(c).Absolute().Left(left).Top(0).Width(width).FillHeight().Background(p.Window).Children(func() {
				ui.Box(c).Absolute().Left(0).Top(0).Width(20).FillHeight().Background(p.Subpanel)
				ui.Box(c).Absolute().Left(5).Top(9).Size(10, 4).Radius(2).Background(p.Theme.TextMuted)
				ui.Box(c).Absolute().Left(5).Top(20).Size(10, 4).Radius(2).Background(p.Theme.TextMuted)
				ui.Box(c).Absolute().Left(25).Right(6).Top(9).Height(5).Radius(2).Background(p.Theme.TextMuted)
				ui.Box(c).Absolute().Left(25).Right(6).Top(21).Bottom(7).Radius(4).Background(p.Panel).Border(1, p.Separator)
			})
		}
		if mode == "auto" {
			draw(0, 50, false)
			draw(50, 50, true)
		} else {
			draw(0, 100, mode == "dark")
		}
	})
}

func (v *SettingsView) glassPreview(c *ui.Context, style string) ui.Element {
	p := palette(c)
	return ui.Box(c).FillWidth().Height(56).Radius(7).Clip().Background(p.Content).Children(func() {
		ui.Box(c).Absolute().Left(0).Top(0).Size(38, 56).Background(p.Subpanel)
		ui.Box(c).Absolute().Left(16).Top(12).Size(62, 8).Radius(4).Background(p.Theme.TextMuted)
		ui.Box(c).Absolute().Left(26).Top(31).Size(64, 12).Radius(4).Background(p.Theme.TextMuted)
		backing := p.ChromeBackground(style, true, c.Preferences())
		pane := ui.Box(c).Absolute().Fill().Radius(7)
		if backing.A == 255 {
			pane.Background(backing)
		} else {
			pane.Material(presentation.LiquidGlass(style, p.Window))
		}
		pane.Children(func() {
			if backing.A != 255 {
				ui.Box(c).Absolute().Fill().Radius(7).Background(backing)
			}
			ui.Box(c).Absolute().Left(8).Right(8).Top(8).Height(8).Radius(4).Background(p.Theme.TextMuted)
			ui.Box(c).Absolute().Left(8).Right(8).Bottom(8).Height(18).Radius(4).Background(p.Panel)
		})
	})
}
func (v *SettingsView) accent(c *ui.Context) {
	v.row(c, "accentColor", "", func() {
		ui.Row(c).FillWidth().Gap(4).Justify(ui.End).Children(func() {
			for _, color := range []string{"#8f96a3", "#2f6dff", "#a65dd9", "#f062a8", "#ff6a57", "#ffb020", "#f5dd00", "#33c96f"} {
				selected := strings.EqualFold(v.Client.Draft.AccentColor, color)
				label := v.msg("colorLabels." + color)
				b := ui.ButtonBase(c.Key(color)).Column().Width(38).MinHeight(70).Padding(0).Gap(4).AlignItems(ui.Center).Radius(7).Label(label).Tooltip(label).Role(ui.RoleToggleButton).Checked(selected).Selected(selected).Background(ui.Transparent)
				b.Children(func() {
					ui.Box(c).Size(38, 38).Center().Children(func() {
						ring := ui.Box(c).Size(24, 24).Radius(12).Center()
						if selected {
							ring.Border(1, c.Theme().TextMuted)
						}
						ring.Children(func() {
							ui.Box(c).Size(18, 18).Radius(9).Background(ui.Hex(color))
						})
					})
					ui.Text(c, label).FillWidth().MinHeight(28).FontSize(11).TextAlign(ui.Center).TextColor(c.Theme().Text)
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
		field := ui.NumberInput(c, &number, minValue, maxValue, step).Width(160).MinHeight(40).Justify(ui.End).Label(v.msg(key))
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
		if ui.Select(c, &selected, labels).Width(160).MinHeight(40).Label(v.msg("localAsrDevice")).Changed() {
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
