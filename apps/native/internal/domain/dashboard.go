package domain

import (
	"errors"
	"math"
	"time"
)

var ErrDashboardRange = errors.New("dashboard_range_invalid")

type DashboardQuery struct {
	Range       string
	StartAt     *string
	Granularity string
	Limit       int
}

// DashboardPeriod uses calendar days in the user's local zone, matching the
// existing repository. Subtracting multiples of 24h is incorrect around DST.
func DashboardPeriod(key string, now time.Time) (DashboardQuery, error) {
	query := DashboardQuery{Range: key, Granularity: "day", Limit: 31}
	days := 0
	switch key {
	case "all":
		query.Granularity, query.Limit = "month", 36
		return query, nil
	case "today":
		query.Granularity, query.Limit = "hour", 24
	case "7d":
		days = 6
	case "30d":
		days = 29
	default:
		return query, ErrDashboardRange
	}
	date := now.AddDate(0, 0, -days)
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, now.Location())
	// Rust chrono's LocalResult::single rejects skipped and repeated midnight.
	if start.Day() != date.Day() || start.Hour() != 0 {
		return query, ErrDashboardRange
	}
	_, offset := start.Zone()
	zoneStart, zoneEnd := start.ZoneBounds()
	for _, neighbor := range []time.Time{zoneStart.Add(-time.Second), zoneEnd} {
		if neighbor.IsZero() || neighbor.Year() < 1 {
			continue
		}
		_, otherOffset := neighbor.In(now.Location()).Zone()
		candidate := start.Add(time.Duration(offset-otherOffset) * time.Second)
		if !candidate.Equal(start) && candidate.Year() == start.Year() && candidate.Month() == start.Month() && candidate.Day() == start.Day() && candidate.Hour() == 0 && candidate.Minute() == 0 && candidate.Second() == 0 {
			return query, ErrDashboardRange
		}
	}
	stamp := start.UTC().Format("2006-01-02T15:04:05.000Z")
	query.StartAt = &stamp
	return query, nil
}

type DashboardOverview struct {
	// Source binds dashboard links to the snapshot even while mode changes.
	// It is runtime-only and does not alter the persisted compatibility DTO.
	Source           string              `json:"-"`
	Range            string              `json:"range"`
	TrendGranularity string              `json:"trendGranularity"`
	Metrics          DashboardMetrics    `json:"metrics"`
	Trend            []DashboardTrend    `json:"trend"`
	AttentionJobs    []DashboardJob      `json:"attentionJobs"`
	RecentResults    []DashboardJob      `json:"recentResults"`
	Resources        DashboardResources  `json:"resources"`
	Companion        *DashboardCompanion `json:"companion"`
}
type DashboardMetrics struct {
	TotalJobs                 int64 `json:"totalJobs"`
	MediaDurationMinutes      int64 `json:"mediaDurationMinutes"`
	ProcessingDurationSeconds int64 `json:"processingDurationSeconds"`
	ActiveJobs                int64 `json:"activeJobs"`
	CompletedJobs             int64 `json:"completedJobs"`
	FailedJobs                int64 `json:"failedJobs"`
	TranscriptReadyJobs       int64 `json:"transcriptReadyJobs"`
	SpeakerEligibleJobs       int64 `json:"speakerEligibleJobs"`
	SpeakerReadyJobs          int64 `json:"speakerReadyJobs"`
	SummaryReadyJobs          int64 `json:"summaryReadyJobs"`
	ExportedJobs              int64 `json:"exportedJobs"`
	WarningJobs               int64 `json:"warningJobs"`
}
type DashboardTrend struct {
	Period                    string `json:"period"`
	TotalJobs                 int64  `json:"totalJobs"`
	CompletedJobs             int64  `json:"completedJobs"`
	FailedJobs                int64  `json:"failedJobs"`
	MediaDurationMinutes      int64  `json:"mediaDurationMinutes"`
	ProcessingDurationSeconds int64  `json:"processingDurationSeconds"`
}
type DashboardJob struct {
	ID                string  `json:"id"`
	Title             string  `json:"title"`
	CreatedAt         string  `json:"createdAt"`
	DurationMinutes   int64   `json:"durationMinutes"`
	OverallStatus     string  `json:"overallStatus"`
	DiarizationStatus string  `json:"diarizationStatus"`
	WarningCount      int     `json:"warningCount"`
	HasSummary        bool    `json:"hasSummary"`
	LastExportedAt    *string `json:"lastExportedAt"`
}
type DashboardResources struct {
	AIModels        int64 `json:"aiModels"`
	EnabledAIModels int64 `json:"enabledAiModels"`
	Templates       int64 `json:"templates"`
	Members         int64 `json:"members"`
}
type DashboardCompanion struct {
	Name                   string `json:"name"`
	Level                  int64  `json:"level"`
	CurrentLevelExperience int64  `json:"currentLevelExperience"`
	NextLevelExperience    int64  `json:"nextLevelExperience"`
	LevelProgressPercent   int64  `json:"levelProgressPercent"`
	LPBalance              int64  `json:"lpBalance"`
	CheckedInToday         bool   `json:"checkedInToday"`
	ClaimableActivities    int64  `json:"claimableActivities"`
}

// PetLevelRequirement follows the production piecewise linear growth curve.
func PetLevelRequirement(level int64) int64 {
	level = min(max(level, 1), 255)
	if level == 255 {
		return 0
	}
	for _, r := range [][4]int64{{1, 10, 15, 30}, {11, 30, 35, 60}, {31, 60, 70, 110}, {61, 100, 120, 180}, {101, 140, 200, 280}, {141, 180, 300, 400}, {181, 220, 430, 560}, {221, 254, 600, 800}} {
		if level <= r[1] {
			return int64(math.Round(float64(r[2]) + float64(r[3]-r[2])*float64(level-r[0])/float64(r[1]-r[0])))
		}
	}
	return 0
}

func (c *DashboardCompanion) SetExperience(experience int64) {
	remaining := max(experience, 0)
	c.Level = 1
	for c.Level < 255 {
		required := PetLevelRequirement(c.Level)
		if remaining < required {
			break
		}
		remaining -= required
		c.Level++
	}
	c.CurrentLevelExperience, c.NextLevelExperience = remaining, PetLevelRequirement(c.Level)
	c.LevelProgressPercent = 100
	if c.NextLevelExperience > 0 {
		c.LevelProgressPercent = int64(math.Round(float64(remaining) / float64(c.NextLevelExperience) * 100))
	}
}
