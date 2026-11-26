package client

import (
	"context"
	"reflect"
	"time"

	"liberty.local/liberty/internal/domain"
)

type PetService interface {
	Profile(context.Context) (domain.PetProfile, error)
	Store(context.Context) (domain.PetStoreState, error)
	Settings(context.Context) (domain.PetSettings, error)
	SaveSettings(context.Context, domain.PetSettings) (domain.PetSettings, error)
	SaveName(context.Context, string) (domain.PetProfile, error)
	Events(context.Context, int) ([]domain.PetEventLedgerEntry, error)
	Cosmetics(context.Context) ([]domain.PetCosmeticUnlock, error)
	Purchase(context.Context, string, int64) (domain.PetStoreState, error)
	Equip(context.Context, string) (domain.PetStoreState, error)
	Unequip(context.Context, string) (domain.PetStoreState, error)
	Use(context.Context, string, int64) (domain.PetStoreState, error)
	GiftBox(context.Context) (domain.PetGiftBoxOpenResult, error)
	CheckIn(context.Context) (domain.PetDailyCheckInState, error)
	ClaimCheckIn(context.Context) (domain.PetDailyCheckInClaimResult, error)
	MakeupCheckIn(context.Context) (domain.PetDailyCheckInMakeupResult, error)
	BlindBox(context.Context) (domain.PetBlindBoxState, error)
	DrawBlindBox(context.Context) (domain.PetBlindBoxDrawResult, error)
	Redeem(context.Context, string) (domain.PetRedeemKeyResult, error)
	Redemptions(context.Context, int) ([]domain.PetRedeemKeyRedemption, error)
	Workflow(context.Context, string, string) (domain.PetProfile, error)
	Interact(context.Context, string) (domain.PetProfile, error)
}

// Public state and method calls belong to the UI dispatcher. Background work
// captures inputs and posts immutable results; it never writes view state.
type PetController struct {
	Profile                    *domain.PetProfile
	Store                      *domain.PetStoreState
	Settings                   *domain.PetSettings
	Events                     []domain.PetEventLedgerEntry
	Cosmetics                  []domain.PetCosmeticUnlock
	CheckIn                    *domain.PetDailyCheckInState
	BlindBox                   *domain.PetBlindBoxState
	Redemptions                []domain.PetRedeemKeyRedemption
	LastRedeem                 *domain.PetRedeemKeyResult
	LastDraw                   *domain.PetBlindBoxDrawResult
	LastGift                   *domain.PetGiftBoxOpenResult
	LastClaim                  *domain.PetDailyCheckInClaimResult
	LastMakeup                 *domain.PetDailyCheckInMakeupResult
	LastAction                 string
	PendingAction              string
	ActionRevision             uint64
	Loading, Busy              bool
	Error                      error
	ErrorIsRefresh             bool
	service                    PetService
	dispatch                   func(func())
	changed                    func()
	ctx                        context.Context
	cancel                     context.CancelFunc
	queryCancel                context.CancelFunc
	closed, querying, polling  bool
	revision                   uint64
	darkDate, recordedDarkDate string
	visible                    bool
}

type petSnapshot struct {
	store       domain.PetStoreState
	settings    domain.PetSettings
	events      []domain.PetEventLedgerEntry
	cosmetics   []domain.PetCosmeticUnlock
	checkIn     domain.PetDailyCheckInState
	blindBox    domain.PetBlindBoxState
	redemptions []domain.PetRedeemKeyRedemption
}

func NewPet(service PetService, dispatch func(func()), changed func()) *PetController {
	ctx, cancel := context.WithCancel(context.Background())
	return &PetController{service: service, dispatch: dispatch, changed: changed, ctx: ctx, cancel: cancel, visible: true}
}

func (c *PetController) SetVisible(visible bool) {
	if c == nil || c.closed || c.visible == visible {
		return
	}
	c.visible = visible
	if visible && c.service != nil {
		c.Refresh()
	}
}

// Hidden shop pages do not need ledgers, inventory, check-in and prize pools.
// Keep desktop companions synchronized using the two small read projections.
func (c *PetController) poll() {
	if c.visible || c.Store == nil || c.darkDate != c.recordedDarkDate {
		c.Refresh()
		return
	}
	if c.closed || c.querying || c.Busy {
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.queryCancel = cancel
	c.revision++
	revision := c.revision
	c.querying = true
	go func() {
		defer cancel()
		profile, err := c.service.Profile(ctx)
		var settings domain.PetSettings
		if err == nil {
			settings, err = c.service.Settings(ctx)
		}
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			c.querying = false
			if err != nil {
				if c.visible {
					c.Refresh()
				}
				return
			} // Full page reads retain their own error/retry state.
			if !reflect.DeepEqual(c.Profile, &profile) || !reflect.DeepEqual(c.Settings, &settings) {
				c.Profile, c.Settings = &profile, &settings
				c.changed()
			}
			if c.visible {
				c.Refresh()
			}
		})
	}()
}

// Start loads every page's data and starts one cancellable refresh loop.
func (c *PetController) Start() {
	if c.closed || c.polling {
		return
	}
	c.polling = true
	c.Refresh()
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
				c.dispatch(func() {
					if !c.closed {
						c.poll()
					}
				})
			}
		}
	}()
}

func (c *PetController) load(ctx context.Context) (petSnapshot, error) {
	var s petSnapshot
	var err error
	if s.store, err = c.service.Store(ctx); err != nil {
		return s, err
	}
	if s.settings, err = c.service.Settings(ctx); err != nil {
		return s, err
	}
	if s.events, err = c.service.Events(ctx, 30); err != nil {
		return s, err
	}
	if s.cosmetics, err = c.service.Cosmetics(ctx); err != nil {
		return s, err
	}
	if s.checkIn, err = c.service.CheckIn(ctx); err != nil {
		return s, err
	}
	if s.blindBox, err = c.service.BlindBox(ctx); err != nil {
		return s, err
	}
	s.redemptions, err = c.service.Redemptions(ctx, 30)
	return s, err
}
func (c *PetController) apply(s petSnapshot) {
	c.Store, c.Profile, c.Settings = &s.store, &s.store.Profile, &s.settings
	c.Events, c.Cosmetics = s.events, s.cosmetics
	c.CheckIn, c.BlindBox, c.Redemptions = &s.checkIn, &s.blindBox, s.redemptions
}
func (c *PetController) applyStore(s domain.PetStoreState) { c.Store, c.Profile = &s, &s.Profile }
func (c *PetController) Refresh()                          { c.query("", nil) }
func (c *PetController) ObserveAppearance(dark bool, now time.Time) {
	if !c.closed && dark {
		day := now.UTC().Format(time.DateOnly)
		if day != c.darkDate {
			c.darkDate = day
			c.Refresh()
		}
	}
}
func (c *PetController) query(name string, action func(context.Context) (func(), error)) {
	if c.closed || c.service == nil || c.Busy || (action == nil && c.querying) {
		return
	}
	if c.queryCancel != nil {
		c.queryCancel()
	}
	ctx, cancel := context.WithCancel(c.ctx)
	c.queryCancel = cancel
	c.revision++
	revision := c.revision
	darkDate := c.darkDate
	recordDark := darkDate != c.recordedDarkDate
	c.querying = true
	c.Loading = c.Store == nil
	c.Busy = action != nil || c.Store == nil
	c.PendingAction = name
	if action != nil {
		c.Error = nil
		c.ErrorIsRefresh = false
		// Action feedback belongs to the accepted attempt. Clear every
		// previous reward before a new operation can render it as current.
		c.LastRedeem = nil
		c.LastDraw = nil
		c.LastGift = nil
		c.LastClaim = nil
		c.LastMakeup = nil
	}
	c.changed()
	go func() {
		defer cancel()
		var applied func()
		var actionErr error
		if action != nil {
			applied, actionErr = action(ctx)
		}
		var appearanceErr error
		if recordDark {
			_, appearanceErr = c.service.Workflow(ctx, "dark_theme_used", darkDate)
		}
		state, readErr := c.load(ctx)
		c.dispatch(func() {
			if c.closed || revision != c.revision {
				return
			}
			if recordDark && appearanceErr == nil {
				c.recordedDarkDate = darkDate
			}
			c.querying, c.Loading, c.Busy = false, false, false
			c.PendingAction = ""
			if action != nil || c.Error == nil || c.ErrorIsRefresh {
				c.Error = readErr
				c.ErrorIsRefresh = readErr != nil
			}
			if actionErr != nil {
				c.Error = actionErr
				c.ErrorIsRefresh = false
			}
			// A committed action remains visible even when the follow-up read fails.
			if action != nil && actionErr == nil {
				if applied != nil {
					applied()
				}
				c.LastAction = name
				c.ActionRevision++
			}
			if readErr == nil {
				c.apply(state)
			}
			c.changed()
		})
	}()
}
func (c *PetController) SaveName(name string) {
	c.query("name", func(ctx context.Context) (func(), error) {
		p, e := c.service.SaveName(ctx, name)
		return func() { c.Profile = &p }, e
	})
}
func (c *PetController) Purchase(key string, quantity int64) {
	c.query("purchase", func(ctx context.Context) (func(), error) {
		s, e := c.service.Purchase(ctx, key, quantity)
		return func() { c.applyStore(s) }, e
	})
}
func (c *PetController) Equip(key string) {
	c.query("equip", func(ctx context.Context) (func(), error) {
		s, e := c.service.Equip(ctx, key)
		return func() { c.applyStore(s) }, e
	})
}
func (c *PetController) Unequip(slot string) {
	c.query("unequip", func(ctx context.Context) (func(), error) {
		s, e := c.service.Unequip(ctx, slot)
		return func() { c.applyStore(s) }, e
	})
}
func (c *PetController) Use(key string, quantity int64) {
	c.query("use", func(ctx context.Context) (func(), error) {
		s, e := c.service.Use(ctx, key, quantity)
		return func() { c.applyStore(s) }, e
	})
}
func (c *PetController) CheckInNow() {
	c.query("check-in", func(ctx context.Context) (func(), error) {
		r, e := c.service.ClaimCheckIn(ctx)
		return func() { c.LastClaim = &r; c.CheckIn = &r.State; c.applyStore(r.State.StoreState) }, e
	})
}
func (c *PetController) Makeup() {
	c.query("makeup", func(ctx context.Context) (func(), error) {
		r, e := c.service.MakeupCheckIn(ctx)
		return func() { c.LastMakeup = &r; c.CheckIn = &r.State; c.applyStore(r.State.StoreState) }, e
	})
}
func (c *PetController) Draw() {
	c.query("draw", func(ctx context.Context) (func(), error) {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		r, e := c.service.DrawBlindBox(ctx)
		return func() { c.LastDraw = &r; c.BlindBox = &r.State; c.applyStore(r.State.StoreState) }, e
	})
}
func (c *PetController) GiftBox() {
	c.query("gift", func(ctx context.Context) (func(), error) {
		r, e := c.service.GiftBox(ctx)
		return func() { c.LastGift = &r; c.applyStore(r.State) }, e
	})
}
func (c *PetController) SettingsSave(value domain.PetSettings) {
	c.query("settings", func(ctx context.Context) (func(), error) {
		s, e := c.service.SaveSettings(ctx, value)
		return func() { c.Settings = &s }, e
	})
}
func (c *PetController) SaveSettings(value domain.PetSettings) { c.SettingsSave(value) }
func (c *PetController) Workflow(source, key string) {
	c.query("workflow", func(ctx context.Context) (func(), error) {
		p, e := c.service.Workflow(ctx, source, key)
		return func() { c.Profile = &p }, e
	})
}
func (c *PetController) Interaction(action string) {
	c.query("interaction", func(ctx context.Context) (func(), error) {
		p, e := c.service.Interact(ctx, action)
		return func() { c.Profile = &p }, e
	})
}
func (c *PetController) Redeem(key string) {
	c.query("redeem", func(ctx context.Context) (func(), error) {
		r, e := c.service.Redeem(ctx, key)
		return func() { c.LastRedeem = &r; c.applyStore(r.State) }, e
	})
}
func (c *PetController) Close() {
	if c.closed {
		return
	}
	c.closed = true
	c.revision++
	if c.queryCancel != nil {
		c.queryCancel()
	}
	if c.cancel != nil {
		c.cancel()
	}
}
