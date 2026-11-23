//go:build remote_acceptance

package remote

import (
	"context"
	"os"
	"testing"
	"time"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/credentials"
)

func TestReadOnlyServiceAcceptance(t *testing.T) {
	address := os.Getenv("LIBERTY_REMOTE_ACCEPTANCE_URL")
	reference := os.Getenv("LIBERTY_REMOTE_ACCEPTANCE_CREDENTIAL_REF")
	jobID, err := domain.RemoteIdentifier(os.Getenv("LIBERTY_REMOTE_ACCEPTANCE_JOB_ID"))
	if address == "" || reference == "" || err != nil {
		t.Fatal("supply the test service URL, OS credential reference and synthetic fixture job ID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	token, err := credentials.NewSystem().Get(ctx, reference)
	if err != nil || token.Empty() {
		t.Fatal("test credential unavailable")
	}
	if err := checkServiceReads(ctx, application.RemoteConnection{URL: address, Token: token}, jobID); err != nil {
		t.Fatal(err)
	}
}
