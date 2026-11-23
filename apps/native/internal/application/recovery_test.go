package application

import (
	"context"
	"errors"
	"liberty.local/liberty/internal/domain"
	"reflect"
	"testing"
)

type recoveryFixture struct {
	steps           []string
	records         []domain.RunRecord
	failTermination bool
}

func (f *recoveryFixture) Recover(context.Context) error {
	f.steps = append(f.steps, "deletions")
	return nil
}
func (f *recoveryFixture) Recoverable(context.Context) ([]domain.RunRecord, error) {
	f.steps = append(f.steps, "list")
	return append([]domain.RunRecord{}, f.records...), nil
}
func (f *recoveryFixture) Requeue(_ context.Context, l domain.RunLease) error {
	f.steps = append(f.steps, "requeue")
	for i := range f.records {
		if f.records[i].JobID == l.JobID {
			f.records[i].Status = "queued"
			f.records[i].Token++
		}
	}
	return nil
}
func (f *recoveryFixture) Terminate(*uint32, *string) error {
	f.steps = append(f.steps, "terminate")
	if f.failTermination {
		return domain.ErrTerminationUnverified
	}
	return nil
}
func (f *recoveryFixture) Enqueue(id string) error {
	f.steps = append(f.steps, "enqueue:"+id)
	return nil
}
func TestRecoveryTerminatesBeforeRequeueAndLaunchesOnlyAfterAllVerified(t *testing.T) {
	fixture := &recoveryFixture{failTermination: true, records: []domain.RunRecord{
		{RunLease: domain.RunLease{JobID: "job-1700000000000-1"}, Status: "queued"},
		{RunLease: domain.RunLease{JobID: "job-1700000000000-2", AttemptID: 2, Token: 3}, Status: "running"},
	}}
	recovery := NewRecovery(fixture, fixture, fixture, fixture)
	if err := recovery.Start(context.Background()); !errors.Is(err, domain.ErrTerminationUnverified) {
		t.Fatal("termination failure hidden")
	}
	if !reflect.DeepEqual(fixture.steps, []string{"deletions", "list", "terminate"}) {
		t.Fatal("launched before all identities verified")
	}
	fixture.steps = nil
	fixture.failTermination = false
	if err := recovery.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	expected := []string{"deletions", "list", "terminate", "requeue", "enqueue:job-1700000000000-1", "enqueue:job-1700000000000-2"}
	if !reflect.DeepEqual(fixture.steps, expected) {
		t.Fatal("recovery ordering changed")
	}
	if err := recovery.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fixture.steps, expected) {
		t.Fatal("startup recovered twice")
	}
}
