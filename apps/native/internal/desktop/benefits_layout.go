package desktop

import (
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func (v *PetView) benefitsWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	if v.ContentWidth != nil {
		width = v.ContentWidth(c)
	}
	return max(0, min(width, shellContentMaxWidth)-16)
}

func (v *PetView) benefitsHeader(c *ui.Context, title, description, label, value string) {
	header := ui.Row(c).FillWidth().Gap(20).AlignItems(ui.Center)
	if v.benefitsWidth(c) < 660 {
		header.Column().AlignItems(ui.Start)
	}
	header.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
			ui.Text(c, title).FontSize(26).Role(ui.RoleHeading).Level(1)
			ui.Text(c, description).FillWidth().FontSize(14).TextColor(c.Theme().TextMuted)
		})
		if value != "" {
			ui.Column(c).Shrink(0).MinWidth(120).Padding(12, 16).Gap(4).Radius(10).Background(palette(c).Panel).Border(1, palette(c).Separator).Children(func() {
				ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
				ui.Text(c, value).FontSize(22)
			})
		}
	})
}

func benefitsPanel(c *ui.Context, content func()) {
	ui.Column(c).FillWidth().MinWidth(0).Padding(20).Radius(12).Border(1, palette(c).Separator).Background(palette(c).Panel).Gap(16).Children(content)
}

func benefitsHeading(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(17).Role(ui.RoleHeading).Level(2)
}

func benefitsEmpty(c *ui.Context, icon, title, copy string) {
	ui.Row(c).FillWidth().Padding(16).Gap(12).AlignItems(ui.Center).Radius(8).Background(palette(c).Subpanel).Role(ui.RoleStatus).Children(func() {
		ui.Icon(c, presentation.Icon(icon)).Size(22, 22).Shrink(0).TextColor(c.Theme().TextMuted)
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, title).FillWidth().FontSize(14)
			ui.Text(c, copy).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
		})
	})
}

// Keep the primary pane readable when the shell's secondary menu is expanded.
func benefitsColumns(c *ui.Context, width float32, main, aside func()) {
	wide := width >= 1000
	row := ui.Row(c).FillWidth().Gap(20).AlignItems(ui.Start)
	if !wide {
		row.Column().AlignItems(ui.Stretch)
	}
	row.Children(func() {
		column := ui.Column(c).MinWidth(0).Gap(20)
		if wide {
			column.Grow(1).Basis(0)
		} else {
			column.FillWidth()
		}
		column.Children(main)
		sidebar := ui.Column(c).Gap(20).MinWidth(0)
		if wide {
			sidebar.Width(300).Shrink(0)
		} else {
			sidebar.FillWidth()
		}
		sidebar.Children(aside)
	})
}
