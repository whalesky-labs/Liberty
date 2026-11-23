package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

func checkServiceReads(ctx context.Context, connection application.RemoteConnection, jobID string) error {
	client := Client{}
	capabilities, err := client.Capabilities(ctx, connection)
	if err != nil {
		return err
	}
	for _, operation := range []string{"jobs.list", "jobs.read", "jobs.result.read"} {
		if !capabilities.Supports(operation) {
			return domain.ErrRemoteUnsupported
		}
	}
	jobs, err := client.List(ctx, connection)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(jobs, func(job domain.Result) bool { return job.Job.ID == jobID }) {
		return errors.New("synthetic fixture absent from service list")
	}
	detail, err := client.Read(ctx, connection, jobID, false)
	if err != nil {
		return err
	}
	result, err := client.Read(ctx, connection, jobID, true)
	if err != nil {
		return err
	}
	if detail.Job.ID != jobID || result.Job.ID != jobID || result.Job.OverallStatus != "completed" || len(result.Transcript) == 0 {
		return errors.New("expected the same completed synthetic meeting with a transcript")
	}
	return nil
}

func TestServiceAcceptanceExercisesOnlyReadRoutes(t *testing.T) {
	job := syntheticJob("fixture")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet {
			t.Error("unexpected mutation")
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch request.URL.Path {
		case "/api/capabilities":
			json.NewEncoder(writer).Encode(syntheticCapabilities())
		case "/api/jobs":
			json.NewEncoder(writer).Encode([]any{job})
		case "/api/jobs/fixture", "/api/jobs/fixture/result":
			json.NewEncoder(writer).Encode(job)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	connection := application.RemoteConnection{URL: server.URL, Token: domain.NewSecret("synthetic")}
	if err := checkServiceReads(context.Background(), connection, "fixture"); err != nil {
		t.Fatal(err)
	}
	if err := checkServiceReads(context.Background(), connection, "missing"); err == nil {
		t.Fatal("missing fixture passed acceptance")
	}
}
