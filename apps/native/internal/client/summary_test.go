package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

type summaryPort struct {
	reads   chan context.Context
	replies chan application.SummaryWindowState
	actions chan string
}

type slowSummaryRead struct {
	*summaryPort
	completed bool
}

func (service *slowSummaryRead) SummaryState(ctx context.Context) (application.SummaryWindowState, error) {
	if !service.completed {
		<-ctx.Done()
		return application.SummaryWindowState{}, ctx.Err()
	}
	return application.SummaryWindowState{Runs: []domain.SummaryRun{{ID: "published", Status: "completed"}}}, nil
}

func TestSummaryRefreshAfterDelayedReadDoesNotResubmit(t *testing.T) {
	service := &slowSummaryRead{summaryPort: &summaryPort{actions: make(chan string, 2)}}
	callbacks := make(chan func(), 1)
	controller := NewSummaryController(service, func(fn func()) { callbacks <- fn }, func() {})
	defer controller.Close()
	controller.readTimeout = time.Millisecond
	controller.Generate(application.SummaryForm{})
	receive(t, callbacks)()
	if controller.Busy || !errors.Is(controller.Error, context.DeadlineExceeded) {
		t.Fatal("read deadline did not release busy state")
	}
	service.completed = true
	controller.Refresh()
	receive(t, callbacks)()
	if controller.SelectedID != "published" || controller.Error != nil || len(service.actions) != 1 {
		t.Fatal("read did not recover independently of generation")
	}
}

func (p *summaryPort) SummaryState(ctx context.Context) (application.SummaryWindowState, error) {
	p.reads <- ctx
	return <-p.replies, nil
}
func (p *summaryPort) GenerateSummary(context.Context, application.SummaryForm) error {
	p.actions <- "generate"
	return nil
}
func (p *summaryPort) SelectSummary(_ context.Context, id string) error {
	p.actions <- "select:" + id
	return nil
}
func (p *summaryPort) DeleteSummary(_ context.Context, id string) error {
	p.actions <- "delete:" + id
	return nil
}
func TestSummaryControllerDispatchesMutationsAndRevokesLateCallbacks(t *testing.T) {
	p := &summaryPort{reads: make(chan context.Context, 8), replies: make(chan application.SummaryWindowState, 8), actions: make(chan string, 8)}
	callbacks := make(chan func(), 8)
	changed := 0
	c := NewSummaryController(p, func(f func()) { callbacks <- f }, func() { changed++ })
	defer c.Close()
	c.Refresh()
	receive(t, p.reads)
	p.replies <- application.SummaryWindowState{Runs: []domain.SummaryRun{{ID: "run1", Status: "completed"}}}
	apply := receive(t, callbacks)
	if !c.Busy || len(c.Runs) != 0 {
		t.Fatal("worker mutated UI without dispatch")
	}
	apply()
	if c.Busy || c.SelectedID != "run1" {
		t.Fatal("refresh did not update selection")
	}
	c.Select("run1")
	if receive(t, p.actions) != "select:run1" {
		t.Fatal("selection missing")
	}
	receive(t, p.reads)
	p.replies <- application.SummaryWindowState{Runs: []domain.SummaryRun{{ID: "run1", Status: "completed"}}}
	receive(t, callbacks)()
	c.Delete("run1", func(context.Context) (bool, error) { return false, nil })
	receive(t, p.reads)
	p.replies <- application.SummaryWindowState{Runs: c.Runs}
	receive(t, callbacks)()
	select {
	case <-p.actions:
		t.Fatal("cancelled confirmation deleted")
	default:
	}
	c.Generate(application.SummaryForm{})
	if receive(t, p.actions) != "generate" {
		t.Fatal("generation missing")
	}
	ctx := receive(t, p.reads)
	c.Close()
	before := changed
	if ctx.Err() == nil {
		t.Fatal("window close did not cancel observation")
	}
	p.replies <- application.SummaryWindowState{Runs: []domain.SummaryRun{{ID: "late"}}}
	receive(t, callbacks)()
	if changed != before || c.SelectedID != "run1" {
		t.Fatal("late callback changed closed window")
	}
}

type summaryReply struct {
	state application.SummaryWindowState
	err   error
}

type recoveringSummaryPort struct {
	replies     chan summaryReply
	forms       chan application.SummaryForm
	actions     chan string
	generateErr error
	deleteErr   error
}

func (p *recoveringSummaryPort) SummaryState(ctx context.Context) (application.SummaryWindowState, error) {
	select {
	case reply := <-p.replies:
		return reply.state, reply.err
	case <-ctx.Done():
		return application.SummaryWindowState{}, ctx.Err()
	}
}
func (p *recoveringSummaryPort) GenerateSummary(_ context.Context, form application.SummaryForm) error {
	p.forms <- form
	return p.generateErr
}
func (p *recoveringSummaryPort) SelectSummary(context.Context, string) error { return nil }
func (p *recoveringSummaryPort) DeleteSummary(_ context.Context, id string) error {
	p.actions <- id
	return p.deleteErr
}

func TestSummaryRefreshRecoversTransientErrorAndAcceptedGenerationSelection(t *testing.T) {
	port := &recoveringSummaryPort{replies: make(chan summaryReply, 4), forms: make(chan application.SummaryForm, 2)}
	callbacks := make(chan func(), 4)
	controller := NewSummaryController(port, func(fn func()) { callbacks <- fn }, func() {})
	defer controller.Close()
	old := domain.SummaryRun{ID: "previous", Status: "completed"}
	controller.Result = &domain.Result{}
	controller.Runs, controller.SelectedID = []domain.SummaryRun{old}, old.ID
	for _, err := range []error{errors.New("temporary_read_failure"), context.DeadlineExceeded} {
		controller.Refresh()
		port.replies <- summaryReply{err: err}
		receive(t, callbacks)()
		if !errors.Is(controller.Error, err) || controller.Busy || controller.Loading {
			t.Fatal("read failure did not release observation", controller.Error)
		}
		controller.Refresh()
		port.replies <- summaryReply{state: application.SummaryWindowState{Runs: []domain.SummaryRun{old}}}
		receive(t, callbacks)()
		if controller.Error != nil || controller.SelectedID != old.ID {
			t.Fatal("successful refresh retained a stale read error or lost preview selection")
		}
	}
	controller.Generate(application.SummaryForm{ModelID: "model", TemplateID: "template"})
	receive(t, port.forms)
	port.replies <- summaryReply{err: context.DeadlineExceeded}
	receive(t, callbacks)()
	controller.Refresh()
	port.replies <- summaryReply{state: application.SummaryWindowState{Runs: []domain.SummaryRun{{ID: "accepted", Status: "running"}, old}}}
	receive(t, callbacks)()
	if controller.SelectedID != "accepted" || controller.Error != nil || len(port.forms) != 0 {
		t.Fatal("accepted generation did not recover its preview without resubmitting", controller.SelectedID, controller.Error)
	}
}

func TestSummaryCloseDuringDeleteConfirmationPreventsMutation(t *testing.T) {
	port := &recoveringSummaryPort{replies: make(chan summaryReply), actions: make(chan string, 1)}
	callbacks := make(chan func(), 2)
	confirmation := make(chan context.Context, 1)
	answer := make(chan bool, 1)
	controller := NewSummaryController(port, func(fn func()) { callbacks <- fn }, func() {})
	controller.Result = &domain.Result{}
	controller.Delete("run", func(ctx context.Context) (bool, error) {
		confirmation <- ctx
		return <-answer, nil
	})
	ctx := receive(t, confirmation)
	controller.Close()
	if ctx.Err() == nil {
		t.Fatal("confirmation outlived closed window")
	}
	answer <- true
	receive(t, callbacks)()
	if len(port.actions) != 0 {
		t.Fatal("closed confirmation deleted a summary")
	}
}

func TestSummarySuccessfulObservationDoesNotHideFailedAction(t *testing.T) {
	failure := errors.New("synthetic_generation_failure")
	port := &recoveringSummaryPort{replies: make(chan summaryReply, 3), forms: make(chan application.SummaryForm, 2), generateErr: failure}
	callbacks := make(chan func(), 3)
	controller := NewSummaryController(port, func(fn func()) { callbacks <- fn }, func() {})
	defer controller.Close()
	controller.Result = &domain.Result{}
	controller.Generate(application.SummaryForm{})
	receive(t, port.forms)
	port.replies <- summaryReply{}
	receive(t, callbacks)()
	controller.Refresh()
	port.replies <- summaryReply{}
	receive(t, callbacks)()
	if !errors.Is(controller.Error, failure) {
		t.Fatal("polling silently discarded a failed user action", controller.Error)
	}
	port.generateErr = nil
	controller.Generate(application.SummaryForm{})
	receive(t, port.forms)
	port.replies <- summaryReply{}
	receive(t, callbacks)()
	if controller.Error != nil {
		t.Fatal("successful retry retained the previous action failure", controller.Error)
	}
}

func TestSummaryRetryKeepsFailedSnapshotAndOnlyObservesAcceptedAction(t *testing.T) {
	failure := errors.New("synthetic_generation_failure")
	port := &recoveringSummaryPort{replies: make(chan summaryReply, 6), forms: make(chan application.SummaryForm, 3), generateErr: failure}
	callbacks := make(chan func(), 6)
	controller := NewSummaryController(port, func(fn func()) { callbacks <- fn }, func() {})
	defer controller.Close()
	controller.Result = &domain.Result{}
	controller.SelectedID = "previous"
	state := application.SummaryWindowState{Runs: []domain.SummaryRun{{ID: "saved", Status: "failed"}, {ID: "previous", Status: "completed"}}}
	form := application.SummaryForm{RunID: "saved"}
	controller.Generate(form)
	if receive(t, port.forms) != form {
		t.Fatal("generation lost its requested run snapshot")
	}
	controller.Retry()
	port.replies <- summaryReply{state: state}
	receive(t, callbacks)()
	if len(port.forms) != 0 || controller.retryAction == nil || controller.retryApplied == nil {
		t.Fatal("busy retry repeated an action or failure did not retain its retry")
	}
	controller.Refresh()
	port.replies <- summaryReply{state: state}
	receive(t, callbacks)()
	if !errors.Is(controller.Error, failure) || controller.retryAction == nil {
		t.Fatal("polling discarded the failed operation or its retry")
	}
	port.generateErr = nil
	controller.Retry()
	if receive(t, port.forms) != form {
		t.Fatal("explicit retry lost the original run snapshot")
	}
	controller.Retry()
	port.replies <- summaryReply{err: context.DeadlineExceeded}
	receive(t, callbacks)()
	if len(port.forms) != 0 || controller.retryAction != nil || controller.retryApplied != nil || controller.afterRead == nil || !errors.Is(controller.Error, context.DeadlineExceeded) {
		t.Fatal("accepted retry retained an action replay or lost pending selection")
	}
	controller.Retry()
	state.Runs[0].Status = "running"
	port.replies <- summaryReply{state: state}
	receive(t, callbacks)()
	if len(port.forms) != 0 || controller.Error != nil || controller.SelectedID != "saved" || controller.afterRead != nil {
		t.Fatal("read retry resubmitted an accepted action or lost its saved run selection")
	}
}

func TestSummaryDeleteRetryRequiresFreshConfirmationAndCanBeCancelled(t *testing.T) {
	port := &recoveringSummaryPort{replies: make(chan summaryReply, 3), actions: make(chan string, 2), deleteErr: errors.New("synthetic_delete_failure")}
	callbacks := make(chan func(), 3)
	controller := NewSummaryController(port, func(fn func()) { callbacks <- fn }, func() {})
	defer controller.Close()
	controller.Result = &domain.Result{}
	controller.SelectedID = "original"
	confirmations := make(chan string, 2)
	allow := make(chan bool, 2)
	controller.Delete("original", func(context.Context) (bool, error) {
		confirmations <- "confirm"
		return <-allow, nil
	})
	receive(t, confirmations)
	allow <- true
	if receive(t, port.actions) != "original" {
		t.Fatal("initial delete used another run")
	}
	state := application.SummaryWindowState{Runs: []domain.SummaryRun{{ID: "original"}, {ID: "other"}}}
	port.replies <- summaryReply{state: state}
	receive(t, callbacks)()
	controller.SelectedID = "other"
	controller.Retry()
	receive(t, confirmations)
	controller.Retry()
	allow <- false
	port.replies <- summaryReply{state: state}
	receive(t, callbacks)()
	if len(port.actions) != 0 || len(confirmations) != 0 || controller.Error != nil || controller.retryAction != nil || controller.SelectedID != "other" {
		t.Fatal("cancelled retry deleted, repeated confirmation, or altered preview selection")
	}
}

func TestSummaryCloseClearsRetryAndRejectsFurtherOperations(t *testing.T) {
	port := &recoveringSummaryPort{replies: make(chan summaryReply, 2), forms: make(chan application.SummaryForm, 2), actions: make(chan string, 2), generateErr: errors.New("synthetic_failure")}
	callbacks := make(chan func(), 2)
	controller := NewSummaryController(port, func(fn func()) { callbacks <- fn }, func() {})
	controller.Result = &domain.Result{}
	controller.Generate(application.SummaryForm{RunID: "original"})
	receive(t, port.forms)
	port.replies <- summaryReply{}
	receive(t, callbacks)()
	if controller.retryAction == nil || controller.retryApplied == nil {
		t.Fatal("test did not establish a failed retryable action")
	}
	controller.Close()
	controller.Retry()
	controller.Refresh()
	controller.Generate(application.SummaryForm{})
	controller.Select("other")
	controller.Delete("other", func(context.Context) (bool, error) {
		t.Error("closed window asked for delete confirmation")
		return true, nil
	})
	if controller.retryAction != nil || controller.retryApplied != nil || controller.afterRead != nil || len(port.forms) != 0 || len(port.actions) != 0 || controller.Busy {
		t.Fatal("closed controller retained retry closures or accepted another operation")
	}
}
