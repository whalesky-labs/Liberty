package application

import (
	"context"
	"errors"
	"liberty.local/liberty/internal/domain"
	"sync"
	"testing"
	"time"
)

type blockingExecutor struct {
	mu          sync.Mutex
	started     chan string
	release     chan struct{}
	active, max int
}

func TestLiveReservationDefersQueuedFilesAndRefusesToInterruptActiveJob(t *testing.T) {
	e := &blockingExecutor{started: make(chan string, 2), release: make(chan struct{})}
	s := NewScheduler(e, 2)
	defer s.Shutdown(context.Background())
	release, err := s.ReserveLive()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveLive(); !errors.Is(err, domain.ErrLiveBusy) {
		t.Fatal("second live reservation admitted", err)
	}
	if err = s.Enqueue("job-1700000000000-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-e.started:
		t.Fatal("batch job competed with live ASR")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	release()
	select {
	case <-e.started:
	case <-time.After(time.Second):
		t.Fatal("queued work did not resume")
	}
	if _, err = s.ReserveLive(); !errors.Is(err, domain.ErrLiveBusy) {
		t.Fatal("live session interrupted active file", err)
	}
}

func (e *blockingExecutor) Execute(ctx context.Context, id string) error {
	e.mu.Lock()
	e.active++
	e.max = max(e.max, e.active)
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.active--; e.mu.Unlock() }()
	e.started <- id
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestSchedulerLimitsDeduplicatesAndDrains(t *testing.T) {
	e := &blockingExecutor{started: make(chan string, 10), release: make(chan struct{})}
	s := NewScheduler(e, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer s.Shutdown(ctx)
	for _, id := range []string{"job-1700000000000-1", "job-1700000000000-1", "job-1700000000000-2", "job-1700000000000-3"} {
		if err := s.Enqueue(id); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		select {
		case <-e.started:
		case <-ctx.Done():
			t.Fatal("worker failed to start")
		}
	}
	s.mu.Lock()
	queued := len(s.queue)
	active := len(s.active)
	s.mu.Unlock()
	if queued != 1 || active != 2 {
		t.Fatal("duplicate admission or unbounded work")
	}
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	maxActive, remaining := e.max, e.active
	e.mu.Unlock()
	if maxActive != 2 || remaining != 0 {
		t.Fatal("shutdown did not drain workers")
	}
	if err := s.Enqueue("job-1700000000000-4"); !errors.Is(err, ErrSchedulerClosed) {
		t.Fatal("closed scheduler accepted work")
	}
}
func TestReservationCancelsBeforeMutationAndBlocksReenqueue(t *testing.T) {
	e := &blockingExecutor{started: make(chan string, 10), release: make(chan struct{})}
	s := NewScheduler(e, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer s.Shutdown(ctx)
	id := "job-1700000000000-1"
	if s.Enqueue(id) != nil {
		t.Fatal("enqueue")
	}
	select {
	case <-e.started:
	case <-ctx.Done():
		t.Fatal("start timeout")
	}
	reservation, err := s.Reserve(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	active := e.active
	e.mu.Unlock()
	if active != 0 {
		t.Fatal("mutation reservation granted before reaping")
	}
	if err := s.Enqueue(id); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("reserved job reentered")
	}
	reservation.Release(true)
	reservation.Release(true)
	select {
	case <-e.started:
	case <-ctx.Done():
		t.Fatal("retry not scheduled")
	}
	s.mu.Lock()
	count := len(s.active) + len(s.queue)
	s.mu.Unlock()
	if count != 1 {
		t.Fatal("duplicate reservation release")
	}
}

func TestSchedulerSavedLimitAppliesWithoutCancellingActiveJobs(t *testing.T) {
	e := &blockingExecutor{started: make(chan string, 4), release: make(chan struct{})}
	s := NewScheduler(e, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer s.Shutdown(ctx)
	for _, id := range []string{"job-1700000000000-1", "job-1700000000000-2", "job-1700000000000-3"} {
		if s.Enqueue(id) != nil {
			t.Fatal("enqueue failed")
		}
	}
	select {
	case <-e.started:
	case <-ctx.Done():
		t.Fatal("first job not started")
	}
	s.SetLimit(2)
	select {
	case <-e.started:
	case <-ctx.Done():
		t.Fatal("raised limit did not start queued job")
	}
	s.SetLimit(1)
	s.mu.Lock()
	active, queued := len(s.active), len(s.queue)
	s.mu.Unlock()
	if active != 2 || queued != 1 {
		t.Fatal("lowered limit cancelled jobs or launched another")
	}
}
