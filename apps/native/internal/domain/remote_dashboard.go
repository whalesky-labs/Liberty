package domain

import (
	"sort"
	"time"
)

// RemoteDashboard matches dashboardOverview.ts: remote jobs determine every
// metric, and local resources/companion never leak into the remote overview.
func RemoteDashboard(results []Result, period string, now time.Time) (DashboardOverview, error) {
	query, err := DashboardPeriod(period, now)
	if err != nil {
		return DashboardOverview{}, err
	}
	o := DashboardOverview{Source: "remote", Range: period, TrendGranularity: query.Granularity, Trend: []DashboardTrend{}, AttentionJobs: []DashboardJob{}, RecentResults: []DashboardJob{}}
	var start time.Time
	if query.StartAt != nil {
		start, _ = time.Parse(time.RFC3339Nano, *query.StartAt)
	}
	filtered := make([]Result, 0, len(results))
	for _, r := range results {
		created, valid := remoteDate(r.Job.CreatedAt, now.Location())
		if query.StartAt != nil && (!valid || created.Before(start)) {
			continue
		}
		filtered = append(filtered, r)
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Job.CreatedAt > filtered[j].Job.CreatedAt })
	trend := map[string]*DashboardTrend{}
	for _, r := range filtered {
		j := r.Job
		hasSummary := r.Summary.Overview != ""
		for _, run := range r.SummaryRuns {
			hasSummary = hasSummary || run.Status == "completed" && run.Result != nil
		}
		summary := DashboardJob{ID: j.ID, Title: j.Title, CreatedAt: j.CreatedAt, DurationMinutes: int64(j.DurationMinutes), OverallStatus: j.OverallStatus, DiarizationStatus: j.DiarizationStatus, WarningCount: len(j.Warnings), HasSummary: hasSummary, LastExportedAt: j.LastExportedAt}
		completed, active, failed := j.OverallStatus == "completed", IsProcessing(j.OverallStatus), j.OverallStatus == "failed"
		hasExport := j.LastExportedAt != nil && *j.LastExportedAt != ""
		duration := int64(0)
		if j.ProcessingDurationSeconds != nil {
			duration = int64(*j.ProcessingDurationSeconds)
		}
		m := &o.Metrics
		m.TotalJobs++
		m.MediaDurationMinutes += int64(j.DurationMinutes)
		m.ProcessingDurationSeconds += duration
		if active {
			m.ActiveJobs++
		}
		if failed {
			m.FailedJobs++
		}
		if len(j.Warnings) > 0 {
			m.WarningJobs++
		}
		if completed {
			m.CompletedJobs++
			if j.ASRStatus == "completed" {
				m.TranscriptReadyJobs++
			}
			if j.EnableSpeaker {
				m.SpeakerEligibleJobs++
			}
			if j.DiarizationStatus == "completed" {
				m.SpeakerReadyJobs++
			}
			if hasSummary {
				m.SummaryReadyJobs++
			}
			if hasExport {
				m.ExportedJobs++
			}
			if len(o.RecentResults) < 6 {
				o.RecentResults = append(o.RecentResults, summary)
			}
		}
		if len(o.AttentionJobs) < 6 && (active || failed || len(j.Warnings) > 0 || completed && (!hasSummary || !hasExport)) {
			o.AttentionJobs = append(o.AttentionJobs, summary)
		}
		created, valid := remoteDate(j.CreatedAt, now.Location())
		key := "NaN-NaN-NaN"
		if valid {
			switch query.Granularity {
			case "hour":
				key = created.Format("15:00")
			case "month":
				key = created.Format("2006-01")
			default:
				key = created.Format("2006-01-02")
			}
		} else if query.Granularity == "hour" {
			key = "NaN:00"
		} else if query.Granularity == "month" {
			key = "NaN-NaN"
		}
		point := trend[key]
		if point == nil {
			point = &DashboardTrend{Period: key}
			trend[key] = point
		}
		point.TotalJobs++
		point.MediaDurationMinutes += int64(j.DurationMinutes)
		point.ProcessingDurationSeconds += duration
		if completed {
			point.CompletedJobs++
		}
		if failed {
			point.FailedJobs++
		}
	}
	for _, point := range trend {
		o.Trend = append(o.Trend, *point)
	}
	sort.Slice(o.Trend, func(i, j int) bool { return o.Trend[i].Period < o.Trend[j].Period })
	if len(o.Trend) > query.Limit {
		o.Trend = o.Trend[len(o.Trend)-query.Limit:]
	}
	return o, nil
}
func remoteDate(value string, location *time.Location) (time.Time, bool) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.In(location), true
	}
	// ECMAScript treats date-only as UTC and zone-less date-times as local.
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.In(location), true
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04"} {
		if parsed, err := time.ParseInLocation(layout, value, location); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
