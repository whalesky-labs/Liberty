package desktop

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func openAccentMenu(t *testing.T, tester *ui.Tester, view *SettingsView) {
	t.Helper()
	trigger, ok := tester.AccessByRole(ui.RoleMenuButton, view.msg("accentColor"))
	if !ok || trigger.Offscreen {
		t.Fatal("accent menu trigger is inaccessible", trigger)
	}
	tester.ClickAt(trigger.Bounds.X+trigger.Bounds.W/2, trigger.Bounds.Y+trigger.Bounds.H/2)
}

func TestSettingsAccentMenuPreservesColorsAndSavesMouseAndKeyboardChoices(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{1440, 1100, 760} {
				t.Run(fmt.Sprintf("%s-dark=%v-width=%d", locale, dark, width), func(t *testing.T) {
					callbacks := make(chan func(), 8)
					service := settingsParityService{saved: make(chan domain.Settings, 4)}
					preferences := domain.DefaultUIPreferences()
					controller := client.NewSettings(service, func(next func()) { callbacks <- next }, func() {}, func(next domain.UIPreferences) { preferences = next })
					defer controller.Close()
					view := &SettingsView{Locale: locale, section: "appearance", Client: controller}
					tester := ui.NewTesterWith(func(c *ui.Context) {
						shell := Shell{Locale: locale, Route: "/settings", Preferences: preferences, Sidebar: view.Sidebar, Content: view.View}
						shell.View(c)
					}, width, 960, ui.TesterOptions{Dark: dark, Preferences: ui.Preferences{ReduceMotion: true}})
					controller.Load()
					flushView(t, callbacks, tester)
					openAccentMenu(t, tester, view)
					checked := 0
					for _, hex := range []string{"#8f96a3", "#2f6dff", "#33c96f", "#f5dd00", "#f062a8", "#ff6a57", "#ffb020", "#a65dd9", "#ffffff"} {
						item, ok := tester.AccessByRole(ui.RoleMenuItemRadio, view.msg("colorLabels."+hex))
						if !ok || item.Offscreen || item.Bounds.Y < 0 || item.Bounds.Y+item.Bounds.H > 960 {
							t.Fatal("color menu item is clipped or unnamed", hex, item)
						}
						if item.Checked {
							checked++
							if hex != "#2f6dff" {
								t.Fatal("menu checked a color other than the saved preference", hex)
							}
						}
						pixel := tester.Image().RGBAAt(int(item.Bounds.X+16), int(item.Bounds.Y+item.Bounds.H/2))
						if (ui.Color{R: pixel.R, G: pixel.G, B: pixel.B, A: pixel.A}) != ui.Hex(hex) {
							t.Fatal("color dot does not represent its named color", hex, pixel)
						}
					}
					trigger, _ := tester.AccessByRole(ui.RoleMenuButton, view.msg("accentColor"))
					blue, _ := tester.AccessByRole(ui.RoleMenuItemRadio, view.msg("colorLabels.#2f6dff"))
					if checked != 1 || !trigger.Expanded || math.Abs(float64(blue.Bounds.X+blue.Bounds.W+5-trigger.Bounds.X-trigger.Bounds.W)) > 1 {
						t.Fatal("menu lost its single selected state or trailing alignment", checked, trigger, blue)
					}
					captureParity(t, tester, fmt.Sprintf("settings-accent-menu-%s-%v-%d", locale, dark, width))
					if err := tester.Click(view.msg("colorLabels.#33c96f")); err != nil {
						t.Fatal(err)
					}
					flushView(t, callbacks, tester)
					if saved := <-service.saved; saved.AccentColor != "#33c96f" || preferences.AccentColor != saved.AccentColor {
						t.Fatal("mouse choice did not save and apply its color", saved.AccentColor, preferences.AccentColor)
					}
					openAccentMenu(t, tester, view)
					tester.Key(0, ui.KeyEnd)
					tester.Key(0, ui.KeyUp)
					tester.Key(0, ui.KeyEnter)
					flushView(t, callbacks, tester)
					if saved := <-service.saved; saved.AccentColor != "#ffffff" || preferences.AccentColor != saved.AccentColor {
						t.Fatal("keyboard choice did not save and apply white", saved.AccentColor, preferences.AccentColor)
					}
					openAccentMenu(t, tester, view)
					tester.Key(0, ui.KeyEscape)
					if !tester.Focused(view.msg("accentColor")) || controller.Dirty() || len(service.saved) != 0 {
						t.Fatal("closing the menu changed settings or lost trigger focus")
					}
				})
			}
		}
	}
}

func TestSettingsCustomAccentValidatesSavesAndReopensWithoutLosingFocus(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 8)
			service := settingsParityService{saved: make(chan domain.Settings, 4)}
			controller := client.NewSettings(service, func(next func()) { callbacks <- next }, func() {}, func(domain.UIPreferences) {})
			defer controller.Close()
			view := &SettingsView{Locale: locale, section: "appearance", Client: controller}
			tester := ui.NewTester(view.View, 1100, 760)
			controller.Load()
			flushView(t, callbacks, tester)
			openCustom := func() {
				t.Helper()
				openAccentMenu(t, tester, view)
				tester.Key(0, ui.KeyEnd)
				tester.Key(0, ui.KeyEnter)
				if dialog, ok := tester.AccessByRole(ui.RoleDialog, view.msg("customAccentColor")); !ok || dialog.Offscreen || !tester.Focused(view.msg("customAccentColorValue")) {
					t.Fatal("custom color dialog is unnamed or lost its initial focus", dialog)
				}
			}
			openCustom()
			tester.Command("selectAll")
			tester.Type("#12zzzz")
			save := presentation.Message(locale, "common.save")
			button, _ := tester.AccessByRole(ui.RoleButton, save)
			if !button.Disabled || !tester.HasText(view.msg("customAccentColorInvalid")) || controller.Draft.AccentColor != "#2f6dff" {
				t.Fatal("invalid color could be saved or changed the persisted draft", button)
			}
			tester.Key(0, ui.KeyEscape)
			if !tester.Focused(view.msg("accentColor")) || controller.Dirty() || len(service.saved) != 0 {
				t.Fatal("cancelling custom color changed settings or lost focus")
			}
			openCustom()
			tester.Command("selectAll")
			tester.Type("#1258AB")
			captureParity(t, tester, "settings-custom-accent-"+locale)
			if locale == "en-US" {
				tester.Key(0, ui.KeyEnter)
			} else if err := tester.Click(save); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tester)
			if saved := <-service.saved; saved.AccentColor != "#1258ab" || controller.Dirty() || !tester.Focused(view.msg("accentColor")) {
				t.Fatal("custom color was not normalized, saved, or restored focus", saved.AccentColor)
			}
			trigger, _ := tester.AccessByRole(ui.RoleMenuButton, view.msg("accentColor"))
			if trigger.Value != view.msg("customAccentColor") {
				t.Fatal("custom color is missing from the trigger", trigger)
			}
			openCustom()
			field, _ := tester.AccessByRole(ui.RoleTextField, view.msg("customAccentColorValue"))
			if field.Value != "#1258ab" {
				t.Fatal("reopening the custom editor lost the saved color", field)
			}
			if err := tester.Click(presentation.Message(locale, "common.cancel")); err != nil {
				t.Fatal(err)
			}
			if !tester.Focused(view.msg("accentColor")) || controller.Dirty() || len(service.saved) != 0 {
				t.Fatal("Cancel did not preserve the saved custom color and focus")
			}
		})
	}
}
