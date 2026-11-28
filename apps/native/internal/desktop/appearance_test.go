package desktop

import (
	"bytes"
	"fmt"
	"image/color"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestWorkspaceStartsWithSavedGlassAppearance(t *testing.T) {
	for _, platform := range []string{"darwin", "windows", "linux"} {
		for _, style := range []string{"transparent", "tinted", "invalid"} {
			t.Run(platform+"/"+style, func(t *testing.T) {
				preferences := domain.DefaultUIPreferences()
				preferences.LiquidGlassStyle, preferences.AccentColor = style, "#f062a8"
				options := workspaceWindowOptionsForPlatform(platform, preferences, func(*ui.Context) {})
				if options.Transparent {
					t.Fatal("unsupported desktop materials must retain an opaque backing")
				}
				if platform == "linux" {
					if options.Vibrancy != mygo.VibrancyNone || options.VibrancyTint != "" {
						t.Fatal("unsupported platform requested a window material")
					}
					return
				}
				if options.Vibrancy != mygo.VibrancyGlass {
					t.Fatal("navigation must use the regular system glass", options.Vibrancy)
				}
				if style == "tinted" {
					assertGlassTint(t, options.VibrancyTint, presentation.PreferenceColors(false, preferences).Window)
				} else if options.VibrancyTint != "" {
					t.Fatal("standard glass retained an artificial tint")
				}
			})
		}
	}
	preferences := domain.DefaultUIPreferences()
	preferences.LiquidGlassStyle, preferences.AccentColor = "tinted", "invalid"
	for _, dark := range []bool{false, true} {
		_, tint := windowGlass(preferences, dark)
		assertGlassTint(t, tint, presentation.PreferenceColors(dark, preferences).Window)
	}
}

func assertGlassTint(t *testing.T, tint string, expected ui.Color) {
	t.Helper()
	var r, g, b int
	var alpha float64
	count, err := fmt.Sscanf(tint, "rgba(%d, %d, %d, %f)", &r, &g, &b, &alpha)
	if err != nil || count != 4 || [3]int{r, g, b} != [3]int{int(expected.R), int(expected.G), int(expected.B)} || alpha < 0.23 || alpha > 0.25 {
		t.Fatal("glass tint must strengthen the semantic window tone, independently of the accent", tint, err)
	}
}

func TestShellGlassLimitsBackdropAndKeepsReadingSurfaceOpaque(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, locale := range []string{"zh-CN", "en-US"} {
			t.Run(fmt.Sprintf("%s/dark=%v", locale, dark), func(t *testing.T) {
				preferences := domain.DefaultUIPreferences()
				preferences.Locale = locale
				shell := &Shell{Locale: locale, Preferences: preferences}
				tester := ui.NewTesterWith(shell.View, 1100, 760, ui.TesterOptions{Dark: dark})
				colors := presentation.PreferenceColors(dark, preferences)
				assertReadingSurface := func() {
					t.Helper()
					image := tester.Image()
					for _, point := range [][2]int{{presentation.SidebarWidth + 40, 100}, {presentation.SidebarWidth + 24, 2}, {presentation.SidebarWidth + 24, 758}, {1098, 758}} {
						if pixel := image.RGBAAt(point[0], point[1]); pixel != (color.RGBA{R: colors.Content.R, G: colors.Content.G, B: colors.Content.B, A: 255}) {
							t.Fatal("header, body and status bar must share an opaque reading surface", point, pixel)
						}
					}
					chrome := image.RGBAAt(2, 2)
					for _, point := range [][2]int{{presentation.SidebarWidth, 0}, {presentation.SidebarWidth, 759}} {
						if pixel := image.RGBAAt(point[0], point[1]); pixel != chrome {
							t.Fatal("content's left corner did not reveal the navigation chrome", point, pixel, chrome)
						}
					}
				}
				assertOpaqueChrome := func() {
					t.Helper()
					pixel := tester.Image().RGBAAt(2, 2)
					if pixel != (color.RGBA{R: colors.Window.R, G: colors.Window.G, B: colors.Window.B, A: 255}) {
						t.Fatal("fallback left translucent chrome", pixel)
					}
					assertReadingSurface()
				}

				assertOpaqueChrome()
				tester.SetVibrancy(true)
				transparent := tester.Image().RGBAAt(2, 2)
				if transparent.A < 240 || transparent.A == 255 {
					t.Fatal("transparent chrome must admit only a subtle background hint", transparent)
				}
				// Every chrome region uses the same restrained backing. The native
				// blur itself is verified separately in a real window.
				for _, point := range [][2]int{{2, 80}, {presentation.SidebarWidth - 2, 758}} {
					if pixel := tester.Image().RGBAAt(point[0], point[1]); pixel != transparent {
						t.Fatal("chrome regions use different background strengths", point, pixel, transparent)
					}
				}
				assertReadingSurface()
				captureParity(t, tester, fmt.Sprintf("glass-transparent-%s-%v", locale, dark))

				shell.Preferences.LiquidGlassStyle = "tinted"
				tester.Frame()
				tinted := tester.Image().RGBAAt(2, 2)
				assertOpaqueChrome()
				captureParity(t, tester, fmt.Sprintf("glass-tinted-%s-%v", locale, dark))

				for _, system := range []ui.Preferences{{ReduceTransparency: true}, {HighContrast: true}} {
					shell.Preferences.LiquidGlassStyle = "transparent"
					tester.SetPreferences(system)
					assertOpaqueChrome()
				}
				tester.SetPreferences(ui.Preferences{})
				if pixel := tester.Image().RGBAAt(2, 2); pixel != transparent {
					t.Fatal("turning off accessibility fallback did not restore glass", pixel, tinted)
				}
				tester.SetVibrancy(false)
				assertOpaqueChrome()
			})
		}
	}
}

func TestSettingsGlassPreviewFollowsSystemTransparencyPreference(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, style := range []string{"transparent", "tinted"} {
			t.Run(fmt.Sprintf("%s/dark=%v", style, dark), func(t *testing.T) {
				view := &SettingsView{}
				tester := ui.NewTesterWith(func(c *ui.Context) {
					ui.Column(c).Width(100).Children(func() { view.glassPreview(c, style) })
				}, 100, 56, ui.TesterOptions{Dark: dark})
				glass := bytes.Clone(tester.Image().Pix)
				tester.SetPreferences(ui.Preferences{ReduceTransparency: true})
				changed := !bytes.Equal(glass, tester.Image().Pix)
				if changed != (style == "transparent") {
					t.Fatal("preview must remove Clear's subtle backdrop while Tinted stays opaque", style)
				}
				tester.SetPreferences(ui.Preferences{})
				if !bytes.Equal(glass, tester.Image().Pix) {
					t.Fatal("glass preview did not restore its material")
				}
				captureParity(t, tester, fmt.Sprintf("glass-preview-%s-%v", style, dark))
			})
		}
	}
}
