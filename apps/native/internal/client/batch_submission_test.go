package client

import (
	"context"
	"testing"

	"liberty.local/liberty/internal/application"
)

func TestBatchSubmissionContinuesAfterFailureAndRetrySkipsCommittedItems(t *testing.T) {
	f := newNewJobFixture(t)
	f.load(t, application.JobDefaults{})
	c := f.controller
	c.Batch = []BatchItem{
		{Media: application.SelectedMedia{Token: "first"}, Title: " First "},
		{Media: application.SelectedMedia{Token: "second"}, Title: "Second"},
		{Media: application.SelectedMedia{Token: "third"}, Title: "Third"},
	}
	c.SubmitWithDefaults("minutes")
	f.load(t, application.JobDefaults{Hotwords: "term"})
	c.SubmitWithDefaults("minutes")
	for i, token := range []string{"first", "second", "third"} {
		call := receiveNewJob(t, f.service.creates)
		if call.form.Selection != token || call.form.Hotwords != "term" || call.form.SummaryTemplate != "minutes" {
			t.Fatal("batch lost per-file selection or shared settings")
		}
		if i == 1 {
			call.reply <- newJobCreateReply{err: application.ErrJobInput}
		} else {
			call.reply <- newJobCreateReply{id: "job-" + token}
		}
		f.dispatch(t)
	}
	f.dispatch(t)
	if c.Busy || c.PendingCount() != 1 || c.Batch[1].Error == nil || len(f.created) != 0 || len(f.service.creates) != 0 {
		t.Fatal("partial admission state or navigation is incorrect")
	}
	c.Submit()
	call := receiveNewJob(t, f.service.creates)
	if call.form.Selection != "second" {
		t.Fatal("retry recreated a durable task")
	}
	call.reply <- newJobCreateReply{id: "job-second"}
	f.dispatch(t)
	f.dispatch(t)
	if c.PendingCount() != 0 || c.CanSubmit() || c.Batch[1].Error != nil || len(f.picker.released) != 3 {
		t.Fatal("batch success was not retained")
	}
	c.Submit()
	if len(f.service.creates) != 0 {
		t.Fatal("completed batch replayed")
	}
}

type batchPickerFixture struct {
	newJobPicker
	selections []application.SelectedMedia
}

func (p *batchPickerFixture) PickMany(context.Context) ([]application.SelectedMedia, error) {
	return p.selections, nil
}

func TestBatchPickerCancellationAndRemovalPreserveOtherGrants(t *testing.T) {
	f := newNewJobFixture(t)
	f.load(t, application.JobDefaults{})
	p := &batchPickerFixture{selections: []application.SelectedMedia{{Token: "a", Name: "a.wav"}, {Token: "b", Name: "b.mp3"}}}
	c := f.controller
	c.picker = p
	c.Pick()
	f.dispatch(t)
	if c.SelectionCount() != 2 || c.Batch[1].Title != "b" {
		t.Fatal("multiple selection was not loaded")
	}
	p.selections = nil
	c.Pick()
	f.dispatch(t)
	if c.SelectionCount() != 2 {
		t.Fatal("cancelled dialog discarded existing selection")
	}
	c.RemoveBatchItem(0)
	if c.SelectionCount() != 1 || c.Media.Token != "b" || c.Batch[0].Media.Token != "b" || !c.CanSubmit() {
		t.Fatal("removing one file invalidated another")
	}
	c.Clear()
	if c.SelectionCount() != 0 || c.CanSubmit() {
		t.Fatal("clear retained batch state")
	}
}
