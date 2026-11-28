package desktop

import (
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

// SelectBase keeps MyGo's selection, keyboard, focus and accessibility behavior.
// Use the shared neutral navigation colors for its look: an accent must not
// become the popup's highlight, text color or focus ring.
func selectControl(c *ui.Context, selected *string, options []string) ui.Element {
	t := c.Theme()
	selectParts := ui.SelectBase(c, selected)
	trigger := styleSelectTrigger(c, selectParts.Trigger)
	trigger.Children(func() {
		ui.Text(c, *selected).SingleLine()
		ui.Icon(c, presentation.Icon("chevronDown")).Size(t.Space(2.5), t.Space(2.5)).Shrink(0).TextColor(t.TextMuted)
	})
	selectParts.Popup(func(panel ui.Element) {
		styleSelectPopup(c, panel)
		ui.Scroll(c).MaxHeight(selectPopupHeight(c, trigger)).Children(func() {
			for _, option := range options {
				item := styleSelectItem(c, selectParts.Item(option), option == *selected).Label(option)
				item.Children(func() {
					ui.Box(c).Size(t.Space(3), t.Space(3)).Shrink(0).Children(func() {
						if option == *selected {
							ui.Icon(c, presentation.Icon("checkmark")).Size(t.Space(3), t.Space(3)).TextColor(t.Text)
						}
					})
					ui.Text(c, option).SingleLine()
				})
			}
		})
	})
	return trigger
}

func styleSelectTrigger(c *ui.Context, trigger ui.Element) ui.Element {
	t := c.Theme()
	trigger.Padding(t.Space(1.5), t.Space(3.5)).Gap(t.Space(1.5)).Radius(t.Radius).
		Border(1, t.Border).Background(t.Surface).TextColor(t.Text).FocusRingColor(t.TextMuted).
		Justify(ui.SpaceBetween).MinWidth(t.Space(35))
	if trigger.Pressed() {
		trigger.Background(t.SurfacePressed)
	} else if trigger.Hovered() {
		trigger.Background(t.SurfaceHover)
	}
	return trigger
}

func styleSelectPopup(c *ui.Context, panel ui.Element) {
	t := c.Theme()
	panel.Margin(t.Space(1), 0, 0, 0).Padding(t.Space(1)).Radius(t.Radius+2).
		Background(palette(c).Content).TextColor(t.Text).Border(1, t.Border).
		Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
}

func selectPopupHeight(c *ui.Context, trigger ui.Element) float32 {
	_, height := c.Size()
	bounds := trigger.Bounds()
	// MyGo flips the panel to the roomier side of the trigger. Reserve
	// its gap, window edge, padding and border before sizing the scroll.
	room := max(bounds.Y, height-bounds.Y-bounds.H) - c.Theme().Space(4) - 2
	return max(0, min(320, room))
}

func styleSelectItem(c *ui.Context, item ui.Element, selected bool) ui.Element {
	t, p := c.Theme(), palette(c)
	item.Padding(t.Space(1.5), t.Space(2.5)).Gap(t.Space(2)).Radius(t.Radius).TextColor(t.Text)
	switch {
	case item.Highlighted():
		item.Background(p.NavSelected)
	case selected:
		item.Background(p.NavHover)
	}
	return item
}
