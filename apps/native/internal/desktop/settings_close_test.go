package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

type settingsCloseReply struct {
	value domain.Settings
	err   error
}

type settingsCloseLoad struct {
	reply chan settingsCloseReply
}

type settingsCloseSave struct {
	value      domain.Settings
	credential domain.CredentialUpdate
	reply      chan settingsCloseReply
}

type settingsCloseService struct {
	loads chan settingsCloseLoad
	saves chan settingsCloseSave
}

func (service *settingsCloseService) Settings(ctx context.Context) (domain.Settings, error) {
	call := settingsCloseLoad{reply: make(chan settingsCloseReply, 1)}
	select {
	case service.loads <- call:
	case <-ctx.Done():
		return domain.Settings{}, ctx.Err()
	}
	select {
	case reply := <-call.reply:
		return reply.value, reply.err
	case <-ctx.Done():
		return domain.Settings{}, ctx.Err()
	}
}

func (service *settingsCloseService) SaveSettings(ctx context.Context, value domain.Settings, credential domain.CredentialUpdate) (domain.Settings, application.MutationOutcome, error) {
	call := settingsCloseSave{value: value, credential: credential, reply: make(chan settingsCloseReply, 1)}
	select {
	case service.saves <- call:
	case <-ctx.Done():
		return domain.Settings{}, application.MutationOutcome{}, ctx.Err()
	}
	select {
	case reply := <-call.reply:
		return reply.value, application.MutationOutcome{}, reply.err
	case <-ctx.Done():
		return domain.Settings{}, application.MutationOutcome{}, ctx.Err()
	}
}

type settingsCloseFixture struct {
	service    *settingsCloseService
	controller *client.SettingsController
	callbacks  chan func()
	gate       settingsCloseGate
	resumes    int
	failures   []error
	applied    domain.UIPreferences
}

func newSettingsCloseFixture(t *testing.T) *settingsCloseFixture {
	t.Helper()
	fixture := &settingsCloseFixture{
		service: &settingsCloseService{
			loads: make(chan settingsCloseLoad, 8),
			saves: make(chan settingsCloseSave, 8),
		},
		callbacks: make(chan func(), 16),
	}
	fixture.controller = client.NewSettings(fixture.service, func(callback func()) {
		fixture.callbacks <- callback
	}, func() {}, func(preferences domain.UIPreferences) {
		fixture.applied = preferences
	}, client.StartupPreferences{Locale: "en-US", ThemeMode: "dark"})
	t.Cleanup(fixture.controller.Close)
	return fixture
}

func receiveSettingsClose[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("settings close operation did not respond")
		var zero T
		return zero
	}
}

func (fixture *settingsCloseFixture) dispatch(t *testing.T) {
	t.Helper()
	receiveSettingsClose(t, fixture.callbacks)()
}

func (fixture *settingsCloseFixture) load(t *testing.T) {
	t.Helper()
	fixture.controller.Load()
	call := receiveSettingsClose(t, fixture.service.loads)
	call.reply <- settingsCloseReply{value: domain.DefaultSettings()}
	fixture.dispatch(t)
	if !fixture.controller.Loaded || fixture.controller.Busy || fixture.controller.Dirty() || fixture.controller.Error != nil {
		t.Fatal("settings close fixture did not load clean settings")
	}
}

func (fixture *settingsCloseFixture) publish(t *testing.T, call settingsCloseSave) domain.Settings {
	t.Helper()
	saved := call.value
	saved.Revision++
	call.reply <- settingsCloseReply{value: saved}
	fixture.dispatch(t)
	return saved
}

func (fixture *settingsCloseFixture) allow() bool {
	return fixture.gate.allow(fixture.controller, func() {
		fixture.resumes++
	}, func(err error) {
		fixture.failures = append(fixture.failures, err)
	})
}

func TestSettingsCloseFlushesUnfocusedDraftBeforeWorkspaceCommit(t *testing.T) {
	fixture := newSettingsCloseFixture(t)
	fixture.load(t)
	controller := fixture.controller
	controller.Draft.DefaultHotwords = "unfocused draft"
	controller.Draft.Threads = 8
	controller.ClearCredential = true
	workspace := &Workspace{
		settings:    &SettingsView{Client: controller},
		preferences: domain.DefaultUIPreferences(),
	}
	resumes := 0
	resume := func() { resumes++ }
	if workspace.allowWorkspaceClose(resume) || !workspace.settingsClosing.pending || workspace.closing.committing || resumes != 0 {
		t.Fatal("workspace committed before flushing the unfocused settings draft")
	}
	call := receiveSettingsClose(t, fixture.service.saves)
	if call.value.DefaultHotwords != "unfocused draft" || call.value.Threads != 8 || call.value.Locale != "zh-CN" || call.value.ThemeMode != "auto" || call.credential.Action != domain.ClearCredential {
		t.Fatal("workspace flush lost draft edits or credential intent, or persisted startup preferences")
	}
	saved := fixture.publish(t, call)
	if workspace.settingsClosing.pending || workspace.closing.committing || resumes != 1 || controller.Busy || controller.Dirty() || controller.Draft != saved {
		t.Fatal("workspace did not resume exactly once after settings publication")
	}
	if fixture.applied.Locale != "en-US" || fixture.applied.ThemeMode != "dark" {
		t.Fatal("closing flush replaced the session startup preferences")
	}
	if !workspace.allowWorkspaceClose(resume) || !workspace.closing.committing || resumes != 1 || len(fixture.service.saves) != 0 {
		t.Fatal("resumed clean workspace close did not commit or saved the settings twice")
	}
}

func TestSettingsCloseDrainsEditsAddedDuringSave(t *testing.T) {
	fixture := newSettingsCloseFixture(t)
	fixture.load(t)
	controller := fixture.controller
	controller.Draft.DefaultHotwords = "initial draft"
	controller.ClearCredential = true
	if fixture.allow() {
		t.Fatal("dirty settings closed before publication")
	}
	first := receiveSettingsClose(t, fixture.service.saves)
	controller.Draft.SummaryTemplate = "edited during close"
	controller.Draft.Threads = 8
	for range 3 {
		if fixture.allow() {
			t.Fatal("repeated close bypassed pending settings publication")
		}
	}
	fixture.publish(t, first)
	if !fixture.gate.pending || fixture.resumes != 0 || len(fixture.failures) != 0 {
		t.Fatal("settings close resumed before draining edits made during the save")
	}
	second := receiveSettingsClose(t, fixture.service.saves)
	if second.value.DefaultHotwords != "initial draft" || second.value.SummaryTemplate != "edited during close" || second.value.Threads != 8 || second.value.Revision != 1 || second.credential.Action != domain.KeepCredential {
		t.Fatal("closing flush lost later edits or replayed credential clearing")
	}
	saved := fixture.publish(t, second)
	if fixture.gate.pending || fixture.resumes != 1 || len(fixture.failures) != 0 || controller.Busy || controller.Dirty() || controller.Draft != saved {
		t.Fatal("settings close did not settle after draining the complete draft")
	}
	if !fixture.allow() || fixture.resumes != 1 || len(fixture.service.saves) != 0 {
		t.Fatal("clean close started a duplicate flush or callback")
	}
}

func TestSettingsCloseDeduplicatesRequestsDuringAnExistingSave(t *testing.T) {
	fixture := newSettingsCloseFixture(t)
	fixture.load(t)
	fixture.controller.Draft.Threads = 8
	fixture.controller.Save()
	call := receiveSettingsClose(t, fixture.service.saves)
	for range 6 {
		if fixture.allow() {
			t.Fatal("close bypassed an existing save")
		}
	}
	if !fixture.gate.pending || fixture.resumes != 0 || len(fixture.failures) != 0 || len(fixture.service.saves) != 0 {
		t.Fatal("repeated close requests duplicated a save or callback")
	}
	fixture.publish(t, call)
	if fixture.gate.pending || fixture.resumes != 1 || fixture.controller.Busy || fixture.controller.Dirty() || len(fixture.service.saves) != 0 || len(fixture.failures) != 0 {
		t.Fatal("waiting for an existing save did not resume once without another write")
	}
	for range 3 {
		if !fixture.allow() {
			t.Fatal("saved settings blocked a subsequent close")
		}
	}
	if fixture.resumes != 1 || fixture.controller.Busy || len(fixture.service.saves) != 0 {
		t.Fatal("clean repeated close requests repeated the save or resume callback")
	}
}

func TestSettingsCloseFailureClearsPendingPreservesDraftAndCanRetry(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "save failure"
		if conflict {
			name = "conflict limit"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newSettingsCloseFixture(t)
			fixture.load(t)
			controller := fixture.controller
			controller.Draft.DefaultHotwords = "draft to retry"
			controller.ClearCredential = true
			if fixture.allow() || fixture.allow() {
				t.Fatal("settings close did not wait for the failed save")
			}
			attempts := 1
			var wantError error = domain.ErrCredentialStore
			if conflict {
				attempts, wantError = 3, domain.ErrSettingsConflict
			}
			for attempt := range attempts {
				call := receiveSettingsClose(t, fixture.service.saves)
				if call.value.DefaultHotwords != "draft to retry" || call.value.Revision != int64(attempt) || call.credential.Action != domain.ClearCredential {
					t.Fatal("closing retry lost the draft, revision or credential intent")
				}
				if conflict {
					remote := domain.DefaultSettings()
					remote.Revision = int64(attempt + 1)
					call.reply <- settingsCloseReply{err: &domain.SettingsConflict{Current: remote}}
				} else {
					call.reply <- settingsCloseReply{err: wantError}
				}
				fixture.dispatch(t)
			}
			if fixture.gate.pending || fixture.resumes != 0 || len(fixture.failures) != 1 || !errors.Is(fixture.failures[0], wantError) || controller.Busy || !controller.Dirty() || !controller.ClearCredential || controller.Draft.DefaultHotwords != "draft to retry" {
				t.Fatal("save failure left close pending, resumed exit or discarded the settings draft")
			}
			if fixture.allow() || !fixture.gate.pending {
				t.Fatal("failed settings close could not start a new flush")
			}
			retry := receiveSettingsClose(t, fixture.service.saves)
			if retry.value.DefaultHotwords != "draft to retry" || retry.credential.Action != domain.ClearCredential {
				t.Fatal("retry did not preserve the unsaved settings and credential intent")
			}
			fixture.publish(t, retry)
			if fixture.gate.pending || fixture.resumes != 1 || len(fixture.failures) != 1 || controller.Busy || controller.Dirty() || len(fixture.service.saves) != 0 {
				t.Fatal("retry did not complete independently of the failed close callback")
			}
		})
	}
}

func TestSettingsCloseWaitsForInitialLoadWithoutSavingCleanSettings(t *testing.T) {
	fixture := newSettingsCloseFixture(t)
	fixture.controller.Load()
	call := receiveSettingsClose(t, fixture.service.loads)
	for range 4 {
		if fixture.allow() {
			t.Fatal("settings close bypassed the initial settings load")
		}
	}
	if !fixture.gate.pending || fixture.resumes != 0 || len(fixture.failures) != 0 || len(fixture.service.loads) != 0 || len(fixture.service.saves) != 0 {
		t.Fatal("waiting for load duplicated a request or prematurely resumed closing")
	}
	call.reply <- settingsCloseReply{value: domain.DefaultSettings()}
	fixture.dispatch(t)
	if fixture.gate.pending || fixture.resumes != 1 || len(fixture.failures) != 0 || !fixture.controller.Loaded || fixture.controller.Busy || fixture.controller.Dirty() || len(fixture.service.saves) != 0 {
		t.Fatal("initial load did not release close without persisting startup preferences")
	}
}

func TestSettingsCloseWaitsForRefreshAndFlushesUnfocusedEdits(t *testing.T) {
	fixture := newSettingsCloseFixture(t)
	fixture.load(t)
	fixture.controller.Load()
	load := receiveSettingsClose(t, fixture.service.loads)
	fixture.controller.Draft.Threads = 8
	if fixture.allow() {
		t.Fatal("settings close bypassed refresh with unsaved edits")
	}
	remote := domain.DefaultSettings()
	remote.Threads, remote.DownloadSource, remote.Revision = 4, "official", 7
	load.reply <- settingsCloseReply{value: remote}
	fixture.dispatch(t)
	if !fixture.gate.pending || fixture.resumes != 0 || len(fixture.failures) != 0 {
		t.Fatal("refresh completion resumed closing before saving the remaining draft")
	}
	call := receiveSettingsClose(t, fixture.service.saves)
	if call.value.Threads != 8 || call.value.DownloadSource != "official" || call.value.Revision != 7 || call.value.Locale != remote.Locale || call.value.ThemeMode != remote.ThemeMode {
		t.Fatal("refresh flush lost draft edits or newer repository values")
	}
	fixture.publish(t, call)
	if fixture.gate.pending || fixture.resumes != 1 || fixture.controller.Busy || fixture.controller.Dirty() || len(fixture.failures) != 0 {
		t.Fatal("close did not resume after refreshing and saving the full draft")
	}
}

func TestSettingsCloseLoadFailureReleasesPendingAndCanRetry(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		name := "initial load"
		if loaded {
			name = "refresh with draft"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newSettingsCloseFixture(t)
			if loaded {
				fixture.load(t)
				fixture.controller.Draft.Threads = 8
			}
			fixture.controller.Load()
			load := receiveSettingsClose(t, fixture.service.loads)
			if fixture.allow() {
				t.Fatal("close bypassed a settings load")
			}
			load.reply <- settingsCloseReply{err: domain.ErrCredentialStore}
			fixture.dispatch(t)
			if fixture.gate.pending || fixture.resumes != 0 || len(fixture.failures) != 1 || !errors.Is(fixture.failures[0], domain.ErrCredentialStore) || fixture.controller.Busy || fixture.controller.Loaded != loaded {
				t.Fatal("settings load failure left close pending or resumed destruction")
			}
			if loaded {
				if !fixture.controller.Dirty() || fixture.controller.Draft.Threads != 8 || fixture.allow() {
					t.Fatal("refresh failure discarded the draft or prevented a closing retry")
				}
				call := receiveSettingsClose(t, fixture.service.saves)
				if call.value.Threads != 8 {
					t.Fatal("retry did not save the draft preserved after refresh failure")
				}
				fixture.publish(t, call)
			} else {
				fixture.controller.Load()
				retry := receiveSettingsClose(t, fixture.service.loads)
				if fixture.allow() {
					t.Fatal("closing retry did not wait for the retried initial load")
				}
				retry.reply <- settingsCloseReply{value: domain.DefaultSettings()}
				fixture.dispatch(t)
			}
			if fixture.gate.pending || fixture.resumes != 1 || len(fixture.failures) != 1 || fixture.controller.Busy || fixture.controller.Dirty() || len(fixture.service.saves) != 0 {
				t.Fatal("retry did not release close independently of the failed load callback")
			}
		})
	}
}

func TestSettingsCloseFinishSuppressesLateLoadAndSaveCallbacks(t *testing.T) {
	for _, operation := range []string{"load", "save"} {
		for _, fail := range []bool{false, true} {
			name := operation + " success"
			if fail {
				name = operation + " failure"
			}
			t.Run(name, func(t *testing.T) {
				fixture := newSettingsCloseFixture(t)
				var reply chan settingsCloseReply
				value := domain.DefaultSettings()
				if operation == "save" {
					fixture.load(t)
					fixture.controller.Draft.Threads = 8
					if fixture.allow() {
						t.Fatal("save was not pending before finish")
					}
					call := receiveSettingsClose(t, fixture.service.saves)
					reply, value = call.reply, call.value
					value.Revision++
				} else {
					fixture.controller.Load()
					call := receiveSettingsClose(t, fixture.service.loads)
					if fixture.allow() {
						t.Fatal("load was not pending before finish")
					}
					reply = call.reply
				}
				fixture.gate.finish()
				if !fixture.gate.finished || fixture.gate.pending {
					t.Fatal("finish retained the pending settings close")
				}
				result := settingsCloseReply{value: value}
				if fail {
					result.err = domain.ErrCredentialStore
				}
				reply <- result
				fixture.dispatch(t)
				if !fixture.gate.finished || fixture.gate.pending || fixture.resumes != 0 || len(fixture.failures) != 0 || !fixture.allow() || len(fixture.service.saves) != 0 || len(fixture.service.loads) != 0 {
					t.Fatal("late settings callback resumed exit, presented an error or restarted a finished gate")
				}
			})
		}
	}
}

func TestSettingsCloseSessionEndingFinishesWorkspaceBeforeLateSave(t *testing.T) {
	fixture := newSettingsCloseFixture(t)
	fixture.load(t)
	fixture.controller.Draft.Threads = 8
	workspace := &Workspace{settings: &SettingsView{Client: fixture.controller}}
	resumes := 0
	resume := func() { resumes++ }
	if workspace.allowWorkspaceClose(resume) {
		t.Fatal("workspace did not wait for settings publication")
	}
	call := receiveSettingsClose(t, fixture.service.saves)
	ending := &mygo.QuitEvent{SessionEnding: true}
	workspace.handleBeforeQuit(ending)
	if ending.DefaultPrevented() || !workspace.settingsClosing.finished || workspace.settingsClosing.pending || !workspace.closing.committing {
		t.Fatal("OS session ending was blocked or retained a pending settings close")
	}
	call.reply <- settingsCloseReply{err: domain.ErrCredentialStore}
	fixture.dispatch(t)
	if resumes != 0 || !workspace.settingsClosing.finished || workspace.settingsClosing.pending || !workspace.allowWorkspaceClose(resume) {
		t.Fatal("late settings failure restarted workspace exit after session ending")
	}
}

func TestSettingsCloseFlushPrecedesResourceConfirmationAndRechecksAfterApproval(t *testing.T) {
	fixture := newSettingsCloseFixture(t)
	fixture.load(t)
	resources := newCloseFixture(t, application.Members)
	request := func() bool {
		if !fixture.allow() {
			return false
		}
		return resources.request(nil)
	}
	fixture.controller.Draft.Threads = 8
	if request() || resources.prompts != 0 || resources.coordinator.committing || !fixture.gate.pending {
		t.Fatal("resource confirmation or workspace destruction started before the settings flush")
	}
	first := receiveSettingsClose(t, fixture.service.saves)
	fixture.publish(t, first)
	if resources.prompts != 0 || fixture.resumes != 1 || resources.coordinator.committing {
		t.Fatal("settings publication bypassed the resumed resource close checks")
	}
	if request() || resources.prompts != 1 || resources.coordinator.committing {
		t.Fatal("resource draft did not require confirmation after the settings flush")
	}
	for range 3 {
		if request() {
			t.Fatal("repeated workspace close bypassed resource confirmation")
		}
	}
	if resources.prompts != 1 || len(fixture.service.saves) != 0 || fixture.controller.Busy {
		t.Fatal("waiting for resource confirmation repeated the prompt or settings save")
	}
	resources.confirm(false)
	if resources.canceled != 1 || resources.coordinator.committing || !resources.editors[0].Dirty() || fixture.controller.Dirty() {
		t.Fatal("canceling resource close discarded a draft or invalidated saved settings")
	}
	if request() || resources.prompts != 2 {
		t.Fatal("cancel prevented a fresh resource confirmation")
	}
	resources.confirm(true)
	fixture.controller.Draft.DefaultHotwords = "edited after resource approval"
	if request() || resources.coordinator.committing || resources.coordinator.approved == nil || resources.resumes != 1 {
		t.Fatal("resource approval authorized exit before newer settings edits were flushed")
	}
	second := receiveSettingsClose(t, fixture.service.saves)
	if second.value.DefaultHotwords != "edited after resource approval" || second.value.Revision != 1 {
		t.Fatal("resumed workspace close failed to save settings edited after resource approval")
	}
	fixture.publish(t, second)
	if fixture.resumes != 2 || fixture.gate.pending || !request() || !resources.coordinator.committing || resources.prompts != 2 || fixture.controller.Dirty() || fixture.controller.Busy || len(fixture.service.saves) != 0 {
		t.Fatal("workspace did not consume valid resource approval after the final settings flush")
	}
}

func TestSettingsCloseFlushFailureRevokesGlobalAttemptAndKeepsEditorUsable(t *testing.T) {
	for _, approved := range []bool{false, true} {
		name := "pending resource confirmation"
		if approved {
			name = "approved resource confirmation"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newSettingsCloseFixture(t)
			fixture.load(t)
			draft := application.NewResourceDraft(application.Members)
			draft.Member.Name = "Synthetic member"
			port := &resourceEditorPort{draft: draft, saved: make(chan domain.CredentialUpdate, 1)}
			editorCallbacks := make(chan func(), 2)
			editor := client.NewResourceEditor(port, draft, func(callback func()) {
				editorCallbacks <- callback
			}, func() {}, func() {})
			t.Cleanup(editor.Close)
			editor.Draft.Member.Department = "Unsaved department"
			editor.MemberSortOrder = "7"
			resources := newCloseFixture(t)
			guard := &closeGuard{check: func() closeCheck { return resourceCloseCheck(editor) }, focus: func() {}}
			resources.coordinator.register(guard)
			if resources.request(nil) || resources.prompts != 1 {
				t.Fatal("global resource close did not ask before discarding the editor draft")
			}
			oldConfirmation := resources.confirm
			if approved {
				oldConfirmation(true)
				if resources.coordinator.approved == nil || resources.resumes != 1 {
					t.Fatal("resource approval was not queued before the resumed workspace close")
				}
			}
			resourceResumes := resources.resumes
			fixture.controller.Draft.DefaultHotwords = "edited after global close request"
			requestWorkspace := func() bool {
				if !fixture.gate.allow(fixture.controller, func() {
					fixture.resumes++
				}, func(err error) {
					fixture.failures = append(fixture.failures, err)
					resources.coordinator.abort(nil)
				}) {
					return false
				}
				return resources.request(nil)
			}
			if requestWorkspace() || !fixture.gate.pending || resources.coordinator.committing || resources.prompts != 1 {
				t.Fatal("global resource attempt bypassed flushing newer settings edits")
			}
			call := receiveSettingsClose(t, fixture.service.saves)
			if call.value.DefaultHotwords != "edited after global close request" {
				t.Fatal("closing flush did not include the newer settings draft")
			}
			call.reply <- settingsCloseReply{err: domain.ErrCredentialStore}
			fixture.dispatch(t)
			if fixture.gate.pending || fixture.gate.finished || fixture.resumes != 0 || len(fixture.failures) != 1 || !errors.Is(fixture.failures[0], domain.ErrCredentialStore) || fixture.controller.Busy || !fixture.controller.Dirty() || fixture.controller.Draft.DefaultHotwords != "edited after global close request" {
				t.Fatal("failed closing flush retained pending exit or discarded the settings draft")
			}
			if resources.coordinator.pending != nil || resources.coordinator.approved != nil || resources.coordinator.committing || len(resources.coordinator.guards) != 1 || resources.coordinator.guards[0] != guard || resources.canceled != 1 || !editor.Dirty() {
				t.Fatal("failed closing flush retained global authorization, removed guards or discarded the editor")
			}
			oldConfirmation(true)
			if resources.resumes != resourceResumes || resources.coordinator.pending != nil || resources.coordinator.approved != nil || resources.coordinator.committing || resources.canceled != 1 {
				t.Fatal("late resource confirmation restored the aborted global exit")
			}
			if resources.request(guard) || resources.prompts != 2 || resources.coordinator.pending == nil || resources.coordinator.pending.target != guard {
				t.Fatal("aborted global exit blocked a fresh individual editor confirmation")
			}
			resources.confirm(false)
			if resources.canceled != 2 || resources.coordinator.pending != nil || resources.coordinator.approved != nil || resources.coordinator.committing || !editor.Dirty() || editor.Draft.Member.Department != "Unsaved department" || editor.MemberSortOrder != "7" {
				t.Fatal("canceling the individual close lost the editor draft or retained shutdown authorization")
			}
			editor.Save()
			if update := receiveSettingsClose(t, port.saved); update.Action != domain.KeepCredential {
				t.Fatal("editor save after canceled exit changed credential intent")
			}
			receiveSettingsClose(t, editorCallbacks)()
			if editor.Busy || editor.Dirty() || editor.Error != nil || editor.Draft.Member.Department != "Unsaved department" || editor.Draft.Member.SortOrder != 7 || editor.Revision != 1 {
				t.Fatal("editor could not save its preserved draft after the failed global exit and canceled individual close")
			}
			if !resources.request(guard) || resources.prompts != 2 || resources.coordinator.committing || !fixture.controller.Dirty() || fixture.controller.Busy || len(fixture.service.saves) != 0 {
				t.Fatal("saved editor could not close independently or silently flushed the failed settings draft")
			}
			if requestWorkspace() || !fixture.gate.pending {
				t.Fatal("workspace could not retry its settings flush after aborting the global resource attempt")
			}
			retry := receiveSettingsClose(t, fixture.service.saves)
			if retry.value != call.value {
				t.Fatal("retry changed the settings draft preserved after the failed exit")
			}
			fixture.publish(t, retry)
			if fixture.gate.pending || fixture.resumes != 1 || len(fixture.failures) != 1 || fixture.controller.Busy || fixture.controller.Dirty() || !requestWorkspace() || !resources.coordinator.committing || resources.prompts != 2 || len(fixture.service.saves) != 0 {
				t.Fatal("fresh workspace close could not complete after the editor save and successful settings retry")
			}
		})
	}
}
