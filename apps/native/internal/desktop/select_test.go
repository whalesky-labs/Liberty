package desktop

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func TestSelectControlKeepsNeutralColorsAcrossAccents(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			var closed, opened []byte
			for _, accent := range []string{"#8f96a3", "#2f6dff", "#a65dd9", "#f062a8", "#ff6a57", "#ffb020", "#f5dd00", "#33c96f"} {
				colors := presentation.Colors(dark, ui.Hex(accent))
				selected := "Auto"
				tester := ui.NewTesterWith(func(c *ui.Context) {
					ui.Themed(c, &colors.Theme).Fill().Children(func() {
						ui.Column(c).Padding(20).AlignItems(ui.Start).Children(func() {
							selectControl(c, &selected, []string{"Auto", "CPU", "Apple Silicon", "CUDA"}).Width(200).Label("Device")
						})
					})
				}, 300, 260, ui.TesterOptions{Dark: dark, Preferences: ui.Preferences{ReduceMotion: true}})
				tester.Key(0, ui.KeyTab)
				if closed == nil {
					closed = bytes.Clone(tester.Image().Pix)
				} else if !bytes.Equal(closed, tester.Image().Pix) {
					t.Fatal("closed selector or focus ring inherited the accent", accent)
				}
				tester.Key(0, ui.KeyEnter)
				option, ok := tester.AccessByRole(ui.RoleOption, "Auto")
				if !ok || !option.Focused || option.Offscreen {
					t.Fatal("current option lost its accessible highlight", option)
				}
				pixel := tester.Image().RGBAAt(int(option.Bounds.X+4), int(option.Bounds.Y+option.Bounds.H/2))
				want := presentation.SolidSurface(colors.NavSelected, colors.Content)
				if (ui.Color{R: pixel.R, G: pixel.G, B: pixel.B, A: pixel.A}) != want {
					t.Fatalf("highlight inherited accent %s: got %v, want neutral %v", accent, pixel, want)
				}
				if opened == nil {
					opened = bytes.Clone(tester.Image().Pix)
				} else if !bytes.Equal(opened, tester.Image().Pix) {
					t.Fatal("popup inherited the accent", accent)
				}
				captureParity(t, tester, fmt.Sprintf("select-neutral-%v-%s", dark, accent[1:]))
			}
		})
	}
}

func TestSelectControlPreservesSelectionKeyboardMouseAndDisabledSemantics(t *testing.T) {
	selected, disabled, changes := "Auto", false, 0
	tester := ui.NewTester(func(c *ui.Context) {
		colors := presentation.Colors(c.Theme().Dark, ui.Hex("#f062a8"))
		ui.Themed(c, &colors.Theme).Fill().Padding(20).AlignItems(ui.Start).Children(func() {
			if selectControl(c, &selected, []string{"Auto", "CPU", "Apple Silicon", "CUDA"}).Width(200).Label("Device").Disabled(disabled).Changed() {
				changes++
			}
			ui.Button(c, "Next")
		})
	}, 300, 280)
	tester.Key(0, ui.KeyTab)
	tester.Key(0, ui.KeyEnter)
	tester.Key(0, ui.KeyEnd)
	if selected != "Auto" || changes != 0 {
		t.Fatal("keyboard navigation committed the choice before confirmation")
	}
	tester.Key(0, ui.KeyEscape)
	if selected != "Auto" || !tester.Focused("Device") {
		t.Fatal("Escape changed the value or lost the trigger focus")
	}
	tester.Key(0, ui.KeyEnter)
	tester.Key(0, ui.KeyDown)
	tester.Key(0, ui.KeyEnter)
	if selected != "CPU" || changes != 1 {
		t.Fatal("keyboard choice did not report exactly one change", selected, changes)
	}
	if err := tester.Click("Device"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("Apple Silicon"); err != nil {
		t.Fatal(err)
	}
	if selected != "Apple Silicon" || changes != 2 {
		t.Fatal("mouse choice did not report exactly one change", selected, changes)
	}
	if err := tester.Click("Device"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyEscape)
	disabled = true
	tester.Frame()
	control, ok := tester.AccessByRole(ui.RolePopUpButton, "Device")
	if !ok || !control.Disabled || control.Value != selected {
		t.Fatal("disabled choice lost its role or value", control)
	}
	tester.ClickAt(control.Bounds.X+10, control.Bounds.Y+10)
	if _, ok := tester.AccessByRole(ui.RoleOption, "Auto"); ok || selected != "Apple Silicon" || changes != 2 {
		t.Fatal("disabled selector accepted input")
	}
}

func TestSelectControlScrollsLongListsAndUpdatesAppearanceWhileOpen(t *testing.T) {
	options := make([]string, 80)
	for index := range options {
		options[index] = fmt.Sprintf("Option %02d", index)
	}
	for _, top := range []float32{20, 120, 200} {
		t.Run(fmt.Sprint(top), func(t *testing.T) {
			selected := options[0]
			tester := ui.NewTesterWith(func(c *ui.Context) {
				colors := presentation.Colors(c.Theme().Dark, ui.Hex("#f5dd00"))
				ui.Themed(c, &colors.Theme).Fill().Padding(top, 20, 0).AlignItems(ui.Start).Children(func() {
					selectControl(c, &selected, options).Width(200).Label("Choice")
				})
			}, 300, 260, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
			tester.Key(0, ui.KeyTab)
			tester.Key(0, ui.KeyEnter)
			tester.SetDark(true)
			tester.Key(0, ui.KeyEnd)
			option, ok := tester.AccessByRole(ui.RoleOption, options[len(options)-1])
			if !ok || option.Offscreen || !option.Focused || option.Bounds.Y < 0 || option.Bounds.Y+option.Bounds.H > 260 {
				t.Fatal("last option is clipped or keyboard scrolling stopped", option)
			}
			pixel := tester.Image().RGBAAt(int(option.Bounds.X+4), int(option.Bounds.Y+option.Bounds.H/2))
			want := presentation.Colors(true, ui.Hex("#f5dd00")).NavSelected
			if (ui.Color{R: pixel.R, G: pixel.G, B: pixel.B, A: pixel.A}) != want {
				t.Fatal("open popup did not follow the changed appearance", pixel, want)
			}
			tester.Key(0, ui.KeyEnter)
			if selected != options[len(options)-1] || !tester.Focused("Choice") {
				t.Fatal("long list choice lost its value or focus", selected)
			}
		})
	}
}

func TestSettingsDeviceSelectUsesSharedNeutralPalette(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-dark=%v", locale, dark), func(t *testing.T) {
				view := settingsParityView(locale, "performance")
				preferences := view.Client.UIPreferences()
				preferences.AccentColor = "#f062a8"
				shell := Shell{Locale: locale, Route: "/settings", Preferences: preferences, Sidebar: view.Sidebar, Content: view.View}
				tester := ui.NewTesterWith(shell.View, 1100, 900, ui.TesterOptions{Dark: dark})
				control, ok := tester.AccessByRole(ui.RolePopUpButton, view.msg("localAsrDevice"))
				if !ok || control.Offscreen {
					t.Fatal("settings device selector is inaccessible", control)
				}
				tester.ClickAt(control.Bounds.X+control.Bounds.W/2, control.Bounds.Y+control.Bounds.H/2)
				option, ok := tester.AccessByRole(ui.RoleOption, view.msg("localAsrDeviceAuto"))
				if !ok || !option.Focused || option.Offscreen {
					t.Fatal("settings device choice is inaccessible", option)
				}
				pixel := tester.Image().RGBAAt(int(option.Bounds.X+4), int(option.Bounds.Y+option.Bounds.H/2))
				want := presentation.PreferenceColors(dark, preferences).NavSelected
				if (ui.Color{R: pixel.R, G: pixel.G, B: pixel.B, A: pixel.A}) != want {
					t.Fatal("settings bypassed the shared neutral selector", pixel, want)
				}
				captureParity(t, tester, fmt.Sprintf("settings-device-select-%s-%v", locale, dark))
			})
		}
	}
}
