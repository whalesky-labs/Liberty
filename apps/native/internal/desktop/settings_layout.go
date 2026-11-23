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
		ui.Column(context).FillWidth().Padding(16, 14, 10).Gap(10).Children(func() {
			var header ui.Element
			if compact {
				header = ui.Row(context).FillWidth().Gap(12).AlignItems(ui.Center)
			} else {
				header = ui.Column(context).FillWidth().Gap(10)
			}
			header.Children(func() {
				if textButton(context, "‹ "+message("backToApp")).MinHeight(34).Padding(5, 8).Justify(ui.Start).TextColor(context.Theme().TextMuted).Disabled(back == nil).Clicked() {
					back()
				}
				ui.Text(context, message("pageTitle")).Padding(2, 8, 4).FontSize(18).Bold().Role(ui.RoleHeading).Level(1)
			})
			ui.TextInput(context, search).FillWidth().MinHeight(38).Placeholder(message("searchPlaceholder")).Label(message("searchPlaceholder"))
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
				groups = ui.Column(context).FillWidth().Padding(8, 10, 14).Gap(18)
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
						groupElement = ui.Column(context.Key(group.key)).FillWidth().Gap(3)
					}
					groupElement.Children(func() {
						if !compact {
							ui.Text(context, message(group.key)).Padding(7, 9, 5).FontSize(11).FontWeight(650).TextColor(context.Theme().TextMuted)
						}
						for _, item := range items {
							active := item.key == section
							button := ui.ButtonBase(context.Key(item.key)).MinHeight(38).Padding(7, 10).Gap(10).Radius(7).Justify(ui.Start).Label(message(item.key)).Description(message(settingsHintKey(item.key))).Selected(active).Disabled(selectSection == nil || available != nil && !available(item.key))
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
	padding := min(float32(48), max(float32(16), width*.03))
	gap, titleSize := float32(22), float32(25)
	titlebarHeight, _, _ := titlebarLayout(context)
	pageHeight := height - titlebarHeight - presentation.StatusbarHeight - 26
	if width <= 760 {
		padding, gap, titleSize = 14, 18, 22
		pageHeight -= 157
	}
	ui.Scroll(context.Key(section)).Fill().Children(func() {
		ui.Column(context).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Column(context).FillWidth().MaxWidth(1320).MinHeight(max(0, pageHeight)).Padding(8, padding, 6).Gap(gap).Children(func() {
				ui.Column(context).FillWidth().Padding(8, 4, 17).Gap(5).BorderWidth(0, 0, 1, 0).BorderColor(palette(context).Separator).Children(func() {
					ui.Text(context, message("pageTitle")).FontSize(11).FontWeight(650).TextColor(context.Theme().TextMuted)
					ui.Text(context, message(section)).FillWidth().FontSize(titleSize).FontWeight(720).Role(ui.RoleHeading).Level(2)
					ui.Text(context, message(settingsHintKey(section))).FillWidth().MaxWidth(680).FontSize(13).TextColor(context.Theme().TextMuted)
				})
				ui.Column(context).FillWidth().Grow(1).Gap(16).Children(content)
				ui.Column(context).FillWidth().BorderWidth(1, 0, 0, 0).BorderColor(palette(context).Separator).Padding(18, 4, 6).Gap(5).Children(func() {
					ui.Text(context, message("copyright")).FillWidth().FontSize(11).TextColor(context.Theme().TextMuted)
					ui.Text(context, message("authorGithub")+" github.com/westng/Liberty").FillWidth().FontSize(11).TextColor(context.Theme().TextMuted)
				})
			})
		})
	})
}

func settingsPanel(context *ui.Context) ui.Element {
	return ui.Column(context).FillWidth().Radius(10).Border(1, palette(context).Separator).Background(palette(context).Panel)
}

func settingsRow(context *ui.Context, label, hint string, control func()) {
	width, _ := context.Size()
	vertical, horizontal, gap := float32(18), float32(24), float32(36)
	stacked := width <= 1180
	if stacked {
		vertical, horizontal, gap = 17, 20, 6
	}
	if width <= 760 {
		vertical, horizontal = 15, 16
	}
	var row ui.Element
	if stacked {
		row = ui.Column(context.Key(label)).FillWidth().MinHeight(82).Padding(vertical, horizontal).Gap(gap)
	} else {
		row = ui.Row(context.Key(label)).FillWidth().MinHeight(82).Padding(vertical, horizontal).Gap(gap).AlignItems(ui.Center)
	}
	row.Children(func() {
		metadata := ui.Column(context).Gap(5)
		if stacked {
			metadata.FillWidth()
		} else {
			metadata.Grow(1).Basis(0).MinWidth(240)
		}
		metadata.Children(func() {
			ui.Text(context, label).FillWidth().FontSize(14).FontWeight(650)
			if hint != "" {
				ui.Text(context, hint).FillWidth().MaxWidth(620).FontSize(12).TextColor(context.Theme().TextMuted)
			}
		})
		controls := ui.Column(context).Gap(8).MinWidth(0)
		if stacked {
			controls.FillWidth().MaxWidth(560).AlignSelf(ui.End).AlignItems(ui.End)
		} else {
			controls.Grow(.9).Basis(0).MinWidth(320).MaxWidth(460).AlignItems(ui.End)
		}
		controls.Children(control)
	})
	ui.Row(context).FillWidth().Padding(0, horizontal).Children(func() { separator(context) })
}
