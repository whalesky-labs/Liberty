package application

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type bindingTransport struct {
	RemoteTransport
	reads, capabilities, writes atomic.Int32
	onRead                      func(context.Context, RemoteConnection)
	onCapabilities              func(context.Context, RemoteConnection)
	onWrite                     func(context.Context, RemoteConnection)
}

func (port *bindingTransport) Read(ctx context.Context, connection RemoteConnection, id string, result bool) (domain.Result, error) {
	port.reads.Add(1)
	if port.onRead != nil {
		port.onRead(ctx, connection)
	}
	speaker := "original"
	return domain.Result{Job: domain.JobSummary{ID: id, Source: "remote", DiarizationStatus: "completed"}, Speakers: []domain.Segment{{Speaker: &speaker, Text: "Synthetic transcript"}}}, nil
}
func (port *bindingTransport) Capabilities(ctx context.Context, connection RemoteConnection) (domain.RemoteCapabilities, error) {
	port.capabilities.Add(1)
	if port.onCapabilities != nil {
		port.onCapabilities(ctx, connection)
	}
	return domain.RemoteCapabilities{Operations: []string{"jobs.result.read", "transcript.speakers.rename"}}, nil
}
func (port *bindingTransport) Rename(ctx context.Context, connection RemoteConnection, id, from, to string) (domain.Result, error) {
	port.writes.Add(1)
	if port.onWrite != nil {
		port.onWrite(ctx, connection)
	}
	return domain.Result{}, nil
}

type bindingFixture struct {
	resources  *Resources
	repository *remoteSettingsRepository
	vault      *remoteVault
	port       *bindingTransport
	mode       *ModeMeetings
	scopes     *WindowScopes
}

func newBindingFixture(t *testing.T) *bindingFixture {
	t.Helper()
	repository := &remoteSettingsRepository{record: SettingsRecord{Snapshot: domain.DefaultSettings(), Reference: "synthetic-reference-a"}}
	repository.record.Snapshot.BackendURL = "https://synthetic.invalid/a"
	repository.record.Snapshot.ProcessingMode = "remote"
	vault := &remoteVault{value: domain.NewSecret("synthetic-credential-a")}
	resources := NewResources(repository, vault)
	t.Cleanup(resources.Close)
	port := &bindingTransport{}
	service := NewRemoteMeetings(resources, port)
	t.Cleanup(service.Close)
	local := &localModePort{}
	mode := NewModeMeetings(resources, local, service, local, local, local)
	scopes := NewWindowScopes(mode)
	scopes.EnableEditing(mode)
	return &bindingFixture{resources, repository, vault, port, mode, scopes}
}

func (fixture *bindingFixture) open(t *testing.T) *JobWindow {
	t.Helper()
	grant, err := fixture.scopes.OpenFromMain(context.Background(), Workbench, "remote", "same-id")
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (fixture *bindingFixture) change(kind string) {
	fixture.resources.mu.Lock()
	defer fixture.resources.mu.Unlock()
	switch kind {
	case "url":
		fixture.repository.record.Snapshot.BackendURL = "https://synthetic.invalid/b"
	case "token":
		fixture.vault.value = domain.NewSecret("synthetic-credential-b")
	case "reference":
		fixture.repository.record.Reference = "synthetic-reference-b"
	case "clear":
		fixture.repository.record.Reference = ""
	case "missing-secret":
		fixture.vault.value = domain.Secret{}
	case "legacy":
		fixture.repository.record.LegacyPresent = true
	case "mode":
		fixture.repository.record.Snapshot.ProcessingMode = "local"
		fixture.repository.record.Snapshot.Revision++
	}
}

func boundWindowOperation(ctx context.Context, grant *JobWindow, operation string, published *atomic.Int32) error {
	switch operation {
	case "result":
		_, err := grant.Result(ctx, "same-id")
		return err
	case "capabilities":
		_, err := grant.RemoteCapabilities(ctx)
		return err
	case "rename":
		return grant.RenameSpeaker(ctx, "original", "replacement")
	case "text":
		return grant.ExportText(ctx, domain.ExportTranscript, domain.TextExportLabels{}, func([]byte) error { published.Add(1); return nil })
	}
	return errors.New("unknown test operation")
}

func receiveWindowError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("window operation did not finish")
		return nil
	}
}

func TestRemoteWindowConnectionChangesRejectBeforeDelegation(t *testing.T) {
	for _, change := range []string{"url", "token", "reference", "clear", "missing-secret", "legacy"} {
		for _, operation := range []string{"result", "capabilities", "rename", "text"} {
			t.Run(change+"/"+operation, func(t *testing.T) {
				fixture := newBindingFixture(t)
				grant := fixture.open(t)
				fixture.port.reads.Store(0)
				fixture.change(change)
				var published atomic.Int32
				err := boundWindowOperation(context.Background(), grant, operation, &published)
				if !errors.Is(err, ErrWindowScope) || fixture.port.reads.Load() != 0 || fixture.port.capabilities.Load() != 0 || fixture.port.writes.Load() != 0 || published.Load() != 0 {
					t.Fatal("stale connection delegated an operation", err)
				}
				encoded, err := json.Marshal(grant)
				if err != nil || string(encoded) != "{}" {
					t.Fatal("private connection escaped the window port")
				}
			})
		}
	}
}

func TestRemoteWindowModeChangePreservesConnection(t *testing.T) {
	fixture := newBindingFixture(t)
	grant := fixture.open(t)
	fixture.change("mode")
	for _, operation := range []string{"result", "capabilities", "rename", "text"} {
		var published atomic.Int32
		if err := boundWindowOperation(context.Background(), grant, operation, &published); err != nil {
			t.Fatal("processing-mode change revoked the original remote connection", operation, err)
		}
	}
}

func TestRemoteWindowObservationRechecksConnectionAndCancellation(t *testing.T) {
	for _, change := range []string{"url", "token", "reference", "cancel"} {
		for _, operation := range []string{"open", "result", "capabilities", "rename", "text"} {
			t.Run(change+"/"+operation, func(t *testing.T) {
				fixture := newBindingFixture(t)
				grant := fixture.open(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				onRead := func(context.Context, RemoteConnection) {
					if change == "cancel" {
						cancel()
					} else {
						fixture.change(change)
					}
				}
				fixture.port.onRead, fixture.port.onCapabilities = onRead, onRead
				var published atomic.Int32
				var err error
				if operation == "open" {
					_, err = fixture.scopes.OpenFromMain(ctx, Workbench, "remote", "same-id")
					fixture.scopes.mu.Lock()
					unchanged := fixture.scopes.current[Workbench] == grant
					fixture.scopes.mu.Unlock()
					if !unchanged {
						t.Error("invalid issuance replaced the existing grant")
					}
				} else {
					err = boundWindowOperation(ctx, grant, operation, &published)
				}
				want := ErrWindowScope
				if change == "cancel" {
					want = context.Canceled
				}
				if !errors.Is(err, want) || fixture.port.writes.Load() != 0 || published.Load() != 0 {
					t.Fatal("invalid observation delivered data or a mutation", err)
				}
			})
		}
	}
}

func TestRemoteWindowRevocationDuringReadPreventsDeliveryAndWrite(t *testing.T) {
	for _, revoke := range []string{"close", "replace"} {
		for _, operation := range []string{"result", "capabilities", "rename", "text"} {
			t.Run(revoke+"/"+operation, func(t *testing.T) {
				fixture := newBindingFixture(t)
				grant := fixture.open(t)
				entered, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				onRead := func(context.Context, RemoteConnection) { close(entered); <-release }
				fixture.port.onRead, fixture.port.onCapabilities = onRead, onRead
				var published atomic.Int32
				returned := make(chan error, 1)
				go func() { returned <- boundWindowOperation(context.Background(), grant, operation, &published) }()
				waitPermissionSignal(t, entered)
				revoked := make(chan error, 1)
				go func() {
					if revoke == "close" {
						grant.Close()
						revoked <- nil
					} else {
						_, err := fixture.scopes.OpenFromMain(context.Background(), Workbench, "local", scopedID)
						revoked <- err
					}
				}()
				if err := receiveWindowError(t, revoked); err != nil {
					t.Fatal("pre-mutation read prevented revocation", err)
				}
				unblock()
				if err := receiveWindowError(t, returned); !errors.Is(err, ErrWindowScope) || fixture.port.writes.Load() != 0 || published.Load() != 0 {
					t.Fatal("revoked read escaped or delegated a write", err)
				}
			})
		}
	}
}

func TestRemoteWindowAcceptedRenamePinsConnectionAndOrdersRevocation(t *testing.T) {
	for _, revoke := range []string{"close", "replace"} {
		t.Run(revoke, func(t *testing.T) {
			fixture := newBindingFixture(t)
			parent, err := fixture.scopes.OpenFromMain(context.Background(), Workbench, "local", scopedID)
			if err != nil {
				t.Fatal(err)
			}
			child, err := parent.OpenChild(MeetingNotes)
			if err != nil {
				t.Fatal(err)
			}
			grant := fixture.open(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			var readConnection RemoteConnection
			fixture.port.onRead = func(_ context.Context, connection RemoteConnection) { readConnection = connection }
			fixture.port.onWrite = func(_ context.Context, connection RemoteConnection) {
				if connection != readConnection {
					t.Error("rename changed the connection used for its fresh read")
				}
				close(entered)
				<-release
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			returned := make(chan error, 1)
			go func() { returned <- grant.RenameSpeaker(ctx, "original", "replacement") }()
			waitPermissionSignal(t, entered)
			cancel()
			fixture.change("url")
			fixture.change("token")
			fixture.change("reference")
			childClosed := make(chan struct{})
			go func() { child.Close(); close(childClosed) }()
			waitPermissionSignal(t, childClosed)
			revoking, revoked := make(chan struct{}), make(chan error, 1)
			go func() {
				close(revoking)
				if revoke == "close" {
					grant.Close()
					revoked <- nil
				} else {
					_, err := fixture.scopes.OpenFromMain(context.Background(), Workbench, "local", scopedID)
					revoked <- err
				}
			}()
			waitPermissionSignal(t, revoking)
			select {
			case <-revoked:
				t.Error("revocation overtook an accepted remote rename")
			case <-time.After(30 * time.Millisecond):
			}
			unblock()
			if err := receiveWindowError(t, returned); err != nil {
				t.Fatal("accepted success was downgraded after cancellation or reconfiguration", err)
			}
			if err := receiveWindowError(t, revoked); err != nil {
				t.Fatal(err)
			}
			if err := grant.RenameSpeaker(context.Background(), "original", "forbidden"); !errors.Is(err, ErrWindowScope) || fixture.port.writes.Load() != 1 {
				t.Fatal("revoked grant accepted another write", err)
			}
		})
	}
}

func TestRemoteWindowCanceledMutationWaiterDoesNotWaitForAcceptedWrite(t *testing.T) {
	fixture := newBindingFixture(t)
	grant := fixture.open(t)
	entered, release, secondRead := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	fixture.port.onWrite = func(context.Context, RemoteConnection) { close(entered); <-release }
	var reads atomic.Int32
	fixture.port.onRead = func(context.Context, RemoteConnection) {
		if reads.Add(1) == 2 {
			close(secondRead)
		}
	}
	first := make(chan error, 1)
	go func() { first <- grant.RenameSpeaker(context.Background(), "original", "replacement") }()
	waitPermissionSignal(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	second := make(chan error, 1)
	go func() { second <- grant.RenameSpeaker(ctx, "original", "forbidden") }()
	waitPermissionSignal(t, secondRead)
	cancel()
	if err := receiveWindowError(t, second); !errors.Is(err, context.Canceled) || fixture.port.writes.Load() != 1 {
		t.Fatal("canceled pending mutation reached the writer or blocked behind it", err)
	}
	unblock()
	if err := receiveWindowError(t, first); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteWindowBoundWriterRechecksConnectionAfterFreshRead(t *testing.T) {
	for _, change := range []string{"url", "token", "reference"} {
		t.Run(change, func(t *testing.T) {
			fixture := newBindingFixture(t)
			grant := fixture.open(t)
			if _, err := grant.Result(context.Background(), "same-id"); err != nil {
				t.Fatal(err)
			}
			fixture.change(change)
			err := fixture.mode.renameSpeakerBound(context.Background(), grant.remote, "same-id", "original", "forbidden")
			if !errors.Is(err, ErrWindowScope) || fixture.port.writes.Load() != 0 {
				t.Fatal("writer reused a connection invalidated after its fresh read", err)
			}
		})
	}
}
