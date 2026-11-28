package desktop

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func palette(c *ui.Context) presentation.Palette {
	return presentation.Colors(c.Theme().Dark, c.Theme().Accent)
}

// Main and settings navigation share neutral selection and hover colors.
func navigationColors(c *ui.Context, button ui.Element, active bool) (ui.Color, ui.Color) {
	color, iconColor := c.Theme().Text, c.Theme().TextMuted
	p := palette(c)
	if active {
		button.Background(p.NavSelected)
		iconColor = color
	} else if button.Hovered() {
		button.Background(p.NavHover)
	}
	return color, iconColor
}

func navigationContent(c *ui.Context, button ui.Element, icon, label string, active bool) {
	color, iconColor := navigationColors(c, button, active)
	button.Children(func() {
		ui.Icon(c, presentation.Icon(icon)).Size(16, 16).TextColor(iconColor)
		ui.Text(c, label).FontSize(13).TextColor(color).SingleLine()
	})
}

func textButton(c *ui.Context, label string) ui.Element {
	return ui.ButtonBase(c).Label(label).Padding(6, 8).Radius(5).TextColor(palette(c).NavAccent).Children(func() { ui.Text(c, label).FontSize(12) })
}

// segmentedControl binds stable values to MyGo's default segmented control.
// Keep dimensions and styling in MyGo so every page follows the same defaults.
func segmentedControl(c *ui.Context, value *string, values []string, labels ...string) ui.Element {
	selected := max(0, slices.Index(values, *value))
	control := ui.Segmented(c, &selected, labels...)
	*value = values[selected]
	return control
}
func separator(c *ui.Context) { ui.Row(c).FillWidth().Height(1).Background(palette(c).Separator) }
func statusBadge(c *ui.Context, locale, status string) {
	t := c.Theme()
	color := t.TextMuted
	switch status {
	case "completed":
		color = t.Success
	case "failed":
		color = t.Danger
	case "queued", "transcribing", "speaker_processing", "summarizing":
		color = t.Accent
	}
	ui.Row(c).Padding(3, 8).Radius(12).Background(color.Alpha(0.1)).Children(func() { ui.Text(c, statusText(locale, status)).TextColor(color).FontSize(11).SingleLine() })
}
func ratio(value, total int64) string {
	if total <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d%%", int64(math.Round(math.Min(100, math.Max(0, float64(value)/float64(total)*100)))))
}
func minutesText(locale string, value int64) string {
	value = max(0, value)
	h, m := value/60, value%60
	if h == 0 {
		return fmt.Sprintf(localized(locale, "%d 分钟", "%d min"), m)
	}
	if m == 0 {
		return fmt.Sprintf(localized(locale, "%d 小时", "%dh"), h)
	}
	return fmt.Sprintf(localized(locale, "%d 小时 %d 分钟", "%dh %dm"), h, m)
}
func createdText(locale, value string) string {
	stamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	stamp = stamp.In(time.Local)
	if locale == "en-US" {
		return stamp.Format("01/02, 03:04 PM")
	}
	return stamp.Format("01/02 15:04")
}
func progressBar(c *ui.Context, value float32, label string) {
	p := palette(c)
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		value = 0
	}
	value = max(0, min(100, value))
	ui.Row(c).FillWidth().Height(5).Radius(3).Background(p.Separator).Label(label).Role(ui.RoleProgress).Range(0, 100, float64(value)).Children(func() {
		ui.Row(c).WidthPercent(value).Height(5).Radius(3).Background(c.Theme().Accent)
	})
}
