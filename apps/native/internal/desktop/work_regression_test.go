package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func focusWorkControl(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	for count := 0; count < 24 && !tester.Focused(label); count++ {
		tester.Key(0, ui.KeyTab)
	}
	if !tester.Focused(label) {
		t.Fatal("control cannot receive keyboard focus", label)
	}
}

func TestWorkFocusedHotspotsRemainClickableWithLongFeedback(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, game := range domain.WorkGameCatalog() {
			t.Run(locale+game.Key, func(t *testing.T) {
				state := workParityState(game)
				for index := range state.Tasks {
					state.Tasks[index].Status = workRunning
				}
				rewards := make([]domain.WorkGameRewardItem, 12)
				for index := range rewards {
					rewards[index] = domain.WorkGameRewardItem{ItemKey: "energy-capsule-tool", Quantity: 1}
				}
				controller := &client.WorkController{GameStates: map[string]domain.WorkGameState{game.Key: state}, LastReward: &domain.WorkGameRewardLedgerEntry{GameKey: game.Key, Rewards: rewards}, Error: errors.New("synthetic refresh failure"), ErrorIsRefresh: true}
				view := &WorkGameView{Client: controller, Locale: locale, GameKey: game.Key, Back: func() {}}
				shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
				tester := ui.NewTester(shell.View, 720, 540)
				tester.SetPreferences(ui.Preferences{ReduceMotion: true})
				for _, task := range state.Tasks {
					label := gameJobLabel(locale, task.Job) + ": " + workStatusLabel(locale, task.Status)
					focusWorkControl(t, tester, label)
					captureParity(t, tester, fmt.Sprintf("work-focus-%s-%s-%d", locale, game.Key, task.SlotIndex))
					if err := tester.Click(label); err != nil {
						t.Fatal("focused hotspot remains covered by the action panel", err)
					}
				}
			})
		}
	}
}

func TestFarmFocusedPlotsRemainClickableWithLongFeedback(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			state := &domain.FarmState{}
			for index := int64(1); index <= 3; index++ {
				state.Plots = append(state.Plots, domain.FarmPlot{ID: fmt.Sprintf("plot-%d", index), PlotIndex: index, Status: "planted"})
			}
			rewards := make([]domain.FarmRewardItem, 12)
			for index := range rewards {
				rewards[index] = domain.FarmRewardItem{ItemKey: "wheat-harvest-food", Quantity: 1}
			}
			controller := &client.WorkController{FarmState: state, LastHarvest: &domain.FarmHarvestLedgerEntry{Rewards: rewards}, Error: errors.New("synthetic refresh failure"), ErrorIsRefresh: true}
			view := &FarmView{Client: controller, Locale: locale, Back: func() {}}
			shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
			tester := ui.NewTester(shell.View, 720, 540)
			tester.SetPreferences(ui.Preferences{ReduceMotion: true})
			for _, plot := range state.Plots {
				label := fmt.Sprintf("%s %d: %s", workText(locale, "Plot", "地块"), plot.PlotIndex, farmStatusLabel(locale, plot.Status))
				focusWorkControl(t, tester, label)
				captureParity(t, tester, fmt.Sprintf("farm-focus-%s-%d", locale, plot.PlotIndex))
				if err := tester.Click(label); err != nil {
					t.Fatal("focused plot remains covered by the action panel", err)
				}
			}
		})
	}
}

type workUIReadResult struct {
	game domain.WorkGameState
	farm domain.FarmState
	err  error
}

type workRecoveryUIService struct {
	*workUIService
	reads chan chan workUIReadResult
}

func receiveWorkRead(t *testing.T, reads <-chan chan workUIReadResult) chan workUIReadResult {
	t.Helper()
	select {
	case reply := <-reads:
		return reply
	case <-time.After(3 * time.Second):
		t.Fatal("work refresh was not dispatched")
		return nil
	}
}

func (service *workRecoveryUIService) Game(context.Context, string) (domain.WorkGameState, error) {
	reply := make(chan workUIReadResult, 1)
	service.reads <- reply
	result := <-reply
	return result.game, result.err
}

func (service *workRecoveryUIService) Farm(context.Context) (domain.FarmState, error) {
	reply := make(chan workUIReadResult, 1)
	service.reads <- reply
	result := <-reply
	return result.farm, result.err
}

func TestWorkCareAndClaimRetainCommittedStateDuringUIReadRecovery(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, game := range domain.WorkGameCatalog() {
			for _, operation := range []string{"care", "claim"} {
				t.Run(locale+game.Key+operation, func(t *testing.T) {
					before, after := workParityState(game), workParityState(game)
					status, next := workNeedsCare, workRunning
					if operation == "claim" {
						status, next = workClaimable, workIdle
					}
					for index := range before.Tasks {
						before.Tasks[index].Status = status
						after.Tasks[index].Status = next
					}
					service := &workRecoveryUIService{workUIService: &workUIService{calls: make(chan workUIRequest, 4), game: after}, reads: make(chan chan workUIReadResult, 4)}
					callbacks := make(chan func(), 4)
					controller := client.NewWorkController(service, func(callback func()) { callbacks <- callback }, func() {})
					defer controller.Close()
					controller.SetRoute("/work-game/" + game.Key)
					view := &WorkGameView{Client: controller, Locale: locale, GameKey: game.Key, Back: func() {}}
					shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
					tester := ui.NewTester(shell.View, 720, 540)
					receiveWorkRead(t, service.reads) <- workUIReadResult{game: before}
					flushView(t, callbacks, tester)
					focusWorkControl(t, tester, workTaskAction(locale, status))
					selected := selectedWorkTask(before.Tasks, view.selected)
					tester.KeyDown(ui.KeyEnter)
					request := receiveWorkUIRequest(t, service.calls)
					if selected == nil || request.action != operation || request.target != selected.ID {
						t.Fatal("keyboard action did not retain its selected job", request)
					}
					tester.KeyRepeat(0, ui.KeyEnter)
					controller.RefreshRoute()
					flushView(t, callbacks, tester)
					receiveWorkRead(t, service.reads) <- workUIReadResult{err: errors.New("synthetic refresh failure")}
					flushView(t, callbacks, tester)
					if controller.GameStates[game.Key].Tasks[0].Status != next || !controller.ErrorIsRefresh || tester.HasText(workTaskAction(locale, status)) {
						t.Fatal("read failure discarded the committed job state")
					}
					if operation == "claim" && (controller.LastReward == nil || !tester.HasText(workText(locale, "Reward claimed", "奖励已领取"))) {
						t.Fatal("read failure discarded the claimed reward")
					}
					tester.KeyUp(ui.KeyEnter)
					focusWorkControl(t, tester, workText(locale, "Retry", "重试"))
					retry, ok := tester.AccessByRole(ui.RoleButton, workText(locale, "Retry", "重试"))
					if !ok || retry.Bounds.Y < 110 || retry.Bounds.Y+retry.Bounds.H > 500 {
						t.Fatal("keyboard focus did not reveal the scrolled recovery control", retry)
					}
					tester.KeyDown(ui.KeySpace)
					receiveWorkRead(t, service.reads) <- workUIReadResult{game: after}
					flushView(t, callbacks, tester)
					tester.KeyRepeat(0, ui.KeySpace)
					tester.KeyUp(ui.KeySpace)
					if controller.Error != nil || controller.BusyKey != "" {
						t.Fatal("successful read recovery did not restore the controls")
					}
					select {
					case extra := <-service.calls:
						t.Fatal("held key or refresh repeated a committed mutation", extra)
					case <-service.reads:
						t.Fatal("held recovery key queued another read")
					default:
					}
				})
			}
		}
	}
}

func TestFarmWaterAndHarvestRetainCommittedStateDuringUIReadRecovery(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, operation := range []string{"water", "harvest"} {
			t.Run(locale+operation, func(t *testing.T) {
				status, next := "needs_water", "planted"
				if operation == "harvest" {
					status, next = "mature", "empty"
				}
				before := domain.FarmState{Plots: []domain.FarmPlot{{ID: "plot-1", PlotIndex: 1, Status: status}}}
				after := domain.FarmState{Plots: []domain.FarmPlot{{ID: "plot-1", PlotIndex: 1, Status: next}}}
				service := &workRecoveryUIService{workUIService: &workUIService{calls: make(chan workUIRequest, 4), farm: after}, reads: make(chan chan workUIReadResult, 4)}
				callbacks := make(chan func(), 4)
				controller := client.NewWorkController(service, func(callback func()) { callbacks <- callback }, func() {})
				defer controller.Close()
				controller.SetRoute("/farm")
				view := &FarmView{Client: controller, Locale: locale, Back: func() {}}
				tester := ui.NewTester(view.View, 720, 420)
				receiveWorkRead(t, service.reads) <- workUIReadResult{farm: before}
				flushView(t, callbacks, tester)
				focusWorkControl(t, tester, fmt.Sprintf("%s 1: %s", workText(locale, "Plot", "地块"), farmActionLabel(locale, status)))
				tester.KeyDown(ui.KeySpace)
				request := receiveWorkUIRequest(t, service.calls)
				if request.action != operation || request.target != "plot-1" {
					t.Fatal("keyboard action did not retain its selected plot", request)
				}
				controller.RefreshRoute()
				flushView(t, callbacks, tester)
				receiveWorkRead(t, service.reads) <- workUIReadResult{err: errors.New("synthetic refresh failure")}
				flushView(t, callbacks, tester)
				if controller.FarmState.Plots[0].Status != next || !controller.ErrorIsRefresh {
					t.Fatal("read failure discarded the committed plot state")
				}
				if operation == "harvest" && (controller.LastHarvest == nil || !tester.HasText(workText(locale, "Harvest collected", "收获已领取"))) {
					t.Fatal("read failure discarded the harvested reward")
				}
				tester.KeyRepeat(0, ui.KeySpace)
				tester.KeyUp(ui.KeySpace)
				select {
				case extra := <-service.calls:
					t.Fatal("held key repeated a committed farm action", extra)
				default:
				}
			})
		}
	}
}

func TestWorkHotspotZoomPreservesPointerAnchor(t *testing.T) {
	for _, game := range domain.WorkGameCatalog() {
		state := workParityState(game)
		view := &WorkGameView{Locale: "en-US", GameKey: game.Key, Client: &client.WorkController{GameStates: map[string]domain.WorkGameState{game.Key: state}}}
		tester := ui.NewTester(view.View, 960, 700)
		label := gameJobLabel(view.Locale, state.Tasks[2].Job) + ": " + workStatusLabel(view.Locale, workNeedsCare)
		focusWorkControl(t, tester, label)
		tester.Key(0, ui.KeyHome)
		bounds, ok := tester.Find(label)
		if !ok {
			t.Fatal("missing zoom target", game.Key)
		}
		pointerX, pointerY := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
		worldX, worldY := (pointerX-view.scene.x)/view.scene.scale, (pointerY-view.scene.y)/view.scene.scale
		initialScale := view.scene.scale
		tester.Pinch(pointerX, pointerY, 1.04)
		if view.scene.scale <= initialScale || math.Abs(float64((pointerX-view.scene.x)/view.scene.scale-worldX)) > .01 || math.Abs(float64((pointerY-view.scene.y)/view.scene.scale-worldY)) > .01 {
			t.Fatal("pinching a hotspot lost the map anchor", game.Key)
		}
	}
}

func TestWorkUnobstructedFocusKeepsLegacyScaleAndCenter(t *testing.T) {
	for index := int64(1); index <= 3; index++ {
		var scene workScene
		scene.resize(960, 560)
		scene.overlays(ui.Rect{W: 960, H: 560}, ui.Rect{X: 14, Y: 14, W: 932, H: 42}, ui.Rect{X: 14, Y: 390, W: 360, H: 156})
		initialScale := scene.scale
		area := farmArea(index)
		wantX := min(float32(0), max(scene.width-workWorldW*initialScale, scene.width/2-(area[0]+area[2]/2)*initialScale))
		wantY := min(float32(0), max(scene.height-workWorldH*initialScale, scene.height/2-(area[1]+area[3]/2)*initialScale))
		scene.focus(area)
		if scene.scale != initialScale || scene.x != wantX || scene.y != wantY {
			t.Fatal("unobstructed focus changed the legacy zoom or centered offset", index, scene.scale, scene.x, scene.y)
		}
	}
}

func TestWorkHotspotGesturesUseSceneCoordinatesInsideShell(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, mapKey := range []string{"farm", "mine", "factory", "convenience-store"} {
			t.Run(locale+mapKey, func(t *testing.T) {
				var render func(*ui.Context)
				var scene *workScene
				var label string
				if mapKey == "farm" {
					state := &domain.FarmState{Plots: []domain.FarmPlot{{ID: "plot-3", PlotIndex: 3, Status: "planted"}}}
					view := &FarmView{Locale: locale, Client: &client.WorkController{FarmState: state}}
					render, scene = view.View, &view.scene
					label = fmt.Sprintf("%s 3: %s", workText(locale, "Plot", "地块"), farmStatusLabel(locale, "planted"))
				} else {
					for _, game := range domain.WorkGameCatalog() {
						if game.Key != mapKey {
							continue
						}
						state := workParityState(game)
						for index := range state.Tasks {
							state.Tasks[index].Status = workRunning
							state.Tasks[index].ProgressRatio = .5
						}
						view := &WorkGameView{Locale: locale, GameKey: game.Key, Client: &client.WorkController{GameStates: map[string]domain.WorkGameState{game.Key: state}}}
						render, scene = view.View, &view.scene
						label = gameJobLabel(locale, state.Tasks[2].Job) + ": " + workStatusLabel(locale, workRunning)
					}
				}
				if render == nil {
					t.Fatal("missing map fixture", mapKey)
				}
				shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: render}
				tester := ui.NewTester(shell.View, 1180, 720)
				tester.SetPreferences(ui.Preferences{ReduceMotion: true})
				focusWorkControl(t, tester, label)
				viewport, ok := tester.Find(workText(locale, "Map scene", "地图场景"))
				if !ok || viewport.X <= 0 || viewport.Y <= 0 {
					t.Fatal("map fixture is missing its shell offset", viewport)
				}
				for _, gesture := range []string{"scroll", "pinch"} {
					bounds, ok := tester.Find(label)
					if !ok {
						t.Fatal("missing hotspot", label)
					}
					pointerX, pointerY := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
					worldX := (pointerX - viewport.X - scene.x) / scene.scale
					worldY := (pointerY - viewport.Y - scene.y) / scene.scale
					wantScale := scene.scale * 1.025
					if gesture == "scroll" {
						wantScale = scene.scale + 25*.0008
						tester.Scroll(pointerX, pointerY, 0, -25)
					} else {
						tester.Pinch(pointerX, pointerY, 1.025)
					}
					if math.Abs(float64(scene.scale-wantScale)) > .00001 || math.Abs(float64((pointerX-viewport.X-scene.x)/scene.scale-worldX)) > .01 || math.Abs(float64((pointerY-viewport.Y-scene.y)/scene.scale-worldY)) > .01 {
						t.Fatal("hotspot gesture was lost, applied twice, or moved its anchor", gesture, scene.scale, wantScale)
					}
				}
				scale, offsetX, offsetY := scene.scale, scene.x, scene.y
				progress := workText(locale, "Next care in ", "距离下次照看 ") + workSeconds(locale, 0)
				button, ok := tester.Find(progress)
				if !ok {
					t.Fatal("missing disabled HUD action", progress)
				}
				tester.Scroll(button.X+button.W/2, button.Y+button.H/2, 0, 25)
				tester.Pinch(button.X+button.W/2, button.Y+button.H/2, 1.025)
				if scene.scale != scale || scene.x != offsetX || scene.y != offsetY {
					t.Fatal("HUD gestures leaked into the map transform")
				}
			})
		}
	}
}

func TestWorkHUDMatchesLegacyResponsiveInsets(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, farm := range []bool{true, false} {
			for _, width := range []int{720, 800, 960} {
				t.Run(fmt.Sprintf("%s/farm-%t/%d", locale, farm, width), func(t *testing.T) {
					var render func(*ui.Context)
					var action string
					panelWidth, inset := float32(390), float32(14)
					if farm {
						state := &domain.FarmState{Plots: []domain.FarmPlot{{ID: "plot-1", PlotIndex: 1, Status: "empty"}}}
						view := &FarmView{Locale: locale, Client: &client.WorkController{FarmState: state}, Back: func() {}}
						render = view.View
						action = fmt.Sprintf("%s 1: %s", workText(locale, "Plot", "地块"), farmActionLabel(locale, "empty"))
						panelWidth = 360
						if width <= 760 {
							panelWidth, inset = 340, 10
						}
					} else {
						game := domain.WorkGameCatalog()[0]
						state := workParityState(game)
						view := &WorkGameView{Locale: locale, GameKey: game.Key, Client: &client.WorkController{GameStates: map[string]domain.WorkGameState{game.Key: state}}, Back: func() {}}
						render = view.View
						action = workTaskAction(locale, workNeedsCare)
						if width <= 860 {
							panelWidth, inset = 360, 10
						}
					}
					shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: render}
					tester := ui.NewTester(shell.View, width, 540)
					tester.SetPreferences(ui.Preferences{ReduceMotion: true})
					viewport, viewportOK := tester.Find(workText(locale, "Map scene", "地图场景"))
					button, actionOK := tester.AccessByRole(ui.RoleButton, action)
					back, backOK := tester.AccessByRole(ui.RoleButton, workText(locale, "Back", "返回牛马市场"))
					if !viewportOK || !actionOK || !backOK {
						t.Fatal("missing map controls", viewportOK, actionOK, backOK)
					}
					wantWidth := min(panelWidth, viewport.W-2*inset) - 24
					if math.Abs(float64(button.Bounds.X-viewport.X-inset-12)) > .5 || math.Abs(float64(button.Bounds.W-wantWidth)) > .5 || math.Abs(float64(back.Bounds.X-viewport.X-inset-10)) > .5 || math.Abs(float64(back.Bounds.Y-viewport.Y-inset-6)) > .5 {
						t.Fatal("map overlays differ from the legacy responsive width or insets", button.Bounds, back.Bounds, viewport, panelWidth, inset)
					}
				})
			}
		}
	}
}
