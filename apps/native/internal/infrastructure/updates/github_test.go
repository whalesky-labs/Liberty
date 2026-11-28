package updates

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

const releaseJSON = `{"tag_name":"v2.0.0-alpha.4","name":"Liberty v2.0.0-alpha.4","body":"## Changes\n\n- Fixed updates","prerelease":true,"draft":false,"published_at":"2026-11-28T00:00:00Z","html_url":"https://untrusted.example/download"}`

func TestGitHubReleasesReadsPublicMetadataAndPagination(t *testing.T) {
	calls := 0
	c := Client{transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.github.com" || r.URL.Path != "/repos/whalesky-labs/Liberty/releases" || r.Header.Get("Authorization") != "" || r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" || r.Header.Get("User-Agent") == "" || r.URL.Query().Get("per_page") != "100" {
			t.Fatal("unexpected request", r.URL, r.Header)
		}
		if calls == 1 {
			if r.URL.Query().Get("page") != "1" {
				t.Fatal(r.URL)
			}
			return response(200, "["+strings.Repeat(releaseJSON+",", 99)+releaseJSON+"]"), nil
		}
		if r.URL.Query().Get("page") != "2" {
			t.Fatal(r.URL)
		}
		return response(200, "["+releaseJSON+"]"), nil
	})}
	releases, err := c.Releases(context.Background())
	if err != nil || len(releases) != 101 || calls != 2 {
		t.Fatal(len(releases), calls, err)
	}
	if releases[0].Notes != "## Changes\n\n- Fixed updates" || !releases[0].Prerelease || releases[0].PublishedAt.IsZero() || releases[0].URL() != domain.ReleaseRepositoryURL+"/releases/tag/v2.0.0-alpha.4" {
		t.Fatal(releases[0])
	}
}

func TestGitHubReleasesRejectsFailuresMalformedAndOversizedResponses(t *testing.T) {
	largeNotes, _ := json.Marshal(strings.Repeat("x", maxNotesBytes+1))
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"server", 503, `[]`}, {"missing", 404, `[]`}, {"object", 200, `{}`}, {"null", 200, `null`}, {"null-row", 200, `[null]`}, {"missing-tag", 200, `[{}]`}, {"truncated", 200, `[`}, {"trailing", 200, `[] {}`}, {"oversized", 200, strings.Repeat(" ", maxResponseBytes+1)}, {"large-notes", 200, `[{"tag_name":"v2.0.0","body":` + string(largeNotes) + `}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := Client{transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(test.status, test.body), nil })}
			if releases, err := c.Releases(context.Background()); err != domain.ErrUpdateCheck || releases != nil {
				t.Fatal(len(releases), err)
			}
		})
	}
	for _, status := range []int{403, 429} {
		c := Client{transport: transportFunc(func(*http.Request) (*http.Response, error) {
			r := response(status, `{}`)
			r.Header.Set("X-RateLimit-Remaining", "0")
			return r, nil
		})}
		if _, err := c.Releases(context.Background()); !errors.Is(err, domain.ErrUpdateRateLimit) {
			t.Fatal(err)
		}
	}
}

func TestGitHubRateLimitDeadlineAndSecondaryLimit(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		status  int
		headers http.Header
		body    string
		wait    time.Duration
		limited bool
	}{
		{"primary", 403, http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1795867800"}}, `{}`, 10 * time.Minute, true},
		{"retry-after", 429, http.Header{"Retry-After": {"120"}}, `{}`, 2 * time.Minute, true},
		{"retry-date", 403, http.Header{"Retry-After": {now.Add(3 * time.Minute).Format(http.TimeFormat)}}, `{}`, 3 * time.Minute, true},
		{"secondary", 403, http.Header{}, `{"message":"You have exceeded a secondary rate limit."}`, time.Minute, true},
		{"no-deadline", 429, http.Header{}, `{}`, time.Minute, true},
		{"malformed", 429, http.Header{"Retry-After": {"not-a-date"}}, `{}`, time.Minute, true},
		{"forbidden", 403, http.Header{}, `{"message":"Forbidden"}`, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := response(test.status, test.body)
			r.Header = test.headers
			_, err := readReleases(r, now)
			var limit *domain.UpdateRateLimitError
			if errors.As(err, &limit) != test.limited {
				t.Fatal(err)
			}
			if test.limited && !limit.RetryAt.Equal(now.Add(test.wait)) {
				t.Fatal(limit.RetryAt, now.Add(test.wait))
			}
		})
	}
}

func TestGitHubReleasesRejectsRedirectAndPartialTraversal(t *testing.T) {
	calls := 0
	c := Client{transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		r := response(302, "")
		r.Header.Set("Location", "https://untrusted.example/releases")
		return r, nil
	})}
	if _, err := c.Releases(context.Background()); err == nil || calls != 1 {
		t.Fatal("followed redirect", calls, err)
	}
	calls = 0
	c.transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(200, "["+strings.Repeat(releaseJSON+",", 99)+releaseJSON+"]"), nil
		}
		return nil, errors.New("synthetic failure")
	})
	if releases, err := c.Releases(context.Background()); err == nil || releases != nil {
		t.Fatal("partial list reported as success", len(releases), err)
	}
	calls = 0
	c.transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, "["+strings.Repeat(releaseJSON+",", 99)+releaseJSON+"]"), nil
	})
	if releases, err := c.Releases(context.Background()); err == nil || releases != nil || calls != maxReleasePages {
		t.Fatal("unbounded or incomplete traversal", len(releases), calls, err)
	}
}

func TestGitHubReleasesCancellation(t *testing.T) {
	c := Client{transport: transportFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Releases(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("request deadline was not preserved", err)
	}
	c.transport = transportFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	if _, err := c.Releases(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("transport timeout was not preserved", err)
	}
}
