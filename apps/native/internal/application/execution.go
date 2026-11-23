package application

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
)

type ExecutionStore interface {
	Begin(context.Context, string, int64) (domain.RunLease, error)
	Attach(context.Context, domain.RunLease, uint32, string, int64) error
	Heartbeat(context.Context, domain.RunLease, int64) error
	Complete(context.Context, domain.Completion) error
	Fail(context.Context, domain.RunLease, int64, *uint32, string, string) error
	Requeue(context.Context, domain.RunLease) error
	Input(context.Context, string) (domain.JobInput, error)
	UpdateLog(context.Context, domain.RunLease, string) error
}
type ExecutionCallbacks struct {
	Started   func(uint32, string) error
	Heartbeat func() error
	Log       func(string) error
}
type ExecutionRunner interface {
	Run(context.Context, domain.JobInput, domain.RunLease, ExecutionCallbacks) (domain.RunnerResult, error)
}

type executionAdmissionError struct{ cause error }

func (e *executionAdmissionError) Error() string { return "job_admission_deferred" }
func (e *executionAdmissionError) Unwrap() error { return e.cause }

func admissionFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, terminal := range []error{context.Canceled, context.DeadlineExceeded, ErrJobNotFound, ErrWindowScope, domain.ErrJobID, domain.ErrLease, domain.ErrConflict, domain.ErrStaleAttempt, domain.ErrTerminationUnverified} {
		if errors.Is(err, terminal) {
			return err
		}
	}
	return &executionAdmissionError{cause: err}
}

// Executor owns the execution use case; no window or UI lifetime is involved.
// Shutdown requeues only after the runner adapter has reaped its process tree.
type Executor struct {
	store  ExecutionStore
	runner ExecutionRunner
	clock  func() time.Time
	pet    PetWorkflow
}

func NewExecutor(store ExecutionStore, runner ExecutionRunner, clock func() time.Time, pet ...PetWorkflow) *Executor {
	return &Executor{store: store, runner: runner, clock: clock, pet: optionalPetWorkflow(pet)}
}
func (e *Executor) Execute(ctx context.Context, id string) error {
	if err := domain.ValidateJobID(id); err != nil {
		return err
	}
	input, err := e.store.Input(ctx, id)
	if err != nil {
		return admissionFailure(ctx, err)
	}
	started := e.clock().UnixMilli()
	lease, err := e.store.Begin(ctx, id, started)
	if err != nil {
		return admissionFailure(ctx, err)
	}
	recordPetWorkflow(ctx, e.pet, "transcription_started", id)
	var log strings.Builder
	result, runErr := e.runner.Run(ctx, input, lease, ExecutionCallbacks{
		Started: func(pid uint32, identity string) error {
			return e.store.Attach(ctx, lease, pid, identity, e.clock().UnixMilli())
		},
		Heartbeat: func() error { return e.store.Heartbeat(ctx, lease, e.clock().UnixMilli()) },
		Log: func(line string) error {
			// The adapter guarantees redaction. Keep a bounded recent projection; job
			// events and the durable snapshot remain the authoritative state.
			log.WriteString(line)
			log.WriteByte('\n')
			if log.Len() > 256*1024 {
				tail := log.String()
				offset := len(tail) - 128*1024
				if end := strings.IndexByte(tail[offset:], '\n'); end >= 0 {
					offset += end + 1
				}
				tail = tail[offset:]
				log.Reset()
				log.WriteString(tail)
			}
			return e.store.UpdateLog(ctx, lease, log.String())
		},
	})
	// Finalization must survive cancellation of the work context, but is bounded.
	finalize, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if errors.Is(runErr, domain.ErrStaleAttempt) || errors.Is(runErr, domain.ErrTerminationUnverified) {
		return runErr
	}
	if ctx.Err() != nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		if err := e.store.Requeue(finalize, lease); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return runErr
	}
	finished := e.clock().UnixMilli()
	duration := uint32(min(max(0, finished-started)/1000, math.MaxUint32))
	if runErr != nil {
		// Only stable adapter error codes are persisted. External errors must never
		// include command lines, credentials, media paths or provider responses.
		reason := "runner_execution_failed"
		if err := e.store.Fail(finalize, lease, finished, &duration, reason, log.String()); err != nil {
			return err
		}
		return runErr
	}
	if err := e.store.Complete(finalize, domain.Completion{Lease: lease, Result: result, FinishedAtMS: finished, DurationSeconds: &duration, FallbackMinutes: input.Job.DurationMinutes, ProcessLog: log.String()}); err != nil {
		if errors.Is(err, domain.ErrStaleAttempt) || errors.Is(err, domain.ErrTerminationUnverified) {
			return err
		}
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		if failure := e.store.Fail(cleanup, lease, finished, &duration, "runner_result_publication_failed", log.String()); failure != nil {
			return errors.Join(err, failure)
		}
		return err
	}
	recordPetWorkflow(ctx, e.pet, "transcription_completed", id)
	return nil
}
