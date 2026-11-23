package application

import (
	"context"
	"errors"
	"testing"

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
