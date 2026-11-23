package client

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type newJobDefaultsReply struct {
	value application.JobDefaults
	err   error
}

type newJobDefaultsCall struct{ reply chan newJobDefaultsReply }
type newJobCreateReply struct {
	id  string
	err error
}
type newJobCreateCall struct {
	form  application.CreateMeeting
	reply chan newJobCreateReply
}
type newJobService struct {
	defaults chan newJobDefaultsCall
	creates  chan newJobCreateCall
}

func (service *newJobService) Defaults(context.Context) (application.JobDefaults, error) {
	call := newJobDefaultsCall{reply: make(chan newJobDefaultsReply, 1)}
	service.defaults <- call
	reply := <-call.reply
	return reply.value, reply.err
}
func (service *newJobService) Create(_ context.Context, form application.CreateMeeting) (string, error) {
	call := newJobCreateCall{form: form, reply: make(chan newJobCreateReply, 1)}
	service.creates <- call
	reply := <-call.reply
	return reply.id, reply.err
}

type newJobPicker struct {
	next     chan application.SelectedMedia
	err      error
	released []string
}

func (picker *newJobPicker) Pick(context.Context) (application.SelectedMedia, error) {
	if picker.err != nil {
		return application.SelectedMedia{}, picker.err
	}
	return <-picker.next, nil
}
func (picker *newJobPicker) Release(token string) { picker.released = append(picker.released, token) }

type newJobFixture struct {
	controller *SubmissionController
	service    *newJobService
	picker     *newJobPicker
	callbacks  chan func()
	created    []string
}

func newNewJobFixture(t *testing.T) *newJobFixture {
	t.Helper()
	fixture := &newJobFixture{
		service: &newJobService{defaults: make(chan newJobDefaultsCall, 8), creates: make(chan newJobCreateCall, 8)},
		picker:  &newJobPicker{next: make(chan application.SelectedMedia, 8)}, callbacks: make(chan func(), 16),
	}
	fixture.controller = NewSubmission(fixture.service, fixture.picker, func(callback func()) { fixture.callbacks <- callback }, func() {}, func(id string) {
		fixture.created = append(fixture.created, id)
	})
	t.Cleanup(fixture.controller.Close)
	fixture.controller.Media = &application.SelectedMedia{Token: "synthetic-grant", Name: "synthetic.wav", Kind: "audio"}
	fixture.controller.Form.Selection = fixture.controller.Media.Token
	fixture.controller.Form.Title = "  Synthetic meeting  "
	return fixture
}

func receiveNewJob[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("new job operation did not respond")
		var zero T
		return zero
	}
}
func (fixture *newJobFixture) dispatch(t *testing.T) {
	t.Helper()
	receiveNewJob(t, fixture.callbacks)()
}
func (fixture *newJobFixture) load(t *testing.T, defaults application.JobDefaults) {
	t.Helper()
	receiveNewJob(t, fixture.service.defaults).reply <- newJobDefaultsReply{value: defaults}
	fixture.dispatch(t)
}

func TestNewJobSubmissionWaitsForDefaultsAndIgnoresStaleLoadAfterCommit(t *testing.T) {
	fixture := newNewJobFixture(t)
	initial := receiveNewJob(t, fixture.service.defaults)
	controller := fixture.controller
	controller.SubmitWithDefaults("Fallback minutes")
	controller.SubmitWithDefaults("Fallback minutes")
	if !controller.Busy || len(fixture.service.creates) != 0 {
		t.Fatal("submission did not freeze while defaults were pending")
	}
	defaults := application.JobDefaults{Hotwords: "Current default", SummaryTemplate: "Current minutes"}
	fixture.load(t, defaults)
	call := receiveNewJob(t, fixture.service.creates)
	if call.form.Title != "Synthetic meeting" || call.form.Hotwords != defaults.Hotwords || call.form.SummaryTemplate != defaults.SummaryTemplate || call.form.Selection != "synthetic-grant" {
		t.Fatal("job did not use the selected media and resolved defaults", call.form)
	}
	controller.Form.Language, controller.Form.Speaker = "ja-JP", false
	call.reply <- newJobCreateReply{id: "synthetic-created"}
	fixture.dispatch(t)
	initial.reply <- newJobDefaultsReply{err: errors.New("stale defaults failure")}
	fixture.dispatch(t)
	if controller.Busy || controller.Error != nil || controller.Media != nil || controller.Form.Title != "" || controller.Form.Selection != "" || controller.Form.Language != "zh-CN" || !controller.Form.Speaker || controller.Form.Hotwords != defaults.Hotwords || len(fixture.created) != 1 || len(fixture.picker.released) != 1 || len(fixture.service.creates) != 0 {
		t.Fatal("durable success was undone or the next draft retained the submitted state")
	}
	controller.SubmitWithDefaults("Fallback minutes")
	if len(fixture.service.defaults) != 0 || len(fixture.service.creates) != 0 {
		t.Fatal("a committed form could be submitted again without another media selection")
	}
}

func TestNewJobSubmissionPreservesCustomAndExplicitlyClearedHotwords(t *testing.T) {
	for _, words := range []string{"Custom terms", "", "  "} {
		t.Run(words, func(t *testing.T) {
			fixture := newNewJobFixture(t)
			defaults := application.JobDefaults{Hotwords: "Default terms", SummaryTemplate: "Default minutes"}
			fixture.load(t, defaults)
			fixture.controller.Form.Hotwords = words
			fixture.controller.SubmitWithDefaults("Fallback minutes")
			defaults.Hotwords = "Updated default terms"
			fixture.load(t, defaults)
			call := receiveNewJob(t, fixture.service.creates)
			if call.form.Hotwords != words {
				t.Fatal("submission overwrote the user's hotword choice", call.form.Hotwords)
			}
			call.reply <- newJobCreateReply{err: application.ErrJobInput}
			fixture.dispatch(t)
			if !errors.Is(fixture.controller.Error, application.ErrJobInput) || fixture.controller.Busy || fixture.controller.Media == nil || fixture.controller.Form.Hotwords != words || len(fixture.created) != 0 || len(fixture.picker.released) != 0 {
				t.Fatal("failed creation discarded the draft or selection")
			}
		})
	}
}

func TestNewJobDefaultsRefreshPreservesClearedHotwords(t *testing.T) {
	fixture := newNewJobFixture(t)
	fixture.load(t, application.JobDefaults{Hotwords: "Original terms"})
	fixture.controller.Form.Hotwords = ""
	fixture.controller.RefreshDefaults()
	fixture.load(t, application.JobDefaults{Hotwords: "Updated terms"})
	if fixture.controller.Form.Hotwords != "" {
		t.Fatal("a settings refresh restored explicitly cleared hotwords")
	}
}

func TestNewJobDefaultsErrorsRecoverWithoutReplacingCreationFailures(t *testing.T) {
	fixture := newNewJobFixture(t)
	want := errors.New("synthetic defaults failure")
	receiveNewJob(t, fixture.service.defaults).reply <- newJobDefaultsReply{err: want}
	fixture.dispatch(t)
	var defaultsError *JobDefaultsError
	if !errors.As(fixture.controller.Error, &defaultsError) || !errors.Is(fixture.controller.Error, want) {
		t.Fatal("initial defaults failure was not identified as a defaults error")
	}
	fixture.controller.RefreshDefaults()
	fixture.load(t, application.JobDefaults{})
	if fixture.controller.Error != nil {
		t.Fatal("successful defaults retry retained its earlier failure")
	}
	fixture.controller.Error = application.ErrJobInput
	fixture.controller.RefreshDefaults()
	receiveNewJob(t, fixture.service.defaults).reply <- newJobDefaultsReply{err: want}
	fixture.dispatch(t)
	if !errors.Is(fixture.controller.Error, application.ErrJobInput) {
		t.Fatal("a later defaults failure hid the creation failure")
	}
	fixture.controller.RefreshDefaults()
	fixture.load(t, application.JobDefaults{})
	if !errors.Is(fixture.controller.Error, application.ErrJobInput) {
		t.Fatal("a defaults retry cleared an unrelated creation failure")
	}
}

func TestNewJobExplicitHotwordEditBeforeInitialDefaultsSurvivesSubmission(t *testing.T) {
	for _, words := range []string{"", "Custom terms"} {
		t.Run(words, func(t *testing.T) {
			fixture := newNewJobFixture(t)
			fixture.controller.SetHotwords(words)
			defaults := application.JobDefaults{Hotwords: "Default terms", SummaryTemplate: "Default minutes"}
			fixture.load(t, defaults)
			if fixture.controller.Form.Hotwords != words {
				t.Fatal("initial defaults overwrote an explicit hotword edit")
			}
			fixture.controller.RefreshDefaults()
			fixture.load(t, defaults)
			fixture.controller.SubmitWithDefaults("Fallback minutes")
			fixture.load(t, defaults)
			call := receiveNewJob(t, fixture.service.creates)
			if call.form.Hotwords != words {
				t.Fatal("submission overwrote an explicit hotword edit")
			}
			call.reply <- newJobCreateReply{id: "synthetic-created"}
			fixture.dispatch(t)
			fixture.controller.RefreshDefaults()
			defaults.Hotwords = "Next draft default"
			fixture.load(t, defaults)
			if fixture.controller.Form.Hotwords != defaults.Hotwords {
				t.Fatal("the committed draft retained its hotword edit state")
			}
		})
	}
}

func TestNewJobDefaultsStartedDuringCreationCanPopulateNextDraft(t *testing.T) {
	fixture := newNewJobFixture(t)
	initial := application.JobDefaults{Hotwords: "Original terms", SummaryTemplate: "Original minutes"}
	fixture.load(t, initial)
	fixture.controller.SubmitWithDefaults("Fallback minutes")
	fixture.load(t, initial)
	call := receiveNewJob(t, fixture.service.creates)
	fixture.controller.RefreshDefaults()
	refresh := receiveNewJob(t, fixture.service.defaults)
	call.reply <- newJobCreateReply{id: "synthetic-created"}
	fixture.dispatch(t)
	current := application.JobDefaults{Hotwords: "Updated terms", SummaryTemplate: "Updated minutes"}
	refresh.reply <- newJobDefaultsReply{value: current}
	fixture.dispatch(t)
	if fixture.controller.Form.Hotwords != current.Hotwords || fixture.controller.Form.SummaryTemplate != current.SummaryTemplate || len(fixture.created) != 1 {
		t.Fatal("a valid defaults refresh arriving after creation was discarded")
	}
}

func TestNewJobSubmissionRejectsDefaultsSupersededDuringPreflight(t *testing.T) {
	for _, staleError := range []error{nil, errors.New("stale defaults read failure")} {
		t.Run(fmt.Sprint(staleError), func(t *testing.T) {
			fixture := newNewJobFixture(t)
			initial := application.JobDefaults{Hotwords: "Original terms", SummaryTemplate: "Original minutes"}
			fixture.load(t, initial)
			fixture.controller.SubmitWithDefaults("Fallback minutes")
			stale := receiveNewJob(t, fixture.service.defaults)
			fixture.controller.RefreshDefaults()
			current := application.JobDefaults{Hotwords: "Current terms", SummaryTemplate: "Current minutes"}
			fixture.load(t, current)
			stale.reply <- newJobDefaultsReply{value: initial, err: staleError}
			fixture.dispatch(t)
			if !fixture.controller.Busy || fixture.controller.Error != nil || len(fixture.service.creates) != 0 {
				t.Fatal("a superseded defaults reply changed submission or started creation")
			}
			fixture.load(t, current)
			call := receiveNewJob(t, fixture.service.creates)
			if call.form.Hotwords != current.Hotwords || call.form.SummaryTemplate != current.SummaryTemplate {
				t.Fatal("submission used superseded defaults", call.form)
			}
			call.reply <- newJobCreateReply{id: "synthetic-created"}
			fixture.dispatch(t)
			if len(fixture.created) != 1 || fixture.controller.Busy {
				t.Fatal("submission did not settle exactly once after rereading defaults")
			}
		})
	}
}

func TestNewJobSubmissionRefreshesUneditedDefaultsAndUsesTemplateFallback(t *testing.T) {
	fixture := newNewJobFixture(t)
	fixture.load(t, application.JobDefaults{Hotwords: "Old terms", SummaryTemplate: "Old minutes"})
	fixture.controller.SubmitWithDefaults("Localized fallback minutes")
	fixture.load(t, application.JobDefaults{Hotwords: "Updated terms", SummaryTemplate: "  "})
	call := receiveNewJob(t, fixture.service.creates)
	if call.form.Hotwords != "Updated terms" || call.form.SummaryTemplate != "Localized fallback minutes" {
		t.Fatal("submission retained stale defaults or an empty summary template", call.form)
	}
	call.reply <- newJobCreateReply{id: "synthetic-created"}
	fixture.dispatch(t)
}

func TestNewJobCommittedDraftKeepsDefaultsRefreshedDuringCreation(t *testing.T) {
	fixture := newNewJobFixture(t)
	initial := application.JobDefaults{Hotwords: "Original terms", SummaryTemplate: "Original minutes"}
	fixture.load(t, initial)
	fixture.controller.SubmitWithDefaults("Fallback minutes")
	fixture.load(t, initial)
	call := receiveNewJob(t, fixture.service.creates)
	fixture.controller.RefreshDefaults()
	current := application.JobDefaults{Hotwords: "Refreshed terms", SummaryTemplate: "Refreshed minutes"}
	fixture.load(t, current)
	call.reply <- newJobCreateReply{id: "synthetic-created"}
	fixture.dispatch(t)
	if call.form.Hotwords != initial.Hotwords || call.form.SummaryTemplate != initial.SummaryTemplate || fixture.controller.Form.Hotwords != current.Hotwords || fixture.controller.Form.SummaryTemplate != current.SummaryTemplate {
		t.Fatal("defaults refresh changed the committed request or left stale defaults in the next draft")
	}
}

func TestNewJobSubmissionDefaultFailureCanRetryWithoutCreating(t *testing.T) {
	fixture := newNewJobFixture(t)
	fixture.load(t, application.JobDefaults{})
	fixture.controller.SubmitWithDefaults("Fallback minutes")
	want := errors.New("defaults read failed")
	receiveNewJob(t, fixture.service.defaults).reply <- newJobDefaultsReply{err: want}
	fixture.dispatch(t)
	var defaultsError *JobDefaultsError
	if fixture.controller.Busy || !errors.As(fixture.controller.Error, &defaultsError) || !errors.Is(fixture.controller.Error, want) || fixture.controller.Media == nil || len(fixture.service.creates) != 0 {
		t.Fatal("defaults failure created a job or discarded the retryable draft")
	}
	fixture.controller.SubmitWithDefaults("Fallback minutes")
	fixture.load(t, application.JobDefaults{})
	call := receiveNewJob(t, fixture.service.creates)
	call.reply <- newJobCreateReply{id: "synthetic-retry"}
	fixture.dispatch(t)
	if fixture.controller.Error != nil || len(fixture.created) != 1 {
		t.Fatal("defaults retry did not create exactly one job")
	}
}

func TestNewJobSubmissionRejectsModeChangeAndLateCallbacksAfterClose(t *testing.T) {
	for _, phase := range []string{"mode-change", "defaults", "committed"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newNewJobFixture(t)
			fixture.load(t, application.JobDefaults{})
			fixture.controller.SubmitWithDefaults("Fallback minutes")
			defaults := receiveNewJob(t, fixture.service.defaults)
			if phase == "mode-change" {
				fixture.controller.Unavailable = true
			} else if phase == "defaults" {
				fixture.controller.Close()
			}
			defaults.reply <- newJobDefaultsReply{}
			fixture.dispatch(t)
			if phase == "committed" {
				call := receiveNewJob(t, fixture.service.creates)
				fixture.controller.Close()
				call.reply <- newJobCreateReply{id: "synthetic-durable-job"}
				fixture.dispatch(t)
			} else if len(fixture.service.creates) != 0 {
				t.Fatal("closed or unavailable form started job creation")
			}
			if len(fixture.created) != 0 {
				t.Fatal("late creation navigated a closed form")
			}
			if phase == "mode-change" && (!errors.Is(fixture.controller.Error, domain.ErrRemoteUnsupported) || fixture.controller.Busy || fixture.controller.Media == nil) {
				t.Fatal("mode change discarded the form or left submission busy")
			}
		})
	}
}

func TestNewJobPickerCancelAndClearedSelectionKeepGrantOwnership(t *testing.T) {
	fixture := newNewJobFixture(t)
	fixture.load(t, application.JobDefaults{})
	fixture.controller.Pick()
	fixture.picker.next <- application.SelectedMedia{}
	fixture.dispatch(t)
	if fixture.controller.Media == nil || fixture.controller.Form.Selection != "synthetic-grant" || len(fixture.picker.released) != 0 {
		t.Fatal("canceling reselect discarded the original media")
	}
	fixture.controller.Pick()
	fixture.controller.Clear()
	fixture.picker.next <- application.SelectedMedia{Token: "late-grant", Name: "late.wav", Kind: "audio"}
	fixture.dispatch(t)
	if fixture.controller.Picking || fixture.controller.Media != nil || fixture.controller.Form.Selection != "" || len(fixture.picker.released) != 2 || fixture.picker.released[0] != "synthetic-grant" || fixture.picker.released[1] != "late-grant" {
		t.Fatal("a late picker result restored a cleared selection or leaked its grant")
	}
}

func TestNewJobPickerCancelPreservesSelectionAndPriorError(t *testing.T) {
	fixture := newNewJobFixture(t)
	prior := errors.New("synthetic creation failure")
	fixture.controller.Error = prior
	fixture.controller.Pick()
	fixture.picker.next <- application.SelectedMedia{}
	fixture.dispatch(t)
	if fixture.controller.Media == nil || fixture.controller.Form.Selection != "synthetic-grant" || fixture.controller.Error != prior || fixture.controller.Picking || len(fixture.picker.released) != 0 {
		t.Fatal("canceling the picker discarded the existing draft, media grant, or submission error")
	}
}

func TestNewJobPickerFailurePreservesSelectionAndReportsRetryableError(t *testing.T) {
	fixture := newNewJobFixture(t)
	failure := errors.New("synthetic file access failure")
	fixture.picker.err = failure
	fixture.controller.Pick()
	fixture.dispatch(t)
	if fixture.controller.Media == nil || fixture.controller.Form.Selection != "synthetic-grant" || !errors.Is(fixture.controller.Error, failure) || fixture.controller.Picking || len(fixture.picker.released) != 0 {
		t.Fatal("a failed picker attempt discarded the existing draft or media grant")
	}
}

func TestNewJobSuccessfulReselectReplacesGrantAndClearsPickerError(t *testing.T) {
	fixture := newNewJobFixture(t)
	fixture.controller.Error = &MediaPickError{Cause: errors.New("synthetic picker failure")}
	fixture.controller.Pick()
	fixture.picker.next <- application.SelectedMedia{Token: "replacement-grant", Name: "replacement.wav", Kind: "audio"}
	fixture.dispatch(t)
	if fixture.controller.Error != nil || fixture.controller.Media == nil || fixture.controller.Media.Token != "replacement-grant" || fixture.controller.Form.Selection != "replacement-grant" || len(fixture.picker.released) != 1 || fixture.picker.released[0] != "synthetic-grant" {
		t.Fatal("successful reselect did not replace the prior grant and clear its retry error")
	}
}
