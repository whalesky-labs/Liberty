package client

import (
	"context"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
)

type Work interface {
	Farm(context.Context) (domain.FarmState, error)
	Market(context.Context) (domain.WorkMarketState, error)
	Plant(context.Context, string, string) (domain.FarmState, error)
	Water(context.Context, string) (domain.FarmState, error)
	Harvest(context.Context, string) (domain.FarmHarvestResult, error)
	Game(context.Context, string) (domain.WorkGameState, error)
	Start(context.Context, string, string, string) (domain.WorkGameState, error)
	Care(context.Context, string, string) (domain.WorkGameState, error)
	Claim(context.Context, string, string) (domain.WorkGameClaimResult, error)
}

type WorkController struct {
	FarmState                 *domain.FarmState
	MarketState               *domain.WorkMarketState
	LastHarvest               *domain.FarmHarvestLedgerEntry
	GameStates                map[string]domain.WorkGameState
	LastReward                *domain.WorkGameRewardLedgerEntry
	BusyKey                   string
	Error                     error
	ErrorIsRefresh            bool
	Loading                   bool
	service                   Work
	dispatch                  func(func())
	changed                   func()
	ctx                       context.Context
	cancel                    context.CancelFunc
	revision                  uint64
	closed                    bool
	route                     string
	polling                   bool
	pending                   bool
	preserveActionErrorOnRead bool
}

func NewWorkController(service Work, dispatch func(func()), changed func()) *WorkController {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkController{service: service, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel, GameStates: map[string]domain.WorkGameState{}}
}

// All public state and calls belong to the supplied UI dispatcher. Only service
// requests and the polling timer run in background goroutines.
// SetRoute loads the visible work page and keeps its time-dependent state fresh.
// An empty route pauses polling while the workspace is showing another page.
func (c *WorkController) SetRoute(route string) {
	if c.closed || c.route == route {
		return
	}
	c.route = route
	c.Error, c.ErrorIsRefresh = nil, false
	c.RefreshRoute()
}

func (c *WorkController) RefreshRoute() {
	if c.closed {
		return
	}
	switch {
	case c.route == "/work-market":
		c.LoadMarket()
	case c.route == "/farm":
		c.LoadFarm()
	case strings.HasPrefix(c.route, "/work-game/"):
		key := strings.TrimPrefix(c.route, "/work-game/")
		if key != "" && !strings.Contains(key, "/") {
			c.LoadGame(key)
		}
	}
}

func (c *WorkController) StartPolling() {
	if c.closed || c.polling {
		return
	}
	c.polling = true
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		c.poll(ticker.C)
	}()
}

func (c *WorkController) poll(ticks <-chan time.Time) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticks:
			// RefreshRoute changes loading/error fields and calls changed. Keep
			// these mutations on the same thread as render and user actions.
			c.dispatch(c.RefreshRoute)
		}
	}
}

func (c *WorkController) LoadMarket() {
	c.run("market", true, func(ctx context.Context) (func(), error) {
		m, err := c.service.Market(ctx)
		if err != nil {
			return nil, err
		}
		return func() { c.MarketState = &m }, nil
	})
}
func (c *WorkController) LoadFarm() {
	c.run("farm", true, func(ctx context.Context) (func(), error) {
		v, err := c.service.Farm(ctx)
		if err != nil {
			return nil, err
		}
		return func() { c.FarmState = &v }, nil
	})
}
func (c *WorkController) LoadGame(key string) {
	c.run("game:"+key, true, func(ctx context.Context) (func(), error) {
		v, err := c.service.Game(ctx, key)
		if err != nil {
			return nil, err
		}
		return func() { c.GameStates[key] = v }, nil
	})
}
func (c *WorkController) RefreshMarket() { c.LoadMarket() }
func (c *WorkController) Plant(plot, crop string) {
	if c.closed || c.BusyKey != "" {
		return
	}
	c.LastHarvest = nil
	c.action("plot:"+plot, func(ctx context.Context) (func(), error) {
		v, err := c.service.Plant(ctx, plot, crop)
		if err != nil {
			return nil, err
		}
		return func() { c.FarmState, c.MarketState = &v, nil }, nil
	})
}
func (c *WorkController) Water(plot string) {
	if c.closed || c.BusyKey != "" {
		return
	}
	c.LastHarvest = nil
	c.action("plot:"+plot, func(ctx context.Context) (func(), error) {
		v, err := c.service.Water(ctx, plot)
		if err != nil {
			return nil, err
		}
		return func() { c.FarmState, c.MarketState = &v, nil }, nil
	})
}
func (c *WorkController) Harvest(plot string) {
	if c.closed || c.BusyKey != "" {
		return
	}
	c.LastHarvest = nil
	c.action("plot:"+plot, func(ctx context.Context) (func(), error) {
		v, err := c.service.Harvest(ctx, plot)
		if err != nil {
			return nil, err
		}
		return func() { c.FarmState, c.MarketState, c.LastHarvest = &v.State, nil, &v.Harvest }, nil
	})
}
func (c *WorkController) Start(key, task, job string) {
	if c.closed || c.BusyKey != "" {
		return
	}
	c.LastReward = nil
	c.action("task:"+task, func(ctx context.Context) (func(), error) {
		v, err := c.service.Start(ctx, key, task, job)
		if err != nil {
			return nil, err
		}
		return func() { c.GameStates[key], c.MarketState = v, nil }, nil
	})
}
func (c *WorkController) Care(key, task string) {
	c.care(key, task, c.route)
}

func (c *WorkController) CareForRoute(key, task, route string) {
	if route != "/work-game/"+key {
		return
	}
	c.care(key, task, route)
}

func (c *WorkController) care(key, task, route string) {
	if c.closed || c.BusyKey != "" {
		return
	}
	c.LastReward = nil
	c.actionForRoute("task:"+task, route, func(ctx context.Context) (func(), error) {
		v, err := c.service.Care(ctx, key, task)
		if err != nil {
			return nil, err
		}
		return func() { c.GameStates[key], c.MarketState = v, nil }, nil
	})
}
func (c *WorkController) Claim(key, task string) {
	if c.closed || c.BusyKey != "" {
		return
	}
	c.LastReward = nil
	c.action("task:"+task, func(ctx context.Context) (func(), error) {
		v, err := c.service.Claim(ctx, key, task)
		if err != nil {
			return nil, err
		}
		return func() { c.GameStates[key], c.MarketState, c.LastReward = v.State, nil, &v.Reward }, nil
	})
}

func (c *WorkController) run(key string, loading bool, fn func(context.Context) (func(), error)) {
	if c.closed {
		return
	}
	if c.BusyKey != "" {
		if c.BusyKey != key {
			c.pending = true
		}
		return
	}
	c.revision++
	revision := c.revision
	requestRoute := c.route
	c.BusyKey, c.Loading = key, loading
	c.changed()
	preserveActionError := c.preserveActionErrorOnRead
	c.preserveActionErrorOnRead = false
	go func() {
		update, err := fn(c.ctx)
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.BusyKey, c.Loading = "", false
			if !preserveActionError || err != nil {
				c.Error = err
				c.ErrorIsRefresh = err != nil
			}
			if requestRoute != c.route {
				c.Error, c.ErrorIsRefresh = nil, false
			}
			if err == nil && update != nil {
				update()
			}
			c.changed()
			c.flushPending()
		})
	}()
}

func (c *WorkController) action(key string, fn func(context.Context) (func(), error)) {
	c.actionForRoute(key, c.route, fn)
}

func (c *WorkController) actionForRoute(key, responseRoute string, fn func(context.Context) (func(), error)) {
	if c.closed || c.BusyKey != "" {
		return
	}
	c.revision++
	revision := c.revision
	requestRoute := responseRoute
	c.BusyKey, c.Error = key, nil
	c.ErrorIsRefresh = false
	c.changed()
	go func() {
		update, err := fn(c.ctx)
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.BusyKey, c.Error = "", err
			if requestRoute != c.route {
				c.Error, c.ErrorIsRefresh = nil, false
			}
			if err == nil && update != nil {
				update()
			}
			if err != nil && requestRoute == c.route && c.pending {
				c.preserveActionErrorOnRead = true
			}
			c.changed()
			c.flushPending()
		})
	}()
}

func (c *WorkController) flushPending() {
	if c.pending {
		c.pending = false
		c.RefreshRoute()
	}
}

func (c *WorkController) Close() {
	c.closed = true
	c.revision++
	c.cancel()
}
