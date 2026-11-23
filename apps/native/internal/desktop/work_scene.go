package desktop

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// workScene uses the same 1536×1024 world, cover scale, edge bleed and pan
// limits as the original farm/work scenes. Artwork and hit targets always
// share this transform, including after resizing, zooming and dragging.
type workScene struct {
	width, height, scale, x, y   float32
	adjusted, dragging           bool
	dragX, dragY, startX, startY float32
	header, hud                  ui.Rect
	focusArea                    *[4]float32
}

func workCoverScale(width, height float32) float32 {
	if width <= 0 || height <= 0 {
		return .6
	}
	return max((width+24)/workWorldW, (height+24)/workWorldH)
}

func (s *workScene) resize(width, height float32) {
	if width <= 0 || height <= 0 || s.width == width && s.height == height {
		return
	}
	s.width, s.height = width, height
	if !s.adjusted {
		s.scale = workCoverScale(width, height)
		s.x, s.y = (width-workWorldW*s.scale)/2, (height-workWorldH*s.scale)/2
	} else {
		s.scale = min(max(s.scale, workCoverScale(width, height)), s.maxScale())
		s.clamp()
	}
}

func (s *workScene) maxScale() float32 {
	minimum := workCoverScale(s.width, s.height)
	return max(1.12, minimum*1.45, minimum+.28)
}

func (s *workScene) clamp() {
	w, h := workWorldW*s.scale, workWorldH*s.scale
	if w <= s.width {
		s.x = (s.width - w) / 2
	} else {
		s.x = min(0, max(s.width-w, s.x))
	}
	if h <= s.height {
		s.y = (s.height - h) / 2
	} else {
		s.y = min(0, max(s.height-h, s.y))
	}
}

func (s *workScene) zoom(scale, x, y float32) {
	if s.scale <= 0 || math.IsNaN(float64(scale)) || math.IsInf(float64(scale), 0) {
		return
	}
	s.adjusted = true
	s.focusArea = nil
	scale = min(s.maxScale(), max(workCoverScale(s.width, s.height), scale))
	ratio := scale / s.scale
	s.x, s.y = x-(x-s.x)*ratio, y-(y-s.y)*ratio
	s.scale = scale
	s.clamp()
}

func (s *workScene) focus(area [4]float32) {
	s.adjusted = true
	s.focusArea = &area
	centerX, centerY := area[0]+area[2]/2, area[1]+area[3]/2
	s.x, s.y = s.width/2-centerX*s.scale, s.height/2-centerY*s.scale
	s.clamp()
	if s.hud.W <= 0 || s.hud.H <= 0 {
		return
	}
	projectedX, projectedY := s.x+centerX*s.scale, s.y+centerY*s.scale
	if projectedY >= s.header.Y+s.header.H+8 && (projectedX+8 <= s.hud.X || projectedX-8 >= s.hud.X+s.hud.W || projectedY+8 <= s.hud.Y || projectedY-8 >= s.hud.Y+s.hud.H) {
		return
	}
	viewport := ui.Rect{W: s.width, H: s.height}
	if s.hud.W > 0 && s.hud.H > 0 {
		top := s.header.Y + s.header.H + 12
		viewport = ui.Rect{X: 12, Y: top, W: max(0, s.width-24), H: max(0, s.hud.Y-top-12)}
		right := ui.Rect{X: s.hud.X + s.hud.W + 12, Y: top, H: max(0, s.height-top-12)}
		right.W = max(0, s.width-right.X-12)
		if right.W*right.H > viewport.W*viewport.H {
			viewport = right
		}
	}
	targetX, targetY := viewport.X+viewport.W/2, viewport.Y+viewport.H/2
	if centerX > 0 && centerX < workWorldW && centerY > 0 && centerY < workWorldH {
		minimum := max(targetX/centerX, (s.width-targetX)/(workWorldW-centerX), targetY/centerY, (s.height-targetY)/(workWorldH-centerY))
		s.scale = min(s.maxScale(), max(s.scale, minimum))
	}
	s.x = targetX - centerX*s.scale
	s.y = targetY - centerY*s.scale
	s.clamp()
}

func (scene *workScene) overlays(root, header, hud ui.Rect) bool {
	if root.W <= 0 || root.H <= 0 || header.H <= 0 || hud.H <= 0 {
		return false
	}
	header.X, header.Y = header.X-root.X, header.Y-root.Y
	hud.X, hud.Y = hud.X-root.X, hud.Y-root.Y
	if scene.header == header && scene.hud == hud {
		return false
	}
	scene.header, scene.hud = header, hud
	if scene.focusArea != nil {
		scene.focus(*scene.focusArea)
		return true
	}
	return false
}

func (scene *workScene) hotspotInput(area [4]float32) func(ui.InputEvent) bool {
	return func(event ui.InputEvent) bool {
		if workIgnoreRepeat(event) {
			return true
		}
		switch event.Kind {
		case ui.InputKeyDown:
			return scene.input(event)
		case ui.InputScroll, ui.InputZoom:
			event.X = scene.x + (area[0]+event.X)*scene.scale
			event.Y = scene.y + (area[1]+event.Y)*scene.scale
			event.DX, event.DY = event.DX*scene.scale, event.DY*scene.scale
			return scene.input(event)
		}
		return false
	}
}

func (s *workScene) input(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputKeyDown:
		if ev.Mods != 0 && ev.Mods != ui.Shift {
			return false
		}
		switch ev.Key {
		case ui.KeyHome:
			s.adjusted = false
			s.focusArea = nil
			s.scale = workCoverScale(s.width, s.height)
			s.x, s.y = (s.width-workWorldW*s.scale)/2, (s.height-workWorldH*s.scale)/2
			return true
		case ui.KeyEqual:
			s.zoom(s.scale*1.12, s.width/2, s.height/2)
			return true
		case ui.KeyMinus:
			s.zoom(s.scale/1.12, s.width/2, s.height/2)
			return true
		case ui.KeyLeft:
			s.x += 40
		case ui.KeyRight:
			s.x -= 40
		case ui.KeyUp:
			s.y += 40
		case ui.KeyDown:
			s.y -= 40
		default:
			return false
		}
		s.adjusted = true
		s.focusArea = nil
		s.clamp()
		return true
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		s.dragging = true
		s.dragX, s.dragY, s.startX, s.startY = ev.X, ev.Y, s.x, s.y
		return true
	case ui.InputPointerMove:
		if !s.dragging {
			return false
		}
		s.adjusted = true
		s.focusArea = nil
		s.x, s.y = s.startX+ev.X-s.dragX, s.startY+ev.Y-s.dragY
		s.clamp()
		return true
	case ui.InputPointerUp:
		if !s.dragging {
			return false
		}
		s.dragging = false
		return true
	case ui.InputScroll:
		s.zoom(s.scale-ev.DY*.0008, ev.X, ev.Y)
		return true
	case ui.InputZoom:
		if ev.Scale > 0 {
			s.zoom(s.scale*ev.Scale, ev.X, ev.Y)
		}
		return true
	}
	return false
}

func (s *workScene) world(c *ui.Context, root ui.Element, locale string, render func()) {
	r := root.LayoutBounds()
	if r.W > 0 && r.H > 0 {
		s.resize(r.W, r.H)
	} else if s.width == 0 {
		s.resize(960, 560)
		c.After(0)
	}
	ui.Box(c).Absolute().Fill().Focusable().Role(ui.RoleGroup).Label(workText(locale, "Map scene", "地图场景")).Description(workText(locale, "Arrow keys pan; plus and minus zoom; Home resets the map.", "方向键移动地图，加减号缩放，Home 恢复初始视角。")).HandleInput(s.input)
	ui.Box(c).Absolute().Left(s.x).Top(s.y).Size(workWorldW, workWorldH).
		TransformOrigin(0, 0).Scale(s.scale, s.scale).PassThrough().Children(render)
}
