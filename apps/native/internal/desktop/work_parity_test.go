package desktop

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func workParityState(game domain.WorkGameDefinition) domain.WorkGameState {
	state := domain.WorkGameState{GameKey: game.Key, NameZh: game.NameZh, NameEn: game.NameEn, Jobs: game.Jobs}
	for _, job := range game.Jobs {
		state.Tasks = append(state.Tasks, domain.WorkGameTask{ID: fmt.Sprintf("%s-slot-%d", game.Key, job.SlotIndex), GameKey: game.Key, SlotIndex: job.SlotIndex, Job: &job, Status: workNeedsCare, ProgressRatio: .5})
	}
	return state
}

func TestWorkNarrowScenesKeepHeaderAndControlsReachable(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, game := range domain.WorkGameCatalog() {
			t.Run(locale+game.Key, func(t *testing.T) {
				state := workParityState(game)
				view := &WorkGameView{Locale: locale, GameKey: game.Key, Back: func() {}, Client: &client.WorkController{GameStates: map[string]domain.WorkGameState{game.Key: state}}}
				shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
				tester := ui.NewTester(shell.View, 760, 540)
				tester.SetPreferences(ui.Preferences{ReduceMotion: true})
				tester.Frame()
				captureParity(t, tester, "work-narrow-"+locale+"-"+game.Key)
				if tester.HasText(workText(locale, "Active 3", "进行中 3")) {
					t.Fatal("compact work header still shows the legacy-hidden metrics")
				}
				back, ok := tester.AccessByRole(ui.RoleButton, workText(locale, "Back", "返回牛马市场"))
				if !ok {
					t.Fatal("missing market return button")
				}
				action, ok := tester.Find(workTaskAction(locale, workNeedsCare))
				if !ok || action.Y <= back.Bounds.Y+back.Bounds.H || action.X < back.Bounds.X || action.X+action.W > 760 || action.Y+action.H > 540 {
					t.Fatal("job action is clipped or covered by the header", action, back)
				}
			})
		}
	}
}

func TestWorkJobKeyboardFocusSelectsWithoutStartingAnAction(t *testing.T) {
	for _, game := range domain.WorkGameCatalog() {
		state := workParityState(game)
		view := &WorkGameView{Locale: "en-US", GameKey: game.Key, Client: &client.WorkController{GameStates: map[string]domain.WorkGameState{game.Key: state}}}
		tester := ui.NewTester(view.View, 720, 560)
		label := gameJobLabel(view.Locale, state.Tasks[2].Job) + ": " + workStatusLabel(view.Locale, workNeedsCare)
		for count := 0; count < 12 && !tester.Focused(label); count++ {
			tester.Key(0, ui.KeyTab)
		}
		if !tester.Focused(label) || view.selected != state.Tasks[2].SlotIndex {
			t.Fatal("keyboard focus did not select the corresponding job", game.Key, view.selected)
		}
		if !tester.HasText(gameJobLabel(view.Locale, state.Tasks[2].Job)) {
			t.Fatal("focused job is missing from the action panel", game.Key)
		}
	}
}

func TestWorkRewardFeedbackIncludesGrantedItemsInBothLocales(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		game := domain.WorkGameCatalog()[0]
		state := workParityState(game)
		reward := &domain.WorkGameRewardLedgerEntry{GameKey: game.Key, LPReward: 7, Rewards: []domain.WorkGameRewardItem{{ItemKey: "energy-capsule-tool", Quantity: 2}}}
		controller := &client.WorkController{GameStates: map[string]domain.WorkGameState{game.Key: state}, LastReward: reward}
		view := &WorkGameView{Locale: locale, GameKey: game.Key, Client: controller}
		tester := ui.NewTester(view.View, 960, 700)
		item, _ := domain.FindPetItem(reward.Rewards[0].ItemKey)
		name := workText(locale, item.NameEn, item.NameZh)
		if !tester.HasText(name + " ×2") {
			t.Fatal("claimed item reward is omitted", locale)
		}
	}
}

func TestWorkErrorsDoNotExposeRawServiceDiagnostics(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		controller := &client.WorkController{Error: errors.New("synthetic private diagnostic")}
		farm := &FarmView{Locale: locale, Client: controller, Back: func() {}}
		tester := ui.NewTester(farm.View, 960, 560)
		if tester.HasText(controller.Error.Error()) {
			t.Fatal("farm error exposes raw diagnostics", locale)
		}
		if _, ok := tester.AccessByRole(ui.RoleAlert, workText(locale, "Could not load the map. Please retry.", "无法读取地图，请重试。")); !ok {
			t.Fatal("map load failure has no localized alert", locale)
		}
	}
}

func TestWorkSceneKeyboardCanPanZoomAndReset(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := &FarmView{Locale: locale, Client: &client.WorkController{FarmState: &domain.FarmState{}}}
		tester := ui.NewTester(view.View, 720, 560)
		label := workText(locale, "Map scene", "地图场景")
		for count := 0; count < 8 && !tester.Focused(label); count++ {
			tester.Key(0, ui.KeyTab)
		}
		if !tester.Focused(label) {
			t.Fatal("map cannot receive keyboard input", locale)
		}
		scale := view.scene.scale
		tester.Key(0, ui.KeyEqual)
		if view.scene.scale <= scale {
			t.Fatal("keyboard zoom did not move the shared world transform")
		}
		x := view.scene.x
		tester.Key(0, ui.KeyRight)
		if view.scene.x >= x {
			t.Fatal("keyboard could not pan the zoomed map")
		}
		scale = view.scene.scale
		tester.Key(ui.Ctrl, ui.KeyEqual)
		if view.scene.scale != scale {
			t.Fatal("map consumed a modified system shortcut")
		}
		tester.Key(0, ui.KeyHome)
		if view.scene.scale != workCoverScale(720, 560) || view.scene.adjusted {
			t.Fatal("keyboard reset did not restore the original cover view")
		}
	}
}

func TestFarmKeyboardSelectionAndActivationIgnoreHeldKeys(t *testing.T) {
	for _, key := range []ui.Key{ui.KeyEnter, ui.KeySpace} {
		state := &domain.FarmState{Crops: domain.FarmCropCatalog()}
		for index := int64(1); index <= 3; index++ {
			state.Plots = append(state.Plots, domain.FarmPlot{ID: fmt.Sprintf("plot-%d", index), PlotIndex: index, Status: "empty"})
		}
		chosen, count := "", 0
		view := &FarmView{Locale: "en-US", Client: &client.WorkController{FarmState: state}, Choose: func(plot domain.FarmPlot, _ []domain.FarmCropConfig) { chosen = plot.ID; count++ }}
		tester := ui.NewTester(view.View, 720, 560)
		label := "Plot 3: Empty"
		for index := 0; index < 12 && !tester.Focused(label); index++ {
			tester.Key(0, ui.KeyTab)
		}
		if !tester.Focused(label) || view.selected != "plot-3" || count != 0 {
			t.Fatal("focusing a plot selected the wrong target or performed an action")
		}
		tester.KeyDown(key)
		tester.KeyUp(key)
		if count != 1 || chosen != "plot-3" {
			t.Fatal("keyboard activation did not open the selected plot")
		}
		tester.KeyRepeat(0, key)
		tester.KeyRepeat(0, key)
		tester.KeyUp(key)
		if count != 1 {
			t.Fatal("held activation key repeatedly opened the crop chooser")
		}
	}
}

type workUIRequest struct {
	action, game, target, job string
}

type workUIService struct {
	client.Work
	calls     chan workUIRequest
	game      domain.WorkGameState
	afterCare *domain.WorkGameState
	careError error
	farm      domain.FarmState
}

func (service *workUIService) Start(_ context.Context, game, task, job string) (domain.WorkGameState, error) {
	service.calls <- workUIRequest{"start", game, task, job}
	return service.game, nil
}
func (service *workUIService) Game(_ context.Context, game string) (domain.WorkGameState, error) {
	service.calls <- workUIRequest{action: "game", game: game}
	return service.game, nil
}
func (service *workUIService) Care(_ context.Context, game, task string) (domain.WorkGameState, error) {
	service.calls <- workUIRequest{"care", game, task, ""}
	if service.careError != nil {
		return domain.WorkGameState{}, service.careError
	}
	if service.afterCare != nil {
		service.game = *service.afterCare
	}
	return service.game, nil
}
func (service *workUIService) Claim(_ context.Context, game, task string) (domain.WorkGameClaimResult, error) {
	service.calls <- workUIRequest{"claim", game, task, ""}
	return domain.WorkGameClaimResult{State: service.game, Reward: domain.WorkGameRewardLedgerEntry{GameKey: game, TaskID: task, LPReward: 7, Rewards: []domain.WorkGameRewardItem{{ItemKey: "energy-capsule-tool", Quantity: 2}}}}, nil
}
func (service *workUIService) Water(_ context.Context, plot string) (domain.FarmState, error) {
	service.calls <- workUIRequest{action: "water", target: plot}
	return service.farm, nil
}
func (service *workUIService) Harvest(_ context.Context, plot string) (domain.FarmHarvestResult, error) {
	service.calls <- workUIRequest{action: "harvest", target: plot}
	return domain.FarmHarvestResult{State: service.farm, Harvest: domain.FarmHarvestLedgerEntry{PlotID: plot, LPReward: 2, Rewards: []domain.FarmRewardItem{{ItemKey: "wheat-harvest-food", Quantity: 1}}}}, nil
}

func receiveWorkUIRequest(t *testing.T, calls <-chan workUIRequest) workUIRequest {
	t.Helper()
	select {
	case request := <-calls:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("work action was not dispatched")
		return workUIRequest{}
	}
}

func TestWorkHUDDispatchesOriginalJobActionsForEveryMap(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, game := range domain.WorkGameCatalog() {
			for _, action := range []struct{ status, operation string }{{workIdle, "start"}, {workNeedsCare, "care"}, {workClaimable, "claim"}} {
				t.Run(locale+game.Key+action.operation, func(t *testing.T) {
					state := workParityState(game)
					for index := range state.Tasks {
						state.Tasks[index].Status = action.status
					}
					service := &workUIService{calls: make(chan workUIRequest, 4), game: state}
					callbacks := make(chan func(), 4)
					controller := client.NewWorkController(service, func(callback func()) { callbacks <- callback }, func() {})
					defer controller.Close()
					controller.GameStates[game.Key] = state
					view := &WorkGameView{Locale: locale, GameKey: game.Key, selected: 2, Client: controller}
					tester := ui.NewTester(view.View, 720, 560)
					if err := tester.Click(workTaskAction(locale, action.status)); err != nil {
						t.Fatal(err)
					}
					request := receiveWorkUIRequest(t, service.calls)
					if request.action != action.operation || request.game != game.Key || request.target != state.Tasks[1].ID || action.operation == "start" && request.job != state.Jobs[1].JobKey {
						t.Fatal("HUD dispatched a different operation or job", request)
					}
					flushView(t, callbacks, tester)
					if controller.Error != nil || controller.BusyKey != "" {
						t.Fatal("completed work action remained busy")
					}
				})
			}
		}
	}
}

func TestFarmHUDDispatchesWaterAndHarvestAndAnnouncesRewards(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, action := range []struct{ status, operation string }{{"needs_water", "water"}, {"mature", "harvest"}} {
			state := domain.FarmState{Plots: []domain.FarmPlot{{ID: "plot-2", PlotIndex: 2, Status: action.status}}}
			service := &workUIService{calls: make(chan workUIRequest, 4), farm: state}
			callbacks := make(chan func(), 4)
			controller := client.NewWorkController(service, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			controller.FarmState = &state
			view := &FarmView{Locale: locale, Client: controller}
			tester := ui.NewTester(view.View, 720, 560)
			label := fmt.Sprintf("%s 2: %s", workText(locale, "Plot", "地块"), farmActionLabel(locale, action.status))
			if err := tester.Click(label); err != nil {
				t.Fatal(err)
			}
			request := receiveWorkUIRequest(t, service.calls)
			if request.action != action.operation || request.target != "plot-2" {
				t.Fatal("farm HUD lost its selected plot", request)
			}
			flushView(t, callbacks, tester)
			if action.operation == "harvest" {
				item, _ := domain.FindPetItem("wheat-harvest-food")
				label := workText(locale, "Harvest collected", "收获已领取") + ": " + workText(locale, item.NameEn, item.NameZh) + " ×1 · LP +2"
				if _, ok := tester.AccessByRole(ui.RoleStatus, label); !ok {
					t.Fatal("actual harvest rewards are not announced", locale)
				}
			}
		}
	}
}

func TestWorkRefreshFailureKeepsRewardFeedbackAndDoesNotOfferClaimAgain(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		game := domain.WorkGameCatalog()[0]
		state := workParityState(game)
		for index := range state.Tasks {
			state.Tasks[index].Status = workIdle
		}
		reward := &domain.WorkGameRewardLedgerEntry{GameKey: game.Key, LPReward: 7, Rewards: []domain.WorkGameRewardItem{{ItemKey: "energy-capsule-tool", Quantity: 2}}}
		controller := &client.WorkController{GameStates: map[string]domain.WorkGameState{game.Key: state}, LastReward: reward, Error: errors.New("synthetic refresh failure"), ErrorIsRefresh: true}
		view := &WorkGameView{Locale: locale, GameKey: game.Key, Client: controller}
		tester := ui.NewTester(view.View, 720, 560)
		message := workText(locale, "Could not refresh the map. Completed actions remain applied. Retry the refresh.", "地图刷新失败，已完成的操作仍然生效，请重试刷新。")
		if _, ok := tester.AccessByRole(ui.RoleAlert, message); !ok || !tester.HasText(workText(locale, "Reward claimed", "奖励已领取")) || tester.HasText(workTaskAction(locale, workClaimable)) {
			t.Fatal("read failure suggests repeating a committed claim", locale)
		}
		controller.BusyKey = "game:mine"
		tester.Frame()
		assertDisabled(t, tester, workText(locale, "Retry", "重试"), true)
	}
}

func TestWorkLongFeedbackScrollsWithoutCoveringReturnNavigation(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		rewards := make([]domain.FarmRewardItem, 12)
		for index := range rewards {
			rewards[index] = domain.FarmRewardItem{ItemKey: "wheat-harvest-food", Quantity: 1}
		}
		state := &domain.FarmState{Plots: []domain.FarmPlot{{ID: "plot-1", PlotIndex: 1, Status: "empty"}}}
		controller := &client.WorkController{FarmState: state, LastHarvest: &domain.FarmHarvestLedgerEntry{Rewards: rewards, LPReward: 2}, Error: errors.New("synthetic refresh failure"), ErrorIsRefresh: true}
		returned := false
		view := &FarmView{Locale: locale, Client: controller, Back: func() { returned = true }, Choose: func(domain.FarmPlot, []domain.FarmCropConfig) {}}
		tester := ui.NewTester(view.View, 720, 420)
		tester.SetPreferences(ui.Preferences{ReduceMotion: true})
		tester.Frame()
		action, ok := tester.Find("Plot 1: Choose Crop")
		if locale != "en-US" {
			action, ok = tester.Find("地块 1: 选择农作物")
		}
		if !ok {
			t.Fatal("missing selected-plot action")
		}
		tester.Scroll(action.X+10, action.Y+5, 0, 5000)
		retry, ok := tester.AccessByRole(ui.RoleButton, workText(locale, "Retry", "重试"))
		if !ok || retry.Bounds.Y < 80 || retry.Bounds.Y+retry.Bounds.H > 420 {
			t.Fatal("long feedback cannot scroll its retry control into view", retry)
		}
		captureParity(t, tester, "farm-feedback-"+locale)
		if err := tester.Click(workText(locale, "Back", "返回牛马市场")); err != nil || !returned {
			t.Fatal("feedback covers the market return button", err)
		}
	}
}

func TestMarketEmptyAndUnavailableMapHaveExplicitRecovery(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		market := &WorkMarketView{Locale: locale, Client: &client.WorkController{MarketState: &domain.WorkMarketState{}}}
		tester := ui.NewTester(market.View, 960, 700)
		if _, ok := tester.AccessByRole(ui.RoleStatus, workText(locale, "No maps are available yet.", "暂无可用地图。")); !ok {
			t.Fatal("empty market renders a blank map grid", locale)
		}
		returned := false
		view := &WorkGameView{Locale: locale, GameKey: "unavailable-map", Client: &client.WorkController{}, Back: func() { returned = true }}
		tester = ui.NewTester(view.View, 720, 560)
		if !tester.HasText(workText(locale, "Map unavailable", "地图不可用")) || tester.HasText(workText(locale, "Loading map...", "正在加载地图…")) {
			t.Fatal("unavailable map pretends to be loading", locale)
		}
		if err := tester.Click(workText(locale, "Back", "返回牛马市场")); err != nil || !returned {
			t.Fatal("unavailable map has no working return path", err)
		}
	}
}
