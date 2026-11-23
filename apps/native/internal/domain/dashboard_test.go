package domain

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestDashboardCalendarRanges(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, zone)
	for _, tc := range []struct {
		key, start, granularity string
		limit                   int
	}{
		{"today", "2026-03-09T04:00:00.000Z", "hour", 24},
		{"7d", "2026-03-03T05:00:00.000Z", "day", 31},
		{"30d", "2026-02-08T05:00:00.000Z", "day", 31},
	} {
		q, err := DashboardPeriod(tc.key, now)
		if err != nil || q.StartAt == nil || *q.StartAt != tc.start || q.Granularity != tc.granularity || q.Limit != tc.limit {
			t.Fatalf("range %s: %+v %v", tc.key, q, err)
		}
	}
	q, err := DashboardPeriod("all", now)
	if err != nil || q.StartAt != nil || q.Limit != 36 || q.Granularity != "month" {
		t.Fatal("all range changed")
	}
	if _, err := DashboardPeriod("quarter", now); err != ErrDashboardRange {
		t.Fatal("unknown range accepted")
	}
}

func TestDashboardRejectsAmbiguousAndMissingMidnight(t *testing.T) {
	for _, tc := range []struct {
		zone  string
		year  int
		month time.Month
		day   int
	}{
		{"America/Havana", 2024, time.November, 3},
		{"America/Sao_Paulo", 2018, time.November, 4},
	} {
		zone, err := time.LoadLocation(tc.zone)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DashboardPeriod("today", time.Date(tc.year, tc.month, tc.day, 12, 0, 0, 0, zone)); err != ErrDashboardRange {
			t.Fatalf("invalid midnight accepted: %s %v", tc.zone, err)
		}
	}
}
