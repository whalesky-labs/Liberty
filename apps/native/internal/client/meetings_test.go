package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type queuedCall struct {
	query domain.QueueQuery
	id    string
	ctx   context.Context
	reply chan struct{}
}

type controlledService struct{ queues, results chan queuedCall }

func (s *controlledService) Queue(ctx context.Context, query domain.QueueQuery) (domain.JobPage, error) {
	call := queuedCall{query: query, ctx: ctx, reply: make(chan struct{})}
	s.queues <- call
	<-call.reply // Deliberately ignore cancellation to exercise the generation fence.
	return domain.JobPage{Items: []domain.JobSummary{{ID: query.Search}}}, nil
}

func (s *controlledService) Result(ctx context.Context, id string) (domain.Result, error) {
	call := queuedCall{id: id, ctx: ctx, reply: make(chan struct{})}
	s.results <- call
	<-call.reply
	return domain.Result{Job: domain.JobSummary{ID: id}}, nil
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not respond")
		var zero T
		return zero
	}
}

func TestLateQueueAndResultCannotReplaceNewSelection(t *testing.T) {
	s := &controlledService{queues: make(chan queuedCall, 4), results: make(chan queuedCall, 4)}
	callbacks := make(chan func(), 4)
	c := New(s, func(f func()) { callbacks <- f }, func() {})
	defer c.Close()
	c.Refresh(domain.QueueQuery{Search: "old"})
	old := receive(t, s.queues)
	c.Refresh(domain.QueueQuery{Search: "new"})
	newer := receive(t, s.queues)
	if old.ctx.Err() == nil {
		t.Fatal("old queue request was not cancelled")
	}
	close(newer.reply)
	receive(t, callbacks)()
	close(old.reply)
	receive(t, callbacks)()
	if len(c.Jobs) != 1 || c.Jobs[0].ID != "new" {
		t.Fatal("stale queue won")
	}
	c.Select("first")
	first := receive(t, s.results)
	c.Select("second")
	second := receive(t, s.results)
	close(second.reply)
	receive(t, callbacks)()
	close(first.reply)
	receive(t, callbacks)()
	if c.Result == nil || c.Result.Job.ID != "second" {
		t.Fatal("stale result won")
	}
}

func TestCloseCancelsAndRejectsOutstandingResult(t *testing.T) {
	s := &controlledService{queues: make(chan queuedCall, 1), results: make(chan queuedCall, 1)}
	callbacks := make(chan func(), 1)
	changes := 0
	c := New(s, func(f func()) { callbacks <- f }, func() { changes++ })
	c.Select("closing")
	call := receive(t, s.results)
	c.Close()
	before := changes
	if call.ctx.Err() == nil {
		t.Fatal("close did not cancel")
	}
	close(call.reply)
	receive(t, callbacks)()
	if c.Result != nil || changes != before {
		t.Fatal("closed view was updated")
	}
}

func TestPollingPreservesDetailOutsideVisiblePageWithoutBlanking(t *testing.T) {
	s := &controlledService{queues: make(chan queuedCall, 4), results: make(chan queuedCall, 4)}
	callbacks := make(chan func(), 4)
	c := New(s, func(f func()) { callbacks <- f }, func() {})
	defer c.Close()
	c.Refresh(domain.QueueQuery{Search: "visible"})
	list := receive(t, s.queues)
	close(list.reply)
	receive(t, callbacks)()
	c.Select("off-page-detail")
	detail := receive(t, s.results)
	close(detail.reply)
	receive(t, callbacks)()
	c.Poll()
	list = receive(t, s.queues)
	close(list.reply)
	receive(t, callbacks)()
	refresh := receive(t, s.results)
	if c.SelectedID != "off-page-detail" || c.Result == nil || c.LoadingResult {
		t.Fatal("background refresh blanked or dismissed detail")
	}
	c.ResultController.Refresh()
	select {
	case <-s.results:
		t.Fatal("overlapping detail polls")
	default:
	}
	close(refresh.reply)
	receive(t, callbacks)()
	if c.Result == nil || c.Result.Job.ID != "off-page-detail" {
		t.Fatal("detail lost on queue poll")
	}
}

func TestIndependentResultRefreshPreservesSnapshotAndStopsAfterClose(t *testing.T) {
	s := &controlledService{results: make(chan queuedCall, 4)}
	callbacks := make(chan func(), 4)
	c := NewResult(s, func(f func()) { callbacks <- f }, func() {})
	defer c.Close()
	c.Select("bound-result")
	first := receive(t, s.results)
	close(first.reply)
	receive(t, callbacks)()
	c.Refresh()
	refresh := receive(t, s.results)
	if c.Result == nil || c.LoadingResult || c.Result.Job.ID != "bound-result" {
		t.Fatal("background result refresh blanked snapshot")
	}
	c.Refresh()
	select {
	case <-s.results:
		t.Fatal("result refresh overlapped")
	default:
	}
	c.Close()
	if refresh.ctx.Err() == nil {
		t.Fatal("closing independent window did not cancel read")
	}
	close(refresh.reply)
	receive(t, callbacks)()
	c.Refresh()
	select {
	case <-s.results:
		t.Fatal("closed independent window refreshed")
	default:
	}
}

type capabilityDetailService struct{ calls chan string }

func (s capabilityDetailService) Queue(_ context.Context, q domain.QueueQuery) (domain.JobPage, error) {
	return domain.JobPage{Source: q.Source}, nil
}
func (s capabilityDetailService) Result(_ context.Context, id string) (domain.Result, error) {
	s.calls <- "result"
	return domain.Result{Job: domain.JobSummary{ID: id, Source: "remote"}}, nil
}
func (s capabilityDetailService) ResultForSource(_ context.Context, source, id string) (domain.Result, error) {
	s.calls <- "result:" + source
	return domain.Result{Job: domain.JobSummary{ID: id, Source: source}}, nil
}
func (s capabilityDetailService) JobForSource(_ context.Context, source, id string) (domain.Result, error) {
	s.calls <- "job:" + source
	return domain.Result{Job: domain.JobSummary{ID: id, Source: source}}, nil
}

func TestMainDetailAndIndependentResultUseDistinctCapabilityPorts(t *testing.T) {
	s := capabilityDetailService{calls: make(chan string, 2)}
	callbacks := make(chan func(), 2)
	c := New(s, func(f func()) { callbacks <- f }, func() {})
	defer c.Close()
	c.Refresh(domain.QueueQuery{Source: "remote"})
	receive(t, callbacks)()
	c.Select("same-id")
	if call := receive(t, s.calls); call != "job:remote" {
		t.Fatal("main detail used result capability", call)
	}
	receive(t, callbacks)()
	r := NewResult(s, func(f func()) { callbacks <- f }, func() {})
	defer r.Close()
	r.source = "remote"
	r.Select("same-id")
	if call := receive(t, s.calls); call != "result:remote" {
		t.Fatal("independent result used job capability", call)
	}
	receive(t, callbacks)()
}

type sourceSwitchQueueReply struct {
	page domain.JobPage
	err  error
}

type sourceSwitchResultReply struct {
	result domain.Result
	err    error
}

type sourceSwitchQueueCall struct {
	query domain.QueueQuery
	ctx   context.Context
	reply chan sourceSwitchQueueReply
}

type sourceSwitchResultCall struct {
	source string
	id     string
	ctx    context.Context
	reply  chan sourceSwitchResultReply
}

type sourceSwitchFixture struct {
	queues  chan sourceSwitchQueueCall
	results chan sourceSwitchResultCall
}

func (fixture *sourceSwitchFixture) Queue(ctx context.Context, query domain.QueueQuery) (domain.JobPage, error) {
	call := sourceSwitchQueueCall{query: query, ctx: ctx, reply: make(chan sourceSwitchQueueReply, 1)}
	fixture.queues <- call
	reply := <-call.reply
	return reply.page, reply.err
}

func (fixture *sourceSwitchFixture) Result(ctx context.Context, id string) (domain.Result, error) {
	return fixture.ResultForSource(ctx, "", id)
}

func (fixture *sourceSwitchFixture) ResultForSource(ctx context.Context, source, id string) (domain.Result, error) {
	call := sourceSwitchResultCall{source: source, id: id, ctx: ctx, reply: make(chan sourceSwitchResultReply, 1)}
	fixture.results <- call
	reply := <-call.reply
	return reply.result, reply.err
}

func TestSourceChangeImmediatelyRevokesPreviousQueueAndResult(t *testing.T) {
	for _, sources := range []struct{ previous, next string }{
		{previous: "local", next: "remote"},
		{previous: "remote", next: "local"},
	} {
		t.Run(sources.previous+"_to_"+sources.next, func(t *testing.T) {
			fixture := &sourceSwitchFixture{queues: make(chan sourceSwitchQueueCall, 3), results: make(chan sourceSwitchResultCall, 2)}
			callbacks := make(chan func(), 8)
			controller := New(fixture, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()

			controller.Refresh(domain.QueueQuery{Source: sources.previous})
			previousQueue := receive(t, fixture.queues)
			previousQueue.reply <- sourceSwitchQueueReply{page: domain.JobPage{Source: sources.previous, Items: []domain.JobSummary{{ID: "same-id", Source: sources.previous}}}}
			receive(t, callbacks)()
			controller.Select("same-id")
			previousResult := receive(t, fixture.results)
			if previousResult.source != sources.previous {
				t.Fatal("selection did not bind to its queue source", previousResult.source)
			}

			controller.Poll()
			staleQueue := receive(t, fixture.queues)
			controller.Refresh(domain.QueueQuery{Source: sources.next})
			nextQueue := receive(t, fixture.queues)
			if staleQueue.ctx.Err() == nil {
				t.Fatal("source switch did not cancel the previous-source queue read")
			}
			if previousResult.ctx.Err() == nil {
				t.Fatal("source switch did not cancel the previous-source detail read")
			}
			if controller.Source != sources.next || controller.ResultController.source != sources.next || controller.SelectedID != "" || controller.Result != nil || len(controller.Jobs) != 0 || controller.Page.Source != sources.next {
				t.Fatal("source switch retained previous-source state", controller.Source, controller.SelectedID, controller.Jobs, controller.Page.Source)
			}

			staleQueue.reply <- sourceSwitchQueueReply{page: domain.JobPage{Source: sources.previous, Items: []domain.JobSummary{{ID: "late-id", Source: sources.previous}}}}
			receive(t, callbacks)()
			previousResult.reply <- sourceSwitchResultReply{result: domain.Result{Job: domain.JobSummary{ID: "same-id", Source: sources.previous}}}
			receive(t, callbacks)()
			if controller.Source != sources.next || controller.Result != nil || controller.SelectedID != "" || len(controller.Jobs) != 0 {
				t.Fatal("late previous-source reply restored revoked state", controller.Source, controller.SelectedID, controller.Jobs)
			}

			nextQueue.reply <- sourceSwitchQueueReply{err: errors.New("synthetic source queue failure")}
			receive(t, callbacks)()
			if controller.Result != nil || controller.SelectedID != "" || len(controller.Jobs) != 0 || controller.Source != sources.next || controller.ListError == nil {
				t.Fatal("failed queue refresh exposed the previous source", controller.Source, controller.SelectedID, controller.Jobs, controller.ListError)
			}
		})
	}
}

func TestUnscopedQueueResponseBindsSubsequentDetailReadToItsSource(t *testing.T) {
	fixture := &sourceSwitchFixture{queues: make(chan sourceSwitchQueueCall, 1), results: make(chan sourceSwitchResultCall, 1)}
	callbacks := make(chan func(), 4)
	controller := New(fixture, func(callback func()) { callbacks <- callback }, func() {})
	defer controller.Close()

	controller.Refresh(domain.QueueQuery{})
	queue := receive(t, fixture.queues)
	queue.reply <- sourceSwitchQueueReply{page: domain.JobPage{Source: "remote", Items: []domain.JobSummary{{ID: "same-id", Source: "remote"}}}}
	receive(t, callbacks)()
	if controller.Source != "remote" || controller.query.Source != "remote" || controller.ResultController.source != "remote" || controller.Page.Source != "remote" {
		t.Fatal("unscoped queue reply did not bind the selected source", controller.Source, controller.query.Source, controller.ResultController.source, controller.Page.Source)
	}

	controller.Select("same-id")
	result := receive(t, fixture.results)
	if result.source != "remote" {
		t.Fatal("detail read was not scoped to the queue response source", result.source)
	}
	result.reply <- sourceSwitchResultReply{result: domain.Result{Job: domain.JobSummary{ID: "same-id", Source: "remote"}}}
	receive(t, callbacks)()
	if controller.Result == nil || controller.Result.Job.ID != "same-id" {
		t.Fatal("source-bound detail did not load")
	}
}

func TestQueueRejectsSourceMismatchWithoutPublishingRows(t *testing.T) {
	fixture := &sourceSwitchFixture{queues: make(chan sourceSwitchQueueCall, 1), results: make(chan sourceSwitchResultCall, 1)}
	callbacks := make(chan func(), 2)
	controller := New(fixture, func(callback func()) { callbacks <- callback }, func() {})
	defer controller.Close()

	controller.Refresh(domain.QueueQuery{Source: "remote"})
	queue := receive(t, fixture.queues)
	queue.reply <- sourceSwitchQueueReply{page: domain.JobPage{Source: "local", Items: []domain.JobSummary{{ID: "wrong-source", Source: "local"}}}}
	receive(t, callbacks)()
	if !errors.Is(controller.ListError, ErrQueueSourceMismatch) || len(controller.Jobs) != 0 || controller.Page.Source == "local" {
		t.Fatal("queue source mismatch was published", controller.ListError, controller.Jobs, controller.Page.Source)
	}
}
