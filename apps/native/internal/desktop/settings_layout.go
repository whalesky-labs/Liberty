package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

type settingsNavigationItem struct{ key, icon string }
type settingsNavigationGroup struct {
	key   string
	items []settingsNavigationItem
}

var settingsNavigation = []settingsNavigationGroup{
	{"personalizationGroup", []settingsNavigationItem{{"appearance", "appearance"}, {"themeSection", "accent"}}},
	{"processingGroup", []settingsNavigationItem{{"runtimeOverview", "mode"}, {"processingDefaults", "processing"}, {"localRuntime", "chip"}}},
	{"systemGroup", []settingsNavigationItem{{"remoteCompatibility", "remote"}, {"diagnostics", "diagnostics"}}},
}

func settingsHintKey(section string) string {
	return section + "Hint"
}

func settingsSidebar(context *ui.Context, locale, section string, search *string, back func(), available func(string) bool, selectSection func(string)) {
	message := func(key string) string { return presentation.Message(locale, "settings."+key) }
	width, _ := context.Size()
	compact := width <= 760
	ui.Column(context).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Column(context).FillWidth().Padding(12, 8, 6).Gap(14).Children(func() {
			button := ui.ButtonBase(context).FillWidth().MinHeight(32).Padding(4, 6).Gap(5).Radius(7).Justify(ui.Start).Label(message("backToApp")).Disabled(back == nil)
			color := context.Theme().TextMuted
			if back != nil && button.Hovered() {
				button.Background(context.Theme().Accent.Alpha(0.09))
				color = context.Theme().Text
			}
			button.Children(func() {
				ui.Icon(context, presentation.Icon("back")).Size(16, 16).TextColor(color)
				ui.Text(context, message("backToApp")).FontSize(13).TextColor(color)
			})
			if button.Clicked() {
				back()
			}
			ui.Box(context).FillWidth().Height(34).Children(func() {
				ui.TextInput(context, search).Fill().Padding(6, 10, 6, 34).FontSize(13).Radius(17).Placeholder(message("searchPlaceholder")).Label(message("searchPlaceholder"))
				ui.Icon(context, presentation.Icon("search")).Absolute().Left(11).Top(10).Size(14, 14).TextColor(context.Theme().TextMuted)
			})
		})
		var navigation ui.Element
		if compact {
			navigation = ui.ScrollHorizontal(context).FillWidth().Height(48)
		} else {
			navigation = ui.Scroll(context).FillWidth().Grow(1).MinHeight(0)
		}
		navigation.Role(ui.RoleNavigation).Label(message("navigationLabel")).Children(func() {
			var groups ui.Element
			if compact {
				groups = ui.Row(context).Padding(0, 10, 9).Gap(4)
			} else {
				groups = ui.Column(context).FillWidth().Padding(8).Gap(14)
			}
			groups.Children(func() {
				query := strings.ToLower(strings.TrimSpace(*search))
				matches := 0
				for _, group := range settingsNavigation {
					var items []settingsNavigationItem
					for _, item := range group.items {
						if strings.Contains(strings.ToLower(message(item.key)+" "+message(settingsHintKey(item.key))), query) {
							items = append(items, item)
						}
					}
					if len(items) == 0 {
						continue
					}
					matches += len(items)
					var groupElement ui.Element
					if compact {
						groupElement = ui.Row(context.Key(group.key)).Gap(4)
					} else {
						groupElement = ui.Column(context.Key(group.key)).FillWidth().Gap(2)
					}
					groupElement.Children(func() {
						if !compact {
							ui.Text(context, message(group.key)).Padding(6, 8, 3).FontSize(13).FontWeight(650).TextColor(context.Theme().TextMuted)
						}
						for _, item := range items {
							active := item.key == section
							button := ui.ButtonBase(context.Key(item.key)).MinHeight(34).Padding(5, 8).Gap(8).Radius(7).Justify(ui.Start).Label(message(item.key)).Description(message(settingsHintKey(item.key))).Selected(active).Disabled(selectSection == nil || available != nil && !available(item.key))
							if !compact {
								button.FillWidth()
							}
							if active {
								button.Current(ui.CurrentPage)
							}
							navigationContent(context, button, item.icon, message(item.key), active)
							if button.Clicked() {
								selectSection(item.key)
							}
						}
					})
				}
				if matches == 0 {
					ui.Text(context.Key("settings-search-empty"), message("searchEmpty")).FillWidth().Padding(12, 9).FontSize(12).TextColor(context.Theme().TextMuted).Role(ui.RoleStatus)
				}
			})
		})
	})
}

func settingsPage(context *ui.Context, locale, section string, content func()) {
	message := func(key string) string { return presentation.Message(locale, "settings."+key) }
	width, height := context.Size()
	padding := min(float32(24), width*.015)
	titlebarHeight, _, _ := titlebarLayout(context)
	pageHeight := height - titlebarHeight - presentation.StatusbarHeight - 26
	if width <= 760 {
		padding = 8
		pageHeight -= 146 + presentation.ActivityBarWidth
	}
	ui.Scroll(context.Key(section)).Fill().Children(func() {
		ui.Column(context).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(context).FillWidth().MaxWidth(1168).MinHeight(max(0, pageHeight)).Padding(0, padding, 6).Gap(18).Children(func() {
				ui.Text(context, message(section)).FillWidth().Padding(5, 14, 0).FontSize(24).FontWeight(720).Role(ui.RoleHeading).Level(1).Description(message(settingsHintKey(section)))
				ui.Column(context).FillWidth().Grow(1).Gap(18).Children(content)
				ui.Column(context).FillWidth().Padding(8, 14, 4).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Text(context, message("copyright")).FontSize(11).TextColor(context.Theme().TextMuted)
					if textButton(context, message("authorGithub")).Role(ui.RoleLink).Padding(0).TextColor(context.Theme().TextMuted).Clicked() {
						context.OpenURL("https://github.com/westng/Liberty")
					}
				})
			})
		})
	})
}

func settingsPanel(context *ui.Context, content func()) {
	ui.Column(context).FillWidth().Padding(0, 14).Radius(10).Border(1, palette(context).Separator).Background(palette(context).Panel).Children(func() {
		ui.Column(context).FillWidth().Gap(1).Dividers(1, palette(context).Separator).Children(content)
	})
}

func settingsRow(context *ui.Context, label, hint string, control func()) {
	settingsFormRow(context, label, hint, 336, control)
}

func settingsFormRow(context *ui.Context, label, hint string, controlWidth float32, control func()) {
	width, _ := context.Size()
	vertical, gap := float32(10), float32(24)
	if width <= 980 {
		gap = 10
	}
	ui.Row(context.Key(label)).FillWidth().MinHeight(60).Padding(vertical, 0).Gap(gap).AlignItems(ui.Center).Children(func() {
		ui.Column(context).Grow(1).Basis(0).MinWidth(140).Gap(5).Children(func() {
			ui.Text(context, label).FillWidth().FontSize(14).FontWeight(650)
			if hint != "" {
				ui.Text(context, hint).FillWidth().MaxWidth(620).FontSize(12).TextColor(context.Theme().TextMuted)
			}
		})
		ui.Row(context).Grow(1).Basis(0).MinWidth(controlWidth).MaxWidth(max(460, controlWidth)).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(control)
	})
}
