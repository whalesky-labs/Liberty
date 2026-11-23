package desktop

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type workspaceJobsService struct {
	remote  atomic.Bool
	creates chan domain.JobInput
	finish  chan struct{}
}

func (service *workspaceJobsService) JobDefaults(context.Context) (application.JobDefaults, error) {
	return application.JobDefaults{Hotwords: "Synthetic terms", SummaryTemplate: "Synthetic minutes"}, nil
}
func (service *workspaceJobsService) ProcessingMode(context.Context) (string, error) {
	if service.remote.Load() {
		return "remote", nil
	}
	return "local", nil
}
func (service *workspaceJobsService) Create(ctx context.Context, input domain.JobInput) error {
	service.creates <- input
	select {
	case <-service.finish:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*workspaceJobsService) Input(context.Context, string) (domain.JobInput, error) {
	return domain.JobInput{}, application.ErrJobInput
}
func (*workspaceJobsService) Retry(context.Context, string, string, string) error { return nil }
func (*workspaceJobsService) Authorize(string) (application.SelectedMedia, error) {
	return application.SelectedMedia{}, application.ErrJobInput
}
func (*workspaceJobsService) Resolve(context.Context, string) (application.MediaInput, error) {
	return application.MediaInput{Path: "/synthetic/media.wav", Source: domain.SourceFile{Name: "synthetic.wav", Kind: "audio"}}, nil
}
func (*workspaceJobsService) Release(string) {}
func (*workspaceJobsService) Status(context.Context) (domain.RuntimeOverview, error) {
	return domain.RuntimeOverview{ShellReady: true}, nil
}
func (*workspaceJobsService) CreateJob(string) error { return nil }
func (*workspaceJobsService) Queue(_ context.Context, query domain.QueueQuery) (domain.JobPage, error) {
	return domain.JobPage{Source: query.Source}, nil
}
func (*workspaceJobsService) Result(context.Context, string) (domain.Result, error) {
	return domain.Result{}, errors.New("unscoped result read")
}
func (*workspaceJobsService) ResultForSource(context.Context, string, string) (domain.Result, error) {
	return domain.Result{}, errors.New("detail used workbench permission")
}
func (*workspaceJobsService) JobForSource(_ context.Context, source, id string) (domain.Result, error) {
	return domain.Result{Job: domain.JobSummary{ID: id, Source: source, Title: source + " synthetic detail", OverallStatus: "failed"}}, nil
}

func awaitWorkspaceJobs(t *testing.T, callbacks <-chan func(), done func() bool) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !done() {
		select {
		case callback := <-callbacks:
			callback()
		case <-timer.C:
			t.Fatal("workspace job operation did not settle")
		}
	}
}

func awaitWorkspaceCreation(t *testing.T, callbacks <-chan func(), creates <-chan domain.JobInput) domain.JobInput {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case input := <-creates:
			return input
		case callback := <-callbacks:
			callback()
		case <-timer.C:
			t.Fatal("workspace did not start local creation")
			return domain.JobInput{}
		}
	}
}

func newWorkspaceJobs(t *testing.T, locale string, actions ...client.JobRetry) (*Workspace, *workspaceJobsService, chan func()) {
	t.Helper()
	service := &workspaceJobsService{creates: make(chan domain.JobInput, 1), finish: make(chan struct{})}
	callbacks := make(chan func(), 64)
	dispatch := func(callback func()) { callbacks <- callback }
	controller := client.New(service, dispatch, func() {})
	preferences := domain.DefaultUIPreferences()
	preferences.Locale = locale
	workspace := &Workspace{
		controller: controller, view: NewMeetings(controller, locale), meetings: service,
		preferences: preferences, tab: "/jobs/new",
		runtime: &RuntimeView{Client: &client.RuntimeController{Panel: &domain.RuntimePanel{Overview: domain.RuntimeOverview{ShellReady: true}}}},
	}
	scheduler := application.NewScheduler(nil, 1)
	if err := scheduler.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	submission := application.NewSubmission(service, service, service, service, scheduler, "/synthetic/runner.py")
	workspace.attachJobs(submission, nil, service, dispatch, actions...)
	t.Cleanup(func() {
		workspace.closed = true
		workspace.closeDetail()
		workspace.newJob.Client.Close()
		workspace.view.Actions.Close()
		controller.Close()
	})
	awaitWorkspaceJobs(t, callbacks, func() bool { return workspace.newJob.Client.Form.SummaryTemplate != "" })
	return workspace, service, callbacks
}

func TestWorkspaceCreatedLocalDetailSurvivesRemoteSwitchDuringCreation(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			workspace, service, callbacks := newWorkspaceJobs(t, locale)
			controller := workspace.newJob.Client
			controller.Media = &application.SelectedMedia{Token: "synthetic-selection", Name: "synthetic.wav", Kind: "audio"}
			controller.Form.Selection, controller.Form.Title = controller.Media.Token, "Synthetic meeting"
			workspace.newJob.submit()
			input := awaitWorkspaceCreation(t, callbacks, service.creates)
			service.remote.Store(true)
			workspace.view.Source = "remote"
			workspace.view.Actions.Source = "remote"
			workspace.applyJobMode("remote")
			workspace.controller.Refresh(domain.QueueQuery{Source: "remote"})
			awaitWorkspaceJobs(t, callbacks, func() bool { return !workspace.controller.LoadingJobs })
			workspace.controller.Select(input.Job.ID)
			awaitWorkspaceJobs(t, callbacks, func() bool { return !workspace.controller.LoadingResult })
			if workspace.controller.ResultError != nil || workspace.controller.Result == nil || workspace.controller.Result.Job.Source != "remote" {
				t.Fatal("remote queue did not establish its independent same-ID selection")
			}
			close(service.finish)
			awaitWorkspaceJobs(t, callbacks, func() bool {
				return !controller.Busy && workspace.detail != nil && workspace.detail.Client.Result != nil
			})
			if workspace.tab != "/jobs/"+input.Job.ID || workspace.detail.Client.Result.Job.Source != "local" || workspace.detail.Client.SelectedID != input.Job.ID || workspace.detail.Source != "local" {
				t.Fatal("committed local creation navigated using the active remote queue")
			}
			if workspace.view.Source != "remote" || workspace.view.Actions.Source != "remote" || !controller.Unavailable || workspace.controller.Result.Job.Source != "remote" {
				t.Fatal("local detail navigation changed the active processing mode or queue selection")
			}
			queueResult, detailResult := workspace.controller.Result, workspace.detail.Client.Result
			workspace.controller.Poll()
			workspace.detail.Client.ResultController.Refresh()
			awaitWorkspaceJobs(t, callbacks, func() bool {
				return !workspace.controller.LoadingJobs && workspace.controller.Result != queueResult && workspace.detail.Client.Result != detailResult
			})
			if workspace.detail.Client.Result.Job.Source != "local" || workspace.controller.Result.Job.Source != "remote" {
				t.Fatal("background queue polling rebound the local detail")
			}
			tester := ui.NewTester(workspace.View, 1180, 1500)
			if !tester.HasText("local synthetic detail") || tester.HasText("remote synthetic detail") {
				t.Fatal("workspace rendered the queue snapshot instead of its bound detail")
			}
			if err := tester.Click(presentation.Message(locale, "jobDetail.backToJobs")); err != nil {
				t.Fatal(err)
			}
			if workspace.detail != nil || workspace.tab != "/jobs" || workspace.view.Source != "remote" {
				t.Fatal("leaving detail did not release its controller and return to the remote queue")
			}
		})
	}
}

func TestWorkspaceDetailKeepsItsSourceWhenQueueModeChanges(t *testing.T) {
	for _, source := range []string{"local", "remote"} {
		t.Run(source, func(t *testing.T) {
			workspace, _, callbacks := newWorkspaceJobs(t, "en-US")
			workspace.view.Source = source
			workspace.view.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read"}}
			workspace.openDetail("synthetic-detail")
			awaitWorkspaceJobs(t, callbacks, func() bool { return workspace.detail.Client.Result != nil })
			other := "remote"
			if source == "remote" {
				other = "local"
			}
			workspace.view.Source = other
			workspace.applyJobMode(other)
			workspace.controller.Select("")
			workspace.controller.Refresh(domain.QueueQuery{Source: other})
			previous := workspace.detail.Client.Result
			workspace.detail.Client.ResultController.Refresh()
			awaitWorkspaceJobs(t, callbacks, func() bool {
				return !workspace.controller.LoadingJobs && workspace.detail.Client.Result != previous
			})
			tester := ui.NewTester(workspace.View, 1180, 1500)
			if workspace.detail.Client.Result.Job.Source != source || !tester.HasText(source+" synthetic detail") {
				t.Fatal("detail did not retain its source after a processing-mode change")
			}
			assertDisabled(t, tester, presentation.Message("en-US", "jobDetail.retryJob"), source == "remote")
		})
	}
}

func TestWorkspaceSettingsReturnReopensBoundDetail(t *testing.T) {
	workspace, _, callbacks := newWorkspaceJobs(t, "en-US")
	workspace.openDetailFromSource("synthetic-detail", "local")
	awaitWorkspaceJobs(t, callbacks, func() bool { return workspace.detail != nil && workspace.detail.Client.Result != nil })

	workspace.navigate("/settings")
	if workspace.detail != nil || workspace.lastAppPath != "/jobs/synthetic-detail" || workspace.lastDetailSource != "local" || workspace.lastDetailID != "synthetic-detail" {
		t.Fatalf("settings navigation did not retain the bound detail: detail=%v path=%q source=%q id=%q", workspace.detail != nil, workspace.lastAppPath, workspace.lastDetailSource, workspace.lastDetailID)
	}

	workspace.returnToApp()
	awaitWorkspaceJobs(t, callbacks, func() bool { return workspace.detail != nil && workspace.detail.Client.Result != nil })
	if workspace.tab != "/jobs/synthetic-detail" || workspace.detail.Source != "local" || workspace.detail.Client.SelectedID != "synthetic-detail" || workspace.lastDetailID != "" {
		t.Fatalf("settings return did not recreate the original detail binding: tab=%q source=%q id=%q pending=%q", workspace.tab, workspace.detail.Source, workspace.detail.Client.SelectedID, workspace.lastDetailID)
	}
}

func TestWorkspaceSettingsReturnKeepsQueueFilter(t *testing.T) {
	for _, filter := range []string{"processing", "completed", "failed"} {
		t.Run(filter, func(t *testing.T) {
			jobs := queueParityJobs("local", 8)
			callbacks := make(chan func(), 16)
			controller := client.New(viewMeetings{jobs: jobs}, func(callback func()) { callbacks <- callback }, func() {})
			t.Cleanup(controller.Close)
			workspace := &Workspace{controller: controller, view: NewMeetings(controller, "en-US"), tab: "/"}
			workspace.navigate("/jobs")
			awaitWorkspaceJobs(t, callbacks, func() bool { return !controller.LoadingJobs })
			tester := ui.NewTester(workspace.view.View, 1100, 800)
			label := map[string]string{"processing": "filterProcessing", "completed": "filterCompleted", "failed": "filterFailed"}[filter]
			button, ok := tester.AccessByRole(ui.RoleButton, workspace.view.msg(label))
			if !ok || button.Disabled || button.Offscreen {
				t.Fatalf("queue filter is unavailable: %+v", button)
			}
			tester.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
			flushView(t, callbacks, tester)
			workspace.navigate("/settings")
			workspace.returnToApp()
			flushView(t, callbacks, tester)
			want := domain.QueryJobs(jobs, domain.QueueQuery{Source: "local", Filter: filter})
			if workspace.tab != "/jobs" || workspace.view.status != filter || len(controller.Page.Items) != want.Total {
				t.Fatalf("queue returned to %q with %q and %d rows, want %q and %d rows", workspace.tab, workspace.view.status, len(controller.Page.Items), filter, want.Total)
			}
		})
	}
}

func TestWorkspaceDetailRejectsUnscopedReaderSourceMismatch(t *testing.T) {
	reader := detailSourceReader{service: viewMeetings{jobs: []domain.JobSummary{{ID: "same-id", Source: "remote"}}}, source: "local"}
	if _, err := reader.Result(context.Background(), "same-id"); !errors.Is(err, client.ErrResultSourceMismatch) {
		t.Fatal("an unscoped fallback reader substituted another source's same-ID job", err)
	}
}

func TestWorkspaceDetailRuntimeWarningUsesJobSourceInsteadOfQueueMode(t *testing.T) {
	for _, source := range []string{"local", "remote"} {
		t.Run(source, func(t *testing.T) {
			port := detailRetryPort{calls: make(chan detailRetryCall, 1)}
			workspace, _, callbacks := newWorkspaceJobs(t, "en-US", port)
			workspace.view.Source, workspace.view.Actions.Source = "remote", "remote"
			if source == "remote" {
				workspace.view.Source, workspace.view.Actions.Source = "local", "local"
			}
			workspace.applyJobMode(workspace.view.Source)
			workspace.view.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read", "jobs.retry"}}
			workspace.runtime.Client.Error = errors.New("synthetic runtime refresh failure")
			warnings := 0
			workspace.newJob.WarnRuntime = func() { warnings++ }
			workspace.openDetailFromSource("synthetic-detail", source)
			awaitWorkspaceJobs(t, callbacks, func() bool { return workspace.detail.Client.Result != nil })
			tester := ui.NewTester(workspace.View, 1180, 1500)
			label := presentation.Message("en-US", "jobDetail.retryJob")
			if err := tester.Click(label); err != nil {
				t.Fatal(err)
			}
			if source == "local" {
				if warnings != 1 || workspace.view.Actions.Busy || len(port.calls) != 0 {
					t.Fatal("local retry under remote mode bypassed its shared runtime warning")
				}
				workspace.runtime.Client.Error = nil
				tester.Frame()
				if err := tester.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			awaitWorkspaceJobs(t, callbacks, func() bool { return !workspace.view.Actions.Busy })
			call := <-port.calls
			if call.source != source || workspace.view.Actions.Source != workspace.view.Source || source == "remote" && warnings != 0 {
				t.Fatal("detail retry used queue mode or local runtime to authorize a remote job", call)
			}
		})
	}
}

func TestWorkspaceQueueRetryChecksLocalRuntimeWithoutBlockingRemote(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, source := range []string{"local", "remote"} {
			t.Run(locale+"/"+source, func(t *testing.T) {
				port := detailRetryPort{calls: make(chan detailRetryCall, 1)}
				workspace, _, callbacks := newWorkspaceJobs(t, locale, port)
				workspace.tab = "/jobs"
				job := queueParityJobs(source, 2)[1]
				workspace.view.Source, workspace.view.Actions.Source = source, source
				workspace.view.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read", "jobs.retry"}}
				workspace.controller.Page = domain.QueryJobs([]domain.JobSummary{job}, domain.QueueQuery{Source: source})
				workspace.controller.Jobs = workspace.controller.Page.Items
				workspace.runtime.Client.Error = errors.New("synthetic runtime refresh failure")
				warnings := 0
				workspace.newJob.WarnRuntime = func() { warnings++ }
				tester := ui.NewTester(workspace.View, 1100, 800)
				retry := func() {
					t.Helper()
					if err := tester.Click(workspace.view.msg("moreActions") + ": " + job.Title); err != nil {
						t.Fatal(err)
					}
					label := presentation.Message(locale, "common.retry")
					entry, ok := tester.AccessByRole(ui.RoleMenuItem, label)
					if !ok || entry.Disabled || entry.Offscreen {
						t.Fatalf("retry menu item is unavailable: %+v", entry)
					}
					tester.ClickAt(entry.Bounds.X+entry.Bounds.W/2, entry.Bounds.Y+entry.Bounds.H/2)
				}
				retry()
				if source == "local" {
					if warnings != 1 || workspace.view.Actions.Busy || len(port.calls) != 0 {
						t.Fatal("queue retry skipped the unavailable local runtime warning")
					}
					workspace.runtime.Client.Error = nil
					tester.Frame()
					retry()
				}
				if !workspace.view.Actions.Busy {
					t.Fatal("retry did not start")
				}
				awaitWorkspaceJobs(t, callbacks, func() bool { return !workspace.view.Actions.Busy })
				call := <-port.calls
				if call.source != source || call.id != job.ID || source == "remote" && warnings != 0 {
					t.Fatal("queue retry used another source or remote retry depended on local runtime", call)
				}
			})
		}
	}
}
