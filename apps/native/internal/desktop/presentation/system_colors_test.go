package presentation

import (
	"testing"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/domain"
)

func TestSemanticSurfacesStayOpaqueAndIndependentOfAccent(t *testing.T) {
	for _, dark := range []bool{false, true} {
		first := Colors(dark, ui.Hex("#2f6dff"))
		second := Colors(dark, ui.Hex("#f062a8"))
		for _, pair := range [][2]ui.Color{{first.Window, second.Window}, {first.Content, second.Content}, {first.Panel, second.Panel}, {first.Subpanel, second.Subpanel}} {
			if pair[0] != pair[1] || pair[0].A != 255 {
				t.Fatal("accent recolored a reading surface or exposed the desktop", pair)
			}
		}
		if first.Theme.Text != systemColors(dark).Label || first.Theme.TextMuted != systemColors(dark).SecondaryLabel {
			t.Fatal("text no longer uses system semantic roles")
		}
		for _, style := range []string{"transparent", "tinted", "invalid"} {
			material := LiquidGlass(style, first.Window)
			if material.Style != glass.Regular || material.Clarity != 0 {
				t.Fatal("navigation selected clear or high-clarity media glass", material)
			}
		}
	}
	light, dark := Colors(false, ui.Hex("#2f6dff")), Colors(true, ui.Hex("#2f6dff"))
	if light.Content.R <= dark.Content.R || light.Theme.Text.R >= dark.Theme.Text.R {
		t.Fatal("light and dark appearances are inverted or share a palette")
	}
}

func TestSystemAppearanceAndExplicitOverrideStayDistinct(t *testing.T) {
	preferences := domain.DefaultUIPreferences()
	for _, dark := range []bool{false, true} {
		if got := PreferenceColors(dark, preferences); got.Theme.Dark != dark {
			t.Fatal("automatic theme ignored the system appearance")
		}
		for _, mode := range []string{"light", "dark"} {
			preferences.ThemeMode = mode
			if got := PreferenceColors(dark, preferences); got.Theme.Dark != (mode == "dark") {
				t.Fatal("explicit appearance followed the opposite system theme")
			}
		}
		preferences.ThemeMode = "auto"
	}
}
