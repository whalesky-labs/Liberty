package application

import (
	"context"
	"errors"
	"liberty.local/liberty/internal/domain"
	"slices"
	"testing"
	"time"
)

type executionRecorder struct {
	state      string
	completion *domain.Completion
	failReason string
	failLog    string
	beginErr   error
	commitErr  error
}

func (r *executionRecorder) Input(context.Context, string) (domain.JobInput, error) {
	return domain.JobInput{}, nil
}
func (r *executionRecorder) Begin(context.Context, string, int64) (domain.RunLease, error) {
	if r.beginErr != nil {
		return domain.RunLease{}, r.beginErr
	}
	r.state = "running"
	return domain.RunLease{JobID: "job-1700000000000-1", AttemptID: 1, Token: 1}, nil
}
func (r *executionRecorder) Attach(context.Context, domain.RunLease, uint32, string, int64) error {
	return nil
}
func (r *executionRecorder) Heartbeat(context.Context, domain.RunLease, int64) error  { return nil }
func (r *executionRecorder) UpdateLog(context.Context, domain.RunLease, string) error { return nil }
func (r *executionRecorder) Complete(_ context.Context, c domain.Completion) error {
	if r.commitErr != nil {
		return r.commitErr
	}
	r.state = "completed"
	r.completion = &c
	return nil
}

func TestTranscriptionRewardsFollowCommittedStatus(t *testing.T) {
	for _, test := range []struct {
		name                        string
		beginErr, runErr, commitErr error
		want                        []string
	}{
		{name: "begin refused", beginErr: domain.ErrConflict},
		{name: "runner failed", runErr: domain.ErrRuntime, want: []string{"transcription_started:running"}},
		{name: "cancelled", runErr: context.Canceled, want: []string{"transcription_started:running"}},
		{name: "lease lost", commitErr: domain.ErrStaleAttempt, want: []string{"transcription_started:running"}},
		{name: "published", want: []string{"transcription_started:running", "transcription_completed:completed"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			const jobID = "job-1700000000000-1"
			store := &executionRecorder{beginErr: test.beginErr, commitErr: test.commitErr}
			var events []string
			hook := workflowFixture(func(_ context.Context, event, key string) (domain.PetProfile, error) {
				if key != jobID {
					t.Fatal("reward key differs from job ID")
				}
				events = append(events, event+":"+store.state)
				return domain.PetProfile{}, domain.ErrPet
			})
			runner := executorFunc(func(context.Context, domain.JobInput, domain.RunLease, ExecutionCallbacks) (domain.RunnerResult, error) {
				return domain.RunnerResult{}, test.runErr
			})
			err := NewExecutor(store, runner, time.Now, hook).Execute(context.Background(), jobID)
			var expected error
			for _, failure := range []error{test.beginErr, test.runErr, test.commitErr} {
				if failure != nil {
					expected = failure
					break
				}
			}
			if !slices.Equal(events, test.want) || !errors.Is(err, expected) {
				t.Fatal(events, err)
			}
		})
	}
}
func (r *executionRecorder) Fail(_ context.Context, _ domain.RunLease, _ int64, _ *uint32, reason, log string) error {
	r.state = "failed"
	r.failReason = reason
	r.failLog = log
	return nil
}
func (r *executionRecorder) Requeue(ctx context.Context, _ domain.RunLease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.state = "queued"
	return nil
}

type executorFunc func(context.Context, domain.JobInput, domain.RunLease, ExecutionCallbacks) (domain.RunnerResult, error)

func (f executorFunc) Run(c context.Context, i domain.JobInput, l domain.RunLease, h ExecutionCallbacks) (domain.RunnerResult, error) {
	return f(c, i, l, h)
}
func TestCancellationFinalizesAfterReapingWithFreshContext(t *testing.T) {
	store := &executionRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	reaped := false
	runner := executorFunc(func(context.Context, domain.JobInput, domain.RunLease, ExecutionCallbacks) (domain.RunnerResult, error) {
		cancel()
		reaped = true
		return domain.RunnerResult{}, context.Canceled
	})
	err := NewExecutor(store, runner, time.Now).Execute(ctx, "job-1700000000000-1")
	if !errors.Is(err, context.Canceled) || !reaped || store.state != "queued" {
		t.Fatal("cancellation lost recoverable state")
	}
}
func TestFailureAndStaleAttemptNeverCommitResult(t *testing.T) {
	for _, failure := range []error{errors.New("private /synthetic/path secret=hidden"), domain.ErrStaleAttempt} {
		store := &executionRecorder{}
		runner := executorFunc(func(context.Context, domain.JobInput, domain.RunLease, ExecutionCallbacks) (domain.RunnerResult, error) {
			return domain.RunnerResult{}, failure
		})
		err := NewExecutor(store, runner, time.Now).Execute(context.Background(), "job-1700000000000-1")
		if !errors.Is(err, failure) || store.completion != nil {
			t.Fatal("failed process committed a result")
		}
		if errors.Is(failure, domain.ErrStaleAttempt) {
			if store.state != "running" {
				t.Fatal("stale execution changed state")
			}
		} else if store.state != "failed" || store.failReason != "runner_execution_failed" {
			t.Fatal("failure persistence leaked diagnostics")
		}
	}
}
