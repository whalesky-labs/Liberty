// Package updates reads public release metadata without credentials or user data.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"liberty.local/liberty/internal/domain"
)

const (
	releasesEndpoint = "https://api.github.com/repos/whalesky-labs/Liberty/releases"
	releasesPerPage  = 100
	maxReleasePages  = 10
	maxResponseBytes = 4 << 20
	maxNotesBytes    = 128 << 10
)

type Client struct{ transport http.RoundTripper }

// Releases reads every page before reporting success. A failed or bounded-out
// traversal must not be presented as evidence that the installed app is current.
func (c Client) Releases(ctx context.Context) ([]domain.Release, error) {
	client := &http.Client{Transport: c.transport, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return domain.ErrUpdateCheck },
	}
	var releases []domain.Release
	for page := 1; page <= maxReleasePages; page++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s?per_page=%d&page=%d", releasesEndpoint, releasesPerPage, page), nil)
		if err != nil {
			return nil, domain.ErrUpdateCheck
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		request.Header.Set("User-Agent", "Liberty-Update-Check")
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, context.DeadlineExceeded
			}
			return nil, domain.ErrUpdateCheck
		}
		batch, err := readReleases(response, time.Now())
		if err != nil {
			return nil, err
		}
		releases = append(releases, batch...)
		if len(batch) < releasesPerPage {
			return releases, nil
		}
	}
	return nil, domain.ErrUpdateCheck
}

func readReleases(response *http.Response, now time.Time) ([]domain.Release, error) {
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		limited := response.StatusCode == http.StatusTooManyRequests
		if response.StatusCode == http.StatusForbidden {
			limited = response.Header.Get("X-RateLimit-Remaining") == "0" || response.Header.Get("Retry-After") != ""
			if !limited {
				// GitHub's secondary limit may omit both rate-limit headers.
				data, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
				var failure struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(data, &failure) == nil {
					limited = strings.Contains(strings.ToLower(failure.Message), "rate limit")
				}
			}
		}
		if limited {
			return nil, &domain.UpdateRateLimitError{RetryAt: rateLimitDeadline(response.Header, now)}
		}
		return nil, domain.ErrUpdateCheck
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, domain.ErrUpdateCheck
	}
	var rows []struct {
		Tag         string     `json:"tag_name"`
		Name        string     `json:"name"`
		Body        string     `json:"body"`
		Draft       bool       `json:"draft"`
		Prerelease  bool       `json:"prerelease"`
		PublishedAt *time.Time `json:"published_at"`
	}
	if json.Unmarshal(data, &rows) != nil || rows == nil || len(rows) > releasesPerPage {
		return nil, domain.ErrUpdateCheck
	}
	releases := make([]domain.Release, len(rows))
	for i, row := range rows {
		if row.Tag == "" || len(row.Tag) > 200 || len(row.Name) > 1024 || len(row.Body) > maxNotesBytes {
			return nil, domain.ErrUpdateCheck
		}
		releases[i] = domain.Release{Tag: row.Tag, Name: row.Name, Notes: row.Body, Draft: row.Draft, Prerelease: row.Prerelease}
		if row.PublishedAt != nil {
			releases[i].PublishedAt = *row.PublishedAt
		}
	}
	return releases, nil
}

func rateLimitDeadline(headers http.Header, now time.Time) time.Time {
	var retryAt time.Time
	if value := headers.Get("Retry-After"); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 32); err == nil && seconds > 0 {
			retryAt = now.Add(time.Duration(seconds) * time.Second)
		} else if date, err := http.ParseTime(value); err == nil {
			retryAt = date
		}
	}
	if headers.Get("X-RateLimit-Remaining") == "0" {
		if seconds, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil && seconds > 0 {
			if reset := time.Unix(seconds, 0); reset.After(retryAt) {
				retryAt = reset
			}
		}
	}
	if !retryAt.After(now) {
		retryAt = now.Add(time.Minute)
	}
	return retryAt
}
