package desktop

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func dashboardFixture() *domain.DashboardOverview {
	return &domain.DashboardOverview{
		Source: "local", Range: "7d", Metrics: domain.DashboardMetrics{TotalJobs: 14, ActiveJobs: 7, CompletedJobs: 4, FailedJobs: 3, MediaDurationMinutes: 28, ProcessingDurationSeconds: 120, TranscriptReadyJobs: 4, SpeakerReadyJobs: 2, SpeakerEligibleJobs: 3, SummaryReadyJobs: 1},
		Trend: []domain.DashboardTrend{
			{Period: "2026-11-17", TotalJobs: 2, MediaDurationMinutes: 3, ProcessingDurationSeconds: 29},
			{Period: "2026-11-18", TotalJobs: 12, MediaDurationMinutes: 25, ProcessingDurationSeconds: 91},
			{Period: "2026-11-19"},
		},
		Resources: domain.DashboardResources{AIModels: 2, EnabledAIModels: 1, Templates: 3, Members: 4},
		Companion: &domain.DashboardCompanion{Name: "Synthetic companion", Level: 30, CurrentLevelExperience: 10, NextLevelExperience: 60, LevelProgressPercent: 17, LPBalance: 100, ClaimableActivities: 2},
	}
}

func TestDashboardResourceLinksRespectNavigationAndRemoteSnapshot(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := NewDashboardView(&client.DashboardController{Overview: dashboardFixture()}, locale)
			view.section = "insights"
			opened := ""
			view.Navigate = func(path string) { opened = path }
			tester := ui.NewTester(view.View, 1440, 1200)
			links := []struct{ zh, en, path string }{
				{"本地运行时", "Local runtime", "/settings"}, {"AI 模型", "AI models", "/models"}, {"总结模板", "Templates", "/templates"}, {"人员库", "Members", "/members"},
				{"查看伙伴", "Open companion", "/pet"}, {"LP", "LP", "/pet-store"}, {"今日签到", "Check-in", "/daily-check-in"}, {"待处理", "Needs action", "/work-market"},
			}
			for _, link := range links {
				label := localized(locale, link.zh, link.en)
				access, ok := tester.Access(label)
				if !ok || access.Role != ui.RoleButton || access.Disabled {
					t.Fatalf("shortcut lacks actionable semantics: %s %+v", label, access)
				}
				if err := tester.Click(label); err != nil || opened != link.path {
					t.Fatalf("shortcut route mismatch: %s -> %s (%v)", label, opened, err)
				}
			}
			if !tester.HasText(localized(locale, "待配置", "Needs setup")) {
				t.Fatal("runtime readiness missing")
			}
			view.RuntimeReady = true
			tester.Frame()
			if !tester.HasText(localized(locale, "已就绪", "Ready")) || tester.HasText(localized(locale, "待配置", "Needs setup")) {
				t.Fatal("runtime readiness did not update")
			}
			runtimeLink, _ := tester.Access(localized(locale, "本地运行时", "Local runtime"))
			if !strings.Contains(runtimeLink.Description, localized(locale, "已就绪", "Ready")) {
				t.Fatal("runtime status unavailable to screen readers", runtimeLink)
			}
			view.CanNavigate = func(path string) bool { return path != "/models" }
			tester.Frame()
			assertDisabled(t, tester, localized(locale, "AI 模型", "AI models"), true)
			view.Navigate = nil
			tester.Frame()
			for _, link := range links {
				assertDisabled(t, tester, localized(locale, link.zh, link.en), true)
			}
			view.Client.Overview.Source = "remote"
			tester.Frame()
			if !tester.HasText(localized(locale, "远端任务快照", "Remote snapshot")) || tester.HasText(localized(locale, "处理资源", "Processing resources")) || tester.HasText(localized(locale, "伙伴与福利", "Companion & benefits")) {
				t.Fatal("remote dashboard exposes local-only insights")
			}
			for _, link := range links {
				if access, ok := tester.Access(localized(locale, link.zh, link.en)); ok && access.Role == ui.RoleButton {
					t.Fatal("remote snapshot retained a local shortcut", link.path)
				}
			}
		})
	}
}

func TestDashboardUsesVerifiedWorkspaceRuntimeReadiness(t *testing.T) {
	controller := &client.Controller{}
	runtimeClient := &client.RuntimeController{Panel: &domain.RuntimePanel{}}
	workspace := Workspace{
		tab: "/", controller: controller, view: NewMeetings(controller, "en-US"), preferences: domain.DefaultUIPreferences(),
		dashboard: NewDashboardView(&client.DashboardController{Overview: dashboardFixture()}, "en-US"), runtime: &RuntimeView{Client: runtimeClient},
	}
	workspace.dashboard.section = "insights"
	tester := ui.NewTester(workspace.View, 1440, 1200)
	if workspace.dashboard.RuntimeReady {
		t.Fatal("missing runtime reported ready")
	}
	runtimeClient.Panel.Overview.ShellReady = true
	tester.Frame()
	if !workspace.dashboard.RuntimeReady {
		t.Fatal("verified runtime status not propagated to dashboard")
	}
	runtimeClient.Error = errors.New("synthetic unavailable runtime")
	tester.Frame()
	if workspace.dashboard.RuntimeReady {
		t.Fatal("failed refresh retained ready status")
	}
	runtimeClient.Error, runtimeClient.Panel = nil, nil
	tester.Frame()
	if workspace.dashboard.RuntimeReady {
		t.Fatal("removed runtime retained ready status")
	}
}

func TestDashboardActivityActionsUseOverviewSource(t *testing.T) {
	for _, source := range []string{"local", "remote"} {
		t.Run(source, func(t *testing.T) {
			overview := &domain.DashboardOverview{
				Source:        source,
				AttentionJobs: []domain.DashboardJob{{ID: "active-id", Title: "Active meeting", OverallStatus: "transcribing"}},
				RecentResults: []domain.DashboardJob{{ID: "result-id", Title: "Completed meeting", OverallStatus: "completed"}},
			}
			view := NewDashboardView(&client.DashboardController{Overview: overview}, "en-US")
			if source == "remote" {
				view.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read", "jobs.result.read"}}
			}
			var detailSource, detailID, resultSource, resultID string
			view.OpenDetailFromSource = func(source, id string) { detailSource, detailID = source, id }
			view.OpenResultFromSource = func(source, id, _ string) { resultSource, resultID = source, id }
			tester := ui.NewTester(view.View, 1440, 1200)
			if err := tester.Click("Details: Active meeting"); err != nil {
				t.Fatal(err)
			}
			if detailSource != source || detailID != "active-id" {
				t.Fatalf("dashboard detail escaped its snapshot source: %q/%q", detailSource, detailID)
			}
			if err := tester.Click("View result: Completed meeting"); err != nil {
				t.Fatal(err)
			}
			if resultSource != source || resultID != "result-id" {
				t.Fatalf("dashboard result escaped its snapshot source: %q/%q", resultSource, resultID)
			}
		})
	}
}

func TestDashboardResponsiveInsightsAndTrendGeometry(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1440, 1100, 980, 760} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				view := NewDashboardView(&client.DashboardController{Overview: dashboardFixture()}, locale)
				view.section, view.Navigate = "insights", func(string) {}
				shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
				tester := ui.NewTesterWith(shell.View, width, 1800, ui.TesterOptions{Dark: locale == "en-US"})
				for _, label := range []string{localized(locale, "本地运行时", "Local runtime"), localized(locale, "AI 模型", "AI models"), localized(locale, "人员库", "Members"), "LP", localized(locale, "今日签到", "Check-in"), localized(locale, "待处理", "Needs action")} {
					access, ok := ui.AccessInfo{}, false
					for _, candidate := range tester.AccessAll() {
						if candidate.Role == ui.RoleButton && candidate.Name == label && candidate.Bounds.X >= presentation.SidebarWidth {
							access, ok = candidate, true
							break
						}
					}
					if !ok || access.Offscreen || access.Bounds.W <= 25 || access.Bounds.X < presentation.SidebarWidth || access.Bounds.X+access.Bounds.W > float32(width-12) {
						t.Fatalf("dashboard shortcut escaped content: %s %+v", label, access)
					}
				}
				trend, ok := tester.Find(localized(locale, "工作量趋势", "Workload trend"))
				coverage, coverageOK := tester.Find(localized(locale, "结果完善度", "Result completeness"))
				if !ok || !coverageOK || width > 980 && math.Abs(float64(trend.Y-coverage.Y)) > 12 || width <= 980 && coverage.Y < trend.Y+174 {
					t.Fatalf("analysis grid ignored its responsive breakpoint: trend=%+v coverage=%+v", trend, coverage)
				}
				unit := localized(locale, "个", "jobs")
				for index, point := range view.Client.Overview.Trend {
					bar, ok := tester.Find(fmt.Sprintf("%s: %d %s", point.Period, point.TotalJobs, unit))
					if !ok || bar.W > 24.1 || point.TotalJobs > 0 && math.Abs(float64(bar.W-24)) > .1 || bar.H > 132.1 || bar.X+bar.W > coverage.X && width > 980 {
						t.Fatalf("trend bar lost its size bounds: %+v", bar)
					}
					expectedHeight := float32(point.TotalJobs) * 132 / 12
					if math.Abs(float64(bar.H-expectedHeight)) > .1 || index == 2 && bar.H != 0 {
						t.Fatalf("trend bar misrepresents stored units: %+v expected %.1f", bar, expectedHeight)
					}
				}
				tester.SetSize(width, 960)
				tester.Frame()
				captureParity(t, tester, fmt.Sprintf("dashboard-%s-%d", locale, width))
			})
		}
	}
}

func TestDashboardTrendUnitsAndSinglePointWidth(t *testing.T) {
	view := NewDashboardView(&client.DashboardController{}, "en-US")
	points := []domain.DashboardTrend{{Period: "2026-11-19", TotalJobs: 1000, MediaDurationMinutes: 40, ProcessingDurationSeconds: 91}}
	tester := ui.NewTester(func(c *ui.Context) { view.chart(c, points) }, 600, 200)
	for _, metric := range []struct{ key, value string }{{"jobs", "1000 jobs"}, {"media", "40 min"}, {"processing", "2 min"}} {
		view.metric = metric.key
		tester.Frame()
		access, ok := tester.AccessByRole(ui.RoleImage, "")
		if !ok || !strings.Contains(access.Name, metric.value) {
			t.Fatal("trend lacks an accurate readable data description", access)
		}
		bar, ok := tester.Find("2026-11-19: " + metric.value)
		if !ok || math.Abs(float64(bar.W-24)) > .1 || math.Abs(float64(bar.H-132)) > .1 {
			t.Fatal("single data point expanded to the whole plot", bar)
		}
	}
	points = nil
	tester.Frame()
	if !tester.HasText("No trend data in this range.") {
		t.Fatal("empty chart lacks its explanation")
	}
}

func TestTitlebarOpensProjectAndTogglesCompanion(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences()}
		shell.TogglePet = func() { shell.PetEnabled = !shell.PetEnabled }
		tester := ui.NewTesterWith(shell.View, 1180, 800, ui.TesterOptions{Dark: locale == "zh-CN"})
		for _, source := range []string{"local", "remote"} {
			shell.Source, shell.SourceReady = source, false
			tester.Frame()
			ready := presentation.Message(locale, "shell."+source+"Ready")
			if tester.HasText(ready) {
				t.Fatal("unverified source marked ready", source)
			}
			shell.SourceReady = true
			tester.Frame()
			if !tester.HasText(ready) {
				t.Fatal("missing source status", source)
			}
		}
		petToggle := presentation.Message(locale, "shell.statusActions.togglePet")
		if err := tester.Click(petToggle); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
		toggle, ok := tester.AccessByRole(ui.RoleToggleButton, petToggle)
		if !ok || !toggle.Checked || !tester.HasText(presentation.Message(locale, "shell.petEnabled")) {
			t.Fatal("missing enabled desktop pet state", toggle)
		}
		image := tester.Image()
		background := image.At(5, int(toggle.Bounds.Y+1))
		if actual := image.At(int(toggle.Bounds.X+1), int(toggle.Bounds.Y+1)); actual != background {
			t.Fatalf("enabled desktop pet uses selected fill: got %v, titlebar background is %v", actual, background)
		}
		captureParity(t, tester, "titlebar-pet-on-"+locale)
		tester.Key(0, ui.KeyEnter)
		tester.Frame()
		if toggle, ok := tester.AccessByRole(ui.RoleToggleButton, petToggle); !ok || toggle.Checked || !tester.HasText(presentation.Message(locale, "shell.petDisabled")) {
			t.Fatal("keyboard activation did not turn off the desktop pet", toggle)
		}
		if err := tester.Click(presentation.Message(locale, "shell.github")); err != nil {
			t.Fatal(err)
		}
		if opened := tester.OpenedURLs(); len(opened) != 1 || opened[0] != presentation.RepositoryURL {
			t.Fatal("unexpected project link")
		}
	}
}

func TestDashboardProjectionAndResultAction(t *testing.T) {
	o := &domain.DashboardOverview{
		Range: "7d", Metrics: domain.DashboardMetrics{TotalJobs: 14, ActiveJobs: 7, CompletedJobs: 4, FailedJobs: 3, MediaDurationMinutes: 28, ProcessingDurationSeconds: 120},
		Trend:         []domain.DashboardTrend{{Period: "2026-11", TotalJobs: 14, CompletedJobs: 4, FailedJobs: 3}},
		AttentionJobs: []domain.DashboardJob{{ID: "job-1700000000000-1", Title: "Needs review", OverallStatus: "failed"}},
		RecentResults: []domain.DashboardJob{{ID: "job-1700000000000-0", Title: "Synthetic result", OverallStatus: "completed"}},
		Resources:     domain.DashboardResources{AIModels: 2, EnabledAIModels: 1, Templates: 3, Members: 4},
		Companion:     &domain.DashboardCompanion{Name: "Synthetic pet", Level: 30, CurrentLevelExperience: 10, NextLevelExperience: 60, LevelProgressPercent: 17, LPBalance: 100},
	}
	v := NewDashboardView(&client.DashboardController{Overview: o}, "en-US")
	opened := ""
	v.OpenResult = func(id, locale string) { opened = id + ":" + locale }
	tt := ui.NewTester(v.View, 1180, 800)
	for _, text := range []string{"Meeting jobs", "14", "Media duration", "Completion rate", "AI summary coverage", "2 min processing time", "Recent jobs"} {
		if !tt.HasText(text) {
			t.Errorf("missing dashboard content: %s", text)
		}
	}
	if err := tt.Click("View result: Synthetic result"); err != nil {
		t.Fatal(err)
	}
	if opened != "job-1700000000000-0:en-US" {
		t.Fatal("result action not scoped", opened)
	}
	if _, ok := tt.Access("Statistics range"); !ok {
		t.Fatal("date filter lacks accessible name")
	}
	if err := tt.Click("Analysis overview"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, text := range []string{"Workload trend", "Result completeness", "2026-11", "Synthetic pet", "1 / 2 enabled", "10 / 60 EXP"} {
		if !tt.HasText(text) {
			t.Errorf("missing insights: %s", text)
		}
	}
	v.Locale = "zh-CN"
	tt.Frame()
	if !tt.HasText("工作量趋势") || !tt.HasText("伙伴与福利") {
		t.Fatal("Chinese dashboard missing")
	}
}
