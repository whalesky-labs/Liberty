package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnostics"
)

type queueReadReply struct {
	page domain.JobPage
	err  error
}

type queueReadService struct {
	replies chan queueReadReply
	queries chan domain.QueueQuery
}

func (service queueReadService) Queue(ctx context.Context, query domain.QueueQuery) (domain.JobPage, error) {
	service.queries <- query
	select {
	case reply := <-service.replies:
		return reply.page, reply.err
	case <-ctx.Done():
		return domain.JobPage{}, ctx.Err()
	}
}

func (queueReadService) Result(context.Context, string) (domain.Result, error) {
	return domain.Result{}, errors.New("unused synthetic detail read")
}

func TestQueueReadStatesAreExclusiveAndKeyboardRetryReadsOnce(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{1100, 1440} {
				t.Run(fmt.Sprintf("%s/dark=%v/width=%d", locale, dark, width), func(t *testing.T) {
					callbacks := make(chan func(), 8)
					service := queueReadService{replies: make(chan queueReadReply, 2), queries: make(chan domain.QueueQuery, 4)}
					controller := client.New(service, func(callback func()) { callbacks <- callback }, func() {})
					defer controller.Close()
					view := NewMeetings(controller, locale)
					shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View, Route: "/jobs"}
					tester := ui.NewTesterWith(shell.View, width, 760, ui.TesterOptions{Dark: dark})
					empty := view.text("还没有会议任务", "No meeting jobs yet")
					loading := view.text("正在读取…", "Loading…")
					failure := view.text("任务读取失败，请重试。", "Could not read jobs. Please retry.")
					retry := presentation.Message(locale, "common.retry")
					view.refresh()
					tester.Frame()
					status, ok := tester.AccessByRole(ui.RoleStatus, loading)
					if !ok || !status.Busy || tester.HasText(empty) || tester.HasText(failure) {
						t.Error("pending queue read lacks an exclusive, busy loading status")
					}
					service.replies <- queueReadReply{err: errors.New("synthetic queue read failure")}
					flushView(t, callbacks, tester)
					if tester.HasText(empty) || tester.HasText(view.msg("emptyTitle")) || tester.HasText(loading) {
						t.Error("failed queue read falsely reports a loading or successful empty-list state")
					}
					if alert, ok := tester.AccessByRole(ui.RoleAlert, failure); !ok || alert.Offscreen {
						t.Error("queue read failure is absent from the visible accessibility alert tree")
					}
					for index := 0; index < 30 && !tester.Focused(retry); index++ {
						tester.Key(0, ui.KeyTab)
					}
					if !tester.Focused(retry) {
						t.Fatal("keyboard cannot reach the queue read retry")
					}
					tester.Key(0, ui.KeyEnter)
					tester.Key(0, ui.KeyEnter)
					if tester.HasText(failure) || tester.HasText(empty) {
						t.Error("retry retained a failure or empty-state claim while loading")
					}
					service.replies <- queueReadReply{page: domain.QueryJobs(nil, domain.QueueQuery{Source: "local"})}
					flushView(t, callbacks, tester)
					if len(service.queries) != 2 || controller.ListError != nil || !tester.HasText(empty) || tester.HasText(loading) || tester.HasText(failure) {
						t.Fatal("keyboard retry did not recover with exactly one fresh read and a distinct empty state")
					}
				})
			}
		}
	}
}

func TestQueueEmptyStateActions(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 8)
			service := queueReadService{replies: make(chan queueReadReply, 1), queries: make(chan domain.QueueQuery, 1)}
			controller := client.New(service, func(f func()) { callbacks <- f }, func() {})
			defer controller.Close()
			view := NewMeetings(controller, locale)
			created := 0
			view.NewJob = func() { created++ }
			shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View, Route: "/jobs"}
			tester := ui.NewTesterWith(shell.View, 1440, 960, ui.TesterOptions{Dark: locale == "zh-CN"})
			empty := view.text("还没有会议任务", "No meeting jobs yet")
			heading, ok := tester.AccessByRole(ui.RoleHeading, empty)
			action, found := tester.AccessByRole(ui.RoleButton, view.text("新建第一个任务", "Create first job"))
			if !ok || !found || action.Offscreen || action.Bounds.Y <= heading.Bounds.Y {
				t.Fatalf("first job action should be visible below the empty state heading: heading=%+v action=%+v", heading, action)
			}
			captureParity(t, tester, "queue-empty-"+locale)
			tester.ClickAt(action.Bounds.X+action.Bounds.W/2, action.Bounds.Y+action.Bounds.H/2)
			if created != 1 {
				t.Fatal("first job action did not open the creation flow")
			}

			view.status, view.query, view.page = "failed", "sample", 2
			tester.Frame()
			if !tester.HasText(view.msg("emptyTitle")) || tester.HasText(empty) {
				t.Fatal("filtered empty queue needs its own message")
			}
			if err := tester.Click(view.text("清除筛选", "Clear filters")); err != nil {
				t.Fatal(err)
			}
			want := domain.QueueQuery{Source: "local", Filter: "all", Page: 1}
			service.replies <- queueReadReply{page: domain.QueryJobs(queueParityJobs("local", 1), want)}
			flushView(t, callbacks, tester)
			if got := <-service.queries; got != want || !tester.HasText("Synthetic meeting 00") {
				t.Fatalf("clear filters did not restore the complete list: %+v", got)
			}
		})
	}
}

func TestQueueUsesCenteredFixedContentWidth(t *testing.T) {
	view := NewMeetings(&client.Controller{ResultController: &client.ResultController{}}, "en-US")
	shell := Shell{Locale: "en-US", Preferences: domain.DefaultUIPreferences(), Content: view.View, Route: "/jobs"}
	contentLeft := func(width int) float32 {
		tester := ui.NewTesterWith(shell.View, width, 960, ui.TesterOptions{})
		heading, ok := tester.AccessByRole(ui.RoleHeading, view.msg("queueTitle"))
		if !ok || heading.Offscreen {
			t.Fatalf("queue heading is missing at width %d: %+v", width, heading)
		}
		return heading.Bounds.X
	}

	mediumWidth, wideWidth := 1640, 1920
	mediumLeft, wideLeft := contentLeft(mediumWidth), contentLeft(wideWidth)
	wantShift := float64(wideWidth-mediumWidth) / 2
	if shift := float64(wideLeft - mediumLeft); math.Abs(shift-wantShift) > 1 {
		t.Fatalf("queue content did not stay centered at its fixed maximum width: medium=%v wide=%v shift=%.1f want=%.1f", mediumLeft, wideLeft, shift, wantShift)
	}
}

func TestQueueFailureReasonTooltipIsSafeAndKeyboardAccessible(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, source := range []string{"local", "remote"} {
				t.Run(fmt.Sprintf("%s/dark=%v/%s", locale, dark, source), func(t *testing.T) {
					reason := localized(locale, "合成任务失败：请检查语音运行时。", "Synthetic job failed: check the speech runtime.")
					sensitive := strings.Join([]string{"synthetic", "sensitive", "value"}, "-")
					reason += " api_key=" + sensitive + ` /Synthetic/private.wav C:\Synthetic Folder\private.wav`
					safe := diagnostics.Text(reason, 4096)
					job := queueParityJobs(source, 1)[0]
					job.OverallStatus, job.FailureReason = "failed", &reason
					page := domain.QueryJobs([]domain.JobSummary{job}, domain.QueueQuery{Source: source})
					view := NewMeetings(&client.Controller{State: client.State{Jobs: page.Items, Page: page}, ResultController: &client.ResultController{}}, locale)
					view.Source, view.OpenDetail = source, func(string) {}
					view.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.read"}}
					shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View, Route: "/jobs"}
					tester := ui.NewTesterWith(shell.View, 1100, 760, ui.TesterOptions{Dark: dark})
					table, ok := tester.AccessByRole(ui.RoleTable, view.msg("queueTitle"))
					if !ok {
						t.Fatal("queue table missing")
					}
					action, _ := tester.Find(view.msg("details") + ": " + job.Title)

					var status ui.Rect
					for _, access := range tester.AccessAll() {
						if access.Role == ui.RoleCell && access.RowIndex == 2 && access.ColumnIndex == 2 {
							status = access.Bounds
						}
					}
					if status.W <= 0 || status.H <= 0 {
						t.Fatal("failed status is not visible beside the task title", status)
					}
					tester.Move(status.X+24, status.Y+status.H/2)
					tester.Advance(time.Second)
					if !tester.HasText(safe) {
						t.Error("hovering the failed status lost the legacy failure reason")
					}
					if source == "local" && dark == (locale == "en-US") {
						captureParity(t, tester, "queue-safe-failure-"+locale)
					}
					tester.Key(0, ui.KeyEscape)
					if tester.HasText(safe) {
						t.Error("Escape did not dismiss the failed-status tooltip")
					}
					tester.Scroll(table.Bounds.X+200, action.Y+10, 1100, 0)
					tester.Frame()
					diarization, _ := tester.Find(view.diarization(job))
					tester.Move(diarization.X+diarization.W/2, diarization.Y+diarization.H/2)
					tester.Advance(time.Second)
					if _, ok := tester.AccessByRole(ui.RoleTooltip, ""); !ok {
						t.Fatal("the existing diarization tooltip did not appear under the same hover clock")
					}
					tester.Scroll(table.Bounds.X+200, action.Y+10, -1100, 0)
					tester.Frame()
					tester.Move(1090, 740)
					focused := func() bool {
						status, ok := tester.AccessByRole(ui.RoleGroup, statusText(locale, "failed"))
						return ok && status.Focused && status.Description == safe
					}
					for index := 0; index < 35 && !focused(); index++ {
						tester.Key(0, ui.KeyTab)
					}
					if !focused() || !tester.HasText(safe) {
						t.Error("Tab cannot expose the same full failure reason")
					}
					for _, access := range tester.AccessAll() {
						if strings.Contains(access.Name+access.Value+access.Description, sensitive) || strings.Contains(access.Name+access.Value+access.Description, "private.wav") {
							t.Fatal("failure reason leaked synthetic secret or path into the accessibility tree")
						}
					}
					job.FailureReason = nil
					page.Items[0] = job
					view.Client.Jobs = page.Items
					tester.Frame()
					if tester.HasText(safe) {
						t.Fatal("failure reason tooltip survived a changed queue projection")
					}
				})
			}
		}
	}
}

func queueParityJobs(source string, count int) []domain.JobSummary {
	jobs := make([]domain.JobSummary, count)
	for index := range jobs {
		progress := float64(index * 7)
		jobs[index] = domain.JobSummary{
			ID: fmt.Sprintf("synthetic-%02d", index), Title: fmt.Sprintf("Synthetic meeting %02d", index), Source: source,
			CreatedAt: fmt.Sprintf("2026-11-%02dT10:30:00Z", index+1), OverallStatus: []string{"completed", "failed", "transcribing", "queued"}[index%4],
			DurationMinutes: 42, EnableSpeaker: true, DiarizationStatus: "legacy_unverified", ProgressPercent: &progress,
			SourceFiles: []domain.SourceFile{{Name: fmt.Sprintf("synthetic-recording-%02d.wav", index)}},
		}
	}
	return jobs
}

func TestQueueTableExposesRowsColumnsAndFixedActions(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			page := domain.QueryJobs(queueParityJobs("local", 20), domain.QueueQuery{Source: "local", Page: 2})
			view := NewMeetings(&client.Controller{State: client.State{Jobs: page.Items, Page: page}, ResultController: &client.ResultController{}}, locale)
			view.OpenDetail, view.OpenResult = func(string) {}, func(string, string) {}
			tester := ui.NewTester(view.View, 1100, 1200)
			table, ok := tester.AccessByRole(ui.RoleTable, view.msg("queueTitle"))
			if !ok || table.ColumnCount != 10 || table.RowCount != page.Total+1 {
				t.Fatalf("queue lacks complete table dimensions: %+v", table)
			}
			for index, column := range queueColumns {
				header, ok := tester.AccessByRole(ui.RoleColumnHeader, view.msg(column.key))
				if !ok || header.ColumnIndex != index+1 || header.RowIndex != 1 {
					t.Fatalf("column header missing index: %s %+v", column.key, header)
				}
			}
			header, ok := tester.AccessByRole(ui.RoleColumnHeader, view.msg("colActions"))
			if !ok || header.ColumnIndex != 10 || header.Offscreen {
				t.Fatalf("fixed action header is not accessible: %+v", header)
			}
			for index, job := range page.Items {
				row, ok := tester.AccessByRole(ui.RoleRow, job.Title)
				if !ok || row.RowIndex != page.From+index+1 {
					t.Fatalf("page-relative row index lost: %+v", row)
				}
			}
			cells := map[int]int{}
			for _, node := range tester.AccessAll() {
				if node.Role == ui.RoleCell {
					if node.RowIndex < page.From+1 || node.RowIndex > page.To+1 || node.ColumnIndex < 1 || node.ColumnIndex > 10 {
						t.Fatal("queue cell lost table position", node)
					}
					cells[node.RowIndex]++
				}
			}
			for index := range page.Items {
				if cells[page.From+index+1] != 10 {
					t.Fatal("queue row is missing logical cells", cells)
				}
			}
			job := page.Items[0]
			label := view.msg("details") + ": " + job.Title
			if job.OverallStatus == "completed" {
				label = view.msg("viewResult") + ": " + job.Title
			}
			before, ok := tester.Find(label)
			if !ok || before.W < 40 {
				t.Fatal("fixed action starts outside viewport", before)
			}
			tester.Scroll(table.Bounds.X+200, before.Y+10, 1100, 0)
			tester.Frame()
			after, ok := tester.Find(label)
			if !ok || math.Abs(float64(before.X-after.X)) > .1 || math.Abs(float64(before.Y-after.Y)) > .1 || math.Abs(float64(before.W-after.W)) > .1 {
				t.Fatalf("action moved with horizontal data scroll: before=%+v after=%+v", before, after)
			}
			if task, ok := tester.Find(view.msg("colTask")); !ok || task.W > .1 {
				t.Fatal("data columns did not scroll while actions stayed fixed", task)
			}
			tester.SetSize(1100, 650)
			tester.Frame()
			tester.Scroll(table.Bounds.X+200, before.Y+10, 0, 2000)
			tester.Frame()
			last := page.Items[len(page.Items)-1]
			lastRow, _ := tester.AccessByRole(ui.RoleRow, last.Title)
			lastLabel := view.msg("details") + ": " + last.Title
			if last.OverallStatus == "completed" {
				lastLabel = view.msg("viewResult") + ": " + last.Title
			}
			lastAction, ok := tester.AccessByRole(ui.RoleButton, lastLabel)
			if !ok || lastRow.Offscreen || lastAction.Offscreen || lastAction.Bounds.Y < lastRow.Bounds.Y || lastAction.Bounds.Y+lastAction.Bounds.H > lastRow.Bounds.Y+lastRow.Bounds.H || math.Abs(float64(lastAction.Bounds.X-before.X)) > .1 {
				t.Fatalf("fixed action detached from its vertically scrolled row: row=%+v action=%+v", lastRow, lastAction)
			}
			captureParity(t, tester, "queue-scrolled-"+locale)
		})
	}
}

func TestQueueLegacyResponsiveControlsRemainReachable(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1440, 1100, 980, 720} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				page := domain.QueryJobs(queueParityJobs("local", 12), domain.QueueQuery{Source: "local"})
				view := NewMeetings(&client.Controller{State: client.State{Jobs: page.Items, Page: page}, ResultController: &client.ResultController{}}, locale)
				view.OpenDetail, view.OpenResult, view.NewJob = func(string) {}, func(string, string) {}, func() {}
				shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
				tester := ui.NewTesterWith(shell.View, width, 960, ui.TesterOptions{Dark: locale == "en-US"})
				search, ok := tester.AccessByRole(ui.RoleTextField, view.msg("searchPlaceholder"))
				if !ok || search.Offscreen || search.Bounds.W < 180 || search.Bounds.X+search.Bounds.W > float32(width-12) {
					t.Fatal("queue search clipped", search)
				}
				filters, _ := tester.Find(view.msg("filterLabel"))
				if width <= 980 && search.Bounds.Y < filters.Y+filters.H || width > 980 && math.Abs(float64(search.Bounds.Y-filters.Y)) > 10 {
					t.Fatalf("queue controls ignored legacy breakpoint: filters=%+v search=%+v", filters, search.Bounds)
				}
				for _, label := range []string{view.msg("newJob"), view.msg("previousPage"), view.msg("nextPage")} {
					button, ok := tester.AccessByRole(ui.RoleButton, label)
					if !ok || button.Offscreen || button.Bounds.W <= 0 || button.Bounds.X+button.Bounds.W > float32(width-12) {
						t.Fatalf("queue control clipped: %s %+v", label, button)
					}
				}
				captureParity(t, tester, fmt.Sprintf("queue-%s-%d", locale, width))
			})
		}
	}
}

func TestQueueActionKeyboardDoesNotSelectAnotherRow(t *testing.T) {
	jobs := queueParityJobs("local", 2)
	callbacks := make(chan func(), 16)
	controller := client.New(viewMeetings{jobs: jobs}, func(callback func()) { callbacks <- callback }, func() {})
	defer controller.Close()
	view := NewMeetings(controller, "en-US")
	opened := ""
	view.OpenDetail = func(id string) { opened = id }
	view.OpenResult = func(id, _ string) { opened = id }
	tester := ui.NewTester(view.View, 1100, 800)
	view.refresh()
	flushView(t, callbacks, tester)
	job := controller.Jobs[0]
	if err := tester.Click(job.Title); err != nil {
		t.Fatal(err)
	}
	flushView(t, callbacks, tester)
	if controller.SelectedID != job.ID {
		t.Fatal("row selection lost")
	}
	tester.Key(0, ui.KeyTab)
	label := view.msg("details") + ": " + job.Title
	if !tester.Focused(label) {
		t.Fatal("Tab did not reach the current row's fixed action")
	}
	tester.Key(0, ui.KeyEnter)
	if opened != job.ID || controller.SelectedID != job.ID {
		t.Fatal("keyboard action opened or selected a different job")
	}
	other := controller.Jobs[1]
	if err := tester.Click(view.msg("viewResult") + ": " + other.Title); err != nil {
		t.Fatal(err)
	}
	if opened != other.ID || controller.SelectedID != job.ID {
		t.Fatal("fixed action bubbled into row selection")
	}
}

func TestProgressBarsExposeBoundedNumericValues(t *testing.T) {
	value := float32(42)
	tester := ui.NewTester(func(c *ui.Context) { progressBar(c, value, "Synthetic progress") }, 300, 30)
	for _, sample := range []struct{ input, expected float32 }{{42, 42}, {-3, 0}, {120, 100}, {float32(math.NaN()), 0}, {float32(math.Inf(1)), 0}} {
		value = sample.input
		tester.Frame()
		progress, ok := tester.AccessByRole(ui.RoleProgress, "Synthetic progress")
		if !ok || progress.Min != 0 || progress.Max != 100 || progress.Now != float64(sample.expected) {
			t.Fatalf("progress lacks finite range semantics: %+v", progress)
		}
	}
}

func TestQueueFixedActionsKeepIndependentRemotePermissions(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		page := domain.QueryJobs(queueParityJobs("remote", 1), domain.QueueQuery{Source: "remote"})
		view := NewMeetings(&client.Controller{State: client.State{Jobs: page.Items, Page: page}, ResultController: &client.ResultController{}}, locale)
		view.Source, view.Actions = "remote", &client.JobActions{}
		view.OpenDetail, view.OpenResult = func(string) { t.Error("unauthorized detail") }, func(string, string) { t.Error("unauthorized result") }
		view.RemoteCapabilities = &domain.RemoteCapabilities{Operations: []string{"jobs.delete"}}
		tester := ui.NewTester(view.View, 1100, 800)
		job := page.Items[0]
		assertDisabled(t, tester, view.msg("viewResult")+": "+job.Title, true)
		if err := tester.Click(view.msg("moreActions") + ": " + job.Title); err != nil {
			t.Fatal("a read-only row disabled its separately authorized actions", err)
		}
		deleteAction, ok := tester.AccessByRole(ui.RoleMenuItem, presentation.Message(locale, "common.delete"))
		if !ok || deleteAction.Disabled {
			t.Fatal("delete capability was incorrectly tied to jobs.read", deleteAction)
		}
		tester.Key(0, ui.KeyEscape)
		page.Items[0].Source, page.Items[0].OverallStatus = "local", "transcribing"
		tester.Frame()
		if _, ok := tester.Access(view.msg("moreActions") + ": " + job.Title); ok {
			t.Fatal("active task exposes a menu containing no available operations")
		}
	}
}

func TestDashboardActivityTableExposesCompleteRowAndCellSemantics(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		overview := dashboardFixture()
		overview.AttentionJobs = []domain.DashboardJob{{ID: "synthetic-active", Title: "Synthetic active", OverallStatus: "transcribing", CreatedAt: "2026-11-19T10:00:00Z"}}
		overview.RecentResults = []domain.DashboardJob{{ID: "synthetic-complete", Title: "Synthetic complete", OverallStatus: "completed", CreatedAt: "2026-11-18T10:00:00Z"}}
		view := NewDashboardView(&client.DashboardController{Overview: overview}, locale)
		tester := ui.NewTester(view.View, 1440, 1000)
		table, ok := tester.AccessByRole(ui.RoleTable, localized(locale, "最近任务", "Recent jobs"))
		if !ok || table.RowCount != 3 || table.ColumnCount != 5 {
			t.Fatal("dashboard table dimensions absent", table)
		}
		counts := map[int]int{}
		headers := 0
		for _, node := range tester.AccessAll() {
			if node.Role == ui.RoleColumnHeader {
				headers++
				if node.RowIndex != 1 || node.ColumnIndex < 1 || node.ColumnIndex > 5 {
					t.Fatal("dashboard header lost row or column position", node)
				}
			}
			if node.Role == ui.RoleCell {
				counts[node.RowIndex]++
				if node.ColumnIndex < 1 || node.ColumnIndex > 5 {
					t.Fatal("dashboard cell lost column position", node)
				}
			}
		}
		if headers != 5 || counts[2] != 5 || counts[3] != 5 || len(counts) != 2 {
			t.Fatalf("dashboard table has disconnected content: headers=%d rows=%v", headers, counts)
		}
	}
}
