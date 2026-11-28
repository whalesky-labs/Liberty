package client

import (
	"strconv"
	"testing"
)

func TestNavigationHistoryBranchesAndPreservesPageIdentity(t *testing.T) {
	var history NavigationHistory
	home := NavigationEntry{Path: "/"}
	queue := NavigationEntry{Path: "/jobs?status=failed"}
	local := NavigationEntry{Path: "/jobs/same-id", DetailSource: "local"}
	remote := NavigationEntry{Path: local.Path, DetailSource: "remote"}
	for _, entry := range []NavigationEntry{home, queue, local, remote} {
		history.Visit(entry)
		history.Visit(entry)
	}
	for _, want := range []NavigationEntry{local, queue, home} {
		if got, ok := history.Travel(-1, nil); !ok || got != want {
			t.Fatalf("back returned %+v (%v), want %+v", got, ok, want)
		}
	}
	if history.CanTravel(-1, nil) {
		t.Fatal("the initial page has a phantom predecessor")
	}
	if got, ok := history.Travel(1, nil); !ok || got != queue {
		t.Fatal("forward did not restore the filtered queue", got, ok)
	}
	history.Visit(queue)
	if !history.CanTravel(1, nil) {
		t.Fatal("revisiting the current page discarded forward history")
	}
	settings := NavigationEntry{Path: "/settings", SettingsSection: "appearance"}
	history.Visit(settings)
	if history.CanTravel(1, nil) {
		t.Fatal("a new page kept the old forward branch")
	}
	settings.SettingsSection = "localRuntime"
	history.Visit(settings)
	if got, ok := history.Travel(-1, nil); !ok || got.SettingsSection != "appearance" {
		t.Fatal("settings sections collapsed into one history entry", got, ok)
	}
}

func TestNavigationHistoryCapturesLocalStateAndSkipsUnavailablePages(t *testing.T) {
	var history NavigationHistory
	history.ReplaceCurrent(NavigationEntry{Path: "/jobs"})
	filtered := NavigationEntry{Path: "/jobs?status=completed"}
	history.ReplaceCurrent(filtered)
	history.Visit(NavigationEntry{Path: "/jobs/new"})
	history.Visit(NavigationEntry{Path: "/live"})
	history.Visit(NavigationEntry{Path: "/settings"})
	available := func(entry NavigationEntry) bool { return entry.Path != "/jobs/new" && entry.Path != "/live" }
	if !history.CanTravel(-1, available) || history.index != 3 {
		t.Fatal("availability checking moved the history cursor")
	}
	if got, ok := history.Travel(-1, available); !ok || got != filtered {
		t.Fatal("back lost local filter state or entered an unavailable page", got, ok)
	}
	if got, ok := history.Travel(1, available); !ok || got.Path != "/settings" {
		t.Fatal("forward did not skip unavailable pages", got, ok)
	}
	if _, ok := history.Travel(0, nil); ok {
		t.Fatal("invalid direction changed history")
	}
}

func TestNavigationHistoryBoundsRetainedEntries(t *testing.T) {
	var history NavigationHistory
	for index := range navigationHistoryLimit + 20 {
		history.Visit(NavigationEntry{Path: "/page/" + strconv.Itoa(index)})
	}
	for range navigationHistoryLimit - 1 {
		if _, ok := history.Travel(-1, nil); !ok {
			t.Fatal("a retained entry was lost")
		}
	}
	if history.entries[history.index].Path != "/page/20" || history.CanTravel(-1, nil) {
		t.Fatal("history did not discard its oldest entries")
	}
}
