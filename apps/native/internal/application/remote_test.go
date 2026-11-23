package application

import (
	"context"
	"errors"
	"liberty.local/liberty/internal/domain"
	"testing"
	"time"
)

type remoteSettingsRepository struct {
	ResourceRepository
	record SettingsRecord
}

func (r *remoteSettingsRepository) Settings(context.Context) (SettingsRecord, error) {
	return r.record, nil
}

type remoteVault struct {
	value domain.Secret
	reads int
}

func (v *remoteVault) Get(context.Context, string) (domain.Secret, error) {
	v.reads++
	return v.value, nil
}
func (*remoteVault) Set(context.Context, string, domain.Secret) error { return nil }
func (*remoteVault) Delete(context.Context, string) error             { return nil }

type remotePort struct {
	calls       []string
	token       domain.Secret
	readResults []bool
}

func (p *remotePort) record(c RemoteConnection, op string) {
	p.calls = append(p.calls, op)
	p.token = c.Token
}
func (p *remotePort) Capabilities(_ context.Context, c RemoteConnection) (domain.RemoteCapabilities, error) {
	p.record(c, "caps")
	return domain.RemoteCapabilities{}, nil
}
func (p *remotePort) List(_ context.Context, c RemoteConnection) ([]domain.Result, error) {
	p.record(c, "list")
	return []domain.Result{{Job: domain.JobSummary{ID: "same", Source: "remote"}}}, nil
}
func (p *remotePort) Read(_ context.Context, c RemoteConnection, id string, result bool) (domain.Result, error) {
	p.record(c, "read")
	p.readResults = append(p.readResults, result)
	return domain.Result{Job: domain.JobSummary{ID: id, Source: "remote"}}, nil
}

func TestModeDetailsUseJobReadWithoutResultCapability(t *testing.T) {
	ctx := context.Background()
	repository := &remoteSettingsRepository{record: SettingsRecord{Snapshot: domain.DefaultSettings(), Reference: "synthetic-ref"}}
	vault := &remoteVault{value: domain.NewSecret("synthetic-token")}
	resources := NewResources(repository, vault)
	defer resources.Close()
	transport := &remotePort{}
	remote := NewRemoteMeetings(resources, transport)
	defer remote.Close()
	local := &localModePort{}
	mode := NewModeMeetings(resources, local, remote, local, local, local)
	// Explicit source is authoritative even while the persisted main mode is local.
	if _, err := mode.JobForSource(ctx, "remote", "same"); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.ResultForSource(ctx, "remote", "same"); err != nil {
		t.Fatal(err)
	}
	if len(transport.readResults) != 2 || transport.readResults[0] || !transport.readResults[1] {
		t.Fatal("detail and result endpoint scopes collapsed", transport.readResults)
	}
	reads := vault.reads
	if _, err := mode.JobForSource(ctx, "local", "same"); err != nil || vault.reads != reads {
		t.Fatal("local detail consulted remote credentials", err)
	}
	if _, err := mode.JobForSource(ctx, "invalid", "same"); err != ErrWindowScope {
		t.Fatal("unscoped detail accepted", err)
	}
}
func (p *remotePort) Retry(_ context.Context, c RemoteConnection, id string) (domain.Result, error) {
	p.record(c, "retry")
	return domain.Result{}, nil
}
func (p *remotePort) Delete(_ context.Context, c RemoteConnection, id string) error {
	p.record(c, "delete")
	return nil
}
func (p *remotePort) Rename(_ context.Context, c RemoteConnection, id, from, to string) (domain.Result, error) {
	p.record(c, "rename")
	return domain.Result{}, nil
}

type localModePort struct{ writes int }

func (*localModePort) ListJobs(context.Context) ([]domain.JobSummary, error) {
	return []domain.JobSummary{{ID: "same", Source: "local"}}, nil
}
func (*localModePort) ReadResult(_ context.Context, id string) (domain.Result, error) {
	return domain.Result{Job: domain.JobSummary{ID: id, Source: "local"}}, nil
}
func (p *localModePort) Retry(context.Context, string) error  { p.writes++; return nil }
func (p *localModePort) Delete(context.Context, string) error { p.writes++; return nil }
func (p *localModePort) RenameSpeaker(context.Context, string, string, string) error {
	p.writes++
	return nil
}

func TestRemoteModeSwitchPreservesBoundSourcesAndNeverFallsBack(t *testing.T) {
	ctx := context.Background()
	repository := &remoteSettingsRepository{record: SettingsRecord{Snapshot: domain.DefaultSettings(), Reference: "synthetic-ref"}}
	vault := &remoteVault{value: domain.NewSecret("synthetic-token")}
	resources := NewResources(repository, vault)
	transport := &remotePort{}
	remote := NewRemoteMeetings(resources, transport)
	local := &localModePort{}
	m := NewModeMeetings(resources, local, remote, local, local, local)
	jobs, err := m.ListJobs(ctx)
	if err != nil || jobs[0].Source != "local" {
		t.Fatal(jobs, err)
	}
	if vault.reads != 0 {
		t.Fatal("local mode polled OS credentials")
	}
	repository.record.Snapshot.ProcessingMode = "remote"
	jobs, err = m.ListJobs(ctx)
	if err != nil || jobs[0].Source != "remote" {
		t.Fatal(jobs, err)
	}
	page, err := m.Queue(ctx, domain.QueueQuery{Source: "local"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Source != "remote" {
		t.Fatal("stale view source hid remote list", page, err)
	}
	bound, err := m.ResultForSource(ctx, "local", "same")
	if err != nil || bound.Job.Source != "local" {
		t.Fatal("mode redirected bound local result")
	}
	if m.RetryForSource(ctx, "local", "same") != nil || m.DeleteForSource(ctx, "remote", "same") != nil || m.RenameSpeakerForSource(ctx, "remote", "same", "A", "B") != nil || local.writes != 1 {
		t.Fatal("crossed mutation source")
	}
	if transport.token.Reveal() != "synthetic-token" {
		t.Fatal("credential not forwarded privately")
	}
	repository.record.Reference = ""
	if _, err = m.ListJobs(ctx); !errors.Is(err, domain.ErrRemote) {
		t.Fatal("remote fell back to local", err)
	}
	if _, err = m.ResultForSource(ctx, "invented", "same"); !errors.Is(err, ErrWindowScope) {
		t.Fatal(err)
	}
}
func TestRemoteRefusesLegacyCredentialsAndClosedResources(t *testing.T) {
	repository := &remoteSettingsRepository{record: SettingsRecord{LegacyPresent: true, Reference: "old"}}
	vault := &remoteVault{value: domain.NewSecret("synthetic-token")}
	resources := NewResources(repository, vault)
	remote := NewRemoteMeetings(resources, &remotePort{})
	if _, err := remote.Capabilities(context.Background()); !errors.Is(err, domain.ErrCredentialMigration) || vault.reads != 0 {
		t.Fatal(err)
	}
	resources.Close()
	if _, err := remote.ListJobs(context.Background()); !errors.Is(err, ErrWindowScope) {
		t.Fatal(err)
	}
}

type waitingRemote struct {
	RemoteTransport
	started, cancelled, release chan struct{}
}

func (p waitingRemote) Capabilities(ctx context.Context, _ RemoteConnection) (domain.RemoteCapabilities, error) {
	close(p.started)
	<-ctx.Done()
	close(p.cancelled)
	<-p.release
	return domain.RemoteCapabilities{}, ctx.Err()
}
func TestRemoteShutdownCancelsAndJoinsAcceptedRequests(t *testing.T) {
	repository := &remoteSettingsRepository{record: SettingsRecord{Reference: "synthetic-ref"}}
	resources := NewResources(repository, &remoteVault{value: domain.NewSecret("synthetic-token")})
	port := waitingRemote{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	remote := NewRemoteMeetings(resources, port)
	returned := make(chan error, 1)
	go func() { _, err := remote.Capabilities(context.Background()); returned <- err }()
	select {
	case <-port.started:
	case <-time.After(time.Second):
		t.Fatal("request not started")
	}
	closed := make(chan struct{})
	go func() { remote.Close(); close(closed) }()
	select {
	case <-port.cancelled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel")
	}
	select {
	case <-closed:
		t.Fatal("shutdown returned before accepted request ended")
	default:
	}
	close(port.release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join")
	}
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := remote.Capabilities(context.Background()); !errors.Is(err, ErrWindowScope) {
		t.Fatal("closed service accepted request", err)
	}
}
