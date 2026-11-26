package application

import (
	"context"
	"errors"
	"liberty.local/liberty/internal/domain"
	"reflect"
	"testing"
)

type deletionFixture struct {
	operation      domain.JobDeletion
	steps          []string
	failPurge      bool
	finished       bool
	terminationErr error
}

func (f *deletionFixture) PrepareDeletion(context.Context, string) (domain.JobDeletion, error) {
	return f.operation, nil
}
func (f *deletionFixture) AdvanceDeletion(_ context.Context, _ domain.JobDeletion, phase string) error {
	f.steps = append(f.steps, phase)
	f.operation.Phase = phase
	return nil
}
func (f *deletionFixture) FinishDeletion(context.Context, domain.JobDeletion) error {
	f.steps = append(f.steps, "finish")
	f.finished = true
	return nil
}
func (f *deletionFixture) PendingDeletions(context.Context) ([]domain.JobDeletion, error) {
	if f.finished {
		return nil, nil
	}
	return []domain.JobDeletion{f.operation}, nil
}
func (f *deletionFixture) Trash(string, string) error { f.steps = append(f.steps, "trash"); return nil }
func (f *deletionFixture) Purge(string) error {
	f.steps = append(f.steps, "purge")
	if f.failPurge {
		return errors.New("synthetic_disk_failure")
	}
	return nil
}
func (f *deletionFixture) Terminate(*uint32, *string) error {
	f.steps = append(f.steps, "terminate")
	return f.terminationErr
}
func TestDeletionPreservesCompensationAfterFilesystemFailure(t *testing.T) {
	fixture := &deletionFixture{operation: domain.JobDeletion{ID: "delete-synthetic", JobID: "job-1700000000000-1", TrashName: "synthetic-trash", Phase: "prepared"}, failPurge: true}
	scheduler := NewScheduler(&blockingExecutor{}, 1)
	deleter := NewDeleter(fixture, fixture, fixture, scheduler, nil)
	if deleter.Delete(context.Background(), fixture.operation.JobID) == nil {
		t.Fatal("purge failure hidden")
	}
	if fixture.finished || fixture.operation.Phase != "database_deleted" {
		t.Fatal("recovery journal lost")
	}
	if !reflect.DeepEqual(fixture.steps, []string{"fenced", "terminate", "trash", "trashed", "database_deleted", "purge"}) {
		t.Fatal("unsafe deletion order")
	}
	fixture.failPurge = false
	fixture.steps = nil
	if err := deleter.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.steps, []string{"purge", "finish"}) || !fixture.finished {
		t.Fatal("recovery repeated destructive steps")
	}
}
func TestDeletionCannotMoveFilesBeforeVerifiedTermination(t *testing.T) {
	fixture := &deletionFixture{operation: domain.JobDeletion{JobID: "job-1700000000000-1", Phase: "prepared"}, terminationErr: domain.ErrTerminationUnverified}
	deleter := NewDeleter(fixture, fixture, fixture, NewScheduler(&blockingExecutor{}, 1), nil)
	if err := deleter.Delete(context.Background(), fixture.operation.JobID); !errors.Is(err, domain.ErrTerminationUnverified) {
		t.Fatal("termination failure hidden")
	}
	if !reflect.DeepEqual(fixture.steps, []string{"fenced", "terminate"}) {
		t.Fatal("files moved before termination")
	}
}
