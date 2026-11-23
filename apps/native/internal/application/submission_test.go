package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type submissionFixture struct {
	input                 *domain.JobInput
	ready, released, fail bool
	mode                  string
	steps                 []string
}

func (f *submissionFixture) Create(_ context.Context, input domain.JobInput) error {
	f.steps = append(f.steps, "persist")
	if f.fail {
		return ErrJobInput
	}
	f.input = &input
	return nil
}
func (f *submissionFixture) Input(context.Context, string) (domain.JobInput, error) {
	return *f.input, nil
}
func (f *submissionFixture) Retry(context.Context, string, string, string) error { return nil }
func (f *submissionFixture) JobDefaults(context.Context) (JobDefaults, error) {
	return JobDefaults{SummaryTemplate: "Synthetic summary"}, nil
}
func (f *submissionFixture) ProcessingMode(context.Context) (string, error) {
	if f.mode == "" {
		return "local", nil
	}
	return f.mode, nil
}
func (f *submissionFixture) Resolve(_ context.Context, token string) (MediaInput, error) {
	f.steps = append(f.steps, "resolve")
	if token != "grant" || f.released {
		return MediaInput{}, ErrJobInput
	}
	return MediaInput{Path: "/synthetic/media.wav", Source: domain.SourceFile{Name: "media.wav", Kind: "audio"}}, nil
}
func (f *submissionFixture) Release(string) { f.steps = append(f.steps, "release"); f.released = true }
func (f *submissionFixture) Status(context.Context) (domain.RuntimeOverview, error) {
	f.steps = append(f.steps, "runtime")
	return domain.RuntimeOverview{ShellReady: f.ready}, nil
}
func (f *submissionFixture) CreateJob(string) error {
	f.steps = append(f.steps, "directory")
	return nil
}
func TestSubmissionRequiresGrantAndRuntimeThenPersistsRecoverableJob(t *testing.T) {
	f := &submissionFixture{}
	q := NewScheduler(nil, 1)
	if q.Shutdown(context.Background()) != nil {
		t.Fatal("shutdown")
	}
	s := NewSubmission(f, f, f, f, q, "/synthetic/runner.py")
	s.clock = func() time.Time { return time.UnixMilli(1700000000000) }
	form := CreateMeeting{Selection: "grant", Title: " Meeting ", Language: "zh-CN", Hotwords: " one, ,two ", Speaker: true, SizeLabel: "Local path"}
	if _, err := s.Create(context.Background(), form); !errors.Is(err, domain.ErrRuntime) {
		t.Fatal("unready runtime accepted")
	}
	if f.input != nil || f.released {
		t.Fatal("failed create consumed selection")
	}
	f.ready = true
	f.steps = nil
	id, err := s.Create(context.Background(), form)
	if err != nil {
		t.Fatal(err)
	}
	if domain.ValidateJobID(id) != nil || f.input.Job.Title != "Meeting" || f.input.Job.SummaryTemplate != "Synthetic summary" || f.input.Job.CreatedAt != "2023-11-14T22:13:20.000Z" || !reflect.DeepEqual(f.input.Job.Hotwords, []string{"one", "two"}) {
		t.Fatal("created input differs")
	}
	if !reflect.DeepEqual(f.steps, []string{"resolve", "runtime", "directory", "persist", "release"}) {
		t.Fatal("publication/grant ordering changed")
	}
	if _, err := s.Create(context.Background(), form); err == nil {
		t.Fatal("selection replay created duplicate")
	}
}
func TestSubmissionFailedPersistencePreservesSelectionForRetry(t *testing.T) {
	f := &submissionFixture{ready: true, fail: true}
	q := NewScheduler(nil, 1)
	_ = q.Shutdown(context.Background())
	s := NewSubmission(f, f, f, f, q, "/synthetic/runner.py")
	if _, err := s.Create(context.Background(), CreateMeeting{Selection: "grant", Title: "Meeting", Language: "en-US"}); err == nil || f.released {
		t.Fatal("failed persistence consumed grant")
	}
}

func TestSubmissionRefusesLocalPersistenceWhenRemoteModeIsSelected(t *testing.T) {
	f := &submissionFixture{ready: true, mode: "remote"}
	s := NewSubmission(f, f, f, f, NewScheduler(nil, 1), "/synthetic/runner.py")
	if _, err := s.Create(context.Background(), CreateMeeting{Selection: "grant", Title: "Meeting", Language: "en-US"}); !errors.Is(err, domain.ErrRemoteUnsupported) {
		t.Fatal("remote mode created a local task", err)
	}
	if len(f.steps) != 0 || f.released {
		t.Fatal("remote mode consumed a local file grant")
	}
}

type workflowFixture func(context.Context, string, string) (domain.PetProfile, error)

func (f workflowFixture) Workflow(ctx context.Context, event, key string) (domain.PetProfile, error) {
	return f(ctx, event, key)
}

func TestSubmissionRewardsOnlyDurablyCreatedJobsAndIgnoresRewardFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint("persist-fails-", failure), func(t *testing.T) {
			f := &submissionFixture{ready: true, fail: failure}
			q := NewScheduler(nil, 1)
			_ = q.Shutdown(context.Background())
			calls := 0
			hook := workflowFixture(func(_ context.Context, event, key string) (domain.PetProfile, error) {
				calls++
				if f.input == nil || f.input.Job.ID != key || event != "job_created" || !f.released {
					t.Error("reward ran before publication or used wrong id")
				}
				return domain.PetProfile{}, domain.ErrPet
			})
			s := NewSubmission(f, f, f, f, q, "/synthetic/runner.py", hook)
			id, err := s.Create(context.Background(), CreateMeeting{Selection: "grant", Title: "Synthetic meeting", Language: "en-US"})
			if failure {
				if err == nil || calls != 0 || id != "" {
					t.Fatal("failed persistence rewarded", id, err, calls)
				}
			} else if err != nil || calls != 1 || id != f.input.Job.ID {
				t.Fatal("reward failure changed accepted job", id, err, calls)
			}
		})
	}
}
