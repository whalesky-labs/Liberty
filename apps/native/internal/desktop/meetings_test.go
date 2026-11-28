package desktop

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type viewMeetings struct{ jobs []domain.JobSummary }

func (s viewMeetings) Queue(_ context.Context, q domain.QueueQuery) (domain.JobPage, error) {
	return domain.QueryJobs(s.jobs, q), nil
}
func (s viewMeetings) Result(_ context.Context, id string) (domain.Result, error) {
	for _, job := range s.jobs {
		if job.ID == id {
			return domain.Result{Job: job, Transcript: []domain.Segment{{ID: "segment-1", Text: "Synthetic transcript", StartMS: 1000, EndMS: 5000}}}, nil
		}
	}
	return domain.Result{}, fmt.Errorf("missing fixture")
}

func flushView(t *testing.T, callbacks <-chan func(), tt *ui.Tester) {
	t.Helper()
	select {
	case f := <-callbacks:
		f()
		tt.Frame()
	case <-time.After(5 * time.Second):
		t.Fatal("no asynchronous view update")
	}
}

func TestQueueInteractionAndBilingualLabels(t *testing.T) {
	s := viewMeetings{}
	for i := 0; i < 12; i++ {
		s.jobs = append(s.jobs, domain.JobSummary{ID: fmt.Sprintf("job-1700000000000-%d", i), Title: fmt.Sprintf("Meeting %02d", i), Source: "local", CreatedAt: fmt.Sprintf("2026-11-%02dT00:00:00Z", i+1), OverallStatus: "completed", SourceFiles: []domain.SourceFile{{Name: fmt.Sprintf("audio-%02d.wav", i)}}})
	}
	callbacks := make(chan func(), 64)
	c := client.New(s, func(f func()) { callbacks <- f }, func() {})
	defer c.Close()
	v := NewMeetings(c, "en-US")
	var opened string
	v.OpenResult = func(id, locale string) { opened = id + ":" + locale }
	tt := ui.NewTester(v.View, 1180, 800)
	v.refresh()
	flushView(t, callbacks, tt)
	if !tt.HasText("Showing 12 / 12") || !tt.HasText("1-10 of 12") {
		t.Fatal("queue projection missing", tt.Texts())
	}
	if err := tt.Click("Meeting 11"); err != nil {
		t.Fatal(err)
	}
	flushView(t, callbacks, tt)
	if c.Result == nil || len(c.Result.Transcript) != 1 {
		t.Fatal("selected detail snapshot absent")
	}
	if tt.HasText("Synthetic transcript") {
		t.Fatal("queue must keep the original table layout; transcript belongs in the result window")
	}
	if err := tt.Click("View result: Meeting 11"); err != nil {
		t.Fatal(err)
	}
	if opened != "job-1700000000000-11:en-US" {
		t.Fatal("wrong result scope", opened)
	}
	if err := tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	flushView(t, callbacks, tt)
	if !tt.HasText("11-12 of 12") || c.SelectedID != "" {
		t.Fatal("page change kept old selection")
	}
	if err := tt.Click("Search jobs or file names"); err != nil {
		t.Fatal(err)
	}
	tt.Type("audio-03")
	flushView(t, callbacks, tt)
	if !tt.HasText("Meeting 03") || c.Page.Total != 1 || c.Page.Page != 1 {
		t.Fatal("filename search did not reset page")
	}
	v.Locale = "zh-CN"
	tt.Frame()
	if !tt.HasText(presentation.Message("zh-CN", "jobs.queueTitle")) || !tt.HasText(presentation.Format("zh-CN", "jobs.visibleCount", map[string]any{"visible": 1, "total": 12})) {
		t.Fatal("language switch did not update view")
	}
	for _, name := range []string{presentation.Message("zh-CN", "jobs.searchPlaceholder"), presentation.Message("zh-CN", "jobs.filterLabel"), presentation.Message("zh-CN", "jobs.queueTitle")} {
		if _, ok := tt.Access(name); !ok {
			t.Errorf("missing accessible name: %s", name)
		}
	}
}

func TestResultUsesOnlyValidSpeakerProjection(t *testing.T) {
	label := "speaker-0"
	c := &client.ResultController{Result: &domain.Result{
		Job:        domain.JobSummary{ID: "job-1700000000000-0", Title: "Synthetic meeting", OverallStatus: "completed", DiarizationStatus: "legacy_unverified"},
		Transcript: []domain.Segment{{ID: "1", StartMS: 0, EndMS: 1000, Text: "Original transcript"}},
		Speakers:   []domain.Segment{{ID: "1", StartMS: 0, EndMS: 1000, Text: "Mismatching transcript", Speaker: &label}},
	}}
	v := NewResultView(c, "en-US")
	tt := ui.NewTester(v.View, 900, 600)
	if !tt.HasText("Original transcript") || tt.HasText("Mismatching transcript") || tt.HasText(label) {
		t.Fatal("unverified mismatch shown")
	}
	// ResultController replaces snapshots and never mutates published slices.
	next := *c.Result
	next.Speakers = slices.Clone(next.Speakers)
	next.Speakers[0].Text = "Original transcript"
	c.Result = &next
	tt.Frame()
	if !tt.HasText(label) {
		t.Fatal("compatible legacy label lost")
	}
	if len(c.Result.PrimarySegments()) != 1 || c.Result.PrimarySegments()[0].Speaker != nil {
		t.Fatal("display upgraded legacy authority")
	}
}
func TestLegacyResultLinksOpenCompletedQueue(t *testing.T) {
	for _, path := range []string{"/results", "/results?old=1", "/jobs/meeting-a/workbench", "/jobs/meeting-a/workbench?from=history"} {
		if queueRedirect(path) != "/jobs?status=completed" {
			t.Errorf("missing completed queue redirect for %s", path)
		}
	}
	for _, path := range []string{"/jobs", "/jobs/new", "/jobs/meeting-a", "/job-workbench", "/jobs/a/b/workbench"} {
		if queueRedirect(path) != path {
			t.Errorf("unexpected queue redirect for %s", path)
		}
	}
}
