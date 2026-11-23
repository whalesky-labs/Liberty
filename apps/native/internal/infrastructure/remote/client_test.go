package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/domain"
)

func syntheticJob(id string) map[string]any {
	return map[string]any{"id": id, "title": "Synthetic meeting", "source": "local", "createdAt": "2026-11-18", "overallStatus": "completed", "durationMinutes": 1, "lang": "zh", "enableSpeaker": true, "summaryTemplate": "default", "uploadStatus": "completed", "asrStatus": "completed", "summaryStatus": "idle", "sourceFiles": []any{}, "transcriptSegments": []any{map[string]any{"id": "s1", "startMs": 0, "endMs": 1000, "text": "Synthetic transcript"}}, "speakerSegments": []any{}, "summaryRuns": []any{}, "summary": map[string]any{}, "pythonPath": "/synthetic/private/path", "runnerScriptPath": "/synthetic/runner"}
}
func syntheticCapabilities() domain.RemoteCapabilities {
	return domain.RemoteCapabilities{Protocol: "liberty-meeting", ProtocolVersion: 1, JobSchemaVersion: 1, ServiceVersion: " 1.0 ", Operations: []string{"jobs.list", "jobs.read", "jobs.result.read", "jobs.retry", "jobs.delete", "transcript.speakers.rename"}}
}

func TestRemoteRealHTTPRoutesAndLegacyNormalization(t *testing.T) {
	var requests atomic.Int32
	id := "job / one"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-token" || r.Header.Get("Accept") != "application/json" || r.URL.RawQuery != "" {
			t.Error("request policy")
		}
		w.Header().Set("Content-Type", "application/vnd.liberty+json")
		if r.URL.Path == "/base/api/capabilities" {
			_ = json.NewEncoder(w).Encode(syntheticCapabilities())
			return
		}
		if r.URL.Path == "/base/api/jobs" {
			_ = json.NewEncoder(w).Encode([]any{syntheticJob(id)})
			return
		}
		if !strings.HasPrefix(r.URL.EscapedPath(), "/base/api/jobs/job%20%2F%20one") {
			t.Errorf("ID not a single escaped segment: %s", r.URL.EscapedPath())
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/speakers/rename") {
			var body map[string]string
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || body["fromSpeaker"] != "Alice" || body["toSpeaker"] != "Bob" {
				t.Error("rename payload")
			}
		} else if strings.HasSuffix(r.URL.Path, "/retry") && r.Method != "POST" {
			t.Error("retry method")
		}
		_ = json.NewEncoder(w).Encode(syntheticJob(id))
	}))
	defer server.Close()
	c := application.RemoteConnection{URL: server.URL + "/base?ignored=value#ignored", Token: domain.NewSecret(" synthetic-token ")}
	client := Client{}
	ctx := context.Background()
	caps, err := client.Capabilities(ctx, c)
	if err != nil || caps.ServiceVersion != "1.0" {
		t.Fatal(caps, err)
	}
	list, err := client.List(ctx, c)
	if err != nil || len(list) != 1 {
		t.Fatal(err)
	}
	job := list[0]
	if job.Job.Source != "remote" || job.Job.DiarizationStatus != "legacy_unverified" || job.Job.ASRBackend != "unknown" || len(job.Job.Warnings) != 1 || len(job.Transcript) != 1 {
		t.Fatalf("legacy projection %+v", job)
	}
	encoded, _ := json.Marshal(job)
	if strings.Contains(string(encoded), "/synthetic/") {
		t.Fatal("server executable path escaped adapter")
	}
	for _, result := range []bool{false, true} {
		if _, err = client.Read(ctx, c, id, result); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = client.Retry(ctx, c, id); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Rename(ctx, c, id, " Alice ", " Bob "); err != nil {
		t.Fatal(err)
	}
	if err = client.Delete(ctx, c, id); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 13 {
		t.Fatalf("each operation must handshake: %d", requests.Load())
	}
}

func TestRemoteMutationsRecheckCapabilitiesAfterSnapshot(t *testing.T) {
	for _, operation := range []string{"jobs.retry", "jobs.delete", "transcript.speakers.rename"} {
		t.Run(operation, func(t *testing.T) {
			var handshake, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/capabilities" {
					capabilities := syntheticCapabilities()
					if handshake.Add(1) > 1 {
						capabilities.Operations = []string{"jobs.read", "jobs.result.read"}
					}
					_ = json.NewEncoder(w).Encode(capabilities)
					return
				}
				writes.Add(1)
				_ = json.NewEncoder(w).Encode(syntheticJob("same-id"))
			}))
			defer server.Close()
			ctx := context.Background()
			connection := application.RemoteConnection{URL: server.URL, Token: domain.NewSecret("synthetic-token")}
			client := Client{}
			caps, err := client.Capabilities(ctx, connection)
			if err != nil || !caps.Supports(operation) {
				t.Fatal("initial capability snapshot", err)
			}
			switch operation {
			case "jobs.retry":
				_, err = client.Retry(ctx, connection, "same-id")
			case "jobs.delete":
				err = client.Delete(ctx, connection, "same-id")
			case "transcript.speakers.rename":
				_, err = client.Rename(ctx, connection, "same-id", "Alice", "Bob")
			}
			if !errors.Is(err, domain.ErrRemote) || handshake.Load() != 2 || writes.Load() != 0 {
				t.Fatal("stale capability snapshot authorized write", err, handshake.Load(), writes.Load())
			}
		})
	}
}

func TestRemoteRejectsErrorsRedirectsOversizeAndMismatchedIDs(t *testing.T) {
	for _, mode := range []string{"html", "mismatch", "error", "redirect", "oversize", "unknown", "unsupported", "null-list", "oversize-delete"} {
		t.Run(mode, func(t *testing.T) {
			var redirected atomic.Int32
			trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
			defer trap.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/capabilities" {
					caps := syntheticCapabilities()
					if mode == "unknown" {
						caps.Operations = append(caps.Operations, "unknown")
					}
					if mode == "unsupported" {
						caps.Operations = []string{}
					}
					_ = json.NewEncoder(w).Encode(caps)
					return
				}
				switch mode {
				case "html":
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte("private"))
				case "mismatch":
					_ = json.NewEncoder(w).Encode(syntheticJob("other"))
				case "error":
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`{"error":{"message":"synthetic-token /synthetic/private"}}`))
				case "redirect":
					http.Redirect(w, r, trap.URL, 302)
				case "oversize":
					_, _ = w.Write([]byte(strings.Repeat(" ", maxResponse+1)))
				case "oversize-delete":
					_, _ = w.Write([]byte(strings.Repeat(" ", maxDiscard+1)))
				case "null-list":
					_, _ = w.Write([]byte("null"))
				default:
					t.Error("operation bypassed handshake")
				}
			}))
			defer server.Close()
			c := application.RemoteConnection{URL: server.URL, Token: domain.NewSecret("synthetic-token")}
			var err error
			if mode == "null-list" {
				_, err = (Client{}).List(context.Background(), c)
			} else if mode == "oversize-delete" {
				err = (Client{}).Delete(context.Background(), c, "id")
			} else {
				_, err = (Client{}).Read(context.Background(), c, "id", true)
			}
			if !errors.Is(err, domain.ErrRemote) || redirected.Load() != 0 {
				t.Fatal("unsafe failure", err, redirected.Load())
			}
			if strings.Contains(fmt.Sprint(err), "synthetic-token") {
				t.Fatal("secret in error")
			}
		})
	}
}
func TestRemoteTargetDNSAndEndpointPolicy(t *testing.T) {
	ctx := context.Background()
	for _, value := range []string{"http://example.com", "https://10.0.0.1", "https://168.63.129.16", "https://192.0.2.1", "https://[2001:db8::1]", "https://user:password@example.com", "file:///tmp/test", "https://example.com:0", "https://example.com:65536"} {
		if _, err := resolveTarget(ctx, value, defaultLookup); err == nil {
			t.Fatal("accepted", value)
		}
	}
	for _, addresses := range [][]netip.Addr{{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.1")}, {netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("::ffff:127.0.0.1")}} {
		if _, err := resolveTarget(ctx, "https://example.com", func(context.Context, string) ([]netip.Addr, error) { return addresses, nil }); err == nil {
			t.Fatal("unsafe DNS allowed")
		}
	}
	target, err := resolveTarget(ctx, "https://example.com/base/?query#fragment", func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.endpoint("api", "jobs", "..") != "https://example.com/base/api/jobs/%2E%2E" {
		t.Fatal("dot segment not escaped")
	}
	transport := target.transport()
	if transport.Proxy != nil {
		t.Fatal("proxy enabled")
	}
	_, err = transport.DialContext(ctx, "tcp", "other.example:443")
	if !errors.Is(err, domain.ErrRemote) {
		t.Fatal("untrusted dial host")
	}
	for _, value := range []string{"http://127.0.0.1:3000", "http://[::1]:3000", "http://[::ffff:127.0.0.1]:3000"} {
		if _, err := resolveTarget(ctx, value, defaultLookup); err != nil {
			t.Fatal("loopback rejected", err)
		}
	}
}
func TestRemoteRequiredCredentialAndMalformedJobs(t *testing.T) {
	if _, err := (Client{}).Capabilities(context.Background(), application.RemoteConnection{URL: "http://127.0.0.1:1"}); !errors.Is(err, domain.ErrRemote) {
		t.Fatal(err)
	}
	for _, field := range []string{"id", "title", "durationMinutes", "lang", "enableSpeaker", "sourceFiles", "transcriptSegments", "summary"} {
		job := syntheticJob("id")
		delete(job, field)
		data, _ := json.Marshal(job)
		if _, err := parseJob(data); err == nil {
			t.Fatal("accepted missing", field)
		}
	}
	for _, status := range []string{"", "invented"} {
		job := syntheticJob("id")
		job["diarizationStatus"] = status
		data, _ := json.Marshal(job)
		if _, err := parseJob(data); err == nil {
			t.Fatal("accepted status", status)
		}
	}
	for _, field := range []string{"source", "durationMinutes", "enableSpeaker", "asrBackend"} {
		job := syntheticJob("id")
		job[field] = nil
		data, _ := json.Marshal(job)
		if _, err := parseJob(data); err == nil {
			t.Fatal("accepted serde-invalid null", field)
		}
	}
	job := syntheticJob("id")
	job["summary"] = map[string]any{"overview": nil}
	data, _ := json.Marshal(job)
	if _, err := parseJob(data); err == nil {
		t.Fatal("accepted null summary scalar")
	}
	job = syntheticJob("id")
	job["summaryRuns"] = []any{map[string]any{"id": "run", "jobId": "id", "includeSpeaker": false, "includeTimestamp": false, "status": "completed", "createdAt": "date", "updatedAt": "date", "minutesPayload": map[string]any{}}}
	data, _ = json.Marshal(job)
	result, err := parseJob(data)
	if err != nil || result.SummaryRuns[0].MinutesPayload.SchemaVersion != 1 || result.SummaryRuns[0].MinutesPayload.MeetingInfoSource != "empty" {
		t.Fatal("minutes serde defaults lost", err)
	}
}
