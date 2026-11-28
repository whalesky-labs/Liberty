package client

import (
	"context"
	"errors"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type SettingsService interface {
	Settings(context.Context) (domain.Settings, error)
	SaveSettings(context.Context, domain.Settings, domain.CredentialUpdate) (domain.Settings, application.MutationOutcome, error)
}

type settingsFailure uint8

const (
	settingsFailureNone settingsFailure = iota
	settingsFailureLoad
	settingsFailureSave
)

type SettingsController struct {
	Draft                        domain.Settings
	Password                     string
	ClearCredential              bool
	Loaded, Busy, CleanupPending bool
	Error                        error
	original                     domain.Settings
	service                      SettingsService
	dispatch                     func(func())
	changed                      func()
	applied                      func(domain.UIPreferences)
	ctx                          context.Context
	cancel                       context.CancelFunc
	queued, closed               bool
	conflicts                    int
	failure                      settingsFailure
	afterSave                    []func(error)
	onSaved                      func(domain.Settings)
	onLoaded                     func(domain.Settings)
	Confirming                   bool
	startup                      StartupPreferences
	selections                   preferenceSelections
}

func NewSettings(service SettingsService, dispatch func(func()), changed func(), applied func(domain.UIPreferences), startup ...StartupPreferences) *SettingsController {
	ctx, cancel := context.WithCancel(context.Background())
	c := &SettingsController{service: service, dispatch: dispatch, changed: changed, applied: applied, ctx: ctx, cancel: cancel}
	if len(startup) > 0 {
		c.startup = startup[0]
	}
	return c
}
func (c *SettingsController) Load() {
	if c.closed || c.Busy {
		return
	}
	c.Busy = true
	c.Error = nil
	c.failure = settingsFailureNone
	c.changed()
	go func() {
		value, err := c.service.Settings(c.ctx)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			if err == nil {
				c.failure = settingsFailureNone
				if c.Loaded {
					c.Draft = rebaseSettings(c.original, c.Draft, value)
				} else {
					c.Draft = value
				}
				c.Draft.UIPreferences = c.selections.apply(c.Draft.UIPreferences)
				c.original = value
				c.Loaded = true
				if c.onLoaded != nil {
					c.onLoaded(value)
				}
				c.applyPreferences(value.UIPreferences)
				if (c.queued || len(c.afterSave) > 0) && c.Dirty() {
					c.save()
					return
				}
				c.queued = false
				c.savedCallbacks()
			} else {
				c.failure = settingsFailureLoad
				c.finishCallbacks(err)
			}
			c.changed()
		})
	}()
}

func (c *SettingsController) CanRetry() bool {
	return !c.closed && c.failure != settingsFailureNone
}

func (c *SettingsController) SaveFailed() bool {
	return !c.closed && c.failure == settingsFailureSave
}

// OnSaved observes each successful durable save, including commits made before
// queued edits finish. SaveThen and Flush wait for the entire draft instead.
func (c *SettingsController) OnSaved(callback func(domain.Settings)) {
	c.onSaved = callback
}

// OnLoaded applies persisted non-UI settings when a snapshot is read.
func (c *SettingsController) OnLoaded(callback func(domain.Settings)) {
	c.onLoaded = callback
}

func (c *SettingsController) Retry() {
	if c.closed || c.Busy {
		return
	}
	switch c.failure {
	case settingsFailureLoad:
		c.Load()
	case settingsFailureSave:
		if c.Dirty() {
			c.Save()
		} else {
			c.Load()
		}
	}
}

func (c *SettingsController) Dirty() bool {
	return c.Loaded && (c.Draft != c.original || c.Password != "" || c.ClearCredential || c.selections.locale != nil || c.selections.themeMode != nil)
}
func (c *SettingsController) Save() {
	if c.closed || !c.Loaded {
		return
	}
	if c.Busy {
		c.queued = true
		return
	}
	if !c.Dirty() {
		return
	}
	c.conflicts = 0
	c.save()
}

// SaveThen waits for queued edits and credential publication before invoking a
// connection check. The callback runs on the UI dispatcher, never a worker.
func (c *SettingsController) SaveThen(next func()) {
	if c.closed || !c.Loaded || next == nil {
		return
	}
	c.Flush(func(err error) {
		if err == nil {
			next()
		}
	})
}
func (c *SettingsController) Flush(done func(error)) {
	if done == nil {
		return
	}
	if c.closed {
		done(context.Canceled)
		return
	}
	c.afterSave = append(c.afterSave, done)
	if !c.Loaded {
		if !c.Busy {
			c.Load()
		}
		return
	}
	if c.Busy || c.Dirty() {
		c.Save()
		return
	}
	c.savedCallbacks()
}
func (c *SettingsController) savedCallbacks() {
	if c.closed || !c.Loaded || c.Busy || c.Dirty() {
		return
	}
	c.finishCallbacks(nil)
}
func (c *SettingsController) finishCallbacks(err error) {
	callbacks := c.afterSave
	c.afterSave = nil
	for _, next := range callbacks {
		next(err)
	}
}
func (c *SettingsController) ConfirmClearToken(confirm func(context.Context) (bool, error)) {
	if c.closed || c.Confirming || confirm == nil {
		return
	}
	c.Confirming = true
	c.Error = nil
	c.failure = settingsFailureNone
	c.changed()
	go func() {
		accepted, err := confirm(c.ctx)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Confirming = false
			if err != nil {
				c.Error = domain.ErrCredentialStore
				c.failure = settingsFailureNone
			} else if accepted {
				c.Password = ""
				c.ClearCredential = true
				c.Save()
			}
			c.changed()
		})
	}()
}
func (c *SettingsController) save() {
	sent, base := c.Draft, c.original
	selections := c.selections
	sent.Revision = base.Revision
	password, clear := c.Password, c.ClearCredential
	credential := domain.CredentialUpdate{Action: domain.KeepCredential}
	if password != "" {
		credential.Action = domain.SetCredential
		credential.Value = domain.NewSecret(password)
	} else if clear {
		credential.Action = domain.ClearCredential
	}
	c.Busy = true
	c.Error = nil
	c.failure = settingsFailureNone
	c.queued = false
	c.changed()
	go func() {
		saved, outcome, err := c.service.SaveSettings(c.ctx, sent, credential)
		c.dispatch(func() {
			if c.closed {
				return
			}
			c.Busy = false
			c.Error = err
			if err != nil {
				c.failure = settingsFailureSave
			}
			c.CleanupPending = outcome.CleanupPending
			var conflict *domain.SettingsConflict
			if errors.As(err, &conflict) {
				c.Draft = rebaseSettings(base, c.Draft, conflict.Current)
				c.Draft.UIPreferences = c.selections.apply(c.Draft.UIPreferences)
				c.original = conflict.Current
				c.conflicts++
				if c.conflicts < 3 {
					c.save()
					return
				}
			} else if err == nil {
				c.failure = settingsFailureNone
				c.Draft = rebaseSettings(sent, c.Draft, saved)
				if c.selections.locale == selections.locale {
					c.selections.locale = nil
				}
				if c.selections.themeMode == selections.themeMode {
					c.selections.themeMode = nil
				}
				c.Draft.UIPreferences = c.selections.apply(c.Draft.UIPreferences)
				c.original = saved
				if c.Password == password {
					c.Password = ""
				}
				if c.ClearCredential == clear {
					c.ClearCredential = false
				}
				c.applyPreferences(saved.UIPreferences)
				if c.onSaved != nil {
					c.onSaved(saved)
				}
				if (c.queued || len(c.afterSave) > 0) && c.Dirty() {
					c.save()
					return
				}
				c.queued = false
				c.savedCallbacks()
			}
			if err != nil {
				c.finishCallbacks(err)
			}
			c.changed()
		})
	}()
}
func mergeSetting[T comparable](base, draft T, remote *T) {
	if draft != base {
		*remote = draft
	}
}

// Reapply only edits made after the supplied baseline. Untouched fields follow
// the latest repository snapshot, including runtime-driven settings revisions.
func rebaseSettings(base, draft, remote domain.Settings) domain.Settings {
	mergeSetting(base.ThemeMode, draft.ThemeMode, &remote.ThemeMode)
	mergeSetting(base.LiquidGlassStyle, draft.LiquidGlassStyle, &remote.LiquidGlassStyle)
	mergeSetting(base.AccentColor, draft.AccentColor, &remote.AccentColor)
	mergeSetting(base.HidePerformanceIndicators, draft.HidePerformanceIndicators, &remote.HidePerformanceIndicators)
	mergeSetting(base.LiveDeviceID, draft.LiveDeviceID, &remote.LiveDeviceID)
	mergeSetting(base.LiveTranslate, draft.LiveTranslate, &remote.LiveTranslate)
	mergeSetting(base.Locale, draft.Locale, &remote.Locale)
	mergeSetting(base.BackendURL, draft.BackendURL, &remote.BackendURL)
	mergeSetting(base.ProcessingMode, draft.ProcessingMode, &remote.ProcessingMode)
	mergeSetting(base.DefaultHotwords, draft.DefaultHotwords, &remote.DefaultHotwords)
	mergeSetting(base.SummaryTemplate, draft.SummaryTemplate, &remote.SummaryTemplate)
	mergeSetting(base.Concurrency, draft.Concurrency, &remote.Concurrency)
	mergeSetting(base.RunnerPath, draft.RunnerPath, &remote.RunnerPath)
	mergeSetting(base.Device, draft.Device, &remote.Device)
	mergeSetting(base.Threads, draft.Threads, &remote.Threads)
	mergeSetting(base.BatchSeconds, draft.BatchSeconds, &remote.BatchSeconds)
	mergeSetting(base.DownloadSource, draft.DownloadSource, &remote.DownloadSource)
	return remote
}
func (c *SettingsController) Close() {
	c.closed = true
	c.cancel()
	c.Password = ""
	c.finishCallbacks(context.Canceled)
}
