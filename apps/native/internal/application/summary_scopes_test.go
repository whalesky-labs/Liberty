package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type permissionSummaryRepository struct {
	SummaryRepository
	calls  []string
	job    string
	run    string
	onList func()
}

func (repository *permissionSummaryRepository) SummaryRun(_ context.Context, job, run string) (domain.SummaryRun, error) {
	repository.calls = append(repository.calls, "resume")
	repository.job, repository.run = job, run
	return domain.SummaryRun{ID: run, JobID: job, Status: "completed"}, nil
}

func (repository *permissionSummaryRepository) ListSummaries(_ context.Context, job string) ([]domain.SummaryRun, error) {
	repository.calls = append(repository.calls, "list")
	repository.job = job
	if repository.onList != nil {
		repository.onList()
	}
	return []domain.SummaryRun{{ID: "run-1", JobID: job, Status: "completed"}}, nil
}

func (repository *permissionSummaryRepository) SelectSummary(_ context.Context, job, run string) error {
	repository.calls = append(repository.calls, "select")
	repository.job, repository.run = job, run
	return nil
}

func (repository *permissionSummaryRepository) DeleteSummary(_ context.Context, job, run string) error {
	repository.calls = append(repository.calls, "delete")
	repository.job, repository.run = job, run
	return nil
}

type permissionSummaryResources struct {
	SummaryResources
	reads  int
	onRead func()
}

func (resources *permissionSummaryResources) SummaryOptions(context.Context) (SummaryOptions, error) {
	resources.reads++
	if resources.onRead != nil {
		resources.onRead()
	}
	return SummaryOptions{Models: []SummaryModelOption{{ID: "model-1", Name: "Synthetic", Model: "synthetic"}}}, nil
}

func enablePermissionSummary(t *testing.T, scopes *WindowScopes) (*permissionSummaryRepository, *permissionSummaryResources) {
	t.Helper()
	repository := &permissionSummaryRepository{}
	resources := &permissionSummaryResources{}
	service := NewSummaryService(nil, repository, resources, nil)
	t.Cleanup(service.Close)
	scopes.EnableSummary(service)
	return repository, resources
}

func TestSummaryScopeCanceledObservationDoesNotReadOptionsOrRuns(t *testing.T) {
	scopes, grant, ports := permissionGrant(t, AISummary, "local")
	repository, resources := enablePermissionSummary(t, scopes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ports.onRead = cancel
	state, err := grant.SummaryState(ctx)
	if !errors.Is(err, context.Canceled) || resources.reads != 0 || len(repository.calls) != 0 || !reflect.DeepEqual(state, SummaryWindowState{}) {
		t.Fatalf("canceled observation delivered or continued: err=%v options=%d calls=%v state=%+v", err, resources.reads, repository.calls, state)
	}
}

func TestSummaryScopeCancellationAtEachObservationStage(t *testing.T) {
	for _, stage := range []string{"options", "runs"} {
		t.Run(stage, func(t *testing.T) {
			scopes, grant, _ := permissionGrant(t, AISummary, "local")
			repository, resources := enablePermissionSummary(t, scopes)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "options" {
				resources.onRead = cancel
			} else {
				repository.onList = cancel
			}
			state, err := grant.SummaryState(ctx)
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(state, SummaryWindowState{}) {
				t.Fatal("canceled observation delivered a partial state", err)
			}
			if stage == "options" && len(repository.calls) != 0 {
				t.Fatal("canceled options read continued into summary runs")
			}
		})
	}
}

func TestSummaryScopeIndependentChildrenKeepTheirOriginalJob(t *testing.T) {
	for _, role := range []WindowRole{AISummary, MeetingNotes} {
		t.Run(string(role), func(t *testing.T) {
			ctx := context.Background()
			scopes, parent, _ := permissionGrant(t, Workbench, "local")
			repository, _ := enablePermissionSummary(t, scopes)
			child, err := parent.OpenChild(role)
			if err != nil {
				t.Fatal(err)
			}
			replacement, err := scopes.OpenFromMain(ctx, Workbench, "local", scopedID+"1")
			if err != nil {
				t.Fatal(err)
			}
			parent.Close()
			result, err := child.Result(ctx, scopedID)
			if err != nil || result.Job.ID != scopedID {
				t.Fatal("parent replacement revoked or redirected an independent child", err)
			}
			if role == AISummary {
				if err := child.GenerateSummary(ctx, SummaryForm{RunID: "run-1"}); err != nil || repository.job != scopedID {
					t.Fatal("independent AI child lost its original mutation scope", err)
				}
			}
			nextChild, err := replacement.OpenChild(role)
			if err != nil {
				t.Fatal(err)
			}
			child.Close()
			if _, err := child.Result(ctx, scopedID); !errors.Is(err, ErrWindowScope) {
				t.Fatal("replaced child could still read its old job", err)
			}
			if _, err := nextChild.Result(ctx, scopedID+"1"); err != nil {
				t.Fatal("closing an old child revoked its replacement", err)
			}
			replacement.Close()
			if _, err := nextChild.Result(ctx, scopedID+"1"); err != nil {
				t.Fatal("closing a parent revoked its independently issued child", err)
			}
		})
	}
}
