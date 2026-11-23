package client

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type workCall struct {
	kind, key, task, job string
	ctx                  context.Context
	reply                chan error
}
type workPort struct{ calls chan workCall }

func (p workPort) call(ctx context.Context, kind, key, task, job string) error {
	call := workCall{kind: kind, key: key, task: task, job: job, ctx: ctx, reply: make(chan error, 1)}
	p.calls <- call
	return <-call.reply
}
func (p workPort) Farm(ctx context.Context) (domain.FarmState, error) {
	return domain.FarmState{MapStatus: "idle"}, p.call(ctx, "farm", "", "", "")
}
func (p workPort) Market(ctx context.Context) (domain.WorkMarketState, error) {
	return domain.WorkMarketState{}, p.call(ctx, "market", "", "", "")
}
func (p workPort) Game(ctx context.Context, key string) (domain.WorkGameState, error) {
	return domain.WorkGameState{GameKey: key}, p.call(ctx, "game", key, "", "")
}
func (p workPort) Plant(ctx context.Context, plot, crop string) (domain.FarmState, error) {
	return domain.FarmState{MapStatus: "running"}, p.call(ctx, "plant", "", plot, crop)
}
func (p workPort) Water(ctx context.Context, plot string) (domain.FarmState, error) {
	return domain.FarmState{}, p.call(ctx, "water", "", plot, "")
}
func (p workPort) Harvest(ctx context.Context, plot string) (domain.FarmHarvestResult, error) {
	return domain.FarmHarvestResult{}, p.call(ctx, "harvest", "", plot, "")
}
func (p workPort) Start(ctx context.Context, key, task, job string) (domain.WorkGameState, error) {
	return domain.WorkGameState{GameKey: key}, p.call(ctx, "start", key, task, job)
}
func (p workPort) Care(ctx context.Context, key, task string) (domain.WorkGameState, error) {
	return domain.WorkGameState{GameKey: key}, p.call(ctx, "care", key, task, "")
}
func (p workPort) Claim(ctx context.Context, key, task string) (domain.WorkGameClaimResult, error) {
	return domain.WorkGameClaimResult{State: domain.WorkGameState{GameKey: key}}, p.call(ctx, "claim", key, task, "")
}

func TestWorkRouteChangeQueuesNewPageAndCloseRevokesCallbacks(t *testing.T) {
	p := workPort{calls: make(chan workCall, 8)}
	callbacks := make(chan func(), 8)
	changes := 0
	c := NewWorkController(p, func(fn func()) { callbacks <- fn }, func() { changes++ })
	defer c.Close()
	c.SetRoute("/farm")
	first := receive(t, p.calls)
	c.SetRoute("/work-game/factory")
	if first.kind != "farm" || c.BusyKey != "farm" {
		t.Fatal("initial route not loaded")
	}
	first.reply <- nil
	receive(t, callbacks)()
	second := receive(t, p.calls)
	if second.kind != "game" || second.key != "factory" {
		t.Fatal("in-flight request swallowed navigation")
	}
	c.SetRoute("")
	c.Close()
	before := changes
	if second.ctx.Err() == nil {
		t.Fatal("close did not cancel request")
	}
	second.reply <- nil
	receive(t, callbacks)()
	if _, ok := c.GameStates["factory"]; ok || changes != before {
		t.Fatal("late callback changed closed view")
	}
}

func TestWorkPollDispatchesBeforeMutatingUIAndStops(t *testing.T) {
	p := workPort{calls: make(chan workCall, 8)}
	callbacks := make(chan func(), 8)
	changes := 0
	c := NewWorkController(p, func(fn func()) { callbacks <- fn }, func() { changes++ })
	defer c.Close()
	c.route = "/work-market"
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() { defer close(done); c.poll(ticks) }()
	ticks <- time.Now()
	fn := receive(t, callbacks)
	if c.BusyKey != "" || changes != 0 {
		t.Fatal("poll mutated UI outside dispatcher")
	}
	fn()
	call := receive(t, p.calls)
	if call.kind != "market" || changes != 1 {
		t.Fatal("dispatch did not refresh current route")
	}
	call.reply <- nil
	receive(t, callbacks)()
	c.SetRoute("")
	ticks <- time.Now()
	receive(t, callbacks)()
	select {
	case <-p.calls:
		t.Fatal("inactive work route polled")
	default:
	}
	c.Close()
	receive(t, done)
}

func TestWorkCommittedPlantPublishesWithoutSecondaryQuery(t *testing.T) {
	p := workPort{calls: make(chan workCall, 8)}
	callbacks := make(chan func(), 8)
	c := NewWorkController(p, func(fn func()) { callbacks <- fn }, func() {})
	defer c.Close()
	c.MarketState = &domain.WorkMarketState{}
	c.Plant("farm-plot-1", "wheat")
	call := receive(t, p.calls)
	if call.kind != "plant" || call.task != "farm-plot-1" || call.job != "wheat" {
		t.Fatal("wrong planting target")
	}
	c.Plant("farm-plot-1", "wheat")
	call.reply <- nil
	receive(t, callbacks)()
	if c.FarmState == nil || c.FarmState.MapStatus != "running" || c.Error != nil || c.BusyKey != "" || c.MarketState != nil {
		t.Fatal("committed state missing")
	}
	select {
	case call := <-p.calls:
		t.Fatalf("unexpected secondary request: %s", call.kind)
	default:
	}
}

func TestWorkCommittedActionsSurviveFailedQueuedRefresh(t *testing.T) {
	for _, action := range []struct {
		name, route string
		invoke      func(*WorkController)
	}{
		{"plant", "/farm", func(controller *WorkController) { controller.Plant("plot-1", "wheat") }},
		{"water", "/farm", func(controller *WorkController) { controller.Water("plot-1") }},
		{"harvest", "/farm", func(controller *WorkController) { controller.Harvest("plot-1") }},
		{"start", "/work-game/mine", func(controller *WorkController) { controller.Start("mine", "task-1", "shallow-vein") }},
		{"care", "/work-game/mine", func(controller *WorkController) { controller.Care("mine", "task-1") }},
		{"claim", "/work-game/mine", func(controller *WorkController) { controller.Claim("mine", "task-1") }},
	} {
		t.Run(action.name, func(t *testing.T) {
			port := workPort{calls: make(chan workCall, 8)}
			callbacks := make(chan func(), 8)
			controller := NewWorkController(port, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			controller.route = action.route
			action.invoke(controller)
			write := receive(t, port.calls)
			action.invoke(controller)
			controller.RefreshRoute()
			write.reply <- nil
			receive(t, callbacks)()
			farm, game := controller.FarmState, controller.GameStates["mine"]
			harvest, reward := controller.LastHarvest, controller.LastReward
			if action.route == "/farm" && farm == nil || action.route != "/farm" && game.GameKey != "mine" || action.name == "harvest" && harvest == nil || action.name == "claim" && reward == nil {
				t.Fatal("successful action did not publish its returned snapshot")
			}
			read := receive(t, port.calls)
			if read.kind != "farm" && read.kind != "game" {
				t.Fatal("duplicate input replayed a mutation", read.kind)
			}
			read.reply <- errors.New("synthetic read failure")
			receive(t, callbacks)()
			if controller.Error == nil || !controller.ErrorIsRefresh || controller.BusyKey != "" || controller.Loading || controller.FarmState != farm || !reflect.DeepEqual(controller.GameStates["mine"], game) || controller.LastHarvest != harvest || controller.LastReward != reward {
				t.Fatal("failed follow-up refresh discarded committed state or became an action failure")
			}
			controller.RefreshRoute()
			recovery := receive(t, port.calls)
			if recovery.kind != read.kind || controller.Error == nil {
				t.Fatal("refresh retry replayed a mutation or hid its in-flight error")
			}
			recovery.reply <- nil
			receive(t, callbacks)()
			if controller.Error != nil || controller.ErrorIsRefresh || controller.BusyKey != "" {
				t.Fatal("successful retry failed to clear the read error")
			}
			select {
			case extra := <-port.calls:
				t.Fatal("unexpected repeated request", extra.kind)
			default:
			}
		})
	}
}

func TestAssemblyCareFailureSurvivesReturnAndAutomaticFactoryRefresh(t *testing.T) {
	port := workPort{calls: make(chan workCall, 8)}
	callbacks := make(chan func(), 8)
	controller := NewWorkController(port, func(callback func()) { callbacks <- callback }, func() {})
	defer controller.Close()
	controller.route = "/assembly-practice"
	controller.CareForRoute("factory", "factory-slot-1", "/work-game/factory")
	care := receive(t, port.calls)
	if care.kind != "care" || care.key != "factory" || care.task != "factory-slot-1" {
		t.Fatalf("wrong care operation: %+v", care)
	}
	controller.SetRoute("/work-game/factory")
	failure := errors.New("synthetic care action failure")
	care.reply <- failure
	receive(t, callbacks)()
	refresh := receive(t, port.calls)
	if refresh.kind != "game" || refresh.key != "factory" {
		t.Fatalf("navigation did not refresh the factory after care: %+v", refresh)
	}
	refresh.reply <- nil
	receive(t, callbacks)()
	if !errors.Is(controller.Error, failure) || controller.ErrorIsRefresh || controller.BusyKey != "" || controller.GameStates["factory"].GameKey != "factory" {
		t.Fatalf("care failure was hidden by return navigation or successful refresh: error=%v refresh=%v state=%+v", controller.Error, controller.ErrorIsRefresh, controller.GameStates["factory"])
	}
	controller.RefreshRoute()
	retry := receive(t, port.calls)
	retry.reply <- nil
	receive(t, callbacks)()
	if controller.Error != nil || controller.BusyKey != "" {
		t.Fatalf("successful explicit map retry did not clear the care failure: %v", controller.Error)
	}
}

func TestWorkNewRewardAttemptClearsPreviousSuccessOnlyWhenAccepted(t *testing.T) {
	for _, harvest := range []bool{false, true} {
		port := workPort{calls: make(chan workCall, 8)}
		callbacks := make(chan func(), 8)
		controller := NewWorkController(port, func(callback func()) { callbacks <- callback }, func() {})
		defer controller.Close()
		oldReward := &domain.WorkGameRewardLedgerEntry{ID: "previous-reward"}
		oldHarvest := &domain.FarmHarvestLedgerEntry{ID: "previous-harvest"}
		controller.LastReward, controller.LastHarvest = oldReward, oldHarvest
		attempt := func() { controller.Claim("mine", "task-1") }
		if harvest {
			attempt = func() { controller.Harvest("plot-1") }
		}
		controller.BusyKey = "farm"
		attempt()
		if controller.LastReward != oldReward || controller.LastHarvest != oldHarvest {
			t.Fatal("ignored input discarded an existing success result")
		}
		controller.BusyKey = ""
		attempt()
		call := receive(t, port.calls)
		if harvest && controller.LastHarvest != nil || !harvest && controller.LastReward != nil {
			t.Fatal("accepted attempt still displays a previous success")
		}
		failure := errors.New("synthetic action failure")
		call.reply <- failure
		receive(t, callbacks)()
		if !errors.Is(controller.Error, failure) || controller.ErrorIsRefresh || controller.BusyKey != "" {
			t.Fatal("action failure was misclassified as a refresh failure")
		}
	}
}

func TestWorkNewFarmAndJobActionsClearPreviousSuccessOnlyWhenAccepted(t *testing.T) {
	for _, operation := range []struct {
		name          string
		kind          string
		invoke        func(*WorkController)
		clearsReward  bool
		clearsHarvest bool
	}{
		{name: "plant", kind: "plant", invoke: func(controller *WorkController) { controller.Plant("plot-1", "wheat") }, clearsHarvest: true},
		{name: "water", kind: "water", invoke: func(controller *WorkController) { controller.Water("plot-1") }, clearsHarvest: true},
		{name: "start", kind: "start", invoke: func(controller *WorkController) { controller.Start("mine", "task-1", "shallow-vein") }, clearsReward: true},
		{name: "care", kind: "care", invoke: func(controller *WorkController) { controller.Care("mine", "task-1") }, clearsReward: true},
	} {
		t.Run(operation.name, func(t *testing.T) {
			port := workPort{calls: make(chan workCall, 1)}
			callbacks := make(chan func(), 1)
			controller := NewWorkController(port, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			oldReward := &domain.WorkGameRewardLedgerEntry{ID: "previous-reward"}
			oldHarvest := &domain.FarmHarvestLedgerEntry{ID: "previous-harvest"}
			controller.LastReward, controller.LastHarvest = oldReward, oldHarvest

			controller.BusyKey = "other-action"
			operation.invoke(controller)
			if controller.LastReward != oldReward || controller.LastHarvest != oldHarvest {
				t.Fatal("ignored input discarded an existing success result")
			}

			controller.BusyKey = ""
			operation.invoke(controller)
			call := receive(t, port.calls)
			if call.kind != operation.kind {
				t.Fatalf("wrong operation dispatched: got %q, want %q", call.kind, operation.kind)
			}
			if operation.clearsReward && controller.LastReward != nil {
				t.Fatal("accepted job action still displays a previous reward")
			}
			if operation.clearsHarvest && controller.LastHarvest != nil {
				t.Fatal("accepted farm action still displays a previous harvest")
			}

			call.reply <- errors.New("synthetic action failure")
			receive(t, callbacks)()
			if controller.Error == nil || controller.ErrorIsRefresh || controller.BusyKey != "" {
				t.Fatal("action failure was misclassified or left the action busy")
			}
		})
	}
}

func TestWorkCareAndClaimPreserveGameAndTaskIdentity(t *testing.T) {
	for _, operation := range []struct {
		name   string
		invoke func(*WorkController)
	}{
		{name: "care", invoke: func(controller *WorkController) { controller.Care("factory", "factory-slot-1") }},
		{name: "claim", invoke: func(controller *WorkController) { controller.Claim("factory", "factory-slot-1") }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			port := workPort{calls: make(chan workCall, 1)}
			callbacks := make(chan func(), 1)
			controller := NewWorkController(port, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			operation.invoke(controller)
			call := receive(t, port.calls)
			if call.kind != operation.name || call.key != "factory" || call.task != "factory-slot-1" {
				t.Fatalf("%s reached the wrong operation or task: %+v", operation.name, call)
			}
			call.reply <- nil
			receive(t, callbacks)()
			if controller.BusyKey != "" || controller.Error != nil || controller.GameStates["factory"].GameKey != "factory" {
				t.Fatal("committed operation did not publish its game state")
			}
			if operation.name == "claim" && controller.LastReward == nil {
				t.Fatal("claim did not publish its reward")
			}
			if operation.name == "care" && controller.LastReward != nil {
				t.Fatal("care incorrectly published a claim reward")
			}
		})
	}
}

func TestWorkCareForRouteRejectsMismatchedMapRoute(t *testing.T) {
	port := workPort{calls: make(chan workCall, 1)}
	callbacks := make(chan func(), 1)
	controller := NewWorkController(port, func(callback func()) { callbacks <- callback }, func() {})
	defer controller.Close()
	controller.CareForRoute("factory", "factory-slot-1", "/work-game/mine")
	select {
	case call := <-port.calls:
		t.Fatalf("mismatched care target triggered an action: %+v", call)
	default:
	}
}

func TestWorkRouteChangesDoNotExposePreviousPageErrors(t *testing.T) {
	for _, action := range []bool{false, true} {
		port := workPort{calls: make(chan workCall, 8)}
		callbacks := make(chan func(), 8)
		controller := NewWorkController(port, func(callback func()) { callbacks <- callback }, func() {})
		defer controller.Close()
		controller.route = "/farm"
		controller.Error, controller.ErrorIsRefresh = errors.New("previous page failure"), true
		if action {
			controller.Water("plot-1")
		} else {
			controller.LoadFarm()
		}
		previous := receive(t, port.calls)
		controller.SetRoute("/work-game/mine")
		if controller.Error != nil || controller.ErrorIsRefresh {
			t.Fatal("new route inherited an existing error from the previous page")
		}
		previous.reply <- errors.New("late previous page failure")
		receive(t, callbacks)()
		current := receive(t, port.calls)
		if current.kind != "game" || current.key != "mine" || controller.Error != nil || controller.ErrorIsRefresh {
			t.Fatal("late previous-page failure leaked into the current map")
		}
		current.reply <- nil
		receive(t, callbacks)()
		if controller.GameStates["mine"].GameKey != "mine" || controller.BusyKey != "" {
			t.Fatal("current map did not recover after navigation")
		}
	}
}

func TestWorkIdenticalInFlightReadsShareTheCurrentRequest(t *testing.T) {
	for _, route := range []string{"/farm", "/work-market", "/work-game/mine"} {
		t.Run(route, func(t *testing.T) {
			port := workPort{calls: make(chan workCall, 8)}
			callbacks := make(chan func(), 8)
			controller := NewWorkController(port, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			controller.SetRoute(route)
			request := receive(t, port.calls)
			for range 3 {
				controller.RefreshRoute()
			}
			if controller.pending {
				t.Error("identical pending reads queued a redundant refresh")
			}
			request.reply <- nil
			receive(t, callbacks)()
			if controller.BusyKey != "" {
				t.Error("completing the shared read immediately started a duplicate", controller.BusyKey)
				extra := receive(t, port.calls)
				extra.reply <- nil
				receive(t, callbacks)()
			}
		})
	}
}
