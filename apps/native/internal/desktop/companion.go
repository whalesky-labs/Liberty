package desktop

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnosticlog"
)

const companionWidth, companionHeight = 320, 220

var companionSprite = mygo.Rectangle{X: 86, Y: 68, Width: 148, Height: 148}

const companionGrowthDuration = 3 * time.Second

type companionGrowth struct {
	baseline, latest time.Time
	seenAtLatest     map[string]struct{}
	started          time.Time
	value            int64
}

func (growth *companionGrowth) observe(now time.Time, events []domain.PetEventLedgerEntry) {
	if growth.baseline.IsZero() {
		if events != nil {
			growth.baseline = now
		}
		return
	}
	var selected int64
	for _, event := range events {
		if event.EventType != "store_food" || event.EventValue <= 0 || event.ID == "" {
			continue
		}
		when, err := time.Parse(time.RFC3339Nano, event.EventTime)
		if err != nil || !when.After(growth.baseline) || when.After(now) || when.Before(growth.latest) {
			continue
		}
		if when.After(growth.latest) {
			growth.latest = when
			clear(growth.seenAtLatest)
			selected = 0
		}
		if _, seen := growth.seenAtLatest[event.ID]; seen {
			continue
		}
		if growth.seenAtLatest == nil {
			growth.seenAtLatest = make(map[string]struct{})
		}
		growth.seenAtLatest[event.ID] = struct{}{}
		if selected == 0 {
			selected = event.EventValue
		}
	}
	if selected > 0 {
		growth.started, growth.value = now, selected
	}
}

func (growth *companionGrowth) render(c *ui.Context) {
	remaining := growth.started.Add(companionGrowthDuration).Sub(c.Now())
	if growth.value == 0 || remaining <= 0 {
		return
	}
	top, opacity := float32(102), float32(1)
	if !c.Preferences().ReduceMotion {
		progress := 1 - float32(remaining)/float32(companionGrowthDuration)
		top -= 34 * progress
		opacity = min(1, (1-progress)/.28)
		c.After(min(remaining, 100*time.Millisecond))
	} else {
		c.After(remaining)
	}
	ui.Text(c.Key("companion-growth"), fmt.Sprintf("+%d", growth.value)).Absolute().Left(196).Top(top).Width(78).
		TextAlign(ui.Center).FontSize(18).Bold().TextColor(ui.Hex("#ff6138")).TextShadow(0, 1, 2, ui.RGBA(0, 0, 0, .65)).Opacity(opacity).PassThrough()
}

type companionPositionWindow interface {
	Position() (int, int)
	SetPosition(int, int)
}

type companionPointer struct {
	window            companionPositionWindow
	remember          func(mygo.Point)
	tap               func()
	record            func(diagnosticlog.Event, diagnosticlog.Instance)
	instance          diagnosticlog.Instance
	pressed, dragging bool
	origin            mygo.Point
	x, y              float32
}

func (pointer *companionPointer) log(event diagnosticlog.Event) {
	if pointer.record != nil {
		pointer.record(event, pointer.instance)
	}
}

func (pointer *companionPointer) input(event ui.InputEvent) bool {
	switch event.Kind {
	case ui.InputPointerDown:
		if event.Button != 0 {
			return false
		}
		pointer.pressed, pointer.dragging = true, false
		pointer.origin.X, pointer.origin.Y = pointer.window.Position()
		pointer.x, pointer.y = event.X, event.Y
		pointer.log(diagnosticlog.PointerPressed)
	case ui.InputPointerMove:
		if !pointer.pressed {
			return false
		}
		// The event is relative to the sprite in the window's current position.
		// Keep the original grab point under it, using the event that arrived
		// rather than a live cursor sample that may already be ahead of it.
		x, y := pointer.window.Position()
		dx := float64(x-pointer.origin.X) + float64(event.X-pointer.x)
		dy := float64(y-pointer.origin.Y) + float64(event.Y-pointer.y)
		if !pointer.dragging && math.Hypot(dx, dy) < 4 {
			return true
		}
		if !pointer.dragging {
			pointer.log(diagnosticlog.DragStarted)
		}
		pointer.dragging = true
		pointer.window.SetPosition(pointer.origin.X+int(math.Round(dx)), pointer.origin.Y+int(math.Round(dy)))
	case ui.InputPointerUp:
		if !pointer.pressed || (event.Button != 0 && !event.Canceled) {
			return false
		}
		dragged := pointer.dragging
		pointer.pressed, pointer.dragging = false, false
		if event.Canceled {
			pointer.log(diagnosticlog.PointerCancelled)
			return true
		}
		if dragged {
			pointer.log(diagnosticlog.DragFinished)
			if pointer.remember != nil {
				x, y := pointer.window.Position()
				pointer.remember(mygo.Point{X: x, Y: y})
			}
		} else if pointer.tap != nil {
			pointer.log(diagnosticlog.Tapped)
			pointer.tap()
		}
	default:
		return false
	}
	return true
}

func companionPosition(settings domain.PetSettings, displays []mygo.Display, extra int) mygo.Point {
	if len(displays) == 0 {
		return mygo.Point{X: 80, Y: 80}
	}
	primary := displays[0].WorkArea
	for _, display := range displays {
		area := display.WorkArea
		if display.Primary {
			primary = area
		}
		if extra == 0 && settings.LastWindowX != nil && settings.LastWindowY != nil {
			x, y := *settings.LastWindowX, *settings.LastWindowY
			if x >= float64(area.X) && x <= float64(area.X+area.Width-companionWidth) && y >= float64(area.Y) && y <= float64(area.Y+area.Height-companionHeight) {
				return mygo.Point{X: int(x), Y: int(y)}
			}
		}
	}
	x, y := primary.X+primary.Width-companionWidth-24, primary.Y+primary.Height-companionHeight-24
	if extra > 0 {
		x -= 168 * (1 + (extra-1)%3)
		y -= 92 * ((extra - 1) / 3 % 3)
	}
	return mygo.Point{X: max(primary.X, x), Y: max(primary.Y, y)}
}

type companionWindow interface {
	Destroy()
	IsVisible() bool
	IsAlwaysOnTop() bool
	SetAlwaysOnTop(bool)
	Invalidate()
	OnClosed(func()) func()
	SetInputRegion([]mygo.Rectangle)
	ShowInactive()
}

type companionWindows struct {
	view        *PetView
	windows     map[int]companionWindow
	create      func(int, domain.PetSettings) companionWindow
	record      func(diagnosticlog.Event, diagnosticlog.Instance)
	nextExtra   int
	awaitEnable bool
	positions   chan mygo.Point
	done        chan struct{}
}

func newCompanions(view *PetView, save func(context.Context, int, int) error, recorders ...func(diagnosticlog.Event, diagnosticlog.Instance)) *companionWindows {
	record := func(diagnosticlog.Event, diagnosticlog.Instance) {}
	if len(recorders) > 0 && recorders[0] != nil {
		record = recorders[0]
	}
	companions := &companionWindows{view: view, windows: make(map[int]companionWindow), record: record, positions: make(chan mygo.Point, 1), done: make(chan struct{})}
	companions.create = companions.createNativeWindow
	go func() {
		defer close(companions.done)
		for point := range companions.positions {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := save(ctx, point.X, point.Y); err != nil {
				record(diagnosticlog.PositionSaveFailed, diagnosticlog.Primary)
			} else {
				record(diagnosticlog.PositionSaved, diagnosticlog.Primary)
			}
			cancel()
		}
	}()
	return companions
}

func (companions *companionWindows) remember(point mygo.Point) {
	select {
	case <-companions.positions:
	default:
	}
	companions.positions <- point
}

func (companions *companionWindows) openExtra() {
	settings := companions.view.Client.Settings
	if settings == nil || companions.view.Client.Busy {
		return
	}
	if !settings.DesktopEnabled {
		companions.awaitEnable = true
		value := *settings
		value.DesktopEnabled = true
		companions.view.Client.SettingsSave(value)
		return
	}
	companions.nextExtra++
	companions.open(companions.nextExtra, *settings)
}

func (companions *companionWindows) sync() {
	settings := companions.view.Client.Settings
	if settings == nil {
		return
	}
	if !settings.DesktopEnabled {
		if !companions.view.Client.Busy {
			companions.awaitEnable = false
		}
		companions.hide()
		return
	}
	if companions.windows[0] == nil {
		companions.open(0, *settings)
	}
	if companions.awaitEnable {
		companions.awaitEnable = false
		companions.nextExtra++
		companions.open(companions.nextExtra, *settings)
	}
	for _, window := range companions.windows {
		if window.IsAlwaysOnTop() != settings.AlwaysOnTop {
			window.SetAlwaysOnTop(settings.AlwaysOnTop)
		}
		window.Invalidate()
	}
}

func (companions *companionWindows) hide() {
	for _, window := range companions.windows {
		window.Destroy()
	}
}

func (companions *companionWindows) visibleCount() int {
	count := 0
	for _, window := range companions.windows {
		if window.IsVisible() {
			count++
		}
	}
	return count
}

func (companions *companionWindows) invalidate() {
	for _, window := range companions.windows {
		window.Invalidate()
	}
}

func (companions *companionWindows) close() {
	companions.hide()
	close(companions.positions)
	<-companions.done
}

func (companions *companionWindows) open(instance int, settings domain.PetSettings) {
	window := companions.create(instance, settings)
	record := companions.record
	logInstance := diagnosticlog.Primary
	if instance != 0 {
		logInstance = diagnosticlog.Extra
	}
	companions.windows[instance] = window
	window.OnClosed(func() {
		record(diagnosticlog.WindowClosed, logInstance)
		delete(companions.windows, instance)
	})
	window.SetInputRegion([]mygo.Rectangle{companionSprite})
	window.ShowInactive()
	record(diagnosticlog.WindowShown, logInstance)
}

func (companions *companionWindows) createNativeWindow(instance int, settings domain.PetSettings) companionWindow {
	position := companionPosition(settings, mygo.Screen.Displays(), instance)
	var window *mygo.Window
	var bubble string
	var until time.Time
	lastPrompt := time.Now()
	view := companions.view
	var growth companionGrowth
	growth.observe(lastPrompt, view.Client.Events)
	logInstance := diagnosticlog.Primary
	if instance != 0 {
		logInstance = diagnosticlog.Extra
	}
	pointer := &companionPointer{instance: logInstance, record: companions.record, tap: func() {
		view.Client.Interaction("tap")
		bubble, until = view.text("我听见你叫我了，我就在旁边。", "I’m here with you."), time.Now().Add(7*time.Second)
	}}
	if instance == 0 {
		pointer.remember = companions.remember
	}
	content := func(c *ui.Context) {
		now := c.Now()
		current := view.Client.Settings
		if current != nil && !current.Muted && !current.FocusModeEnabled && current.ProactiveLevel > 0 {
			interval := []time.Duration{0, 55 * time.Second, 35 * time.Second, 20 * time.Second}[min(current.ProactiveLevel, 3)]
			if now.Sub(lastPrompt) >= interval && !now.Before(until) {
				bubble, until, lastPrompt = view.text("你继续推进，我在桌面上给你盯着状态。", "Keep going. I’m here with you."), now.Add(7*time.Second), now
			}
		}
		view.companion(c, bubble, until, &growth, pointer.input)
	}
	window = newDesktopWindow(mygo.WindowOptions{Title: view.text("Liberty 桌宠", "Liberty Desktop Companion"), Width: companionWidth, Height: companionHeight, X: position.X, Y: position.Y, Hidden: true, Frameless: true, Transparent: true, AlwaysOnTop: settings.AlwaysOnTop, SkipTaskbar: true, DisableShadow: true, DisableResize: true, Content: ui.View(content)})
	pointer.window = window
	return window
}

func (view *PetView) companion(c *ui.Context, bubble string, until time.Time, growth *companionGrowth, input func(ui.InputEvent) bool) {
	c.Root().Background(ui.Transparent)
	ui.Box(c).Fill().Children(func() {
		if c.Now().Before(until) {
			ui.Text(c, bubble).Absolute().Left(10).Top(8).Width(300).Padding(8).Radius(10).Background(c.Theme().Surface).TextColor(c.Theme().Text).FontSize(13)
		}
		mood := "idle"
		if view.Client.Profile != nil {
			mood = view.Client.Profile.CurrentMood
		}
		action := petVisualAction(view.Environment, mood, view.Client.Events, c.Now())
		// Prompts can appear or expire during a drag. A stable key keeps the
		// pointer capture on the sprite across those changes and animation frames.
		ui.Image(c.Key("companion-sprite"), presentation.PetFrame(c, action)).Absolute().Left(float32(companionSprite.X)).Top(float32(companionSprite.Y)).Size(float32(companionSprite.Width), float32(companionSprite.Height)).Fit(ui.Contain).Label(view.text("桌面伙伴", "Desktop companion")).HandleInput(input)
		if growth != nil {
			growth.observe(c.Now(), view.Client.Events)
			growth.render(c)
		}
	})
}
