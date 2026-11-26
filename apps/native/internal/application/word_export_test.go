package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type minutesFixture struct {
	result   domain.Result
	saveErr  error
	existing bool
	calls    int
}

func (f *minutesFixture) Result(context.Context, string) (domain.Result, error)   { return f.result, nil }
func (f *minutesFixture) Members(context.Context) ([]domain.MeetingMember, error) { return nil, nil }
func (f *minutesFixture) BackfillMinutes(_ context.Context, _ string, run string, p domain.MinutesPayload) (bool, error) {
	f.calls++
	if f.saveErr != nil {
		return false, f.saveErr
	}
	if f.existing {
		p.MeetingInfo = domain.MinutesInfo{MeetingName: "Concurrent user title"}
		p.MeetingInfoSource = "user"
		p.GlobalSummary = []string{"Concurrent authoritative summary"}
	}
	f.result.SummaryRuns[0].MinutesPayload = &p
	return !f.existing, nil
}
func TestWordBackfillAndBoundPublication(t *testing.T) {
	ctx := context.Background()
	run := "run"
	for _, mode := range []string{"saved", "concurrent", "failed"} {
		t.Run(mode, func(t *testing.T) {
			f := &minutesFixture{result: domain.Result{Job: domain.JobSummary{ID: scopedID, Source: "local", Title: "Original", ActiveSummaryRunID: &run}, SummaryRuns: []domain.SummaryRun{{ID: run, Status: "completed", Result: &domain.SummaryResult{Overview: "Unstructured preserved summary"}}}}, existing: mode == "concurrent"}
			if mode == "failed" {
				f.saveErr = errors.New("synthetic")
			}
			exporter := NewWordExport(f, f)
			s := NewWindowScopes(f)
			s.EnableWordExport(exporter)
			g, err := s.OpenFromMain(ctx, Workbench, "local", scopedID)
			if err != nil {
				t.Fatal(err)
			}
			published := false
			rewards := 0
			s.EnableExportRewards(workflowFixture(func(_ context.Context, event, key string) (domain.PetProfile, error) {
				if !published || event != "export_completed" || key != scopedID {
					t.Fatal("unexpected Word reward")
				}
				rewards++
				return domain.PetProfile{}, domain.ErrPet
			}))
			publish := func(d domain.MinutesDocument) error {
				published = true
				if mode == "concurrent" && d.Info.MeetingName != "Concurrent user title" {
					t.Fatal("concurrent payload ignored")
				}
				if len(d.SpeechBlocks) == 0 {
					t.Fatal("unstructured fallback lost")
				}
				return nil
			}
			if err := g.ExportWord(ctx, "", publish); err != nil || !published || f.calls != 1 {
				t.Fatal("Word prepare failed", err)
			}
			child, err := g.OpenChild(MeetingNotes)
			if err != nil {
				t.Fatal(err)
			}
			published = false
			if child.ExportWord(ctx, "", publish) != ErrWindowScope || published {
				t.Fatal("reader child exported")
			}
			g.Close()
			if g.ExportWord(ctx, "", publish) != ErrWindowScope || published || rewards != 1 {
				t.Fatal("revoked workbench exported")
			}
		})
	}
}

func TestWordPublicationLeavesOtherWindowGrantsResponsive(t *testing.T) {
	ctx := context.Background()
	run := "run"
	f := &minutesFixture{result: domain.Result{Job: domain.JobSummary{ID: scopedID, Source: "local", ActiveSummaryRunID: &run}, SummaryRuns: []domain.SummaryRun{{ID: run, Status: "completed", Result: &domain.SummaryResult{Overview: "Synthetic summary"}}}}}
	scopes := NewWindowScopes(f)
	scopes.EnableWordExport(NewWordExport(f, f))
	grant, err := scopes.OpenFromMain(ctx, Workbench, "local", scopedID)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	go func() {
		done <- grant.ExportWord(ctx, "", func(domain.MinutesDocument) error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("publisher did not start")
	}
	childDone := make(chan error, 1)
	go func() {
		child, err := grant.OpenChild(MeetingNotes)
		if err == nil {
			_, err = child.Result(ctx, scopedID)
			child.Close()
		}
		childDone <- err
	}()
	select {
	case err := <-childDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Word publication blocked another window")
	}
	closed := make(chan struct{})
	go func() { grant.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("accepted export was revoked before publication finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	released = true
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("grant did not close after publication")
	}
	if err := grant.ExportWord(ctx, "", func(domain.MinutesDocument) error { t.Error("revoked publisher called"); return nil }); err != ErrWindowScope {
		t.Fatal(err)
	}
}
