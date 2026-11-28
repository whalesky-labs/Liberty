package domain

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	ErrUpdateCheck     = errors.New("update_check_failed")
	ErrUpdateRateLimit = errors.New("update_rate_limited")
	ErrUpdateVersion   = errors.New("update_version_invalid")
)

// UpdateRateLimitError carries only a safe retry deadline, never the remote
// error body (which can contain the caller's public IP address).
type UpdateRateLimitError struct{ RetryAt time.Time }

func (e *UpdateRateLimitError) Error() string { return ErrUpdateRateLimit.Error() }
func (e *UpdateRateLimitError) Unwrap() error { return ErrUpdateRateLimit }

type UpdateChannel string

const (
	StableUpdates        UpdateChannel = "stable"
	PreviewUpdates       UpdateChannel = "preview"
	ReleaseRepositoryURL               = "https://github.com/whalesky-labs/Liberty"
)

type Release struct {
	Tag, Name, Notes  string
	PublishedAt       time.Time
	Draft, Prerelease bool
}

// URL is derived from the fixed repository and a validated tag, never from
// release prose or an untrusted html_url field in the API response.
func (r Release) URL() string {
	if !strings.HasPrefix(r.Tag, "v") {
		return ""
	}
	if _, ok := parseReleaseVersion(r.Tag); !ok {
		return ""
	}
	return ReleaseRepositoryURL + "/releases/tag/" + url.PathEscape(r.Tag)
}

type UpdateResult struct {
	CurrentVersion string
	Channel        UpdateChannel
	Latest         *Release
	Available      bool
	CheckedAt      time.Time
	Cached         bool
}

func DefaultUpdateChannel(version string) UpdateChannel {
	v, ok := parseReleaseVersion(version)
	if ok && len(v.pre) > 0 {
		return PreviewUpdates
	}
	return StableUpdates
}

func SelectUpdate(current string, channel UpdateChannel, releases []Release) (UpdateResult, error) {
	result := UpdateResult{CurrentVersion: current, Channel: channel}
	installed, ok := parseReleaseVersion(current)
	if !ok || channel != StableUpdates && channel != PreviewUpdates {
		return result, ErrUpdateVersion
	}
	var latest releaseVersion
	for _, release := range releases {
		version, valid := parseReleaseVersion(release.Tag)
		if !valid || !strings.HasPrefix(release.Tag, "v") || release.Draft || release.PublishedAt.IsZero() {
			continue
		}
		if channel == StableUpdates && (release.Prerelease || len(version.pre) > 0) {
			continue
		}
		if result.Latest == nil || compareReleaseVersions(version, latest) > 0 {
			copy := release
			result.Latest, latest = &copy, version
		}
	}
	result.Available = result.Latest != nil && compareReleaseVersions(latest, installed) > 0
	return result, nil
}

type releaseVersion struct{ core, pre []string }

var releaseVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func parseReleaseVersion(value string) (releaseVersion, bool) {
	if len(value) > 200 {
		return releaseVersion{}, false
	}
	match := releaseVersionPattern.FindStringSubmatch(value)
	if match == nil {
		return releaseVersion{}, false
	}
	v := releaseVersion{core: match[1:4]}
	if match[4] != "" {
		v.pre = strings.Split(match[4], ".")
		for _, identifier := range v.pre {
			if numericVersionPart(identifier) && len(identifier) > 1 && identifier[0] == '0' {
				return releaseVersion{}, false
			}
		}
	}
	return v, true
}

func numericVersionPart(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func compareVersionPart(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func compareReleaseVersions(a, b releaseVersion) int {
	for i := range a.core {
		if order := compareVersionPart(a.core[i], b.core[i]); order != 0 {
			return order
		}
	}
	if len(a.pre) == 0 && len(b.pre) > 0 {
		return 1
	}
	if len(a.pre) > 0 && len(b.pre) == 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, yn := numericVersionPart(x), numericVersionPart(y)
		if xn && !yn {
			return -1
		}
		if !xn && yn {
			return 1
		}
		order := strings.Compare(x, y)
		if xn && yn {
			order = compareVersionPart(x, y)
		}
		if order != 0 {
			return order
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}
