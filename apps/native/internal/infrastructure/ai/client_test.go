package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"liberty.local/liberty/internal/domain"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func syntheticRequest(base string) domain.SummaryRequest {
	return domain.SummaryRequest{BaseURL: base, Model: " synthetic ", Credential: domain.NewSecret("synthetic-test-value"), SystemPrompt: "synthetic system", UserPrompt: "synthetic transcript"}
}
func TestActualHTTPFallbackAndOriginalPayload(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic-test-value" {
			t.Error("wrong request")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("bad payload")
		}
		if body["model"] != "synthetic" || body["temperature"] != nil {
			t.Error("provider payload differs from original")
		}
		if call == 1 {
			if body["response_format"] == nil {
				t.Error("missing format")
			}
			w.WriteHeader(422)
			io.WriteString(w, `{"error":"unsupported response_format"}`)
			return
		}
		if call != 2 || body["response_format"] != nil {
			t.Error("invalid fallback")
		}
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{}"}}]}`)
	}))
	defer server.Close()
	raw, err := (Client{}).Complete(context.Background(), syntheticRequest(server.URL+"/v1/chat/completions/"))
	if err != nil || calls.Load() != 2 || !strings.Contains(raw, "choices") {
		t.Fatal("fallback failed", err)
	}
}
func TestRedirectAndHTTPFailuresDoNotLeakBody(t *testing.T) {
	for _, status := range []int{302, 307, 401, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
				io.WriteString(w, "synthetic-sensitive-server-body")
			}))
			defer server.Close()
			_, err := (Client{}).Complete(context.Background(), syntheticRequest(server.URL))
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) || httpErr.Status != status || calls.Load() != 0 || strings.Contains(err.Error(), "synthetic-sensitive") {
				t.Fatal("unsafe failure", err)
			}
		})
	}
}
func TestBodyLimitAndContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", domain.MaxAIResponseBytes+1))
	}))
	defer server.Close()
	if _, err := (Client{}).Complete(context.Background(), syntheticRequest(server.URL)); !errors.Is(err, domain.ErrAIResponse) {
		t.Fatal("oversize accepted", err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer slow.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := (Client{}).Complete(ctx, syntheticRequest(slow.URL)); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request never arrived")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel didn't stop request")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNetworkRetryIsBoundedAndRedacted(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("synthetic-sensitive-url-and-credential")
	})}
	_, err := completeWithRetry(context.Background(), client, "https://example.test/v1/chat/completions", syntheticRequest(""))
	if calls != 2 || !errors.Is(err, ErrNetwork) || strings.Contains(err.Error(), "synthetic-sensitive") {
		t.Fatal("retry policy", err)
	}
}
func TestDestinationPolicyAndPinnedDNS(t *testing.T) {
	public := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	for _, value := range []string{"https://8.8.8.8/v1", "http://127.0.0.1:8787", "http://[::1]:8787", "http://[::ffff:127.0.0.1]:8787", "https://example.test/v1"} {
		if _, err := resolveTarget(context.Background(), value, public); err != nil {
			t.Errorf("valid synthetic target rejected: %s", value)
		}
	}
	for _, value := range []string{"file:///tmp/x", "http://example.test", "http://localhost", "https://u:p@example.test", "https://example.test/?key=synthetic", "https://example.test/?", "https://example.test/#", "https://10.0.0.1", "https://168.63.129.16", "https://[fe80::1]", "https://[::ffff:192.168.1.1]", "https://[2001:db8::1]", "https://[3fff::1]", "https://127.0.0.1:0", "https://127.0.0.1:65536"} {
		if _, err := resolveTarget(context.Background(), value, public); !errors.Is(err, ErrTarget) {
			t.Errorf("unsafe target accepted: %s", value)
		}
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1"} {
		mixed := func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr(address)}, nil
		}
		if _, err := resolveTarget(context.Background(), "https://example.test", mixed); err == nil {
			t.Fatal("mixed DNS accepted")
		}
	}
	calls := 0
	pin := func(context.Context, string) ([]netip.Addr, error) { calls++; return public(context.Background(), "") }
	target, err := resolveTarget(context.Background(), "https://example.test:8443/v1", pin)
	if err != nil {
		t.Fatal(err)
	}
	transport := target.transport()
	defer transport.CloseIdleConnections()
	if transport.Proxy != nil || target.endpoint() != "https://example.test:8443/v1/chat/completions" {
		t.Fatal("target/port/proxy changed")
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "other.test:8443"); !errors.Is(err, ErrTarget) {
		t.Fatal("target escaped")
	}
	if calls != 1 || target.addresses[0].String() != "8.8.8.8" {
		t.Fatal("DNS was not pinned")
	}
}
