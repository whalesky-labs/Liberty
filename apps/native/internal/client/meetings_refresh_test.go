package client

import (
	"context"
	"errors"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type revisionResults struct {
	revision uint64
	reads    int
	result   domain.Result
	err      error
}

func (s *revisionResults) ResultRevision(context.Context, string) (uint64, error) {
	return s.revision, s.err
}
func (s *revisionResults) Result(_ context.Context, id string) (domain.Result, error) {
	s.reads++
	r := s.result
	r.Job.ID = id
	return r, s.err
}

func TestResultPollingReusesUnchangedCompletedSnapshot(t *testing.T) {
	s := &revisionResults{revision: 1, result: domain.Result{Job: domain.JobSummary{OverallStatus: "completed"}, Transcript: []domain.Segment{{Text: "synthetic"}}}}
	callbacks := make(chan func(), 4)
	changes := 0
	c := NewResult(s, func(f func()) { callbacks <- f }, func() { changes++ })
	defer c.Close()
	c.Select("meeting")
	receive(t, callbacks)()
	snapshot, before := c.Result, changes
	for range 10 {
		c.Refresh()
		receive(t, callbacks)()
	}
	if s.reads != 1 || c.Result != snapshot || changes != before {
		t.Fatal("idle polling reread or invalidated the snapshot")
	}
	s.revision++
	c.Refresh()
	receive(t, callbacks)()
	if s.reads != 2 || c.Result != snapshot || changes != before {
		t.Fatal("unrelated commit invalidated transcript caches")
	}
	s.result.Transcript = []domain.Segment{{Text: "renamed speaker", Speaker: new("Speaker 1")}}
	s.revision++
	c.Refresh()
	receive(t, callbacks)()
	if c.Result == snapshot || c.Result.Transcript[0].Text != "renamed speaker" {
		t.Fatal("committed result was not published")
	}
	s.err = errors.New("revoked")
	c.Refresh()
	receive(t, callbacks)()
	if c.ResultError == nil {
		t.Fatal("revision probe bypassed revocation")
	}
	before = changes
	c.Refresh()
	receive(t, callbacks)()
	if c.ResultError == nil || changes != before+2 {
		t.Fatal("identical retry failure did not restore error feedback")
	}
	s.err = nil
	c.Refresh()
	receive(t, callbacks)()
	if c.ResultError != nil || c.Refreshing() {
		t.Fatal("retry did not recover")
	}
}

func TestResultPollingReadsActiveAndUnversionedServices(t *testing.T) {
	for _, scenario := range []struct {
		status   string
		revision uint64
	}{{"transcribing", 1}, {"completed", 0}} {
		s := &revisionResults{revision: scenario.revision, result: domain.Result{Job: domain.JobSummary{OverallStatus: scenario.status}}}
		callbacks := make(chan func(), 4)
		c := NewResult(s, func(f func()) { callbacks <- f }, func() {})
		c.Select("meeting")
		receive(t, callbacks)()
		s.result.Job.ProgressPercent = new(float64(50))
		c.Refresh()
		receive(t, callbacks)()
		if s.reads != 2 || c.Result.Job.ProgressPercent == nil {
			t.Fatal("live progress was skipped")
		}
		c.Close()
	}
}

func TestQueueBackgroundRefreshDoesNotRedrawIdenticalPage(t *testing.T) {
	s := &controlledService{queues: make(chan queuedCall, 2), results: make(chan queuedCall, 1)}
	callbacks := make(chan func(), 2)
	changes := 0
	c := New(s, func(f func()) { callbacks <- f }, func() { changes++ })
	defer c.Close()
	c.Refresh(domain.QueueQuery{Source: "local", Search: "same"})
	call := receive(t, s.queues)
	close(call.reply)
	receive(t, callbacks)()
	before := changes
	c.Poll()
	call = receive(t, s.queues)
	close(call.reply)
	receive(t, callbacks)()
	if changes != before || c.LoadingJobs {
		t.Fatal("identical background page caused a repaint")
	}
}
