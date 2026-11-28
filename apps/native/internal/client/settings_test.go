package client

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type settingsReply struct {
	value domain.Settings
	err   error
}
type settingsCall struct {
	ctx        context.Context
	value      domain.Settings
	credential domain.CredentialUpdate
	reply      chan settingsReply
}
type settingsService struct {
	loaded domain.Settings
	calls  chan settingsCall
}

type failedSettingsLoad struct{ err error }

func (s failedSettingsLoad) Settings(context.Context) (domain.Settings, error) {
	return domain.Settings{}, s.err
}
func (s failedSettingsLoad) SaveSettings(context.Context, domain.Settings, domain.CredentialUpdate) (domain.Settings, application.MutationOutcome, error) {
	return domain.Settings{}, application.MutationOutcome{}, s.err
}

func (s settingsService) Settings(context.Context) (domain.Settings, error) { return s.loaded, nil }
func (s settingsService) SaveSettings(ctx context.Context, value domain.Settings, credential domain.CredentialUpdate) (domain.Settings, application.MutationOutcome, error) {
	call := settingsCall{ctx, value, credential, make(chan settingsReply, 1)}
	s.calls <- call
	reply := <-call.reply
	return reply.value, application.MutationOutcome{}, reply.err
}
func TestSettingsPreservesEditsAcrossSaveAndRuntimeConflict(t *testing.T) {
	service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 4)}
	callbacks := make(chan func(), 8)
	applied := domain.UIPreferences{}
	c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(p domain.UIPreferences) { applied = p })
	defer c.Close()
	c.Load()
	receive(t, callbacks)()
	c.Draft.ThemeMode = "dark"
	c.Password = "synthetic-token"
	c.Save()
	first := receive(t, service.calls)
	c.Draft.Locale = "en-US"
	c.Save()
	// The runtime source selection concurrently advanced settings revision.
	remote := service.loaded
	remote.Revision = 1
	remote.DownloadSource = "official"
	first.reply <- settingsReply{err: &domain.SettingsConflict{Current: remote}}
	receive(t, callbacks)()
	second := receive(t, service.calls)
	if second.value.ThemeMode != "dark" || second.value.Locale != "en-US" || second.value.DownloadSource != "official" || second.value.Revision != 1 || second.credential.Action != domain.SetCredential {
		t.Fatal("dirty rebase lost edits or new runtime settings")
	}
	c.Draft.AccentColor = "#f062a8"
	c.Save()
	saved := second.value
	saved.Revision = 2
	saved.TokenConfigured = true
	second.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	third := receive(t, service.calls)
	if third.value.AccentColor != "#f062a8" || third.value.Revision != 2 || third.credential.Action != domain.KeepCredential {
		t.Fatal("queued save replayed credential or lost edit")
	}
	saved = third.value
	saved.Revision = 3
	third.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	if c.Busy || c.Error != nil || c.Password != "" || c.Draft.Revision != 3 || applied.Locale != "en-US" || applied.AccentColor != "#f062a8" {
		t.Fatal("final preferences differ")
	}
	c.Draft.ThemeMode = "light"
	c.Save()
	last := receive(t, service.calls)
	c.Close()
	if last.ctx.Err() == nil {
		t.Fatal("save not cancelled")
	}
	last.reply <- settingsReply{value: last.value}
	receive(t, callbacks)()
	if applied.ThemeMode != "dark" {
		t.Fatal("closed view applied a late preference")
	}
}

func TestSettingsOnSavedReportsEachCommitWhileSaveThenWaitsForQueuedEdits(t *testing.T) {
	service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 3)}
	callbacks := make(chan func(), 4)
	c := NewSettings(service, func(callback func()) { callbacks <- callback }, func() {}, func(domain.UIPreferences) {})
	defer c.Close()
	var committed []domain.Settings
	var loaded []domain.Settings
	c.OnSaved(func(settings domain.Settings) { committed = append(committed, settings) })
	c.OnLoaded(func(settings domain.Settings) { loaded = append(loaded, settings) })

	c.Load()
	receive(t, callbacks)()
	if len(loaded) != 1 || loaded[0] != service.loaded {
		t.Fatal("loaded settings callback did not report the stored snapshot", loaded)
	}
	c.Draft.ProcessingMode = "remote"
	c.Save()
	first := receive(t, service.calls)
	c.Draft.Threads = 4
	continued := false
	c.SaveThen(func() { continued = true })
	firstSaved := first.value
	firstSaved.Revision++
	first.reply <- settingsReply{value: firstSaved}
	receive(t, callbacks)()
	second := receive(t, service.calls)
	if len(committed) != 1 || committed[0] != firstSaved || continued {
		t.Fatal("committed snapshot was not reported separately from queued completion")
	}
	secondSaved := second.value
	secondSaved.Revision++
	second.reply <- settingsReply{value: secondSaved}
	receive(t, callbacks)()
	if len(committed) != 2 || committed[1] != secondSaved || !continued {
		t.Fatalf("saved callback did not receive the final committed settings: %+v", committed)
	}

	c.Draft.ProcessingMode = "local"
	c.Save()
	failed := receive(t, service.calls)
	failed.reply <- settingsReply{err: errors.New("synthetic save failure")}
	receive(t, callbacks)()
	if len(committed) != 2 {
		t.Fatal("failed settings save was reported as committed")
	}
}

func TestSettingsRebaseDoesNotOverwriteUneditedFields(t *testing.T) {
	base := domain.DefaultSettings()
	draft := base
	draft.DefaultHotwords = "edited"
	draft.LiveTranslate = false
	draft.LiveDeviceID = "3:synthetic-device"
	remote := base
	remote.BackendURL = "https://example.test"
	remote.Threads = 4
	remote.Revision = 9
	got := rebaseSettings(base, draft, remote)
	if got.DefaultHotwords != "edited" || got.LiveTranslate || got.LiveDeviceID != "3:synthetic-device" || got.BackendURL != remote.BackendURL || got.Threads != 4 || got.Revision != 9 {
		t.Fatal("merge lost newer values")
	}
}

func TestSettingsCanReturnToOriginalWhileSaveIsPending(t *testing.T) {
	service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 3)}
	callbacks := make(chan func(), 4)
	c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {})
	defer c.Close()
	c.Load()
	receive(t, callbacks)()
	c.Draft.ThemeMode = "dark"
	c.Save()
	first := receive(t, service.calls)
	c.Draft.ThemeMode = "auto"
	c.Save()
	saved := first.value
	saved.Revision = 1
	first.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	second := receive(t, service.calls)
	if second.value.ThemeMode != "auto" || second.value.Revision != 1 {
		t.Fatal("return to original preference lost")
	}
	saved = second.value
	saved.Revision = 2
	second.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	if c.Busy || c.Draft.ThemeMode != "auto" {
		t.Fatal("pending preference not applied")
	}
}

func TestStartupPreferencesApplyOnlyExplicitFields(t *testing.T) {
	stored := domain.DefaultUIPreferences()
	stored.Locale, stored.ThemeMode = "en-US", "light"
	for _, test := range []struct {
		name      string
		startup   StartupPreferences
		locale    string
		themeMode string
	}{
		{name: "none", locale: "en-US", themeMode: "light"},
		{name: "locale", startup: StartupPreferences{Locale: "zh-CN"}, locale: "zh-CN", themeMode: "light"},
		{name: "theme", startup: StartupPreferences{ThemeMode: "dark"}, locale: "en-US", themeMode: "dark"},
		{name: "explicit auto", startup: StartupPreferences{ThemeMode: "auto"}, locale: "en-US", themeMode: "auto"},
		{name: "both", startup: StartupPreferences{Locale: "zh-CN", ThemeMode: "auto"}, locale: "zh-CN", themeMode: "auto"},
	} {
		t.Run(test.name, func(t *testing.T) {
			want := stored
			want.Locale, want.ThemeMode = test.locale, test.themeMode
			if got := test.startup.Apply(stored); got != want {
				t.Fatalf("startup preferences = %+v, want %+v", got, want)
			}
		})
	}
}

func TestSettingsStartupPreferencesRemainSessionOnlyAcrossLoadRefreshAndSave(t *testing.T) {
	stored := domain.DefaultSettings()
	service := &settingsService{loaded: stored, calls: make(chan settingsCall, 4)}
	callbacks := make(chan func(), 8)
	startup := StartupPreferences{Locale: "en-US", ThemeMode: "dark"}
	var applied domain.UIPreferences
	c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(preferences domain.UIPreferences) {
		applied = preferences
		if preferences.Locale != startup.Locale || preferences.ThemeMode != startup.ThemeMode {
			t.Fatalf("session preferences reverted: %+v", preferences)
		}
	}, startup)
	defer c.Close()
	c.Load()
	if !c.Busy || c.Loaded || len(service.calls) != 0 {
		t.Fatal("startup unexpectedly saved settings")
	}
	receive(t, callbacks)()
	if c.Draft != stored || c.original != stored || c.Dirty() || c.UIPreferences() != startup.Apply(stored.UIPreferences) {
		t.Fatal("startup preferences contaminated persisted settings or the draft")
	}
	c.Save()
	checked := false
	c.SaveThen(func() { checked = true })
	if !checked || len(service.calls) != 0 {
		t.Fatal("clean startup preferences triggered a save or blocked SaveThen")
	}
	c.Draft.Threads = 4
	c.ClearCredential = true
	c.Save()
	first := receive(t, service.calls)
	if first.value.Locale != stored.Locale || first.value.ThemeMode != stored.ThemeMode || first.credential.Action != domain.ClearCredential {
		t.Fatal("unrelated save persisted startup preferences or lost credential intent")
	}
	remote := stored
	remote.ThemeMode, remote.DownloadSource, remote.Revision = "light", "official", 1
	first.reply <- settingsReply{err: &domain.SettingsConflict{Current: remote}}
	receive(t, callbacks)()
	second := receive(t, service.calls)
	if second.value.Locale != remote.Locale || second.value.ThemeMode != remote.ThemeMode || second.value.Threads != 4 || second.value.DownloadSource != "official" || second.value.Revision != 1 || second.credential.Action != domain.ClearCredential {
		t.Fatal("conflict rebase persisted startup preferences or lost pending changes")
	}
	c.Draft.AccentColor = "#f062a8"
	c.Save()
	saved := second.value
	saved.Revision = 2
	second.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	third := receive(t, service.calls)
	if third.value.Locale != remote.Locale || third.value.ThemeMode != remote.ThemeMode || third.value.AccentColor != "#f062a8" || third.value.Revision != 2 || third.credential.Action != domain.KeepCredential {
		t.Fatal("queued save leaked startup preferences or replayed credential clearing")
	}
	saved = third.value
	saved.Revision = 3
	third.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	if c.Dirty() || c.Busy || c.Error != nil || c.ClearCredential || applied.AccentColor != saved.AccentColor {
		t.Fatal("unrelated queued saves did not settle")
	}
	service.loaded = saved
	service.loaded.ThemeMode, service.loaded.Revision = "auto", 4
	service.loaded.LiquidGlassStyle = "tinted"
	c.Load()
	receive(t, callbacks)()
	if c.Dirty() || c.Draft != service.loaded || applied.LiquidGlassStyle != "tinted" || c.UIPreferences() != applied || len(service.calls) != 0 {
		t.Fatal("refresh lost the session projection or changed stored settings")
	}
}

func TestSettingsStartupPreferenceSelectionsPersistIndependently(t *testing.T) {
	for _, test := range []struct {
		name      string
		locale    string
		themeMode string
	}{
		{name: "locale equals stored", locale: "zh-CN"},
		{name: "locale equals startup", locale: "en-US"},
		{name: "theme equals stored", themeMode: "auto"},
		{name: "theme equals startup", themeMode: "dark"},
		{name: "third theme", themeMode: "light"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stored := domain.DefaultSettings()
			service := &settingsService{loaded: stored, calls: make(chan settingsCall, 3)}
			callbacks := make(chan func(), 4)
			startup := StartupPreferences{Locale: "en-US", ThemeMode: "dark"}
			var applied domain.UIPreferences
			c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(preferences domain.UIPreferences) { applied = preferences }, startup)
			defer c.Close()
			c.Load()
			receive(t, callbacks)()
			wantStored, wantApplied := stored, startup.Apply(stored.UIPreferences)
			if test.locale != "" {
				c.SetLocale(test.locale)
				wantStored.Locale, wantApplied.Locale = test.locale, test.locale
			} else {
				c.SetThemeMode(test.themeMode)
				wantStored.ThemeMode, wantApplied.ThemeMode = test.themeMode, test.themeMode
			}
			first := receive(t, service.calls)
			if first.value != wantStored || first.credential.Action != domain.KeepCredential || c.UIPreferences() != wantApplied {
				t.Fatal("explicit selection saved the other startup preference or ignored the selected value")
			}
			remote := stored
			remote.Locale, remote.ThemeMode, remote.Revision = "en-US", "light", 1
			first.reply <- settingsReply{err: &domain.SettingsConflict{Current: remote}}
			receive(t, callbacks)()
			second := receive(t, service.calls)
			wantStored = remote
			if test.locale != "" {
				wantStored.Locale = test.locale
			} else {
				wantStored.ThemeMode = test.themeMode
			}
			if second.value != wantStored || second.credential.Action != domain.KeepCredential {
				t.Fatal("conflict replaced the explicit selection, including one equal to the old stored value")
			}
			saved := second.value
			saved.Revision = 2
			second.reply <- settingsReply{value: saved}
			receive(t, callbacks)()
			if applied != wantApplied || c.Dirty() || c.Busy || c.Error != nil {
				t.Fatal("selected preference did not apply or failed to release its startup override")
			}
			service.loaded = saved
			service.loaded.Locale, service.loaded.ThemeMode, service.loaded.Revision = "zh-CN", "auto", 3
			c.Load()
			receive(t, callbacks)()
			wantApplied = startup.Apply(service.loaded.UIPreferences)
			if test.locale != "" {
				wantApplied.Locale = service.loaded.Locale
			} else {
				wantApplied.ThemeMode = service.loaded.ThemeMode
			}
			if applied != wantApplied || c.UIPreferences() != wantApplied || c.Dirty() || len(service.calls) != 0 {
				t.Fatal("refresh reactivated a released startup preference or released the other preference")
			}
		})
	}
}

func TestSettingsStartupPreferencesQueuedSelectionsPreserveLatestIntent(t *testing.T) {
	service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 6)}
	callbacks := make(chan func(), 8)
	var applied domain.UIPreferences
	c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(preferences domain.UIPreferences) { applied = preferences }, StartupPreferences{Locale: "en-US", ThemeMode: "dark"})
	defer c.Close()
	c.Load()
	receive(t, callbacks)()
	c.ClearCredential = true
	c.Save()
	first := receive(t, service.calls)
	c.SetLocale("en-US")
	c.SetThemeMode("dark")
	checked := false
	c.SaveThen(func() { checked = true })
	saved := first.value
	saved.Revision = 1
	first.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	second := receive(t, service.calls)
	if first.credential.Action != domain.ClearCredential || second.credential.Action != domain.KeepCredential || second.value.Locale != "en-US" || second.value.ThemeMode != "dark" || checked {
		t.Fatal("queued preference selection lost an edit, replayed credentials or ran SaveThen early")
	}
	c.SetLocale("zh-CN")
	c.SetThemeMode("auto")
	remote := second.value
	remote.Revision = 2
	remote.DownloadSource = "official"
	second.reply <- settingsReply{err: &domain.SettingsConflict{Current: remote}}
	receive(t, callbacks)()
	third := receive(t, service.calls)
	if third.value.Locale != "zh-CN" || third.value.ThemeMode != "auto" || third.value.DownloadSource != "official" || third.value.Revision != 2 {
		t.Fatal("latest preference selection lost during conflict rebase")
	}
	c.SetLocale("en-US")
	c.SetThemeMode("dark")
	c.SetLocale("zh-CN")
	c.SetThemeMode("auto")
	saved = third.value
	saved.Revision = 3
	third.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	fourth := receive(t, service.calls)
	if fourth.value.Locale != "zh-CN" || fourth.value.ThemeMode != "auto" || fourth.value.Revision != 3 || applied.Locale != "zh-CN" || applied.ThemeMode != "auto" || checked {
		t.Fatal("an in-flight save acknowledged a newer selection or applied stale preferences")
	}
	remote = fourth.value
	remote.Locale, remote.ThemeMode, remote.Revision = "en-US", "dark", 4
	fourth.reply <- settingsReply{err: &domain.SettingsConflict{Current: remote}}
	receive(t, callbacks)()
	fifth := receive(t, service.calls)
	if fifth.value.Locale != "zh-CN" || fifth.value.ThemeMode != "auto" || fifth.value.Revision != 4 || fifth.credential.Action != domain.KeepCredential {
		t.Fatal("reselection of an in-flight value lost its intent during the next conflict")
	}
	saved = fifth.value
	saved.Revision = 5
	fifth.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	if c.Dirty() || c.Busy || c.Error != nil || !checked || applied != saved.UIPreferences || c.UIPreferences() != applied {
		t.Fatal("queued selections or SaveThen did not settle after final publication")
	}
}

func TestSettingsStartupPreferenceSelectionSurvivesFailedSaveAndRefresh(t *testing.T) {
	service := &settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 3)}
	callbacks := make(chan func(), 4)
	var applied domain.UIPreferences
	c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(preferences domain.UIPreferences) { applied = preferences }, StartupPreferences{Locale: "en-US", ThemeMode: "dark"})
	defer c.Close()
	c.Load()
	receive(t, callbacks)()
	c.SetLocale("zh-CN")
	first := receive(t, service.calls)
	checked := false
	c.SaveThen(func() { checked = true })
	first.reply <- settingsReply{err: domain.ErrResourceInvalid}
	receive(t, callbacks)()
	if !c.Dirty() || c.Busy || c.Error == nil || checked {
		t.Fatal("failed save discarded explicit selection or ran SaveThen")
	}
	service.loaded.Locale, service.loaded.ThemeMode, service.loaded.Revision = "en-US", "light", 1
	c.Load()
	receive(t, callbacks)()
	retry := receive(t, service.calls)
	if retry.value.Locale != "zh-CN" || retry.value.ThemeMode != "light" || retry.value.Revision != 1 || applied.Locale != "zh-CN" || applied.ThemeMode != "dark" {
		t.Fatal("refresh lost the explicit selection or persisted the remaining startup theme")
	}
	saved := retry.value
	saved.Revision = 2
	retry.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	if c.Dirty() || c.Busy || c.Error != nil || checked || applied.Locale != "zh-CN" || applied.ThemeMode != "dark" {
		t.Fatal("retry did not settle independently of the failed SaveThen callback")
	}
	c.Close()
	c.SetLocale("en-US")
	c.SetThemeMode("light")
	if c.Draft != saved || len(service.calls) != 0 {
		t.Fatal("closed controller accepted a preference selection")
	}
}

func TestSettingsFlushSavesDraftAndEditsMadeDuringPublication(t *testing.T) {
	service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 3)}
	callbacks := make(chan func(), 4)
	c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {}, StartupPreferences{Locale: "en-US", ThemeMode: "dark"})
	defer c.Close()
	c.Load()
	receive(t, callbacks)()
	c.Draft.DefaultHotwords = "unfocused draft"
	c.ClearCredential = true
	completed := 0
	c.Flush(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
		completed++
	})
	first := receive(t, service.calls)
	if completed != 0 || first.value.DefaultHotwords != "unfocused draft" || first.value.Locale != "zh-CN" || first.value.ThemeMode != "auto" || first.credential.Action != domain.ClearCredential {
		t.Fatal("flush completed before saving the unfocused draft or persisted startup preferences")
	}
	c.Draft.Threads = 8
	saved := first.value
	saved.Revision = 1
	first.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	second := receive(t, service.calls)
	if completed != 0 || second.value.Threads != 8 || second.value.Revision != 1 || second.credential.Action != domain.KeepCredential {
		t.Fatal("flush did not drain later draft edits or replayed credential intent")
	}
	saved = second.value
	saved.Revision = 2
	second.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	if completed != 1 || c.Dirty() || c.Busy || c.Draft != saved {
		t.Fatal("flush did not finish exactly once after the entire draft was saved")
	}
	c.Flush(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
		completed++
	})
	if completed != 2 || len(service.calls) != 0 {
		t.Fatal("clean flush should complete without another save")
	}
}

func TestSettingsFlushFailureCompletesWaitersAndPreservesDraft(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "save failure"
		if conflict {
			name = "conflict limit"
		}
		t.Run(name, func(t *testing.T) {
			service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 4)}
			callbacks := make(chan func(), 5)
			c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {})
			defer c.Close()
			c.Load()
			receive(t, callbacks)()
			c.Draft.DefaultHotwords = "draft to retry"
			results := make(chan error, 3)
			c.Flush(func(err error) { results <- err })
			c.Flush(func(err error) { results <- err })
			attempts := 1
			var wantError error = domain.ErrResourceInvalid
			if conflict {
				attempts, wantError = 3, domain.ErrSettingsConflict
			}
			for attempt := range attempts {
				call := receive(t, service.calls)
				if call.value.DefaultHotwords != "draft to retry" || call.value.Revision != int64(attempt) {
					t.Fatal("flush retry lost the draft or revision")
				}
				if conflict {
					remote := service.loaded
					remote.Revision = int64(attempt + 1)
					call.reply <- settingsReply{err: &domain.SettingsConflict{Current: remote}}
				} else {
					call.reply <- settingsReply{err: wantError}
				}
				receive(t, callbacks)()
			}
			for range 2 {
				if got := receive(t, results); !errors.Is(got, wantError) {
					t.Fatalf("flush result = %v, want %v", got, wantError)
				}
			}
			if !c.Dirty() || c.Busy || c.Draft.DefaultHotwords != "draft to retry" || len(c.afterSave) != 0 || len(service.calls) != 0 {
				t.Fatal("failed flush discarded the draft, kept waiters pending or retried indefinitely")
			}
			c.Flush(func(err error) { results <- err })
			retry := receive(t, service.calls)
			saved := retry.value
			saved.Revision++
			retry.reply <- settingsReply{value: saved}
			receive(t, callbacks)()
			if got := receive(t, results); got != nil || c.Dirty() || c.Busy || len(results) != 0 {
				t.Fatal("flush could not be retried after failure or invoked old waiters again")
			}
		})
	}
}

func TestSettingsFlushWaitsForInFlightSaveWithoutDuplicateWrite(t *testing.T) {
	service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 2)}
	callbacks := make(chan func(), 3)
	c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {})
	defer c.Close()
	c.Load()
	receive(t, callbacks)()
	c.Draft.Threads = 8
	c.Save()
	call := receive(t, service.calls)
	completed := false
	c.Flush(func(err error) {
		if err != nil {
			t.Fatal(err)
		}
		completed = true
	})
	if completed {
		t.Fatal("flush completed before the in-flight save")
	}
	saved := call.value
	saved.Revision = 1
	call.reply <- settingsReply{value: saved}
	receive(t, callbacks)()
	if !completed || c.Busy || c.Dirty() || c.Draft != saved || len(service.calls) != 0 {
		t.Fatal("flush should complete after publication without saving an unchanged snapshot again")
	}
}

func TestSettingsFlushWaitsForLoadAndReportsLoadFailure(t *testing.T) {
	t.Run("initial load", func(t *testing.T) {
		service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 1)}
		callbacks := make(chan func(), 2)
		c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {})
		defer c.Close()
		results := make(chan error, 2)
		c.Flush(func(err error) { results <- err })
		if !c.Busy || len(results) != 0 {
			t.Fatal("flush completed before the initial settings load")
		}
		c.Flush(func(err error) { results <- err })
		receive(t, callbacks)()
		for range 2 {
			if got := receive(t, results); got != nil {
				t.Fatal(got)
			}
		}
		if !c.Loaded || c.Busy || c.Dirty() || len(service.calls) != 0 {
			t.Fatal("loading settings during flush should not save clean settings")
		}
	})
	t.Run("load failure", func(t *testing.T) {
		callbacks := make(chan func(), 2)
		c := NewSettings(failedSettingsLoad{err: domain.ErrCredentialStore}, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {})
		defer c.Close()
		results := make(chan error, 1)
		c.Flush(func(err error) { results <- err })
		receive(t, callbacks)()
		if got := receive(t, results); !errors.Is(got, domain.ErrCredentialStore) || c.Loaded || c.Busy || len(c.afterSave) != 0 {
			t.Fatal("load failure left flush pending")
		}
	})
}

func TestSettingsFlushReportsCancellationOnClose(t *testing.T) {
	service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 1)}
	callbacks := make(chan func(), 2)
	c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {})
	c.Load()
	receive(t, callbacks)()
	c.Draft.Threads = 8
	results := make(chan error, 2)
	c.Flush(func(err error) { results <- err })
	call := receive(t, service.calls)
	c.Close()
	if got := receive(t, results); !errors.Is(got, context.Canceled) || call.ctx.Err() == nil {
		t.Fatal("closing settings did not cancel the pending flush")
	}
	call.reply <- settingsReply{value: call.value}
	receive(t, callbacks)()
	if len(results) != 0 {
		t.Fatal("late save completed the cancelled flush again")
	}
	c.Flush(func(err error) { results <- err })
	if got := receive(t, results); !errors.Is(got, context.Canceled) || len(service.calls) != 0 {
		t.Fatal("flush on a closed controller should report cancellation without saving")
	}
}

func TestSettingsCleanPreferenceReselectionDoesNotSaveBeforeAChangedSelection(t *testing.T) {
	for _, field := range []string{"locale", "theme"} {
		t.Run(field, func(t *testing.T) {
			service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 2)}
			callbacks := make(chan func(), 3)
			c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {})
			defer c.Close()
			c.Load()
			receive(t, callbacks)()
			if field == "locale" {
				c.SetLocale("zh-CN")
			} else {
				c.SetThemeMode("auto")
			}
			if c.Busy || c.Dirty() || len(service.calls) != 0 {
				t.Fatal("focusing an already-selected clean preference triggered a save")
			}
			if field == "locale" {
				c.SetLocale("en-US")
			} else {
				c.SetThemeMode("light")
			}
			call := receive(t, service.calls)
			want := service.loaded
			if field == "locale" {
				want.Locale = "en-US"
			} else {
				want.ThemeMode = "light"
			}
			if call.value != want {
				t.Fatal("first save persisted the focusing click instead of the changed preference")
			}
			saved := call.value
			saved.Revision++
			call.reply <- settingsReply{value: saved}
			receive(t, callbacks)()
			if c.Busy || c.Dirty() || c.Draft != saved || len(service.calls) != 0 {
				t.Fatal("changed preference did not settle after its single save")
			}
		})
	}
}

func TestSettingsCleanPreferenceReselectionLeavesUnrelatedDraftUnflushed(t *testing.T) {
	service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 1)}
	callbacks := make(chan func(), 2)
	c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {})
	defer c.Close()
	c.Load()
	receive(t, callbacks)()
	c.Draft.Threads = 8
	c.SetLocale("zh-CN")
	c.SetThemeMode("auto")
	if c.Busy || !c.Dirty() || c.Draft.Threads != 8 || len(service.calls) != 0 || c.selections.locale != nil || c.selections.themeMode != nil {
		t.Fatal("clean preference reselect should not implicitly save an unrelated draft")
	}
}

func TestSettingsPreferenceReselectionDuringSavePreservesExplicitIntent(t *testing.T) {
	for _, field := range []string{"locale", "theme"} {
		t.Run(field, func(t *testing.T) {
			service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 3)}
			callbacks := make(chan func(), 4)
			c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {})
			defer c.Close()
			c.Load()
			receive(t, callbacks)()
			c.Draft.Threads = 8
			c.Save()
			first := receive(t, service.calls)
			if field == "locale" {
				c.SetLocale("zh-CN")
			} else {
				c.SetThemeMode("auto")
			}
			remote := service.loaded
			remote.Locale, remote.ThemeMode, remote.Revision = "en-US", "light", 1
			first.reply <- settingsReply{err: &domain.SettingsConflict{Current: remote}}
			receive(t, callbacks)()
			second := receive(t, service.calls)
			want := remote
			want.Threads = 8
			if field == "locale" {
				want.Locale = "zh-CN"
			} else {
				want.ThemeMode = "auto"
			}
			if second.value != want {
				t.Fatal("same-value selection made during a save lost its intent on conflict")
			}
			saved := second.value
			saved.Revision++
			second.reply <- settingsReply{value: saved}
			receive(t, callbacks)()
			if c.Busy || c.Dirty() || c.Draft != saved || len(service.calls) != 0 {
				t.Fatal("explicit in-flight selection did not settle after conflict retry")
			}
		})
	}
}

func TestSettingsPreferenceReselectionRetriesFailedExplicitChoice(t *testing.T) {
	for _, field := range []string{"locale", "theme"} {
		t.Run(field, func(t *testing.T) {
			service := settingsService{loaded: domain.DefaultSettings(), calls: make(chan settingsCall, 2)}
			callbacks := make(chan func(), 3)
			c := NewSettings(service, func(f func()) { callbacks <- f }, func() {}, func(domain.UIPreferences) {}, StartupPreferences{Locale: "en-US", ThemeMode: "dark"})
			defer c.Close()
			c.Load()
			receive(t, callbacks)()
			selectPreference := func() {
				if field == "locale" {
					c.SetLocale("zh-CN")
				} else {
					c.SetThemeMode("auto")
				}
			}
			selectPreference()
			first := receive(t, service.calls)
			first.reply <- settingsReply{err: domain.ErrResourceInvalid}
			receive(t, callbacks)()
			if c.Busy || !c.Dirty() || c.Error == nil {
				t.Fatal("failed explicit selection did not remain available to retry")
			}
			selectPreference()
			second := receive(t, service.calls)
			if second.value != first.value {
				t.Fatal("retrying the same explicit choice changed the intended settings")
			}
			saved := second.value
			saved.Revision++
			second.reply <- settingsReply{value: saved}
			receive(t, callbacks)()
			if c.Busy || c.Dirty() || c.Error != nil || len(service.calls) != 0 {
				t.Fatal("same-value selection was incorrectly ignored after a failed explicit save")
			}
		})
	}
}
