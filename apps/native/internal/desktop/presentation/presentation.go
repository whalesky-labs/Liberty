// Package presentation supplies the native desktop's shared appearance.
// Messages, icons and images come from desktop-resources; macOS colors come
// from AppKit's semantic color roles in the selected appearance.
package presentation

import (
	_ "embed"
	"fmt"
	"liberty.local/liberty/internal/domain"
	"regexp"
	"strings"

	"github.com/egoist/mygo/plugins/glass"
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
	ActivityBarWidth = 52
	SidebarMenuWidth = 232
	SidebarWidth     = ActivityBarWidth + SidebarMenuWidth
	TitlebarHeight   = 42
	StatusbarHeight  = 26
	WindowWidth      = 1440
	WindowHeight     = 960
	WindowMinWidth   = 1100
	WindowMinHeight  = 760
	TrafficLightX    = 20
	TrafficLightY    = 23
)

type Palette struct {
	Theme                                                            ui.Theme
	Window, Content, Panel, Subpanel, Selected, NavAccent, Separator ui.Color
	NavSelected, NavHover                                            ui.Color
}

func Colors(dark bool, accent ui.Color) Palette {
	t := *ui.LightTheme()
	if dark {
		t = *ui.DarkTheme()
	}
	native := systemColors(dark)
	p := Palette{Window: native.Window, Content: native.TextBackground,
		Panel: SolidSurface(native.SubtleFill, native.TextBackground), Subpanel: SolidSurface(native.Fill, native.TextBackground),
		Selected: accent.Alpha(0.14), NavAccent: ui.Hex("#000000").Mix(accent, 0.78), Separator: native.Separator,
		NavSelected: native.Selection, NavHover: native.Fill}
	if dark {
		p.Selected, p.NavAccent = accent.Alpha(0.22), ui.Hex("#ffffff").Mix(accent, 0.74)
	}
	t.Text, t.TextMuted, t.Border = native.Label, native.SecondaryLabel, native.Separator
	t.Surface, t.SurfaceHover, t.SurfacePressed = p.Panel, p.Subpanel, native.Selection
	t.Success, t.Warning, t.Danger = native.Green, native.Orange, native.Red
	t.Background, t.Accent, t.Selection = p.Window, accent, p.Selected
	t.AccentHover, t.AccentPressed = ui.Hex("#000000").Mix(accent, 0.78), ui.Hex("#000000").Mix(accent, 0.65)
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

// ChromeBackground exposes the system's frosted material without adding a
// second translucent color wash. Reading surfaces remain opaque.
func (p Palette) ChromeBackground(_ string, material bool, preferences ui.Preferences) ui.Color {
	if !material || preferences.ReduceTransparency || preferences.HighContrast {
		return p.Window
	}

	return ui.Transparent
}

// LiquidGlass is shared by the live window material and its settings preview.
func LiquidGlass(style string, window ui.Color) glass.Glass {
	if style == "tinted" {
		return glass.Glass{Style: glass.Regular, Tint: window.Alpha(0.24)}
	}
	// The stored "transparent" value is retained for compatibility. It means
	// standard untinted navigation glass, not the Clear style for media overlays.
	return glass.Glass{Style: glass.Regular}
}

type NavItem struct{ Key, Path, Icon string }
type NavSection struct {
	Key, Icon string
	Zh, En    string
	Items     []NavItem
}

var Navigation = []NavSection{
	{"work", "dashboard", "工作", "Work", []NavItem{{"dashboard", "/", "dashboard"}, {"newJob", "/jobs/new", "plus"}, {"jobs", "/jobs", "tray"}, {"live", "/live", "microphone"}}},
	{"games", "market", "打工", "Work Games", []NavItem{{"workMarket", "/work-market", "market"}}},
	{"resources", "doc", "资源", "Resources", []NavItem{{"models", "/models", "chip"}, {"templates", "/templates", "doc"}, {"members", "/members", "people"}}},
	{"companion", "pet", "伙伴", "Companion", []NavItem{{"pet", "/pet", "pet"}, {"petStore", "/pet-store", "store"}}},
	{"benefits", "gift", "福利", "Benefits", []NavItem{{"redeemKey", "/redeem-key", "key"}, {"dailyCheckIn", "/daily-check-in", "check"}, {"dailyBlindBox", "/daily-blind-box", "gift"}}},
}

func (item NavItem) Active(route string) bool {
	path, _, _ := strings.Cut(route, "?")
	switch item.Path {
	case "/work-market":
		return path == item.Path || path == "/farm" || strings.HasPrefix(path, "/work-game/")
	case "/jobs":
		return path == item.Path || (strings.HasPrefix(path, "/jobs/") && path != "/jobs/new")
	default:
		return path == item.Path
	}
}

func NavigationSection(route string) NavSection {
	for _, section := range Navigation {
		for _, item := range section.Items {
			if item.Active(route) {
				return section
			}
		}
	}
	return Navigation[0]
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
