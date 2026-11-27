package presentation

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"image/png"
	"liberty.local/liberty/internal/domain"
	"testing"
	"time"
)

func TestApplicationIconIsEmbeddedAtFullWindowsResolution(t *testing.T) {
	image, err := png.Decode(bytes.NewReader(ApplicationIconPNG))
	if err != nil || image.Bounds().Dx() != 256 || image.Bounds().Dy() != 256 {
		t.Fatal("application icon must remain a valid embedded 256px PNG", err)
	}
}

func TestOriginalAnimationRetainsAllFramesAndTiming(t *testing.T) {
	if len(Mascot.frames) != 11 {
		t.Fatalf("original mascot frames: %d", len(Mascot.frames))
	}
	width, height := ui.ImageSize(Mascot.frames[0].bitmap)
	if width != 304 || height != 264 {
		t.Fatal(width, height)
	}
	elapsed := time.Duration(0)
	for _, frame := range Mascot.frames {
		at, remaining := Mascot.at(elapsed)
		if at != frame.bitmap || remaining != frame.duration {
			t.Fatal("frame boundary lost")
		}
		elapsed += frame.duration
	}
	if at, _ := Mascot.at(elapsed); at != Mascot.frames[0].bitmap {
		t.Fatal("original infinite loop lost")
	}
	for _, cut := range []int{0, 11, 20, len(mascotBytes) - 1} {
		if _, err := decodeAnimation(mascotBytes[:cut]); err == nil {
			t.Fatal("truncated animation accepted")
		}
	}
}
func TestThemeAndAccentContrastFollowAppearancePreferences(t *testing.T) {
	p := domain.DefaultUIPreferences()
	p.ThemeMode = "dark"
	p.AccentColor = "#f0e0a0"
	got := PreferenceColors(false, p)
	if !got.Theme.Dark || got.Content != systemColors(true).TextBackground || got.Theme.AccentText != ui.Hex("#14151a") {
		t.Fatal("dark/custom accent preferences lost")
	}
	p.ThemeMode = "light"
	p.AccentColor = "invalid"
	got = PreferenceColors(true, p)
	if got.Theme.Dark || got.Theme.Accent != ui.Hex("#2f6dff") {
		t.Fatal("invalid appearance cannot crash rendering")
	}
}
func TestEveryOriginalNavigationItemHasBothLanguagesAndIcon(t *testing.T) {
	for _, section := range Navigation {
		for _, item := range section.Items {
			for _, locale := range []string{"zh-CN", "en-US"} {
				if Message(locale, "nav."+item.Key) == "" {
					t.Fatal("missing nav text")
				}
			}
			if Icon(item.Icon) == nil {
				t.Fatal("missing original icon", item.Icon)
			}
		}
	}
}
