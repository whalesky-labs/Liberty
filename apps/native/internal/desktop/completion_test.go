package desktop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type completionQueueService struct {
	mu   sync.Mutex
	page domain.JobPage
	err  error
}

func (service *completionQueueService) Queue(context.Context, domain.QueueQuery) (domain.JobPage, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.page, service.err
}

func (*completionQueueService) Result(context.Context, string) (domain.Result, error) {
	return domain.Result{}, errors.New("unused completion test result")
}

func (service *completionQueueService) setPage(page domain.JobPage, err error) {
	service.mu.Lock()
	service.page = page
	service.err = err
	service.mu.Unlock()
}

func receiveCompletionCallback(t *testing.T, callbacks <-chan func()) {
	t.Helper()
	select {
	case callback := <-callbacks:
		callback()
	case <-time.After(5 * time.Second):
		t.Fatal("queue refresh did not settle")
	}
}

func TestJobCompletionObserverOnlyReportsObservedTransitions(t *testing.T) {
	observer := jobCompletionObserver{}
	if got := observer.Observe("local", []domain.JobSummary{{ID: "historical", OverallStatus: "completed"}}, false); len(got) != 0 {
		t.Fatal("loading snapshot emitted a completion")
	}
	if got := observer.Observe("local", []domain.JobSummary{{ID: "historical", OverallStatus: "completed"}, {ID: "new", OverallStatus: "queued"}}, true); len(got) != 0 {
		t.Fatal("initial history emitted a completion")
	}
	job := domain.JobSummary{ID: "new", Source: "local", Title: "Synthetic meeting", OverallStatus: "completed"}
	if got := observer.Observe("local", []domain.JobSummary{job}, true); len(got) != 1 || got[0].ID != job.ID {
		t.Fatal("observed job transition was not reported", got)
	}
	if got := observer.Observe("local", []domain.JobSummary{job}, true); len(got) != 0 {
		t.Fatal("repeated completed snapshot emitted a duplicate", got)
	}
}

func TestJobCompletionObserverSeparatesSourcesAndReportsRetryCompletion(t *testing.T) {
	observer := jobCompletionObserver{}
	observer.Observe("local", []domain.JobSummary{{ID: "shared", OverallStatus: "failed"}}, true)
	observer.Observe("remote", []domain.JobSummary{{ID: "shared", OverallStatus: "queued"}}, true)

	remote := domain.JobSummary{ID: "shared", Source: "remote", Title: "Remote synthetic", OverallStatus: "completed"}
	if got := observer.Observe("remote", []domain.JobSummary{remote}, true); len(got) != 1 || got[0].Source != "remote" {
		t.Fatal("source-specific completion was not reported", got)
	}
	local := domain.JobSummary{ID: "shared", Source: "local", Title: "Local synthetic", OverallStatus: "completed"}
	if got := observer.Observe("local", []domain.JobSummary{local}, true); len(got) != 1 || got[0].Source != "local" {
		t.Fatal("retry completion or duplicate-source identity was lost", got)
	}
}

func TestWorkspaceObservesCompletionFromQueuePolling(t *testing.T) {
	service := &completionQueueService{page: domain.JobPage{Source: "local", Items: []domain.JobSummary{{ID: "historical", OverallStatus: "completed"}}}}
	callbacks := make(chan func(), 8)
	workspace := &Workspace{preferences: domain.UIPreferences{Locale: "en-US"}}
	controller := client.New(service, func(callback func()) { callbacks <- callback }, workspace.onJobsChanged)
	workspace.controller = controller
	workspace.showJobNotification = func(string, string) context.CancelFunc { return nil }
	defer func() {
		workspace.stopJobCompletionFeedback()
		controller.Close()
	}()

	controller.Refresh(domain.QueueQuery{Source: "local"})
	receiveCompletionCallback(t, callbacks)
	if workspace.completionMessage != "" {
		t.Fatal("historical completed job emitted a notification")
	}

	service.setPage(domain.JobPage{Source: "local", Items: []domain.JobSummary{{ID: "new", Title: "Synthetic meeting", OverallStatus: "queued"}}}, nil)
	controller.Poll()
	receiveCompletionCallback(t, callbacks)
	service.setPage(domain.JobPage{Source: "local", Items: []domain.JobSummary{{ID: "new", Title: "Synthetic meeting", OverallStatus: "completed"}}}, errors.New("synthetic queue refresh failure"))
	controller.Poll()
	receiveCompletionCallback(t, callbacks)
	if workspace.completionMessage != "" {
		t.Fatal("failed queue refresh emitted a completion")
	}
	service.setPage(domain.JobPage{Source: "local", Items: []domain.JobSummary{{ID: "new", Title: "Synthetic meeting", OverallStatus: "completed"}}}, nil)
	controller.Poll()
	receiveCompletionCallback(t, callbacks)
	want := presentation.Format("en-US", "shell.jobCompletedBody", map[string]any{"title": "Synthetic meeting"})
	if workspace.completionMessage != want {
		t.Fatalf("queue completion did not publish status: got %q want %q", workspace.completionMessage, want)
	}
}

func TestShellCompletionStatusReplacesReadyMessage(t *testing.T) {
	locale := "en-US"
	message := presentation.Format(locale, "shell.jobCompletedBody", map[string]any{"title": "Synthetic meeting"})
	shell := &Shell{Locale: locale, StatusMessage: message, StatusTone: "success"}
	tester := ui.NewTester(shell.View, presentation.WindowWidth, presentation.WindowHeight)
	if !tester.HasText(message) || tester.HasText(presentation.Message(locale, "shell.statusReady")) {
		t.Fatal("completion status did not replace the idle status message")
	}
	if _, ok := tester.AccessByRole(ui.RoleStatus, message); !ok {
		t.Fatal("completion status is not exposed to assistive technology")
	}
}

func TestWorkspaceJobCompletionPublishesStatusAndStopsFeedback(t *testing.T) {
	workspace := &Workspace{preferences: domain.UIPreferences{Locale: "en-US"}}
	shown := make(chan [2]string, 1)
	canceled := make(chan struct{}, 1)
	workspace.showJobNotification = func(title, body string) context.CancelFunc {
		shown <- [2]string{title, body}
		return func() { canceled <- struct{}{} }
	}
	job := domain.JobSummary{ID: "synthetic", Title: "Synthetic meeting"}
	workspace.presentJobCompletion(job)
	wantBody := presentation.Format("en-US", "shell.jobCompletedBody", map[string]any{"title": job.Title})
	if workspace.completionMessage != wantBody || workspace.completionTimer == nil {
		t.Fatal("completion status was not retained for its display interval")
	}
	if got := <-shown; got[0] != presentation.Message("en-US", "shell.jobCompletedTitle") || got[1] != wantBody {
		t.Fatal("native notification did not use localized completion content", got)
	}
	workspace.stopJobCompletionFeedback()
	if workspace.completionTimer != nil || workspace.notificationCancel != nil {
		t.Fatal("closing feedback left a timer or notification request active")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("pending notification was not canceled")
	}
}

func TestWorkspaceJobCompletionReplacesPreviousNotification(t *testing.T) {
	workspace := &Workspace{preferences: domain.UIPreferences{Locale: "zh-CN"}}
	canceled := 0
	workspace.showJobNotification = func(string, string) context.CancelFunc {
		return func() { canceled++ }
	}
	workspace.presentJobCompletion(domain.JobSummary{ID: "first", Title: "First"})
	workspace.presentJobCompletion(domain.JobSummary{ID: "second", Title: "Second"})
	if canceled != 1 || workspace.completionRevision != 2 || workspace.completionMessage != presentation.Format("zh-CN", "shell.jobCompletedBody", map[string]any{"title": "Second"}) {
		t.Fatal("new completion did not replace the current status and pending notification")
	}
	workspace.stopJobCompletionFeedback()
}
