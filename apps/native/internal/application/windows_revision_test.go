package application

import (
	"context"
	"testing"

	"liberty.local/liberty/internal/domain"
)

type revisionWindowReader struct{ started, release chan struct{} }

func (*revisionWindowReader) Result(_ context.Context, id string) (domain.Result, error) {
	return domain.Result{Job: domain.JobSummary{ID: id, Source: "local"}}, nil
}
func (r *revisionWindowReader) ResultRevision(context.Context, string) (uint64, error) {
	close(r.started)
	<-r.release
	return 1, nil
}

func TestRevisionProbeCannotOutliveWindowGrant(t *testing.T) {
	reader := &revisionWindowReader{make(chan struct{}), make(chan struct{})}
	scopes := NewWindowScopes(reader)
	grant, err := scopes.OpenFromMain(context.Background(), Workbench, "local", scopedID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grant.ResultRevision(context.Background(), scopedID+"other"); err != ErrWindowScope {
		t.Fatal("cross-job revision allowed")
	}
	done := make(chan error, 1)
	go func() { _, err := grant.ResultRevision(context.Background(), scopedID); done <- err }()
	<-reader.started
	grant.Close()
	close(reader.release)
	if err := <-done; err != ErrWindowScope {
		t.Fatal("revoked revision probe was published", err)
	}
}
