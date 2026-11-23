package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"liberty.local/liberty/internal/domain"
)

var ErrSchedulerClosed = errors.New("scheduler_closed")

type JobExecutor interface {
	Execute(context.Context, string) error
}
type scheduledJob struct {
	id     string
	cancel context.CancelFunc
	done   chan struct{}
}

// Scheduler is process-owned, not page-owned. Durable queue state lives in the
// repository; this only bounds concurrent work and coordinates exclusivity.
type Scheduler struct {
	mu       sync.Mutex
	executor JobExecutor
	limit    int
	queue    []string
	queued   map[string]bool
	active   map[string]*scheduledJob
	delayed  map[string]*scheduledJob
	retries  map[string]uint32
	reserved map[string]bool
	closed   bool
	changed  chan struct{}
}

func NewScheduler(executor JobExecutor, limit uint32) *Scheduler {
	return &Scheduler{executor: executor, limit: int(max(1, min(limit, 8))), queued: map[string]bool{}, active: map[string]*scheduledJob{}, delayed: map[string]*scheduledJob{}, retries: map[string]uint32{}, reserved: map[string]bool{}, changed: make(chan struct{})}
}
func (s *Scheduler) notifyLocked() { close(s.changed); s.changed = make(chan struct{}) }
func (s *Scheduler) Enqueue(id string) error {
	if err := domain.ValidateJobID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSchedulerClosed
	}
	if s.reserved[id] {
		return domain.ErrConflict
	}
	if s.queued[id] || s.active[id] != nil || s.delayed[id] != nil {
		return nil
	}
	s.queue = append(s.queue, id)
	s.queued[id] = true
	s.launchLocked()
	return nil
}
func (s *Scheduler) launchLocked() {
	for !s.closed && len(s.active) < s.limit && len(s.queue) > 0 {
		id := s.queue[0]
		s.queue = s.queue[1:]
		delete(s.queued, id)
		if s.reserved[id] {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		task := &scheduledJob{id: id, cancel: cancel, done: make(chan struct{})}
		s.active[id] = task
		go s.execute(ctx, task)
	}
	s.notifyLocked()
}

func admissionRetryDelay(retries uint32) time.Duration {
	return min(100*time.Millisecond<<min(retries, 5), 2*time.Second)
}

func (s *Scheduler) execute(ctx context.Context, task *scheduledJob) {
	err := s.executor.Execute(ctx, task.id)
	s.mu.Lock()
	delete(s.active, task.id)
	_, deferred := err.(*executionAdmissionError)
	if deferred && ctx.Err() == nil && !s.closed && !s.reserved[task.id] {
		delay := admissionRetryDelay(s.retries[task.id])
		s.retries[task.id] = min(s.retries[task.id]+1, 5)
		s.delayed[task.id] = task
		s.launchLocked()
		s.mu.Unlock()
		timer := time.NewTimer(delay)
		elapsed := false
		select {
		case <-timer.C:
			elapsed = true
		case <-ctx.Done():
		}
		timer.Stop()
		s.mu.Lock()
		delete(s.delayed, task.id)
		if elapsed && ctx.Err() == nil && !s.closed && !s.reserved[task.id] {
			s.queue = append(s.queue, task.id)
			s.queued[task.id] = true
		} else {
			delete(s.retries, task.id)
		}
	} else {
		delete(s.retries, task.id)
	}
	task.cancel()
	close(task.done)
	s.notifyLocked()
	s.launchLocked()
	s.mu.Unlock()
}

// Reserve removes a queued entry and waits for active work to finish cancelling.
// Callers must keep this reservation until retry/deletion has committed.
type Reservation struct {
	release      func(bool)
	wasScheduled bool
}

func (r *Reservation) Release(enqueue bool) { r.release(enqueue) }
func (r *Reservation) Restore()             { r.release(r.wasScheduled) }

func (s *Scheduler) Reserve(ctx context.Context, id string) (*Reservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := domain.ValidateJobID(id); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSchedulerClosed
	}
	if s.reserved[id] {
		s.mu.Unlock()
		return nil, domain.ErrConflict
	}
	s.reserved[id] = true
	wasQueued := s.queued[id]
	if wasQueued {
		filtered := s.queue[:0]
		for _, item := range s.queue {
			if item != id {
				filtered = append(filtered, item)
			}
		}
		s.queue = filtered
		delete(s.queued, id)
	}
	delete(s.retries, id)
	running := s.active[id]
	if running == nil {
		running = s.delayed[id]
	}
	if running != nil {
		running.cancel()
	}
	s.mu.Unlock()
	var once sync.Once
	release := func(enqueue bool) {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.reserved, id)
			if enqueue && !s.closed && !s.queued[id] && s.active[id] == nil {
				s.queue = append(s.queue, id)
				s.queued[id] = true
			}
			s.launchLocked()
		})
	}
	if running != nil {
		select {
		case <-running.done:
		case <-ctx.Done():
			// Do not release exclusivity while the child is still winding down.
			go func() { <-running.done; release(true) }()
			return nil, ctx.Err()
		}
	}
	return &Reservation{release: release, wasScheduled: wasQueued || running != nil}, nil
}
func (s *Scheduler) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.queue = nil
	s.queued = map[string]bool{}
	s.retries = map[string]uint32{}
	for _, task := range s.active {
		task.cancel()
	}
	for _, task := range s.delayed {
		task.cancel()
	}
	for len(s.active)+len(s.delayed) > 0 {
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
		s.mu.Lock()
	}
	s.mu.Unlock()
	return nil
}

// SetLimit applies saved resource preferences to subsequent launches. Lowering
// the limit lets active jobs finish; it never interrupts an existing transcript.
func (s *Scheduler) SetLimit(limit uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.limit = int(max(1, min(8, limit)))
	s.launchLocked()
}
