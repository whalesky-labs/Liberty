package application

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func waitDelayedTask(t *testing.T, scheduler *Scheduler, id string) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		scheduler.mu.Lock()
		delayed, changed := scheduler.delayed[id] != nil, scheduler.changed
		scheduler.mu.Unlock()
		if delayed {
			return
		}
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatal("admission retry did not enter bounded backoff")
		}
	}
}

type schedulerExecutorFunc func(context.Context, string) error

func (f schedulerExecutorFunc) Execute(ctx context.Context, id string) error { return f(ctx, id) }

func TestSchedulerAdmissionBackoffIsBounded(t *testing.T) {
	previous := time.Duration(0)
	for _, retries := range []uint32{0, 1, 2, 3, 4, 5, 6, 100, ^uint32(0)} {
		delay := admissionRetryDelay(retries)
		if delay < 100*time.Millisecond || delay > 2*time.Second || delay < previous {
			t.Fatal("admission retry delay is unbounded or nonmonotonic")
		}
		previous = delay
	}
}

func TestSchedulerAdmissionBackoffReleasesCapacityAndDeduplicates(t *testing.T) {
	first, second := "job-1700000000000-1", "job-1700000000000-2"
	var firstCalls atomic.Int32
	completed := make(chan string, 4)
	executor := schedulerExecutorFunc(func(_ context.Context, id string) error {
		if id == first && firstCalls.Add(1) == 1 {
			return &executionAdmissionError{cause: errors.New("synthetic_busy")}
		}
		completed <- id
		return nil
	})
	scheduler := NewScheduler(executor, 1)
	scheduler.retries[first] = 5
	t.Cleanup(func() { _ = scheduler.Shutdown(context.Background()) })
	if scheduler.Enqueue(first) != nil {
		t.Fatal("synthetic first task admission failed")
	}
	waitDelayedTask(t, scheduler, first)
	for range 10 {
		if scheduler.Enqueue(first) != nil {
			t.Fatal("delayed task could not be joined")
		}
	}
	if scheduler.Enqueue(second) != nil {
		t.Fatal("synthetic second task admission failed")
	}
	for _, expected := range []string{second, first} {
		select {
		case actual := <-completed:
			if actual != expected {
				t.Fatal("admission backoff blocked another queued task")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("admission retry did not resume")
		}
	}
	if scheduler.Shutdown(context.Background()) != nil || firstCalls.Load() != 2 {
		t.Fatal("delayed enqueue replayed the synthetic task")
	}
}

func TestSchedulerAdmissionBackoffCancelsPromptlyForReservationAndShutdown(t *testing.T) {
	for _, action := range []string{"reservation", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			id := "job-1700000000000-1"
			var calls atomic.Int32
			scheduler := NewScheduler(schedulerExecutorFunc(func(context.Context, string) error {
				calls.Add(1)
				return &executionAdmissionError{cause: errors.New("synthetic_busy")}
			}), 1)
			scheduler.retries[id] = 5
			t.Cleanup(func() { _ = scheduler.Shutdown(context.Background()) })
			if scheduler.Enqueue(id) != nil {
				t.Fatal("synthetic task admission failed")
			}
			waitDelayedTask(t, scheduler, id)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			if action == "shutdown" {
				if scheduler.Shutdown(ctx) != nil {
					t.Fatal("shutdown waited for the two-second admission timer")
				}
			} else {
				reservation, err := scheduler.Reserve(ctx, id)
				if err != nil {
					t.Fatal("reservation waited for the two-second admission timer")
				}
				reservation.Release(false)
			}
			scheduler.mu.Lock()
			remaining := len(scheduler.active) + len(scheduler.delayed) + len(scheduler.queue) + len(scheduler.retries)
			scheduler.mu.Unlock()
			if remaining != 0 || calls.Load() != 1 {
				t.Fatal("canceled admission retry retained work or executed it again")
			}
		})
	}
}

func TestSchedulerNeverRetriesOrdinaryExecutionErrors(t *testing.T) {
	var calls atomic.Int32
	scheduler := NewScheduler(schedulerExecutorFunc(func(context.Context, string) error {
		calls.Add(1)
		return errors.New("synthetic_post_execution_failure")
	}), 1)
	t.Cleanup(func() { _ = scheduler.Shutdown(context.Background()) })
	id := "job-1700000000000-1"
	if scheduler.Enqueue(id) != nil {
		t.Fatal("synthetic task admission failed")
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		scheduler.mu.Lock()
		remaining, changed := len(scheduler.active)+len(scheduler.delayed)+len(scheduler.queue), scheduler.changed
		scheduler.mu.Unlock()
		if remaining == 0 {
			break
		}
		select {
		case <-changed:
		case <-deadline.C:
			t.Fatal("ordinary error entered automatic admission retry")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("ordinary execution error repeated business work")
	}
}

type canceledAdmissionStore struct {
	executionRecorder
	entered chan struct{}
}

func (s *canceledAdmissionStore) Input(ctx context.Context, _ string) (domain.JobInput, error) {
	close(s.entered)
	<-ctx.Done()
	return domain.JobInput{}, errors.New("synthetic_input_unavailable")
}

func TestSchedulerShutdownDuringAdmissionDoesNotStartWorkOrScheduleRetry(t *testing.T) {
	store := &canceledAdmissionStore{entered: make(chan struct{})}
	var calls atomic.Int32
	runner := executorFunc(func(context.Context, domain.JobInput, domain.RunLease, ExecutionCallbacks) (domain.RunnerResult, error) {
		calls.Add(1)
		return domain.RunnerResult{}, nil
	})
	scheduler := NewScheduler(NewExecutor(store, runner, time.Now), 1)
	t.Cleanup(func() { _ = scheduler.Shutdown(context.Background()) })
	if scheduler.Enqueue("job-1700000000000-1") != nil {
		t.Fatal("synthetic task admission failed")
	}
	select {
	case <-store.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic admission did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if scheduler.Shutdown(ctx) != nil || calls.Load() != 0 || store.state != "" {
		t.Fatal("canceled admission delayed shutdown or began execution")
	}
	scheduler.mu.Lock()
	remaining := len(scheduler.active) + len(scheduler.delayed) + len(scheduler.queue) + len(scheduler.retries)
	scheduler.mu.Unlock()
	if remaining != 0 {
		t.Fatal("canceled admission retained a delayed retry")
	}
}
