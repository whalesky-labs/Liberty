package presentation

import "github.com/egoist/mygo/ui"

// SystemColors names the semantic roles used by the native desktop. macOS
// resolves them from NSColor; other platforms use neutral opaque fallbacks.
type SystemColors struct {
	Window, TextBackground, Label, SecondaryLabel ui.Color
	Separator, Selection, Fill, SubtleFill        ui.Color
	Red, Orange, Green                            ui.Color
}

func fallbackSystemColors(dark bool) SystemColors {
	colors := SystemColors{
		Window: ui.Hex("#ffffff"), TextBackground: ui.Hex("#ffffff"),
		Label: ui.RGBA(0, 0, 0, 0.85), SecondaryLabel: ui.RGBA(0, 0, 0, 0.55),
		Separator: ui.RGBA(0, 0, 0, 0.10), Selection: ui.Hex("#dcdcdc"),
		Fill: ui.RGBA(0, 0, 0, 0.05), SubtleFill: ui.RGBA(0, 0, 0, 0.03),
		Red: ui.Hex("#ff383c"), Orange: ui.Hex("#ff8d28"), Green: ui.Hex("#34c759"),
	}
	if dark {
		colors.Window, colors.TextBackground = ui.Hex("#1e1e1e"), ui.Hex("#1e1e1e")
		colors.Label, colors.SecondaryLabel = ui.RGBA(255, 255, 255, 0.85), ui.RGBA(255, 255, 255, 0.55)
		colors.Separator, colors.Selection = ui.RGBA(255, 255, 255, 0.10), ui.Hex("#464646")
		colors.Fill, colors.SubtleFill = ui.RGBA(255, 255, 255, 0.05), ui.RGBA(255, 255, 255, 0.03)
		colors.Red, colors.Orange, colors.Green = ui.Hex("#ff4245"), ui.Hex("#ff9230"), ui.Hex("#30d158")
	}
	return colors
}

// SolidSurface composites a semantic fill over an opaque reading surface.
// Panels must not become holes in the system glass behind the window.
func SolidSurface(fill, background ui.Color) ui.Color {
	alpha := uint32(fill.A)
	channel := func(f, b uint8) uint8 { return uint8((uint32(f)*alpha + uint32(b)*(255-alpha) + 127) / 255) }
	return ui.Color{R: channel(fill.R, background.R), G: channel(fill.G, background.G), B: channel(fill.B, background.B), A: 255}
}
