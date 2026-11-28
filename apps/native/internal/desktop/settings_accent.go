package desktop

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

var accentColorPresets = []string{"#8f96a3", "#2f6dff", "#33c96f", "#f5dd00", "#f062a8", "#ff6a57", "#ffb020", "#a65dd9", "#ffffff"}

type accentPickerState struct {
	open  bool
	value string
}

func parseAccentColor(value string) (ui.Color, bool) {
	if len(value) == 7 && value[0] == '#' {
		if _, err := strconv.ParseUint(value[1:], 16, 24); err == nil {
			return ui.Hex(value), true
		}
	}
	return ui.Color{}, false
}

func accentColorDot(c *ui.Context, color ui.Color) {
	ui.Box(c).Size(12, 12).Shrink(0).Radius(6).Background(color).Border(1, c.Theme().Text.Alpha(0.14)).Role(ui.RoleNone)
}

func (v *SettingsView) accent(c *ui.Context) {
	v.row(c, "accentColor", "accentColorHint", func() {
		v.accentSelector(c)
	})
}

func (v *SettingsView) accentSelector(c *ui.Context) {
	const custom = "custom"
	current := strings.ToLower(v.Client.Draft.AccentColor)
	selected := current
	if !slices.Contains(accentColorPresets, current) {
		selected = custom
	}
	label := func(value string) string {
		if value == custom {
			return v.msg("customAccentColor")
		}
		return v.msg("colorLabels." + value)
	}
	color := c.Theme().Accent
	if parsed, ok := parseAccentColor(current); ok {
		color = parsed
	}
	menuOpen := ui.Local(c.Root(), "accent-color-menu", func() bool { return false })
	// The picker owns only an uncommitted widget value. Durable preferences and
	// asynchronous saves continue through the existing settings controller.
	editor := ui.Local(c.Root(), "custom-accent-color", func() accentPickerState { return accentPickerState{} })
	parts := ui.MenuBase(c.Key("accent-color"), menuOpen)
	width := float32(100)
	if v.Locale == "en-US" {
		width = 160
	}
	trigger := styleSelectTrigger(c, parts.Trigger).MinWidth(width).Width(width).Radius(16).
		Label(v.msg("accentColor")).Description(v.msg("accentColorHint")).Value(label(selected))
	trigger.Children(func() {
		accentColorDot(c, color)
		ui.Text(c, label(selected)).Grow(1).SingleLine()
		ui.Icon(c, presentation.Icon("chevronDown")).Size(10, 10).Shrink(0).TextColor(c.Theme().TextMuted)
	})
	parts.Popup(func(panel ui.Element) {
		styleSelectPopup(c, panel)
		panel.Width(220).Radius(18).Background(c.Theme().Surface).
			AttachTo(trigger, ui.AnchorBottomEnd, ui.AnchorTopEnd).
			MaxHeight(selectPopupHeight(c, trigger) + c.Theme().Space(2))
		for _, value := range append(slices.Clone(accentColorPresets), custom) {
			chosen := value == selected
			item := styleSelectItem(c, parts.RadioItem(chosen), chosen).Label(label(value)).MinHeight(30)
			item.Children(func() {
				if value != custom {
					accentColorDot(c, ui.Hex(value))
				}
				ui.Text(c, label(value)).Grow(1).SingleLine()
				ui.Box(c).Size(14, 14).Shrink(0).Children(func() {
					if chosen {
						ui.Icon(c, presentation.Icon("checkmark")).Size(14, 14).TextColor(c.Theme().Text)
					}
				})
			})
			if item.Clicked() {
				if value == custom {
					editor.open, editor.value = true, current
				} else {
					v.Client.Draft.AccentColor = value
					v.Client.Save()
				}
			}
		}
	})
	wasOpen := editor.open
	ui.Modal(c.Key("custom-accent-dialog"), &editor.open, func() {
		ui.Column(c).Width(280).Gap(16).Children(func() {
			ui.Text(c, v.msg("customAccentColor")).FontSize(20).Role(ui.RoleHeading).Level(2)
			ui.Text(c, v.msg("customAccentColorHint")).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
			preview, valid := parseAccentColor(editor.value)
			ui.Text(c, v.msg("customAccentColorValue")).FontSize(13)
			var input ui.Element
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				if !valid {
					preview = color
				}
				ui.Box(c).Size(32, 32).Shrink(0).Radius(16).Background(preview).Border(1, c.Theme().Border).Role(ui.RoleNone)
				input = ui.TextInput(c, &editor.value).Grow(1).MinWidth(0).MaxLength(7).Placeholder("#2F6DFF").
					Label(v.msg("customAccentColorValue")).Description(v.msg("customAccentColorHint")).AutoFocus().Invalid(!valid)
			})
			if !valid {
				ui.Text(c, v.msg("customAccentColorInvalid")).FillWidth().FontSize(12).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
			}
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, presentation.Message(v.Locale, "common.cancel")).Clicked() {
					editor.open = false
				}
				save := ui.PrimaryButton(c, presentation.Message(v.Locale, "common.save")).Disabled(!valid)
				if (save.Clicked() || input.Submitted()) && valid {
					v.Client.Draft.AccentColor = strings.ToLower(editor.value)
					v.Client.Save()
					editor.open = false
				}
			})
		})
	}).Label(v.msg("customAccentColor"))
	if wasOpen && !editor.open {
		trigger.Focus()
	}
}
