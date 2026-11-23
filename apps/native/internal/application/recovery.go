package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
	"sync"
)

type RecoveryStore interface {
	Recoverable(context.Context) ([]domain.RunRecord, error)
	Requeue(context.Context, domain.RunLease) error
}
type RecoveryDeletions interface{ Recover(context.Context) error }
type JobQueue interface{ Enqueue(string) error }

// Recovery matches the legacy startup order: finish deletion compensation,
// verify/terminate old running processes, fence/requeue them, then launch fresh
// attempts. Existing result files never bypass the persisted execution lease.
type Recovery struct {
	mu        sync.Mutex
	completed bool
	store     RecoveryStore
	deletions RecoveryDeletions
	processes ProcessTerminator
	queue     JobQueue
}

func NewRecovery(store RecoveryStore, deletions RecoveryDeletions, processes ProcessTerminator, queue JobQueue) *Recovery {
	return &Recovery{store: store, deletions: deletions, processes: processes, queue: queue}
}
func (r *Recovery) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completed {
		return nil
	}
	if err := r.deletions.Recover(ctx); err != nil {
		return err
	}
	records, err := r.store.Recoverable(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if record.Status == "running" {
			if err := record.RunLease.Validate(); err != nil {
				return err
			}
			if err := r.processes.Terminate(record.PID, record.ProcessIdentity); err != nil {
				return err
			}
			if err := r.store.Requeue(ctx, record.RunLease); err != nil {
				return err
			}
		} else if record.Status != "queued" {
			return domain.ErrConflict
		}
	}
	for _, record := range records {
		if err := r.queue.Enqueue(record.JobID); err != nil {
			return err
		}
	}
	r.completed = true
	return nil
}
