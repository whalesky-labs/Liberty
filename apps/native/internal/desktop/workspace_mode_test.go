package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type processingModeReply struct {
	mode string
	err  error
}

type processingModeCall struct {
	reply chan processingModeReply
}

type processingModeReader struct {
	calls chan processingModeCall
}

func (service *processingModeReader) ProcessingMode(ctx context.Context) (string, error) {
	call := processingModeCall{reply: make(chan processingModeReply, 1)}
	service.calls <- call
	select {
	case reply := <-call.reply:
		return reply.mode, reply.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func receiveProcessingModeCall(t *testing.T, calls <-chan processingModeCall) processingModeCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(2 * time.Second):
		t.Fatal("processing mode read did not start")
		return processingModeCall{}
	}
}

type workspaceModeService struct {
	queues     chan domain.QueueQuery
	dashboards chan string
	source     string
}

func (service *workspaceModeService) Queue(_ context.Context, query domain.QueueQuery) (domain.JobPage, error) {
	service.queues <- query
	return domain.JobPage{Source: query.Source}, nil
}

func (*workspaceModeService) Result(context.Context, string) (domain.Result, error) {
	return domain.Result{}, errors.New("synthetic result is not used")
}

func (service *workspaceModeService) Overview(_ context.Context, period string) (domain.DashboardOverview, error) {
	service.dashboards <- period
	return domain.DashboardOverview{Source: service.source}, nil
}

func drainWorkspaceModeRefresh(t *testing.T, callbacks <-chan func(), service *workspaceModeService, source string) {
	t.Helper()
	query := <-service.queues
	if query.Source != source {
		t.Fatalf("queue refresh used %q instead of %q", query.Source, source)
	}
	if period := <-service.dashboards; period != "7d" {
		t.Fatalf("dashboard refresh used %q instead of the active period", period)
	}
	for range 2 {
		(<-callbacks)()
	}
}

func TestWorkspaceProcessingModeSwitchRefreshesBoundViews(t *testing.T) {
	service := &workspaceModeService{queues: make(chan domain.QueueQuery, 4), dashboards: make(chan string, 4)}
	callbacks := make(chan func(), 8)
	dispatch := func(callback func()) { callbacks <- callback }
	meetings := client.New(service, dispatch, func() {})
	dashboard := client.NewDashboard(service, dispatch, func() {})
	view := NewMeetings(meetings, "en-US")
	action := func() {}
	workspace := &Workspace{
		controller:   meetings,
		view:         view,
		dashboard:    NewDashboardView(dashboard, "en-US"),
		newJob:       &NewJobView{Client: &client.SubmissionController{}},
		newJobAction: action,
	}
	t.Cleanup(func() {
		meetings.Close()
		dashboard.Close()
	})
	view.Source = "local"
	view.NewJob = action
	dashboard.Overview = &domain.DashboardOverview{Source: "local"}

	service.source = "remote"
	workspace.applyProcessingMode("remote")
	if view.Source != "remote" || view.NewJob != nil || !workspace.newJob.Client.Unavailable {
		t.Fatal("remote mode did not disable local-only workbench actions")
	}
	drainWorkspaceModeRefresh(t, callbacks, service, "remote")
	if meetings.Source != "remote" || meetings.Page.Source != "remote" || dashboard.Overview.Source != "remote" {
		t.Fatal("remote mode did not bind the queue and dashboard snapshots")
	}

	service.source = "local"
	workspace.applyProcessingMode("local")
	if view.Source != "local" || view.NewJob == nil || workspace.newJob.Client.Unavailable {
		t.Fatal("local mode did not restore local-only workbench actions")
	}
	drainWorkspaceModeRefresh(t, callbacks, service, "local")
	if meetings.Source != "local" || meetings.Page.Source != "local" {
		t.Fatal("local mode did not restore the local queue snapshot")
	}

	workspace.applyProcessingMode("unsupported")
	if view.Source != "local" || view.NewJob == nil || workspace.newJob.Client.Unavailable {
		t.Fatal("unsupported processing mode polluted the active state")
	}
}

func TestWorkspaceProcessingModeReadFailureIsVisibleUntilSuccessfulRefresh(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			workspace := &Workspace{
				preferences: domain.DefaultUIPreferences(),
				view:        &Meetings{Locale: locale, Source: "remote"},
			}
			workspace.applyProcessingModeResult("", errors.New("synthetic mode read failure"))
			if !workspace.processingModeError || workspace.view.Source != "remote" {
				t.Fatal("mode read failure did not preserve the last known mode")
			}
			message, tone := workspace.shellStatus()
			if message != presentation.Message(locale, "shell.modeReadFailed") || tone != "error" {
				t.Fatalf("mode read failure was not exposed in the shell status: %q/%q", message, tone)
			}

			workspace.applyProcessingModeResult("local", nil)
			if workspace.processingModeError || workspace.view.Source != "local" {
				t.Fatal("successful mode refresh did not clear the visible error")
			}
			if message, tone := workspace.shellStatus(); message != "" || tone != "success" {
				t.Fatalf("successful mode refresh retained stale status: %q/%q", message, tone)
			}
		})
	}
}

func TestWorkspaceProcessingModeRetryIsAccessibleAndDeduplicated(t *testing.T) {
	service := &processingModeReader{calls: make(chan processingModeCall, 2)}
	callbacks := make(chan func(), 2)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	workspace := &Workspace{
		modeService: service,
		modeContext: ctx,
		modeCancel:  cancel,
		modeEpoch:   1,
		modeDispatch: func(callback func()) {
			callbacks <- callback
		},
		view:                &Meetings{Source: "remote"},
		processingModeError: true,
	}

	for _, locale := range []string{"zh-CN", "en-US"} {
		shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), ModeReadFailed: true, RetryMode: workspace.retryProcessingMode}
		tester := ui.NewTester(shell.View, 720, 420)
		retry, ok := tester.AccessByRole(ui.RoleButton, presentation.Message(locale, "common.retry"))
		if !ok || retry.Disabled {
			t.Fatalf("%s mode retry is not an enabled, accessible button: %+v", locale, retry)
		}
	}

	workspace.retryProcessingMode()
	call := receiveProcessingModeCall(t, service.calls)
	if !workspace.processingModeBusy {
		t.Fatal("mode read did not expose its in-flight state")
	}
	workspace.retryProcessingMode()
	select {
	case <-service.calls:
		t.Fatal("repeated retry started a duplicate mode read")
	default:
	}

	call.reply <- processingModeReply{mode: "local"}
	(<-callbacks)()
	if workspace.processingModeBusy || workspace.processingModeError || workspace.view.Source != "local" {
		t.Fatal("successful retry did not apply and settle the current processing mode")
	}
}

func TestSavedSettingsApplyModeImmediatelyAndRejectStaleReads(t *testing.T) {
	service := &processingModeReader{calls: make(chan processingModeCall, 1)}
	callbacks := make(chan func(), 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	workspace := &Workspace{
		modeService: service,
		modeContext: ctx,
		modeCancel:  cancel,
		modeEpoch:   3,
		modeDispatch: func(callback func()) {
			callbacks <- callback
		},
		view: &Meetings{Source: "remote"},
	}

	workspace.refreshProcessingMode()
	staleRead := receiveProcessingModeCall(t, service.calls)
	workspace.applySavedSettings(domain.Settings{ProcessingMode: "local"})
	if workspace.view.Source != "local" || workspace.modeRevision != 1 {
		t.Fatal("committed settings did not switch the workspace immediately")
	}
	staleRead.reply <- processingModeReply{mode: "remote"}
	(<-callbacks)()
	if workspace.view.Source != "local" {
		t.Fatal("an older mode read overwrote the committed settings mode")
	}
}

func TestWorkspaceIgnoresModeResultAfterCloseOrServiceReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	workspace := &Workspace{view: &Meetings{Source: "remote"}, modeEpoch: 4, processingModeError: true}
	workspace.closed = true
	cancel()
	workspace.completeProcessingModeRefresh(ctx, 4, 0, "local", nil)
	if workspace.view.Source != "remote" || !workspace.processingModeError {
		t.Fatal("late mode read mutated the closed workspace")
	}

	workspace.closed = false
	workspace.modeEpoch = 5
	workspace.completeProcessingModeRefresh(context.Background(), 4, 0, "local", nil)
	if workspace.view.Source != "remote" || !workspace.processingModeError {
		t.Fatal("mode result from a replaced service mutated the workspace")
	}
}
