package presentation

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

//go:embed bloub_frames_generated.json.gz
var bloubFrames []byte

type bloubFrame struct {
	BodyPath     string
	BodyAlpha    float32
	Eyes         []bloubEye
	Dots         []bloubDot
	DotsBehind   bool
	Arcs         []bloubArc
	Notif, Notch *bloubDot
	bodyPoints   []float32
}
type bloubEye struct {
	D, Matrix string
	Alpha     float32
	path      *ui.Path
	transform ui.Transform
}
type bloubDot struct {
	X, Y, R, Opacity, Rot float32
	D, Color              string
	Depth                 *float32
	path                  *ui.Path
}
type bloubArc struct {
	Front, Back    string
	Width, Opacity float32
	Grad           struct {
		X1, Y1, X2, Y2 float32
		Stops          []string
	}
	front, back                 *ui.Path
	frontGradient, backGradient ui.LinearGradient
}
type bloubClip struct {
	FPS      int
	Frames   []bloubFrame
	Sequence []bloubSequenceBlock
}
type bloubSequenceBlock struct {
	State              string
	FirstFrame, Frames int
}
type bloubData struct {
	Version           int
	Source            string
	ViewBox, Radius   float32
	NotificationColor string
	Clips             map[string]bloubClip
}

var readBloub = sync.OnceValue(func() *bloubData {
	z, err := gzip.NewReader(bytes.NewReader(bloubFrames))
	if err != nil {
		panic(err)
	}
	defer z.Close()
	var data bloubData
	if err = json.NewDecoder(io.LimitReader(z, 24<<20)).Decode(&data); err != nil {
		panic(err)
	}
	if data.Version != 2 || data.ViewBox != 316 || data.Radius != 100 || len(data.Clips) != 6 {
		panic("invalid embedded bloub animation")
	}
	paths := map[string]*ui.Path{}
	parse := func(d string) *ui.Path {
		if p := paths[d]; p != nil {
			return p
		}
		p := ui.MustParsePath(d)
		paths[d] = p
		return p
	}
	for _, clip := range data.Clips {
		if len(clip.Frames) == 0 || (clip.FPS != 0 && clip.FPS != 30) {
			panic("invalid embedded bloub clip")
		}
		for i := range clip.Frames {
			f := &clip.Frames[i]
			// The upstream closedPath emits M followed by cubic segments and Z.
			// Keep coordinates too, so the large body can be painted directly in
			// window space instead of CPU-sampling a transformed glyph per pixel.
			for _, number := range strings.Fields(strings.NewReplacer("M", " ", "C", " ", "Z", " ").Replace(f.BodyPath)) {
				value, err := strconv.ParseFloat(number, 32)
				if err != nil {
					panic(err)
				}
				f.bodyPoints = append(f.bodyPoints, float32(value))
			}
			if len(f.bodyPoints) < 8 || (len(f.bodyPoints)-2)%6 != 0 {
				panic("invalid bloub body path")
			}
			f.BodyPath = ""
			for j := range f.Eyes {
				e := &f.Eyes[j]
				e.path = parse(e.D)
				e.transform, err = bloubMatrix(e.Matrix)
				if err != nil {
					panic(err)
				}
				e.D, e.Matrix = "", ""
			}
			for j := range f.Arcs {
				a := &f.Arcs[j]
				a.front, a.back = parse(a.Front), parse(a.Back)
				a.frontGradient, a.backGradient = a.gradient(a.front), a.gradient(a.back)
				a.Front, a.Back, a.Grad.Stops = "", "", nil
			}
			for j := range f.Dots {
				d := &f.Dots[j]
				if d.D != "" {
					key := "dot:" + d.D
					d.path = paths[key]
					if d.path == nil {
						// Upstream non-circular dots are polygons in radius units.
						// Scale before rasterization, or subpixel points collapse
						// to zero when MyGo creates their vector mask.
						numbers := strings.Fields(strings.NewReplacer("M", " ", "L", " ", "Z", " ").Replace(d.D))
						if len(numbers) < 6 || len(numbers)%2 != 0 {
							panic("invalid bloub dot polygon")
						}
						d.path = &ui.Path{}
						for k := 0; k < len(numbers); k += 2 {
							x, xErr := strconv.ParseFloat(numbers[k], 32)
							y, yErr := strconv.ParseFloat(numbers[k+1], 32)
							if xErr != nil || yErr != nil {
								panic("invalid bloub dot coordinate")
							}
							if k == 0 {
								d.path.MoveTo(float32(x)*data.Radius, float32(y)*data.Radius)
							} else {
								d.path.LineTo(float32(x)*data.Radius, float32(y)*data.Radius)
							}
						}
						d.path.Close()
						paths[key] = d.path
					}
					d.D = ""
				}
			}
		}
	}
	return &data
})

// QR decomposition maps SVG's affine eye matrices to MyGo's native transforms.
// Coordinates, capsules and timing remain those sampled from the upstream engine.
func bloubMatrix(value string) (ui.Transform, error) {
	parts := strings.Fields(strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(value, "matrix("), ")"), ",", " "))
	if len(parts) != 6 {
		return ui.Transform{}, fmt.Errorf("invalid bloub eye matrix")
	}
	var m [6]float64
	for i, part := range parts {
		v, err := strconv.ParseFloat(part, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return ui.Transform{}, fmt.Errorf("invalid bloub eye coordinate")
		}
		m[i] = v
	}
	sx := math.Hypot(m[0], m[1])
	det := m[0]*m[3] - m[1]*m[2]
	if sx < 1e-8 || math.Abs(det) < 1e-8 {
		return ui.Scaling(0, 0), nil
	}
	sy, skew := det/sx, math.Atan((m[0]*m[2]+m[1]*m[3])/det)*180/math.Pi
	rotation := math.Atan2(m[1], m[0]) * 180 / math.Pi
	return ui.Scaling(float32(sx), float32(sy)).Then(ui.Skewing(float32(skew), 0)).
		Then(ui.Rotation(float32(rotation))).Then(ui.Translation(float32(m[4]), float32(m[5]))), nil
}

// BloubPlayer draws original component frames without a web runtime or texture cache.
// All geometry, layer ordering and colours come from the upstream engine.
type BloubPlayer struct{ body ui.Path }

func (player *BloubPlayer) Draw(p *ui.Painter, r ui.Rect, clipName string, elapsed time.Duration, ink, paper ui.Color, gain float32) {
	data := readBloub()
	clip, ok := data.Clips[clipName]
	if !ok {
		clip = data.Clips["idle"]
	}
	index := int(max(0, elapsed).Seconds()*float64(clip.FPS)) % len(clip.Frames)
	f := &clip.Frames[index]
	size := min(r.W, r.H)
	scale := size / data.ViewBox
	sx, sy, cx, cy := scale*(1+.035*gain), scale*(1+.024*gain), r.X+r.W/2, r.Y+r.H/2-size*.018*gain
	pose := ui.Scaling(sx, sy).Then(ui.Translation(cx, cy))
	points := f.bodyPoints
	body := player.body.Reset().MoveTo(cx+sx*points[0], cy+sy*points[1])
	for i := 2; i < len(points); i += 6 {
		body.CubeTo(cx+sx*points[i], cy+sy*points[i+1], cx+sx*points[i+2], cy+sy*points[i+3], cx+sx*points[i+4], cy+sy*points[i+5])
	}
	body.Close()
	p.Transform(pose, 0, 0, func() {
		for i := range f.Arcs {
			f.Arcs[i].draw(p, false)
		}
		if f.DotsBehind {
			for i := range f.Dots {
				f.Dots[i].draw(p, ink, paper)
			}
		}
	})
	// Paper is only needed to occlude the back half of comet arcs; elsewhere
	// the view already supplies the paper behind the opaque body.
	if len(f.Arcs) > 0 || f.DotsBehind {
		p.FillPath(body, paper.Alpha(f.BodyAlpha))
	}
	p.FillPath(body, ink.Alpha(f.BodyAlpha))
	p.ClipPath(body, func() {
		p.Transform(pose, 0, 0, func() {
			for i := range f.Eyes {
				e := &f.Eyes[i]
				if e.Alpha > 0 {
					p.Transform(e.transform, 0, 0, func() { p.FillPath(e.path, paper.Alpha(e.Alpha*f.BodyAlpha)) })
				}
			}
			if d := f.Notch; d != nil {
				p.Fill(ui.Rect{X: d.X - d.R, Y: d.Y - d.R, W: d.R * 2, H: d.R * 2}, paper, d.R)
			}
		})
	})
	p.Transform(pose, 0, 0, func() {
		if !f.DotsBehind {
			for i := range f.Dots {
				f.Dots[i].draw(p, ink, paper)
			}
		}
		if d := f.Notif; d != nil {
			p.Fill(ui.Rect{X: d.X - d.R, Y: d.Y - d.R, W: d.R * 2, H: d.R * 2}, ui.Hex(data.NotificationColor), d.R)
		}
		for i := range f.Arcs {
			f.Arcs[i].draw(p, true)
		}
	})
}

func (d *bloubDot) draw(p *ui.Painter, ink, paper ui.Color) {
	color := ink
	if d.Color != "" {
		color = ui.Hex(d.Color)
	} else if d.Depth != nil {
		color = paper.Mix(ink, *d.Depth)
	}
	color = color.Alpha(d.Opacity)
	if d.path != nil {
		pose := ui.Rotation(d.Rot).Then(ui.Translation(d.X, d.Y))
		p.Transform(pose, 0, 0, func() { p.FillPath(d.path, color) })
	} else {
		p.Fill(ui.Rect{X: d.X - d.R, Y: d.Y - d.R, W: d.R * 2, H: d.R * 2}, color, d.R)
	}
}

func (a *bloubArc) draw(p *ui.Painter, front bool) {
	if a.Opacity <= 0 {
		return
	}
	path, gradient := a.back, a.backGradient
	if front {
		path, gradient = a.front, a.frontGradient
	}
	p.StrokePathGradient(path, a.Width, gradient)
}

func (a *bloubArc) gradient(path *ui.Path) ui.LinearGradient {
	// Map SVG user-space gradient positions onto MyGo's bounding-box line.
	b := path.Bounds()
	dx, dy := a.Grad.X2-a.Grad.X1, a.Grad.Y2-a.Grad.Y1
	length := float32(math.Hypot(float64(dx), float64(dy)))
	if length == 0 {
		return ui.LinearGradient{}
	}
	ux, uy := dx/length, dy/length
	span := float32(math.Abs(float64(ux)))*b.W + float32(math.Abs(float64(uy)))*b.H
	if span == 0 {
		return ui.LinearGradient{}
	}
	center := (b.X+b.W/2)*ux + (b.Y+b.H/2)*uy
	start := (a.Grad.X1*ux + a.Grad.Y1*uy - center + span/2) / span
	stops := make([]ui.GradientStop, len(a.Grad.Stops))
	for i, color := range a.Grad.Stops {
		stops[i] = ui.GradientStop{Color: ui.Hex(color).Alpha(a.Opacity), At: start + length/span*float32(i)/float32(max(1, len(stops)-1))}
	}
	return ui.LinearGradient{Angle: float32(math.Atan2(float64(dx), float64(-dy)) * 180 / math.Pi), Stops: stops}
}
