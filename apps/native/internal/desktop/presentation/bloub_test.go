package presentation

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Render the original component's SVG masks, matrices and user-space gradients
// as an independent oracle for the native painter adapter.
func bloubReferenceSVG(f bloubFrame, paper, ink string) string {
	var b strings.Builder
	fmt.Fprint(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="316" height="316" viewBox="-158 -158 316 316"><defs><mask id="body" maskUnits="userSpaceOnUse" x="-158" y="-158" width="316" height="316">`)
	fmt.Fprintf(&b, `<path d="%s" fill="white"/>`, f.BodyPath)
	for _, e := range f.Eyes {
		fmt.Fprintf(&b, `<path d="%s" transform="%s" opacity="%g" fill="black"/>`, e.D, e.Matrix, e.Alpha)
	}
	if d := f.Notch; d != nil {
		fmt.Fprintf(&b, `<circle cx="%g" cy="%g" r="%g" fill="black"/>`, d.X, d.Y, d.R)
	}
	fmt.Fprint(&b, `</mask>`)
	for i, a := range f.Arcs {
		fmt.Fprintf(&b, `<linearGradient id="g%d" gradientUnits="userSpaceOnUse" x1="%g" y1="%g" x2="%g" y2="%g">`, i, a.Grad.X1, a.Grad.Y1, a.Grad.X2, a.Grad.Y2)
		for j, stop := range a.Grad.Stops {
			fmt.Fprintf(&b, `<stop offset="%g" stop-color="%s"/>`, float64(j)/float64(len(a.Grad.Stops)-1), stop)
		}
		fmt.Fprint(&b, `</linearGradient>`)
	}
	fmt.Fprint(&b, `</defs>`)
	for i, a := range f.Arcs {
		fmt.Fprintf(&b, `<path fill="none" d="%s" stroke="url(#g%d)" stroke-width="%g" opacity="%g" stroke-linecap="round"/>`, a.Back, i, a.Width, a.Opacity)
	}
	dots := func() {
		for _, d := range f.Dots {
			fill := ink
			if d.Color != "" {
				fill = d.Color
			} else if d.Depth != nil {
				color := ui.Hex(paper).Mix(ui.Hex(ink), *d.Depth)
				fill = fmt.Sprintf("#%02x%02x%02x", color.R, color.G, color.B)
			}
			if d.D != "" {
				fmt.Fprintf(&b, `<path d="%s" transform="translate(%g %g) rotate(%g) scale(100)" fill="%s" opacity="%g"/>`, d.D, d.X, d.Y, d.Rot, fill, d.Opacity)
			} else {
				fmt.Fprintf(&b, `<circle cx="%g" cy="%g" r="%g" fill="%s" opacity="%g"/>`, d.X, d.Y, d.R, fill, d.Opacity)
			}
		}
	}
	if f.DotsBehind {
		dots()
	}
	fmt.Fprintf(&b, `<g opacity="%g"><path d="%s" fill="%s"/><g mask="url(#body)"><rect x="-158" y="-158" width="316" height="316" fill="%s"/></g></g>`, f.BodyAlpha, f.BodyPath, paper, ink)
	if !f.DotsBehind {
		dots()
	}
	if d := f.Notif; d != nil {
		fmt.Fprintf(&b, `<circle cx="%g" cy="%g" r="%g" fill="#2496e8"/>`, d.X, d.Y, d.R)
	}
	for i, a := range f.Arcs {
		fmt.Fprintf(&b, `<path fill="none" d="%s" stroke="url(#g%d)" stroke-width="%g" opacity="%g" stroke-linecap="round"/>`, a.Front, i, a.Width, a.Opacity)
	}
	fmt.Fprint(&b, `</svg>`)
	return b.String()
}

func TestBloubNativeFramesMatchOriginalSVGComponent(t *testing.T) {
	z, err := gzip.NewReader(bytes.NewReader(bloubFrames))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var raw bloubData
	if err := json.NewDecoder(z).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		clip string
		at   float64
	}{{"idle", 0}, {"idle", 1.48}, {"idle", 5}, {"comet", .8}, {"comet", 1.15}, {"comet", 2.3}, {"thinking", 1.1}, {"paused", 0}, {"failed", 0}}
	for _, block := range raw.Clips["showcase"].Sequence {
		for _, offset := range []float64{.1, float64(block.Frames) / 60} {
			cases = append(cases, struct {
				clip string
				at   float64
			}{"showcase", float64(block.FirstFrame)/30 + offset})
		}
	}
	board := image.NewRGBA(image.Rect(0, 0, 316*5, 316*((len(cases)+4)/5)))
	for i, tc := range cases {
		t.Run(fmt.Sprint(tc.clip, tc.at), func(t *testing.T) {
			clip := raw.Clips[tc.clip]
			f := clip.Frames[int(tc.at*float64(clip.FPS))%len(clip.Frames)]
			svg := ui.MustParseSVG([]byte(bloubReferenceSVG(f, "#ffffff", "#0a0a0c")))
			want := image.NewRGBA(image.Rect(0, 0, 316, 316))
			draw.Draw(want, want.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
			draw.Draw(want, want.Bounds(), svg.Image(316, 316, nil), image.Point{}, draw.Over)
			var player BloubPlayer
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Box(c).Size(316, 316).Background(ui.Hex("#ffffff")).Draw(func(p *ui.Painter, r ui.Rect) {
					player.Draw(p, r, tc.clip, time.Duration(tc.at*float64(time.Second)), ui.Hex("#0a0a0c"), ui.Hex("#ffffff"), 0)
				})
			}, 316, 316)
			got := tester.Image()
			var errorSum float64
			for index, value := range got.Pix {
				errorSum += math.Abs(float64(value) - float64(want.Pix[index]))
			}
			// Different rasterizers antialias edge pixels differently. A whole
			// misplaced eye, missing arc or wrong mask exceeds this image budget.
			mae := errorSum / float64(len(got.Pix))
			if mae > .5 {
				t.Errorf("native rendering differs from upstream SVG: mean channel error %.3f", mae)
			}
			t.Logf("mean channel error %.3f / 255", mae)
			draw.Draw(board, image.Rect(i%5*316, i/5*316, (i%5+1)*316, (i/5+1)*316), got, image.Point{}, draw.Src)
		})
	}
	if directory := os.Getenv("LIBERTY_NATIVE_LIVE_SCREENSHOT_DIR"); filepath.IsAbs(directory) {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(directory, "bloub-original-states.png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, board); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBloubShowcaseIncludesEveryUpstreamStateAndLoops(t *testing.T) {
	data := readBloub()
	clip := data.Clips["showcase"]
	expected := []string{"idle", "thinking", "wink", "wide", "alert", "notify", "exclaim", "sleep", "egg", "hexagon", "play", "orbit", "burst", "comet"}
	if len(clip.Sequence) != len(expected) || clip.FPS != 30 {
		t.Fatal("incomplete original animation sequence")
	}
	frames := 0
	for i, block := range clip.Sequence {
		if block.State != expected[i] || block.FirstFrame != frames || block.Frames <= 0 {
			t.Fatal("incorrect animation order or timing", block)
		}
		frames += block.Frames
	}
	if frames != len(clip.Frames) || frames != 936 {
		t.Fatal("incorrect full animation duration", frames)
	}
	var player BloubPlayer
	elapsed := time.Duration(0)
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Size(240, 240).Background(ui.Hex("#ffffff")).Draw(func(p *ui.Painter, r ui.Rect) {
			player.Draw(p, r, "showcase", elapsed, ui.Hex("#0a0a0c"), ui.Hex("#ffffff"), 0)
		})
	}, 240, 240)
	first := append([]byte(nil), tester.Image().Pix...)
	elapsed = time.Duration(frames) * time.Second / 30
	tester.Frame()
	if !bytes.Equal(first, tester.Image().Pix) {
		t.Fatal("showcase does not repeat after the full sequence")
	}
}

func BenchmarkBloubNativeFrame(b *testing.B) {
	readBloub()
	elapsed := time.Duration(0)
	var player BloubPlayer
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Size(240, 240).Draw(func(p *ui.Painter, r ui.Rect) {
			player.Draw(p, r, "idle", elapsed, ui.Hex("#0a0a0c"), ui.Hex("#ffffff"), 0)
		})
	}, 240, 240)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		elapsed = time.Duration(i%360) * time.Second / 30
		tester.Frame()
	}
}
