// Package presentation adapts the existing Liberty visual language to MyGo.
// Generated messages, icons and images come from desktop-resources; layout values
// below follow global.css's final cascade, not the toolkit's default style.
package presentation

import (
	_ "embed"
	"fmt"
	"liberty.local/liberty/internal/domain"
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui"
)

//go:embed sidebar-mascot.webp
var mascotBytes []byte

var Mascot = mustAnimation(mascotBytes)
var icons = loadIcons()

func loadIcons() map[string]*ui.SVG {
	result := make(map[string]*ui.SVG, len(iconSources))
	for name, source := range iconSources {
		result[name] = ui.MustParseSVG([]byte(source))
	}
	return result
}

func Icon(name string) *ui.SVG { return icons[name] }

func PetSourceLabel(locale, source string) string {
	if label := messages[locale]["petSource."+source]; label != "" {
		return label
	}
	if locale == "en-US" {
		return "Unknown"
	}
	return "未知"
}

func Message(locale, key string) string {
	if locale != "en-US" {
		locale = "zh-CN"
	}
	if message, ok := messages[locale][key]; ok {
		return message
	}
	panic("unknown Liberty message: " + key)
}

func Format(locale, key string, values map[string]any) string {
	text := Message(locale, key)
	for key, value := range values {
		text = strings.ReplaceAll(text, "{"+key+"}", fmt.Sprint(value))
	}
	return text
}

const (
	SidebarWidth    = 232
	TitlebarHeight  = 42
	StatusbarHeight = 26
	WindowWidth     = 1440
	WindowHeight    = 960
	WindowMinWidth  = 1100
	WindowMinHeight = 760
	TrafficLightX   = 20
	TrafficLightY   = 23
)

type Palette struct {
	Theme                                                            ui.Theme
	Window, Content, Panel, Subpanel, Selected, NavAccent, Separator ui.Color
}

func Colors(dark bool, accent ui.Color) Palette {
	t := *ui.LightTheme()
	p := Palette{Window: ui.Hex("#f5f5f7"), Content: ui.Hex("#fbfbfd"), Panel: ui.Hex("#fbfbfd"), Subpanel: ui.Hex("#f5f5f7"),
		Selected: accent.Alpha(0.14), NavAccent: ui.Hex("#081225").Mix(accent, 0.78), Separator: ui.RGBA(60, 60, 67, 0.14)}
	t.Text, t.TextMuted, t.Border = ui.Hex("#14151a"), ui.Hex("#6d7280"), ui.RGBA(60, 60, 67, 0.16)
	t.Surface, t.SurfaceHover = ui.Hex("#ffffff"), ui.RGBA(60, 60, 67, 0.06)
	t.Success, t.Warning, t.Danger = ui.Hex("#1f8f4d"), ui.Hex("#b86a0a"), ui.Hex("#de3b40")
	if dark {
		t = *ui.DarkTheme()
		p.Window, p.Content, p.Panel, p.Subpanel = ui.Hex("#1e1e20"), ui.Hex("#1c1c1e"), ui.Hex("#2c2c2e"), ui.Hex("#252528")
		p.Selected, p.NavAccent, p.Separator = accent.Alpha(0.22), ui.Hex("#ffffff").Mix(accent, 0.74), ui.RGBA(255, 255, 255, 0.1)
		t.Text, t.TextMuted, t.Border = ui.Hex("#f5f5f7"), ui.Hex("#a1a1a6"), p.Separator
		t.Surface, t.SurfaceHover = ui.Hex("#2c2c2e"), ui.RGBA(255, 255, 255, 0.06)
		t.Success, t.Warning, t.Danger = ui.Hex("#5ccc88"), ui.Hex("#ffbf5f"), ui.Hex("#ff7e86")
	}
	t.Background, t.Accent, t.Selection = p.Window, accent, p.Selected
	t.AccentHover, t.AccentPressed = ui.Hex("#081225").Mix(accent, 0.78), ui.Hex("#081225").Mix(accent, 0.65)
	t.AccentText = ui.Hex("#ffffff")
	// Matches resolveAccentContrast in the original appearance adapter.
	if (int(accent.R)*299+int(accent.G)*587+int(accent.B)*114)/1000 >= 160 {
		t.AccentText = ui.Hex("#14151a")
	}
	t.FontSize, t.Radius, t.Spacing = 14, 7, 4
	t.Focus = accent.Alpha(0.28)
	p.Theme = t
	return p
}

type NavItem struct{ Key, Path, Icon string }
type NavSection struct {
	Zh, En string
	Items  []NavItem
}

var Navigation = []NavSection{
	{"工作", "Work", []NavItem{{"dashboard", "/", "dashboard"}, {"newJob", "/jobs/new", "plus"}, {"jobs", "/jobs", "tray"}}},
	{"打工", "Work Games", []NavItem{{"workMarket", "/work-market", "market"}}},
	{"资源", "Resources", []NavItem{{"models", "/models", "chip"}, {"templates", "/templates", "doc"}, {"members", "/members", "people"}}},
	{"伙伴", "Companion", []NavItem{{"pet", "/pet", "pet"}, {"petStore", "/pet-store", "store"}}},
	{"福利", "Benefits", []NavItem{{"redeemKey", "/redeem-key", "key"}, {"dailyCheckIn", "/daily-check-in", "check"}, {"dailyBlindBox", "/daily-blind-box", "gift"}}},
}

var accentPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func PreferenceColors(systemDark bool, p domain.UIPreferences) Palette {
	dark := systemDark
	if p.ThemeMode == "light" {
		dark = false
	} else if p.ThemeMode == "dark" {
		dark = true
	}
	accent := ui.Hex("#2f6dff")
	if accentPattern.MatchString(p.AccentColor) {
		accent = ui.Hex(p.AccentColor)
	}
	return Colors(dark, accent)
}
