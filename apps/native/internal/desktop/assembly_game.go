package desktop

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/plugins/game"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type assemblyGameView struct {
	Locale         string
	Back           func()
	CareTaskID     string
	OnCareComplete func()

	round        domain.AssemblyRound
	mode         domain.AssemblyMode
	seed         uint64
	services     ui.Services
	active       bool
	focusNext    bool
	dark         bool
	reduceMotion bool
	visualTime   time.Duration
	hitAge       time.Duration
	lastHit      domain.AssemblyHit
	lastPart     domain.AssemblyPart
}

func (v *assemblyGameView) text(zh, en string) string { return localized(v.Locale, zh, en) }

func (v *assemblyGameView) pause() {
	wasPlaying := v.round.Snapshot().Phase == domain.AssemblyPlaying
	v.round.Pause()
	v.active = false
	if wasPlaying {
		v.services.Invalidate()
	}
}

func (v *assemblyGameView) start() {
	// This seed only shuffles a local practice order; it does not draw rewards.
	v.seed++
	v.round.Start(v.mode, v.seed)
	v.active, v.focusNext = true, true
	v.visualTime, v.hitAge = 0, 0
	v.lastHit, v.lastPart = domain.AssemblyIgnored, 0
}

func (v *assemblyGameView) resume() {
	v.round.Resume()
	v.active, v.focusNext = true, true
	v.lastHit, v.lastPart = domain.AssemblyIgnored, 0
	v.hitAge = 0
}

func (v *assemblyGameView) place(part domain.AssemblyPart) {
	hit := v.round.Place(part)
	if hit != domain.AssemblyIgnored {
		v.lastHit, v.lastPart, v.hitAge = hit, part, 0
		v.services.Invalidate()
		if v.CareTaskID != "" && v.round.Snapshot().Phase == domain.AssemblyCompleted {
			v.CareTaskID = ""
			if v.OnCareComplete != nil {
				v.OnCareComplete()
			}
		}
	}
}

// Update runs only while the active game surface is mounted. Pausing removes
// game.View, so resuming gets a fresh loop clock instead of catching up lost time.
func (v *assemblyGameView) Update(_ *game.Input) {
	if !v.active {
		return
	}
	visibleChanged := v.round.Advance(time.Second / game.Rate)
	v.hitAge += time.Second / game.Rate
	if !v.reduceMotion {
		v.visualTime += time.Second / game.Rate
	}
	if visibleChanged {
		v.services.Invalidate()
	}
}

func (v *assemblyGameView) View(c *ui.Context) {
	v.services = c.Services()
	v.dark, v.reduceMotion = c.Theme().Dark, c.Preferences().ReduceMotion
	if !c.WindowActive() || c.WindowHidden() {
		v.pause()
	}
	// Native controls retain Tab, Space/Enter and assistive-technology actions.
	// A shortcut is one press, never an operating system key-repeat event.
	root := contentScroll(c).Fill()
	_, viewportHeight := c.Size()
	if bounds := root.LayoutBounds(); bounds.H > 0 {
		viewportHeight = bounds.H
	}
	stageHeight := float32(224)
	if viewportHeight < 600 {
		stageHeight = 192
	}
	root.Children(func() {
		region := ui.Column(c).FillWidth().MaxWidth(shellContentMaxWidth).AlignSelf(ui.Center).Padding(8).Gap(12).Role(ui.RoleGroup).Label(v.text("工厂装配练习", "Factory assembly practice"))
		region.OnShortcut(0, ui.KeyEscape, func() {
			if !c.ShortcutRepeated() && v.round.Snapshot().Phase == domain.AssemblyPlaying {
				v.pause()
			}
		})
		for index, key := range []ui.Key{ui.Key1, ui.Key2, ui.Key3} {
			part := domain.AssemblyPart(index + 1)
			region.OnShortcut(0, key, func() {
				if !c.ShortcutRepeated() {
					v.place(part)
				}
			})
		}
		focused := false
		region.Children(func() {
			state := v.round.Snapshot()
			playing := state.Phase == domain.AssemblyPlaying
			ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Text(c, v.text("工厂 · 短局练习", "FACTORY · PRACTICE ROUND")).FontSize(11).TextColor(palette(c).NavAccent)
					ui.Text(c, v.text("装配挑战", "Assembly Challenge")).FontSize(26).Role(ui.RoleHeading).Level(1)
				})
				back := ui.Button(c, v.text("返回工厂", "Back to Factory")).MinHeight(40)
				focused = focused || back.Focused()
				if back.Clicked() {
					v.pause()
					if v.Back != nil {
						v.Back()
					}
				}
			})
			if state.Phase == domain.AssemblyReady {
				v.modePicker(c)
			} else {
				ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
					v.metric(c, v.text("装配进度", "ASSEMBLED"), fmt.Sprintf("%d / %d", state.Position, len(state.Order)))
					v.metric(c, v.text("剩余时间", "TIME LEFT"), fmt.Sprintf("%02d s", assemblySeconds(state.Remaining)))
					v.metric(c, v.text("错配", "MISTAKES"), fmt.Sprint(state.Mistakes))
					if playing {
						button := ui.Button(c, v.text("暂停", "Pause")).MinHeight(40).KeyShortcuts(0, ui.KeyEscape)
						focused = focused || button.Focused()
						if button.Clicked() {
							v.pause()
						}
					}
				})
			}
			stage := ui.Box(c).FillWidth().Height(stageHeight).Clip().Radius(14).Border(1, palette(c).Separator).Background(palette(c).Subpanel)
			stage.Children(func() {
				if playing {
					// Replace the plugin's all-key handler: it otherwise consumes Tab.
					// The plugin supplies only a fixed-step clock and drawing here.
					canvas := game.View(c.Key("assembly-active-world"), v).Fill().TabStop(false).PassThrough().
						HandleInput(func(ui.InputEvent) bool { return false }).Label(v.text("装配工作台", "Assembly workbench"))
					if v.focusNext {
						v.focusNext = false
						canvas.Focus()
					}
					focused = focused || canvas.Focused()
				} else {
					ui.Box(c).Key("assembly-static-world").Fill().PassThrough().Draw(func(p *ui.Painter, r ui.Rect) { v.Draw(p, r, 0) })
				}
				if !playing {
					ui.Box(c).Absolute().Fill().Background(ui.RGBA(10, 20, 25, .62)).PassThrough()
					ui.Column(c).Absolute().Fill().Padding(22).Center().Gap(9).Children(func() {
						title, description := v.phaseText(state)
						ui.Text(c, title).FontSize(25).TextColor(ui.Hex("#ffffff")).Role(ui.RoleHeading).Level(2)
						ui.Text(c, description).FillWidth().MaxWidth(530).TextAlign(ui.Center).FontSize(13).TextColor(ui.Hex("#e4edf0"))
						label := v.text("开始装配", "Start Assembly")
						if state.Phase == domain.AssemblyPaused {
							label = v.text("继续装配", "Resume Assembly")
						} else if state.Phase != domain.AssemblyReady {
							label = v.text("再来一局", "Play Again")
						}
						button := ui.PrimaryButton(c, label).MinHeight(44).MinWidth(164).AutoFocus()
						if button.Clicked() {
							if state.Phase == domain.AssemblyPaused {
								v.resume()
							} else {
								v.start()
							}
						}
					})
				}
			})
			v.orderStrip(c, state)
			ui.Row(c).FillWidth().Gap(12).Children(func() {
				for _, part := range []domain.AssemblyPart{domain.AssemblyBolt, domain.AssemblyWasher, domain.AssemblyGear} {
					part := part
					label := fmt.Sprintf("%d · %s", part, v.partName(part))
					button := ui.ButtonBase(c.Key(part)).Grow(1).Basis(0).MinWidth(0).Height(68).Radius(10).Padding(12).
						Background(palette(c).Panel).Border(1, palette(c).Separator).Label(label).Disabled(!playing).
						KeyShortcuts(0, ui.Key1+ui.Key(part-1)).Cursor(ui.CursorPointer).
						HandleInput(func(ev ui.InputEvent) bool {
							return ev.Kind == ui.InputKeyDown && ev.Repeat && (ev.Key == ui.KeyEnter || ev.Key == ui.KeySpace)
						})
					if button.Hovered() || button.FocusVisible() {
						button.Background(palette(c).Selected).Border(2, palette(c).NavAccent)
					}
					button.Children(func() {
						ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
							ui.Box(c).Size(38, 38).PassThrough().Draw(func(p *ui.Painter, r ui.Rect) { drawAssemblyPart(p, r, part, assemblyPartColor(part), v.dark) })
							ui.Text(c, label).FontSize(15).SingleLine()
						})
					})
					focused = focused || button.Focused()
					if button.Clicked() {
						v.place(part)
					}
				}
			})
			feedback, wrong := v.feedback(state)
			color := c.Theme().TextMuted
			if wrong {
				color = c.Theme().Danger
			}
			ui.Text(c, feedback).FillWidth().MinHeight(22).FontSize(12).TextColor(color).Role(ui.RoleStatus)
			ui.Text(c, v.text("按 1 / 2 / 3 或点击配件；Tab 切换控件，Esc 暂停。练习不发放 LP 或商品。", "Press 1 / 2 / 3 or click a part. Tab moves focus; Esc pauses. Practice awards no LP or items.")).FillWidth().FontSize(11).TextColor(c.Theme().TextMuted)
			if playing && (!focused || !c.WindowActive() || c.WindowHidden()) {
				v.pause()
			}
		})
	})
}

func (v *assemblyGameView) modePicker(c *ui.Context) {
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		for _, mode := range []domain.AssemblyMode{domain.AssemblySteady, domain.AssemblyRush} {
			mode := mode
			name, detail := v.text("稳妥单", "Steady Order"), v.text("6 个配件 · 60 秒 · 错配扣 3 秒", "6 parts · 60 seconds · -3s per mistake")
			if mode == domain.AssemblyRush {
				name, detail = v.text("急单", "Rush Order"), v.text("10 个配件 · 45 秒 · 错配扣 5 秒", "10 parts · 45 seconds · -5s per mistake")
			}
			button := ui.ButtonBase(c.Key(mode)).Grow(1).Basis(0).MinWidth(0).Padding(12).Radius(10).Label(name).Selected(v.mode == mode).
				Background(palette(c).Panel).Border(1, palette(c).Separator).Cursor(ui.CursorPointer)
			if v.mode == mode {
				button.Background(palette(c).Selected).Border(2, palette(c).NavAccent)
			}
			button.Children(func() {
				ui.Column(c).FillWidth().Gap(4).Children(func() {
					ui.Text(c, name).FontSize(14).TextColor(c.Theme().Text)
					ui.Text(c, detail).FontSize(11).TextColor(c.Theme().TextMuted)
				})
			})
			if button.Clicked() {
				v.mode = mode
			}
		}
	})
}

func (v *assemblyGameView) metric(c *ui.Context, label, value string) {
	ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(3).Children(func() {
		ui.Text(c, label).FontSize(10).TextColor(c.Theme().TextMuted)
		ui.Text(c, value).FontSize(21).FontFeatures("tnum")
	})
}

func (v *assemblyGameView) orderStrip(c *ui.Context, state domain.AssemblySnapshot) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		heading := v.text("按订单从左到右装配", "Follow the order from left to right")
		if state.Phase == domain.AssemblyPlaying && state.Position < len(state.Order) {
			heading = fmt.Sprintf(v.text("下一步：%d · %s", "NEXT: %d · %s"), state.Order[state.Position], v.partName(state.Order[state.Position]))
		}
		ui.Text(c, heading).FontSize(13)
		ui.Row(c).FillWidth().Gap(5).Children(func() {
			order := state.Order
			if len(order) == 0 {
				order = []domain.AssemblyPart{1, 2, 3, 1, 3, 2}
			}
			for index, part := range order {
				part, index := part, index
				color := palette(c).Subpanel
				border := palette(c).Separator
				if state.Phase != domain.AssemblyReady && index < state.Position {
					color, border = c.Theme().Success.Alpha(.12), c.Theme().Success
				} else if state.Phase != domain.AssemblyReady && index == state.Position {
					color, border = palette(c).Selected, palette(c).NavAccent
				}
				ui.Box(c).Grow(1).Basis(0).MinWidth(0).Height(48).Radius(7).Background(color).Border(1, border).
					Label(fmt.Sprintf(v.text("第 %d 步：%s", "Step %d: %s"), index+1, v.partName(part))).Children(func() {
					ui.Column(c).Fill().Center().Gap(1).Children(func() {
						prefix := fmt.Sprint(part)
						if state.Phase != domain.AssemblyReady && index < state.Position {
							prefix = "✓"
						}
						ui.Text(c, prefix).FontSize(16)
						ui.Text(c, fmt.Sprint(index+1)).FontSize(9).TextColor(c.Theme().TextMuted)
					})
				})
			}
		})
	})
}

func (v *assemblyGameView) partName(part domain.AssemblyPart) string {
	switch part {
	case domain.AssemblyBolt:
		return v.text("螺栓", "Bolt")
	case domain.AssemblyWasher:
		return v.text("垫圈", "Washer")
	default:
		return v.text("齿轮", "Gear")
	}
}

func (v *assemblyGameView) phaseText(state domain.AssemblySnapshot) (string, string) {
	switch state.Phase {
	case domain.AssemblyPaused:
		return v.text("已暂停", "Paused"), v.text("本局进度已保留；继续后才计时。", "Your order is held. The clock runs only after you resume.")
	case domain.AssemblyCompleted:
		return v.text("订单完成", "Order Complete"), fmt.Sprintf(v.text("装配 %d / %d · 准确率 %d%% · 剩余 %d 秒", "%d / %d assembled · %d%% accuracy · %ds left"), state.Position, len(state.Order), state.AccuracyPercent, assemblySeconds(state.Remaining))
	case domain.AssemblyTimedOut:
		return v.text("时间用尽", "Time's Up"), fmt.Sprintf(v.text("装配 %d / %d · 准确率 %d%% · 再试一次，找到自己的节奏。", "%d / %d assembled · %d%% accuracy · Try again at your own pace."), state.Position, len(state.Order), state.AccuracyPercent)
	default:
		return v.text("开一张属于你的订单", "Your workbench is ready"), v.text("看清订单顺序，放入正确配件。选稳妥单练手，或挑战节奏更快的急单。", "Read the sequence and fit each part. Learn with a steady order, or take the faster rush order.")
	}
}

func (v *assemblyGameView) feedback(state domain.AssemblySnapshot) (string, bool) {
	switch state.Phase {
	case domain.AssemblyPaused:
		return fmt.Sprintf(v.text("练习已暂停，已完成 %d/%d。", "Practice paused. %d of %d parts fitted."), state.Position, len(state.Order)), false
	case domain.AssemblyCompleted:
		return fmt.Sprintf(v.text("订单完成：完成 %d/%d，准确率 %d%%。", "Order complete: %d of %d parts fitted, %d%% accuracy."), state.Position, len(state.Order), state.AccuracyPercent), false
	case domain.AssemblyTimedOut:
		return fmt.Sprintf(v.text("时间用尽：完成 %d/%d，准确率 %d%%。", "Time's up: %d of %d parts fitted, %d%% accuracy."), state.Position, len(state.Order), state.AccuracyPercent), false
	}
	if v.lastHit == domain.AssemblyWrong {
		return fmt.Sprintf(v.text("配件不对：%s。扣 %d 秒；当前步骤：%s。", "Wrong part: %s. -%ds; current step: %s."), v.partName(v.lastPart), int(state.Penalty/time.Second), v.partName(state.Order[state.Position])), true
	}
	if v.lastHit == domain.AssemblyCorrect {
		return fmt.Sprintf(v.text("%s 已安装。进度 %d/%d；下一步：%s。", "%s fitted. Progress %d of %d; next: %s."), v.partName(v.lastPart), state.Position, len(state.Order), v.partName(state.Order[state.Position])), false
	}
	if state.Phase == domain.AssemblyPlaying && state.Position < len(state.Order) {
		return fmt.Sprintf(v.text("当前步骤：%d/%d，安装%s。", "Current step: %d of %d, fit %s."), state.Position+1, len(state.Order), v.partName(state.Order[state.Position])), false
	}
	return v.text("数字对应配件；错配会扣时间，不会倒退已完成的步骤。", "Numbers match the parts. A wrong part costs time, but keeps your completed steps."), false
}

func assemblySeconds(value time.Duration) int64 {
	return int64(max(0, value+time.Second-1) / time.Second)
}

func assemblyPartColor(part domain.AssemblyPart) ui.Color {
	switch part {
	case domain.AssemblyBolt:
		return ui.Hex("#60b9ce")
	case domain.AssemblyWasher:
		return ui.Hex("#f0bf68")
	default:
		return ui.Hex("#d2a7dd")
	}
}

// Draw presents an actual workbench and its assembled parts. The belt and brief
// insertion response move only during an active round and respect ReduceMotion.
func (v *assemblyGameView) Draw(p *ui.Painter, r ui.Rect, _ float64) {
	state := v.round.Snapshot()
	p.Image(presentation.WorkArt("factory-map.webp"), r, ui.Cover)
	p.Fill(r, ui.RGBA(10, 24, 30, .56), 0)
	p.FillGradient(r, ui.LinearGradient{From: ui.RGBA(7, 24, 30, .85), To: ui.RGBA(7, 24, 30, .12), Angle: 90}, 0)
	worker := ui.Rect{X: r.X + r.W*.76, Y: r.Y + 16, W: r.H * .77, H: r.H * .77}
	p.Image(presentation.WorkArt("factory-worker-frame-1.png"), worker, ui.Contain)
	bench := ui.Rect{X: r.X + 28, Y: r.Y + 30, W: r.W * .67, H: r.H - 88}
	p.Fill(ui.Rect{X: bench.X, Y: bench.Y + 8, W: bench.W, H: bench.H}, ui.RGBA(0, 0, 0, .32), 14)
	p.Fill(bench, ui.Hex("#d9e2df"), 14)
	p.Stroke(bench, ui.Hex("#9daea9"), 14, 2)
	for _, x := range []float32{bench.X + 12, bench.X + bench.W - 14} {
		for _, y := range []float32{bench.Y + 12, bench.Y + bench.H - 14} {
			p.Fill(ui.Rect{X: x, Y: y, W: 5, H: 5}, ui.Hex("#7a8d88"), 3)
		}
	}
	parts := state.Order
	if len(parts) == 0 {
		parts = []domain.AssemblyPart{1, 2, 3, 1, 3, 2}
	}
	size := min(float32(43), (bench.W-40)/float32(len(parts)))
	x := bench.X + (bench.W-float32(len(parts))*size)/2
	for i, part := range parts {
		a := ui.Rect{X: x + float32(i)*size + 2, Y: bench.Y + (bench.H-size)/2, W: size - 4, H: size - 4}
		p.Fill(a, ui.Hex("#bbc9c5"), 7)
		if i < state.Position {
			offset := float32(0)
			if i == state.Position-1 && !v.reduceMotion && v.hitAge < 240*time.Millisecond && v.lastHit == domain.AssemblyCorrect {
				offset = -9 * float32(1-float64(v.hitAge)/float64(240*time.Millisecond))
			}
			a.Y += offset
			drawAssemblyPart(p, a, part, assemblyPartColor(part), false)
		} else {
			p.Stroke(a, ui.Hex("#7e9990"), 7, 1)
			if i == state.Position {
				p.Stroke(ui.Rect{X: a.X - 2, Y: a.Y - 2, W: a.W + 4, H: a.H + 4}, ui.Hex("#167b70"), 8, 2)
			}
		}
	}
	belt := ui.Rect{X: r.X, Y: r.Y + r.H - 42, W: r.W, H: 30}
	p.Fill(belt, ui.Hex("#283d43"), 0)
	phase := float32(math.Mod(v.visualTime.Seconds()*25, 36))
	for x := belt.X - 36 + phase; x < belt.X+belt.W; x += 36 {
		p.Line(x, belt.Y+8, x+9, belt.Y+15, 2, ui.Hex("#536970"))
		p.Line(x+9, belt.Y+15, x, belt.Y+22, 2, ui.Hex("#536970"))
	}
	p.Line(belt.X, belt.Y, belt.X+belt.W, belt.Y, 3, ui.Hex("#a9b998"))
	if state.TimeLimit > 0 {
		p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 4, W: r.W * float32(float64(state.Remaining)/float64(state.TimeLimit)), H: 4}, ui.Hex("#71d2be"), 0)
	}
}

func drawAssemblyPart(p *ui.Painter, r ui.Rect, part domain.AssemblyPart, color ui.Color, dark bool) {
	x, y := r.X+r.W/2, r.Y+r.H/2
	size := min(r.W, r.H) * .34
	outline := ui.Hex("#35515a")
	if dark {
		outline = ui.Hex("#b8cdd2")
	}
	switch part {
	case domain.AssemblyBolt:
		p.Fill(ui.Rect{X: x - size*.25, Y: y - size*.2, W: size * .5, H: size * 1.05}, color, 2)
		p.Fill(ui.Rect{X: x - size*.65, Y: y - size*.75, W: size * 1.3, H: size * .66}, color, 4)
		p.Line(x-size*.33, y-size*.42, x+size*.33, y-size*.42, 2, outline)
		for line := float32(.2); line < .8; line += .25 {
			p.Line(x-size*.24, y+size*line, x+size*.24, y+size*(line-.1), 1, outline)
		}
	case domain.AssemblyWasher:
		p.Fill(ui.Rect{X: x - size, Y: y - size, W: 2 * size, H: 2 * size}, color, size)
		p.Stroke(ui.Rect{X: x - size*.43, Y: y - size*.43, W: size * .86, H: size * .86}, outline, size*.43, size*.28)
	case domain.AssemblyGear:
		for index := range 8 {
			angle := float64(index) * math.Pi / 4
			dx, dy := float32(math.Cos(angle)), float32(math.Sin(angle))
			p.Line(x+dx*size*.55, y+dy*size*.55, x+dx*size*1.05, y+dy*size*1.05, size*.43, color)
		}
		p.Fill(ui.Rect{X: x - size*.77, Y: y - size*.77, W: size * 1.54, H: size * 1.54}, color, size)
		p.Stroke(ui.Rect{X: x - size*.28, Y: y - size*.28, W: size * .56, H: size * .56}, outline, size*.28, size*.25)
	}
}
