package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type finalizationStore struct {
	executionRecorder
	failCalls int
	failErr   error
	lease     domain.RunLease
	onCommit  func()
	cleanupOK bool
}

func (s *finalizationStore) Complete(ctx context.Context, completion domain.Completion) error {
	if s.onCommit != nil {
		s.onCommit()
	}
	return s.executionRecorder.Complete(ctx, completion)
}

func (s *finalizationStore) Fail(ctx context.Context, lease domain.RunLease, finished int64, duration *uint32, reason, log string) error {
	s.failCalls++
	s.lease = lease
	deadline, bounded := ctx.Deadline()
	s.cleanupOK = ctx.Err() == nil && bounded && time.Until(deadline) > 0 && time.Until(deadline) <= 5*time.Second
	if s.failErr != nil {
		return s.failErr
	}
	return s.executionRecorder.Fail(ctx, lease, finished, duration, reason, log)
}

func TestExecutionPublicationFailureUsesOriginalLeaseAndFreshBoundedCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("synthetic_publication_failure")
	store := &finalizationStore{executionRecorder: executionRecorder{commitErr: failure}, onCommit: cancel}
	runner := executorFunc(func(context.Context, domain.JobInput, domain.RunLease, ExecutionCallbacks) (domain.RunnerResult, error) {
		return domain.RunnerResult{}, nil
	})
	err := NewExecutor(store, runner, time.Now).Execute(ctx, "job-1700000000000-1")
	if !errors.Is(err, failure) || store.failCalls != 1 || store.state != "failed" || store.failReason != "runner_result_publication_failed" || !store.cleanupOK || store.lease != (domain.RunLease{JobID: "job-1700000000000-1", AttemptID: 1, Token: 1}) {
		t.Fatal("publication cleanup lost its fence, context, stable reason, or original error")
	}
}

func TestExecutionPublicationCleanupPreservesFailureAndNeverRetriesWork(t *testing.T) {
	for _, cleanupErr := range []error{domain.ErrStaleAttempt, errors.New("synthetic_cleanup_unavailable")} {
		store := &finalizationStore{executionRecorder: executionRecorder{commitErr: errors.New("synthetic_publication_failure")}, failErr: cleanupErr}
		calls := 0
		runner := executorFunc(func(context.Context, domain.JobInput, domain.RunLease, ExecutionCallbacks) (domain.RunnerResult, error) {
			calls++
			return domain.RunnerResult{}, nil
		})
		err := NewExecutor(store, runner, time.Now).Execute(context.Background(), "job-1700000000000-1")
		if !errors.Is(err, store.commitErr) || !errors.Is(err, cleanupErr) || store.failCalls != 1 || store.state != "running" || calls != 1 {
			t.Fatal("failed cleanup hid an error, overwrote a fence, or repeated work")
		}
		if _, deferred := err.(*executionAdmissionError); deferred {
			t.Fatal("publication failure was eligible for automatic execution retry")
		}
	}
}

func TestExecutionNeverFinalizesStaleOrUnverifiedTermination(t *testing.T) {
	for _, phase := range []string{"runner", "publication"} {
		for _, failure := range []error{domain.ErrStaleAttempt, domain.ErrTerminationUnverified} {
			store := &finalizationStore{}
			runner := executorFunc(func(context.Context, domain.JobInput, domain.RunLease, ExecutionCallbacks) (domain.RunnerResult, error) {
				if phase == "runner" {
					return domain.RunnerResult{}, failure
				}
				return domain.RunnerResult{}, nil
			})
			if phase == "publication" {
				store.commitErr = failure
			}
			err := NewExecutor(store, runner, time.Now).Execute(context.Background(), "job-1700000000000-1")
			if !errors.Is(err, failure) || store.failCalls != 0 || store.state != "running" {
				t.Fatal("stale lease or unverified termination was finalized")
			}
		}
	}
}

type admissionStore struct {
	executionRecorder
	inputErr error
}

func (s *admissionStore) Input(context.Context, string) (domain.JobInput, error) {
	return domain.JobInput{}, s.inputErr
}

func TestExecutionAdmissionRetryIsStructuredAndEndsBeforeLeaseOwnership(t *testing.T) {
	for _, phase := range []string{"input", "begin"} {
		for _, failure := range []error{errors.New("synthetic_database_busy"), ErrJobNotFound, ErrWindowScope, domain.ErrConflict, domain.ErrJobID, domain.ErrLease, domain.ErrStaleAttempt, domain.ErrTerminationUnverified, context.Canceled, context.DeadlineExceeded} {
			store := &admissionStore{}
			if phase == "input" {
				store.inputErr = failure
			} else {
				store.beginErr = failure
			}
			calls := 0
			runner := executorFunc(func(context.Context, domain.JobInput, domain.RunLease, ExecutionCallbacks) (domain.RunnerResult, error) {
				calls++
				return domain.RunnerResult{}, nil
			})
			err := NewExecutor(store, runner, time.Now).Execute(context.Background(), "job-1700000000000-1")
			deferred, classified := err.(*executionAdmissionError)
			if !errors.Is(err, failure) || classified != (failure.Error() == "synthetic_database_busy") || calls != 0 || store.state != "" {
				t.Fatal("admission classification changed ownership, terminal semantics, or ran work")
			}
			if classified && deferred.Error() != "job_admission_deferred" {
				t.Fatal("admission error exposed its repository details")
			}
		}
	}
}
