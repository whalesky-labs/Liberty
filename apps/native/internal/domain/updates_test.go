package domain

import (
	"testing"
	"time"
)

func publishedRelease(tag string) Release {
	return Release{Tag: tag, PublishedAt: time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)}
}

func TestSelectUpdateUsesSemanticVersionsAndChannel(t *testing.T) {
	releases := []Release{publishedRelease("v2.0.0-alpha.2"), publishedRelease("v1.10.0"), publishedRelease("v1.9.9"), publishedRelease("v2.0.0-alpha.10"), publishedRelease("v2.0.0-alpha.3")}
	// A mislabeled prerelease tag must not leak into the stable channel.
	releases[3].Prerelease = true
	draft := publishedRelease("v9.0.0")
	draft.Draft = true
	flagged := publishedRelease("v8.0.0")
	flagged.Prerelease = true
	releases = append(releases, draft, flagged, Release{Tag: "v7.0.0"}, publishedRelease("nightly"))
	for _, test := range []struct {
		current   string
		channel   UpdateChannel
		latest    string
		available bool
	}{
		{"1.9.9", StableUpdates, "v1.10.0", true},
		{"1.10.0", StableUpdates, "v1.10.0", false},
		{"2.0.0-alpha.3", StableUpdates, "v1.10.0", false},
		{"2.0.0-alpha.3", PreviewUpdates, "v8.0.0", true},
	} {
		result, err := SelectUpdate(test.current, test.channel, releases)
		if err != nil || result.Latest == nil || result.Latest.Tag != test.latest || result.Available != test.available {
			t.Fatalf("%+v: got %+v, %v", test, result, err)
		}
	}
	result, err := SelectUpdate("2.0.0-alpha.3", PreviewUpdates, releases[:5])
	if err != nil || result.Latest.Tag != "v2.0.0-alpha.10" || !result.Available {
		t.Fatal(result, err)
	}
	result, err = SelectUpdate("2.0.0-alpha.3", StableUpdates, nil)
	if err != nil || result.Latest != nil || result.Available {
		t.Fatal("empty channel must not offer a release", result, err)
	}
}

func TestReleaseVersionPrecedence(t *testing.T) {
	ordered := []string{"1.9.9", "1.10.0", "2.0.0-alpha", "2.0.0-alpha.1", "2.0.0-alpha.2", "2.0.0-alpha.10", "2.0.0-alpha.beta", "2.0.0-beta", "2.0.0-beta.2", "2.0.0-beta.11", "2.0.0-rc.1", "2.0.0", "999999999999999999999.0.0"}
	for i, value := range ordered {
		parsed, ok := parseReleaseVersion(value)
		if !ok {
			t.Fatal(value)
		}
		if compareReleaseVersions(parsed, parsed) != 0 {
			t.Fatal("version unequal to itself", value)
		}
		for _, earlier := range ordered[:i] {
			previous, _ := parseReleaseVersion(earlier)
			if compareReleaseVersions(parsed, previous) <= 0 || compareReleaseVersions(previous, parsed) >= 0 {
				t.Fatal(value, earlier)
			}
		}
	}
	a, _ := parseReleaseVersion("v2.0.0+build.9")
	b, _ := parseReleaseVersion("2.0.0+build.10")
	if compareReleaseVersions(a, b) != 0 {
		t.Fatal("build metadata affects precedence")
	}
	for _, invalid := range []string{"", "2.0", "2.0.0.0", "01.0.0", "2.0.0-01", "2.0.0-alpha.01", "2.0.0-", "2.0.0+", "2.0.0/evil", " 2.0.0", "2.0.0\n", "2.0.0-中文"} {
		if _, ok := parseReleaseVersion(invalid); ok {
			t.Fatal("accepted invalid version", invalid)
		}
	}
}

func TestReleaseLinksAndDefaultChannel(t *testing.T) {
	for _, tag := range []string{"../../evil", "https://example.com", "v2.0.0?other", "2.0.0"} {
		if (Release{Tag: tag}).URL() != "" {
			t.Fatal("unsafe release link", tag)
		}
	}
	if got := (Release{Tag: "v2.0.0-alpha.4"}).URL(); got != ReleaseRepositoryURL+"/releases/tag/v2.0.0-alpha.4" {
		t.Fatal(got)
	}
	if DefaultUpdateChannel("2.0.0-alpha.3") != PreviewUpdates || DefaultUpdateChannel("2.0.0") != StableUpdates {
		t.Fatal("incorrect default channel")
	}
	if _, err := SelectUpdate("unknown", StableUpdates, nil); err != ErrUpdateVersion {
		t.Fatal(err)
	}
	if _, err := SelectUpdate("2.0.0", "other", nil); err != ErrUpdateVersion {
		t.Fatal(err)
	}
}
