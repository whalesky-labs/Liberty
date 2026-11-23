package desktop

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func assertDisabled(t *testing.T, tt *ui.Tester, label string, disabled bool) {
	t.Helper()
	node, ok := tt.Access(label)
	if !ok || node.Disabled != disabled {
		t.Fatalf("control %q disabled=%v, want %v (present=%v)", label, node.Disabled, disabled, ok)
	}
}

func TestRemoteQueueCapabilitySnapshotAndLocalBehavior(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			job := domain.JobSummary{ID: "remote-id", Source: "remote", Title: "Synthetic remote", OverallStatus: "completed"}
			page := domain.QueryJobs([]domain.JobSummary{job}, domain.QueueQuery{Source: "remote"})
			controller := &client.Controller{State: client.State{Jobs: page.Items, Page: page}, ResultController: &client.ResultController{}}
			v := NewMeetings(controller, locale)
			v.Source = "remote"
			v.Actions = &client.JobActions{}
			opened := 0
			v.OpenResult = func(string, string) { opened++ }
			v.OpenDetail = func(string) { opened++ }
			v.NewJob = func() { t.Error("remote upload was exposed") }
			tt := ui.NewTester(v.View, 1180, 800)
			assertDisabled(t, tt, v.msg("viewResult")+": "+job.Title, true)
			row, ok := tt.AccessByRole(ui.RoleRow, job.Title)
			if !ok || row.Focusable {
				t.Fatal("unavailable detail row can take keyboard input", row)
			}
			tt.ClickAt(row.Bounds.X+20, row.Bounds.Y+20)
			tt.Key(0, ui.KeyEnter)
			if opened != 0 || controller.SelectedID != "" {
				t.Fatal("unadvertised remote read action was invoked")
			}
			assertDisabled(t, tt, v.msg("newJob"), true)
			if v.CanReadJob(job) || v.CanReadResult(job) || v.CanDelete(job) {
				t.Fatal("missing capability snapshot enabled remote action")
			}
			v.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read"}}
			tt.Frame()
			row, _ = tt.AccessByRole(ui.RoleRow, job.Title)
			if !row.Focusable {
				t.Fatal("advertised detail row cannot take keyboard input")
			}
			assertDisabled(t, tt, v.msg("viewResult")+": "+job.Title, true)
			v.RemoteCapabilities.Operations = append(v.RemoteCapabilities.Operations, "jobs.result.read", "jobs.retry", "jobs.delete")
			tt.Frame()
			assertDisabled(t, tt, v.msg("viewResult")+": "+job.Title, false)
			if !v.CanDelete(job) || v.CanRetry(job) {
				t.Fatal("wrong completed action policy")
			}
			job.OverallStatus = "failed"
			if !v.CanRetry(job) {
				t.Fatal("advertised retry unavailable")
			}
			job.OverallStatus = "transcribing"
			if v.CanDelete(job) {
				t.Fatal("active job deletion enabled")
			}
			job.Source, job.OverallStatus = "local", "failed"
			v.RemoteCapabilities = nil
			if !v.CanReadJob(job) || !v.CanReadResult(job) || !v.CanRetry(job) || !v.CanDelete(job) {
				t.Fatal("remote snapshot disabled local action")
			}
		})
	}
}

func TestRemoteResultCapabilitiesGateRenameAndUnavailableClientFeatures(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			speaker := "speaker-0"
			result := &domain.Result{Job: domain.JobSummary{ID: "remote-id", Source: "remote", Title: "Synthetic remote", OverallStatus: "completed", DiarizationStatus: "completed"}, Speakers: []domain.Segment{{ID: "s1", Speaker: &speaker, Text: "Synthetic transcript", EndMS: 1000}}}
			v := NewResultView(&client.ResultController{Result: result}, locale)
			port := &workbenchPort{}
			v.Actions = client.NewResultActions(port, port, func(context.Context, string, string, string) (bool, error) { return true, nil }, func(fn func()) { fn() }, func() {}, func(string, string) {})
			defer v.Actions.Close()
			v.EditingEnabled, v.WordExportEnabled = true, true
			v.OpenSummary, v.OpenNotes = func(string) { t.Error("remote AI opened") }, func(string) { t.Error("remote notes opened") }
			tt := ui.NewTester(v.View, 1000, 760)
			if !tt.HasText(v.msg("remoteOperationUnavailable")) || tt.HasText("Synthetic transcript") {
				t.Fatal("unavailable remote result remained actionable")
			}
			v.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read", "jobs.result.read"}}
			tt.Frame()
			assertDisabled(t, tt, presentation.Message(locale, "common.edit"), true)
			for _, key := range []string{"aiSummary", "viewNotes", "exportWord"} {
				assertDisabled(t, tt, v.msg(key), true)
			}
			for _, key := range []string{"exportTranscript", "exportNotes", "exportBundle"} {
				assertDisabled(t, tt, v.msg(key), false)
			}
			v.RemoteCapabilities.Operations = append(v.RemoteCapabilities.Operations, "transcript.speakers.rename")
			tt.Frame()
			assertDisabled(t, tt, presentation.Message(locale, "common.edit"), false)
			if err := tt.Click(presentation.Message(locale, "common.edit")); err != nil {
				t.Fatal(err)
			}
			v.RemoteCapabilities.Operations = []string{"jobs.read", "jobs.result.read"}
			tt.Frame()
			assertDisabled(t, tt, v.msg("speakerInputPlaceholder"), true)
			assertDisabled(t, tt, presentation.Message(locale, "common.save"), true)
		})
	}
}

func TestRemoteDetailAndDashboardRequireReadCapabilities(t *testing.T) {
	job := domain.JobSummary{ID: "remote-id", Source: "remote", Title: "Synthetic remote", OverallStatus: "completed"}
	v := NewMeetings(&client.Controller{ResultController: &client.ResultController{Result: &domain.Result{Job: job}}}, "en-US")
	v.OpenResult = func(string, string) {}
	tt := ui.NewTester(func(c *ui.Context) { v.Detail(c, func() {}) }, 1180, 800)
	if tt.HasText(job.Title) || !tt.HasText(presentation.Message("en-US", "workbench.remoteOperationUnavailable")) {
		t.Fatal("detail exposed without jobs.read")
	}
	v.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read"}}
	tt.Frame()
	assertDisabled(t, tt, presentation.Message("en-US", "jobDetail.viewWorkbench"), true)
	o := &domain.DashboardOverview{Source: "remote", RecentResults: []domain.DashboardJob{{ID: job.ID, Title: job.Title, OverallStatus: job.OverallStatus}}}
	d := NewDashboardView(&client.DashboardController{Overview: o}, "en-US")
	d.OpenResult, d.OpenDetail = func(string, string) {}, func(string) {}
	d.Navigate, d.CanNavigate = func(string) {}, func(string) bool { return true }
	dt := ui.NewTester(d.View, 1180, 800)
	assertDisabled(t, dt, "View result: "+job.Title, true)
	assertDisabled(t, dt, "New Job", true)
	d.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read", "jobs.result.read"}}
	dt.Frame()
	assertDisabled(t, dt, "View result: "+job.Title, false)
}

func TestRemoteNewJobPreventsPickReselectAndSubmit(t *testing.T) {
	controller := &client.SubmissionController{}
	v := &NewJobView{Client: controller, Remote: true, Locale: "en-US"}
	tt := ui.NewTester(v.View, 1180, 900)
	assertDisabled(t, tt, v.msg("addFiles"), true)
	assertDisabled(t, tt, v.msg("createJob"), true)
	controller.Media = &application.SelectedMedia{Name: "synthetic.wav", Kind: "audio"}
	controller.Form.Title = "Synthetic media"
	tt.Frame()
	assertDisabled(t, tt, v.msg("reselect"), true)
	assertDisabled(t, tt, v.msg("createJob"), true)
}
