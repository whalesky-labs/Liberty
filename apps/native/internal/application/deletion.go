package application

import (
	"context"
	"liberty.local/liberty/internal/domain"
)

type DeletionStore interface {
	PrepareDeletion(context.Context, string) (domain.JobDeletion, error)
	AdvanceDeletion(context.Context, domain.JobDeletion, string) error
	FinishDeletion(context.Context, domain.JobDeletion) error
	PendingDeletions(context.Context) ([]domain.JobDeletion, error)
}
type DeletionFiles interface {
	Trash(string, string) error
	Purge(string) error
}
type ProcessTerminator interface{ Terminate(*uint32, *string) error }

type Deleter struct {
	store     DeletionStore
	files     DeletionFiles
	processes ProcessTerminator
	scheduler *Scheduler
}

func NewDeleter(store DeletionStore, files DeletionFiles, processes ProcessTerminator, scheduler *Scheduler) *Deleter {
	return &Deleter{store: store, files: files, processes: processes, scheduler: scheduler}
}
func (d *Deleter) Delete(ctx context.Context, id string) error {
	reservation, err := d.scheduler.Reserve(ctx, id)
	if err != nil {
		return err
	}
	// Failure before writing the tombstone may resume a cancelled queued run;
	// once the tombstone exists, recovery owns it and no job may be rescheduled.
	prepared := false
	defer func() {
		if prepared {
			reservation.Release(false)
		} else {
			reservation.Restore()
		}
	}()
	operation, err := d.store.PrepareDeletion(ctx, id)
	if err != nil {
		return err
	}
	prepared = true
	return d.resume(ctx, operation)
}
func (d *Deleter) Recover(ctx context.Context) error {
	operations, err := d.store.PendingDeletions(ctx)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		reservation, err := d.scheduler.Reserve(ctx, operation.JobID)
		if err != nil {
			return err
		}
		err = d.resume(ctx, operation)
		reservation.Release(false)
		if err != nil {
			return err
		}
	}
	return nil
}
func (d *Deleter) resume(ctx context.Context, operation domain.JobDeletion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if operation.Phase == "prepared" {
		if err := d.store.AdvanceDeletion(ctx, operation, "fenced"); err != nil {
			return err
		}
		operation.Phase = "fenced"
	}
	if operation.Phase == "fenced" {
		if err := d.processes.Terminate(operation.PID, operation.ProcessIdentity); err != nil {
			return err
		}
		if err := d.files.Trash(operation.JobID, operation.TrashName); err != nil {
			return err
		}
		if err := d.store.AdvanceDeletion(ctx, operation, "trashed"); err != nil {
			return err
		}
		operation.Phase = "trashed"
	}
	if operation.Phase == "trashed" {
		if err := d.store.AdvanceDeletion(ctx, operation, "database_deleted"); err != nil {
			return err
		}
		operation.Phase = "database_deleted"
	}
	if operation.Phase != "database_deleted" {
		return domain.ErrConflict
	}
	if err := d.files.Purge(operation.TrashName); err != nil {
		return err
	}
	return d.store.FinishDeletion(ctx, operation)
}
