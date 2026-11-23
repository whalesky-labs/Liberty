package application

import (
	"context"
	"strings"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type speakerWrites struct{ id, from, to string }

func (w *speakerWrites) RenameSpeaker(_ context.Context, id, from, to string) error {
	w.id, w.from, w.to = id, from, to
	return nil
}

func TestWorkbenchWritesAndExportsRemainBoundToTheirGrant(t *testing.T) {
	ctx := context.Background()
	speaker := "original"
	s := NewWindowScopes(resultFunc(func(_ context.Context, id string) (domain.Result, error) {
		return domain.Result{Job: domain.JobSummary{ID: id, Source: "local", DiarizationStatus: "completed"}, Speakers: []domain.Segment{{Speaker: &speaker, Text: "synthetic", EndMS: 1000}}}, nil
	}))
	writer := &speakerWrites{}
	s.EnableEditing(writer)
	g, err := s.OpenFromMain(ctx, Workbench, "local", scopedID)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.RenameSpeaker(ctx, "original", "replacement"); err != nil || writer.id != scopedID || writer.to != "replacement" {
		t.Fatal("bound rename failed", err)
	}
	child, err := g.OpenChild(AISummary)
	if err != nil {
		t.Fatal(err)
	}
	if child.RenameSpeaker(ctx, "original", "forbidden") != ErrWindowScope {
		t.Fatal("summary child obtained transcript mutation")
	}
	published := false
	rewards := 0
	s.EnableExportRewards(workflowFixture(func(_ context.Context, event, key string) (domain.PetProfile, error) {
		if !published || event != "export_completed" || key != scopedID {
			t.Fatal("unexpected export reward")
		}
		rewards++
		return domain.PetProfile{}, domain.ErrPet
	}))
	publish := func(data []byte) error {
		published = true
		if !strings.Contains(string(data), "original: synthetic") {
			t.Error("wrong export snapshot")
		}
		return nil
	}
	if child.ExportText(ctx, domain.ExportTranscript, domain.TextExportLabels{}, publish) != ErrWindowScope || published {
		t.Fatal("summary child exported")
	}
	if err := g.ExportText(ctx, domain.ExportTranscript, domain.TextExportLabels{}, func([]byte) error { return context.Canceled }); err != context.Canceled || rewards != 0 {
		t.Fatal("failed publication rewarded", err)
	}
	if err := g.ExportText(ctx, domain.ExportTranscript, domain.TextExportLabels{}, publish); err != nil || !published || rewards != 1 {
		t.Fatal("bound export failed", err)
	}
	_, err = s.OpenFromMain(ctx, Workbench, "local", scopedID+"1")
	if err != nil {
		t.Fatal(err)
	}
	published = false
	if g.ExportText(ctx, domain.ExportTranscript, domain.TextExportLabels{}, publish) != ErrWindowScope || published {
		t.Fatal("revoked grant exported")
	}
	if g.RenameSpeaker(ctx, "original", "forbidden") != ErrWindowScope || writer.to != "replacement" {
		t.Fatal("revoked grant renamed")
	}
}

type resultFunc func(context.Context, string) (domain.Result, error)

func (f resultFunc) Result(ctx context.Context, id string) (domain.Result, error) { return f(ctx, id) }

const scopedID = "job-1700000000000-0"

func TestWindowCapabilities(t *testing.T) {
	ctx := context.Background()
	s := NewWindowScopes(resultFunc(func(_ context.Context, id string) (domain.Result, error) {
		return domain.Result{Job: domain.JobSummary{ID: id, Source: "local"}}, nil
	}))
	main, err := s.OpenFromMain(ctx, Workbench, "local", scopedID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := main.Result(ctx, scopedID+"1"); err != ErrWindowScope {
		t.Fatal("cross-job read allowed")
	}
	if _, err := main.OpenChild(Workbench); err != ErrWindowScope {
		t.Fatal("workbench issued workbench")
	}
	child, err := main.OpenChild(AISummary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := child.OpenChild(MeetingNotes); err != ErrWindowScope {
		t.Fatal("nested child issuance allowed")
	}
	replacement, err := s.OpenFromMain(ctx, Workbench, "local", scopedID+"1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := main.Result(ctx, scopedID); err != ErrWindowScope {
		t.Fatal("old grant still valid")
	}
	if _, err := main.OpenChild(MeetingNotes); err != ErrWindowScope {
		t.Fatal("old parent issued child")
	}
	main.Close()
	if _, err := replacement.Result(ctx, scopedID+"1"); err != nil {
		t.Fatal("old close revoked replacement")
	}
	// Existing Rust grants are independent after issuance; child remains bound
	// to its original job until that child closes or its own role is replaced.
	if _, err := child.Result(ctx, scopedID); err != nil {
		t.Fatal("existing child changed scope")
	}
	child.Close()
	if _, err := child.Result(ctx, scopedID); err != ErrWindowScope {
		t.Fatal("closed child allowed")
	}
	for _, bad := range []struct {
		role       WindowRole
		source, id string
	}{
		{AISummary, "remote", "remote-1"}, {Workbench, "local", "../job"}, {Workbench, "remote", scopedID}, {WindowRole("model-editor"), "local", scopedID},
	} {
		if _, err := s.OpenFromMain(ctx, bad.role, bad.source, bad.id); err != ErrWindowScope {
			t.Fatalf("invalid grant: %+v %v", bad, err)
		}
	}
}

func TestScopeRevokedDuringRead(t *testing.T) {
	var grant *JobWindow
	s := NewWindowScopes(resultFunc(func(_ context.Context, id string) (domain.Result, error) {
		if grant != nil {
			grant.Close()
		}
		return domain.Result{Job: domain.JobSummary{ID: id, Source: "local"}}, nil
	}))
	var err error
	grant, err = s.OpenFromMain(context.Background(), Workbench, "local", scopedID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grant.Result(context.Background(), scopedID); err != ErrWindowScope {
		t.Fatal("revoked result delivered")
	}
}

type sourceWindowPorts struct {
	mode, writtenSource, writtenID string
	capabilityRead                 func()
}

func (p *sourceWindowPorts) Result(ctx context.Context, id string) (domain.Result, error) {
	return p.ResultForSource(ctx, p.mode, id)
}
func (p *sourceWindowPorts) ResultForSource(_ context.Context, source, id string) (domain.Result, error) {
	speaker := source + " speaker"
	return domain.Result{Job: domain.JobSummary{ID: id, Source: source, DiarizationStatus: "completed"}, Speakers: []domain.Segment{{Speaker: &speaker, Text: source + " transcript"}}}, nil
}
func (p *sourceWindowPorts) RenameSpeaker(context.Context, string, string, string) error {
	return ErrWindowScope
}
func (p *sourceWindowPorts) RenameSpeakerForSource(_ context.Context, source, id, _, _ string) error {
	p.writtenSource, p.writtenID = source, id
	return nil
}
func (p *sourceWindowPorts) RemoteCapabilities(context.Context) (domain.RemoteCapabilities, error) {
	if p.capabilityRead != nil {
		p.capabilityRead()
	}
	return domain.RemoteCapabilities{ServiceVersion: "synthetic", Operations: []string{"jobs.read", "jobs.result.read"}}, nil
}

func TestWindowCapabilitySnapshotIsSourceBoundAndRevocable(t *testing.T) {
	ctx := context.Background()
	ports := &sourceWindowPorts{mode: "remote"}
	scopes := NewWindowScopes(ports)
	g, err := scopes.OpenFromMain(ctx, Workbench, "remote", "remote-id")
	if err != nil {
		t.Fatal(err)
	}
	ports.mode = "local"
	capabilities, err := g.RemoteCapabilities(ctx)
	if err != nil || !capabilities.Supports("jobs.result.read") {
		t.Fatal("source-bound capability lookup failed", err)
	}
	ports.capabilityRead = g.Close
	if _, err := g.RemoteCapabilities(ctx); err != ErrWindowScope {
		t.Fatal("revoked capability reply delivered", err)
	}
	ports.capabilityRead = nil
	local, err := scopes.OpenFromMain(ctx, Workbench, "local", scopedID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.RemoteCapabilities(ctx); err != ErrWindowScope {
		t.Fatal("local workbench received remote capability port", err)
	}
}

func TestWindowRetainsSourceAcrossModeChangeAndIDCollision(t *testing.T) {
	ctx := context.Background()
	ports := &sourceWindowPorts{mode: "remote"}
	scopes := NewWindowScopes(ports)
	scopes.EnableEditing(ports)
	g, err := scopes.OpenFromMain(ctx, Workbench, "remote", scopedID)
	if err != nil {
		t.Fatal(err)
	}
	ports.mode = "local"
	r, err := g.Result(ctx, scopedID)
	if err != nil || r.Job.Source != "remote" || r.Speakers[0].Text != "remote transcript" {
		t.Fatal("mode change redirected bound read", err)
	}
	if err := g.RenameSpeaker(ctx, "remote speaker", "renamed"); err != nil || ports.writtenSource != "remote" || ports.writtenID != scopedID {
		t.Fatal("mode change redirected bound rename", err)
	}
	if err := g.ExportText(ctx, domain.ExportTranscript, domain.TextExportLabels{}, func(data []byte) error {
		if !strings.Contains(string(data), "remote transcript") || strings.Contains(string(data), "local transcript") {
			t.Fatal("export read wrong source")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.OpenChild(AISummary); err != ErrWindowScope {
		t.Fatal("remote grant obtained local AI port")
	}
	// An adapter without a source-aware writer must never fall back to its
	// local writer merely because the remote ID is a valid local ID.
	localWriter := &speakerWrites{}
	scopes.EnableEditing(localWriter)
	if err := g.RenameSpeaker(ctx, "remote speaker", "forbidden"); err != ErrWindowScope || localWriter.id != "" {
		t.Fatal("remote grant reached local writer", err)
	}
}
