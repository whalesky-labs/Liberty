package desktop

import (
	"math"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type orbitCard struct {
	asset          string
	x, y, z, angle float64
}

func orbitCards(pool []domain.PetBlindBoxPoolItem, elapsed, charge, converge float64) []orbitCard {
	cards := make([]orbitCard, 0, len(pool))
	for index, prize := range pool {
		ring := float64(index % 4)
		offset := float64(index) * 0.37
		radius := 2.85 + ring*.92 + math.Sin(float64(index)*1.7)*.22 + math.Sin(elapsed*.7+offset)*.18
		radius += (.28 + float64(index%5)*.055 - radius) * converge
		angle := float64(index)/float64(len(pool))*2*math.Pi + ring*.22 + elapsed*(.32+float64(index%5)*.035)*(.55+3*charge) + converge*(2.6+ring*.18)
		height := (float64(index%7-3)*.44 + math.Sin(elapsed*(.86+float64(index%6)*.1)+offset)*.22) * (1 - converge)
		cards = append(cards, orbitCard{asset: prize.Item.AssetKey, x: math.Cos(angle) * radius, y: height, z: math.Sin(angle)*radius + float64(index%5-2)*.34*(1-converge), angle: angle})
	}
	sort.SliceStable(cards, func(left, right int) bool { return cards[left].z < cards[right].z })
	return cards
}

func (v *PetView) drawScene(c *ui.Context, pool []domain.PetBlindBoxPoolItem, drawing bool) {
	now := c.Now()
	if v.blindSceneStart.IsZero() {
		v.blindSceneStart = now
	}
	elapsed := now.Sub(v.blindSceneStart).Seconds()
	charge, converge, burst, fade, progress := 0.0, 0.0, 0.0, 0.0, 0.0
	if drawing {
		age := now.Sub(v.drawStart).Seconds()
		charge = 1 - math.Pow(1-min(1, max(0, age/2.8)), 3)
		converge = float64(ui.EaseInOut(float32(min(1, max(0, (age-3.05)/1.3)))))
		burst = sceneSmoothstep(4.32, 4.85, age)
		fade = sceneSmoothstep(4.55, 4.95, age)
		progress = min(1, max(0, age/5))
	}
	if c.Preferences().ReduceMotion {
		elapsed, charge, converge, burst, fade = 0, 0, 0, 0, 0
	} else {
		c.After(time.Second / 30)
	}
	result := v.Client.LastDraw
	scene := ui.Box(c).Fill().Clip().Radius(8).Background(palette(c).Subpanel).Border(1, palette(c).Separator).Label(v.text("伙伴盲盒", "Companion blind box"))
	if result != nil && !drawing || drawing && c.Preferences().ReduceMotion {
		progress = 1
	}
	if progress > 0 {
		stop := float32(progress)
		scene.Border(2, ui.Transparent).BorderConicGradient(ui.ConicGradient{Angle: -90, Stops: []ui.GradientStop{{Color: ui.Hex("#f6c04f")}, {Color: ui.Hex("#62b878"), At: stop}, {Color: ui.Transparent, At: stop}, {Color: ui.Transparent, At: 1}}})
	}
	scene.Draw(func(p *ui.Painter, area ui.Rect) {
		centerX, centerY := area.X+area.W/2, area.Y+area.H/2
		focal := float64(area.H) / (2 * math.Tan(21*math.Pi/180))
		gold := ui.Hex("#ffd15f")
		p.FillRadialGradient(area, ui.RadialGradient{Center: ui.Point{X: .28, Y: .18}, Stops: []ui.GradientStop{{Color: gold.Alpha(.18)}, {Color: gold.Alpha(0), At: .42}}}, 8)
		p.FillRadialGradient(area, ui.RadialGradient{Center: ui.Point{X: .72, Y: .70}, Stops: []ui.GradientStop{{Color: ui.Hex("#62b878").Alpha(.12)}, {Color: ui.Transparent, At: .46}}}, 8)
		if result != nil && !drawing && result.Prize.ItemType == "none" {
			gold = ui.Hex("#d9dee8")
		}
		for index := 0; index < 240; index++ {
			seed := float64(index) * 2.399963
			angle := seed + elapsed*(.6+1.6*charge)
			radius := (1.6 + float64(index%73)*.1) * (1 - .6*converge)
			depth := focal / (10.8 - radius*math.Sin(angle))
			x := centerX + float32(radius*math.Cos(angle)*depth)
			y := centerY + float32((float64(index%67)*.1-3.35)*depth)
			size := float32(min(3, depth*.04))
			p.Fill(ui.Rect{X: x, Y: y, W: size, H: size}, gold.Alpha(.5), size/2)
		}
		energy := float32(focal/10.8) * float32(1+.16*charge+.22*burst)
		drawCore := func() { drawBlindCore(p, centerX, centerY, energy, elapsed, charge) }
		if result != nil && !drawing {
			drawCore()
			depth := focal / (10.8 - 1.25)
			scale := float32(depth * (1.06 + math.Sin(elapsed*3)*.03))
			y := centerY - float32(depth*(.18+math.Sin(elapsed*2)*.04))
			transform := ui.RotationX(float32(math.Sin(elapsed*1.4) * .08 * 180 / math.Pi)).Then(ui.RotationY(float32(math.Sin(elapsed*.8) * .18 * 180 / math.Pi)))
			p.Transform(transform, centerX, y, func() {
				p.Image(presentation.PetArt(result.Prize.AssetKey), ui.Rect{X: centerX - .71*scale, Y: y - .86*scale, W: 1.42 * scale, H: 1.72 * scale}, ui.Contain)
			})
			return
		}
		if fade >= .99 {
			drawCore()
			return
		}
		coreVisible := false
		for _, card := range orbitCards(pool, elapsed, charge, converge) {
			if card.z >= 0 && !coreVisible {
				drawCore()
				coreVisible = true
			}
			depth := focal / (10.8 - card.z)
			x, y := centerX+float32(card.x*depth), centerY-float32(card.y*depth)
			width, height := float32(.76*depth), float32(.96*depth)
			transform := ui.RotationX(-9).Then(ui.RotationY(float32(90 - card.angle*180/math.Pi))).Then(ui.Perspective(500))
			p.Transform(transform, x, y, func() {
				p.Image(presentation.PetArt(card.asset), ui.Rect{X: x - width/2, Y: y - height/2, W: width, H: height}, ui.Contain)
			})
		}
		if !coreVisible {
			drawCore()
		}
	})
}

func sceneSmoothstep(start, end, value float64) float64 {
	fraction := min(1, max(0, (value-start)/(end-start)))
	return fraction * fraction * (3 - 2*fraction)
}

func drawBlindCore(p *ui.Painter, centerX, centerY, scale float32, elapsed, charge float64) {
	gold := ui.Hex("#f6c04f")
	radius := scale * 1.48
	p.Fill(ui.Rect{X: centerX - radius, Y: centerY - radius, W: 2 * radius, H: 2 * radius}, gold.Alpha(float32(.18+.12*charge)), radius)
	sineX, cosineX := math.Sincos(elapsed * .38)
	sineY, cosineY := math.Sincos(elapsed * (.62 + .7*charge))
	var projected [12]ui.Point
	for index, vertex := range coreVertices {
		x := vertex[0]*cosineY + vertex[2]*sineY
		z := vertex[2]*cosineY - vertex[0]*sineY
		y := vertex[1]*cosineX - z*sineX
		depth := 10.8 / (10.8 - vertex[1]*sineX - z*cosineX)
		projected[index] = ui.Point{X: centerX + scale*float32(depth*x), Y: centerY - scale*float32(depth*y)}
	}
	for _, edge := range coreEdges {
		start, end := projected[edge[0]], projected[edge[1]]
		p.Line(start.X, start.Y, end.X, end.Y, 1, gold.Alpha(.3))
	}
}

var coreVertices, coreEdges = blindIcosahedron()

func blindIcosahedron() ([12][3]float64, [][2]int) {
	var vertices [12][3]float64
	var edges [][2]int
	index := 0
	radius := math.Sqrt(1 + math.Phi*math.Phi)
	for axis := range 3 {
		for _, side := range []float64{-1, 1} {
			for _, end := range []float64{-1, 1} {
				vertices[index][(axis+1)%3] = side / radius
				vertices[index][(axis+2)%3] = end * math.Phi / radius
				index++
			}
		}
	}
	for right, vertex := range vertices {
		for left, other := range vertices[:right] {
			distance := math.Hypot(math.Hypot(vertex[0]-other[0], vertex[1]-other[1]), vertex[2]-other[2])
			if math.Abs(distance-2/radius) < .000001 {
				edges = append(edges, [2]int{left, right})
			}
		}
	}
	return vertices, edges
}
