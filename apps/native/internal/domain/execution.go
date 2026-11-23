package domain

import (
	"errors"
	"math"
	"regexp"
)

var (
	ErrJobID                 = errors.New("job_id_invalid")
	ErrLease                 = errors.New("job_lease_invalid")
	ErrConflict              = errors.New("job_state_conflict")
	ErrStaleAttempt          = errors.New("job_attempt_stale")
	ErrTerminationUnverified = errors.New("runner_termination_unverified")
)

var jobIDPattern = regexp.MustCompile(`^job-[0-9]{13}-[0-9]{1,20}$`)

func ValidateJobID(id string) error {
	if !jobIDPattern.MatchString(id) {
		return ErrJobID
	}
	return nil
}

type RunLease struct {
	JobID     string `json:"jobId"`
	AttemptID int64  `json:"attemptId"`
	Token     int64  `json:"leaseToken"`
	OutputDir string `json:"outputDir"`
}

func (l RunLease) Validate() error {
	if ValidateJobID(l.JobID) != nil || l.AttemptID < 1 || l.Token < 1 || l.AttemptID == math.MaxInt64 || l.Token == math.MaxInt64 {
		return ErrLease
	}
	return nil
}

type RunRecord struct {
	RunLease
	Status          string
	PID             *uint32
	ProcessIdentity *string
	StartedAtMS     *int64
	HeartbeatAtMS   *int64
}

// JobInput is an application-owned persisted input, never a public arbitrary
// file access grant. File selection and runtime resolution precede creation.
type JobInput struct {
	Job        JobSummary
	SourcePath string
	PythonPath string
	RunnerPath string
}

type Completion struct {
	Lease           RunLease
	Result          RunnerResult
	FinishedAtMS    int64
	DurationSeconds *uint32
	FallbackMinutes uint32
	ProcessLog      string
}

type JobDeletion struct {
	ID, JobID, TrashName, Phase string
	PID                         *uint32
	ProcessIdentity             *string
}
