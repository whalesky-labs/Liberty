package desktop

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/diagnostics"
)

type Meetings struct {
	Client                   *client.Controller
	Locale                   string
	Source                   string
	query, status            string
	page                     int
	selected                 int
	OpenResult               func(string, string)
	OpenDetail               func(string)
	NewJob                   func()
	OpeningResult, OpenError bool
	detailProgress           client.DetailProgress
	detailLog                detailLogDocument
	Actions                  *client.JobActions
	RemoteCapabilities       *domain.RemoteCapabilities
	confirmLocalRuntime      func() bool
}

func NewMeetings(c *client.Controller, locale string) *Meetings {
	return &Meetings{Client: c, Locale: locale, Source: "local", page: 1, status: "all", selected: -1}
}
func localized(locale, zh, en string) string {
	if locale == "en-US" {
		return en
	}
	return zh
}
func (v *Meetings) text(zh, en string) string { return localized(v.Locale, zh, en) }
func (v *Meetings) msg(key string) string     { return presentation.Message(v.Locale, "jobs."+key) }
func (v *Meetings) format(key string, values map[string]any) string {
	return presentation.Format(v.Locale, "jobs."+key, values)
}
func statusText(locale, status string) string {
	switch status {
	case "idle", "uploaded", "queued", "transcribing", "speaker_processing", "summarizing", "completed", "failed":
		return presentation.Message(locale, "status."+status)
	}
	return status
}
func jobFailureReason(job domain.JobSummary) string {
	if job.FailureReason == nil {
		return ""
	}
	return diagnostics.Text(*job.FailureReason, 4096)
}
func (v *Meetings) refresh() {
	v.Client.Refresh(domain.QueueQuery{Source: v.Source, Search: v.query, Filter: v.status, Page: v.page})
}

// Capabilities are an advisory UI snapshot. The remote transport checks the
// current service capabilities again before every request.
func meetingOperationAvailable(source string, capabilities *domain.RemoteCapabilities, operation string) bool {
	return source == "local" || (source == "remote" && capabilities != nil && capabilities.Supports(operation))
}
func (v *Meetings) CanReadJob(job domain.JobSummary) bool {
	return v.OpenDetail != nil && meetingOperationAvailable(job.Source, v.RemoteCapabilities, "jobs.read")
}
func (v *Meetings) CanReadResult(job domain.JobSummary) bool {
	return v.OpenResult != nil && !v.OpeningResult &&
		meetingOperationAvailable(job.Source, v.RemoteCapabilities, "jobs.read") &&
		meetingOperationAvailable(job.Source, v.RemoteCapabilities, "jobs.result.read")
}
func (v *Meetings) CanRetry(job domain.JobSummary) bool {
	return job.OverallStatus == "failed" && v.Actions != nil && !v.Actions.Busy &&
		meetingOperationAvailable(job.Source, v.RemoteCapabilities, "jobs.retry")
}
func (v *Meetings) retry(job domain.JobSummary) {
	if !v.CanRetry(job) || job.Source == "local" && v.confirmLocalRuntime != nil && !v.confirmLocalRuntime() {
		return
	}
	v.Actions.RetryForSource(job.Source, job.ID)
}
func (v *Meetings) CanDelete(job domain.JobSummary) bool {
	return !domain.IsProcessing(job.OverallStatus) && v.Actions != nil && !v.Actions.Busy &&
		meetingOperationAvailable(job.Source, v.RemoteCapabilities, "jobs.delete")
}
func (v *Meetings) View(c *ui.Context) {
	width := shellContentWidth(c)
	p := palette(c)
	filtered := v.status != "all" || strings.TrimSpace(v.query) != ""
	hasJobs := len(v.Client.Jobs) > 0
	firstJob := !hasJobs && !filtered && !v.Client.LoadingJobs && v.Client.ListError == nil && v.Source == "local" && v.NewJob != nil
	ui.Column(c).Fill().MaxWidth(shellContentMaxWidth).AlignSelf(ui.Center).Padding(12, 8, 16).Gap(16).Children(func() {
		ui.Row(c).FillWidth().MinHeight(40).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, v.msg("queueTitle")).FontSize(26).Role(ui.RoleHeading).Level(1)
			if v.Client.Page.Counts.Total > 0 {
				ui.Text(c, v.format("visibleCount", map[string]any{"visible": v.Client.Page.Total, "total": v.Client.Page.Counts.Total})).FontSize(12).TextColor(c.Theme().TextMuted)
			}
			ui.Row(c).Grow(1)
			if !firstJob {
				v.newJobButton(c, v.msg("newJob"))
			}
		})
		panel := ui.Column(c).FillWidth().MinHeight(0).Radius(12).Border(1, p.Separator).Clip().Background(p.Panel)
		if hasJobs {
			panel.Grow(1)
		}
		panel.Children(func() {
			v.toolbar(c, width)
			separator(c)
			if v.OpenError {
				ui.Text(c, v.text("无法打开结果窗口，请重试。", "Could not open result. Please retry.")).Padding(8, 16).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
			}
			if v.Actions != nil && v.Actions.Error != nil {
				ui.Text(c, v.text("任务操作失败，请重试。", "Job action failed. Please retry.")).Padding(8, 16).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
			}
			if !v.Client.LoadingJobs && v.Client.ListError != nil {
				ui.Column(c).FillWidth().Padding(20).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, v.text("任务读取失败，请重试。", "Could not read jobs. Please retry.")).Role(ui.RoleAlert)
					if textButton(c, presentation.Message(v.Locale, "common.retry")).Clicked() {
						v.refresh()
					}
				})
			}
			if hasJobs {
				v.table(c)
				separator(c)
				v.pagination(c, width)
			} else if v.Client.LoadingJobs {
				ui.Column(c).FillWidth().Height(280).Center().Children(func() {
					ui.Text(c, v.text("正在读取…", "Loading…")).Role(ui.RoleStatus).Busy(true)
				})
			} else if v.Client.ListError == nil {
				v.emptyQueue(c, filtered, firstJob)
			}
		})
	})
}

func (v *Meetings) newJobButton(c *ui.Context, label string) {
	if ui.PrimaryButton(c, label).MinHeight(32).Disabled(v.NewJob == nil || v.Source == "remote").Clicked() {
		v.NewJob()
	}
}

func (v *Meetings) toolbar(c *ui.Context, width float32) {
	counts := v.Client.Page.Counts
	row := ui.Row(c).FillWidth().Padding(12, 14).Gap(12).AlignItems(ui.Center)
	if width < 700 {
		row.Column().AlignItems(ui.Stretch)
	}
	row.Children(func() {
		var filters ui.Element
		if width < 460 {
			filters = ui.ScrollHorizontal(c).FillWidth()
		} else {
			filters = ui.Row(c)
		}
		filters.MinWidth(0).Children(func() {
			values := []string{"all", "processing", "completed", "failed"}
			labels := []string{
				fmt.Sprintf("%s %d", v.msg("filterAll"), counts.Total),
				fmt.Sprintf("%s %d", v.msg("filterProcessing"), counts.Processing),
				fmt.Sprintf("%s %d", v.msg("filterCompleted"), counts.Completed),
				fmt.Sprintf("%s %d", v.msg("filterFailed"), counts.Failed),
			}
			if segmentedControl(c, &v.status, values, labels...).Label(v.msg("filterLabel")).Changed() {
				v.page = 1
				v.refresh()
			}
		})
		if width >= 700 {
			ui.Row(c).Grow(1)
		}
		field := ui.TextInput(c, &v.query).Width(216).MinHeight(32).Placeholder(v.msg("searchPlaceholder")).Label(v.msg("searchPlaceholder"))
		if width < 700 {
			field.FillWidth()
		}
		if field.Changed() {
			v.page = 1
			v.refresh()
		}
	})
}

func (v *Meetings) emptyQueue(c *ui.Context, filtered, firstJob bool) {
	icon := "tray"
	title := v.text("还没有会议任务", "No meeting jobs yet")
	description := v.text("会议任务会显示在这里。", "Meeting jobs will appear here.")
	if firstJob {
		description = v.text("选择会议录音或视频，开始第一次转写。", "Choose a meeting recording or video to start your first transcript.")
	} else if filtered {
		icon, title = "search", v.msg("emptyTitle")
		description = v.text("试试其他关键词，或清除筛选查看全部任务。", "Try a different search or clear the filters to see all jobs.")
	}
	p := palette(c)
	ui.Column(c).FillWidth().MinHeight(280).Padding(24).Gap(12).Center().Children(func() {
		ui.Box(c).Size(52, 52).Radius(14).Background(p.Selected).Center().Children(func() {
			ui.Icon(c, presentation.Icon(icon)).Size(26, 26).TextColor(p.NavAccent)
		})
		ui.Column(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(18).Role(ui.RoleHeading).Level(2)
			ui.Text(c, description).FontSize(13).TextColor(c.Theme().TextMuted)
		})
		if firstJob {
			v.newJobButton(c, v.text("新建第一个任务", "Create first job"))
		} else if filtered && ui.Button(c, v.text("清除筛选", "Clear filters")).Clicked() {
			v.query, v.status, v.page = "", "all", 1
			v.refresh()
		}
	})
}

func (v *Meetings) pagination(c *ui.Context, width float32) {
	p := v.Client.Page
	if p.Total > 0 {
		pagination := ui.Row(c).FillWidth().Padding(8, 14).AlignItems(ui.Center).Gap(12).Label(v.msg("paginationLabel"))
		if width < 460 {
			pagination.Column().AlignItems(ui.Stretch)
		}
		pagination.Children(func() {
			ui.Text(c, v.format("paginationSummary", map[string]any{"from": p.From, "to": p.To, "total": p.Total})).Grow(1).FontSize(14).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
			var buttons ui.Element
			if width < 460 {
				buttons = ui.ScrollHorizontal(c).FillWidth()
			} else {
				buttons = ui.Row(c)
			}
			buttons.Gap(8).AlignItems(ui.Center).Children(func() {
				if textButton(c, v.msg("previousPage")).Disabled(v.Client.LoadingJobs || p.Page <= 1).Clicked() {
					v.page = p.Page - 1
					v.refresh()
				}
				first, last := max(1, p.Page-2), min(p.PageCount, p.Page+2)
				labels := make([]string, 0, last-first+1)
				for page := first; page <= last; page++ {
					labels = append(labels, fmt.Sprint(page))
				}
				selected := p.Page - first
				if ui.Segmented(c, &selected, labels...).Label(v.msg("paginationLabel")).Disabled(v.Client.LoadingJobs).Changed() {
					v.page = first + selected
					v.refresh()
				}
				if textButton(c, v.msg("nextPage")).Disabled(v.Client.LoadingJobs || p.Page >= p.PageCount).Clicked() {
					v.page = p.Page + 1
					v.refresh()
				}
			})
		})
	}
}

var queueColumns = []struct {
	key   string
	width float32
}{
	{"colTask", 248}, {"colStatus", 124}, {"colProgress", 130}, {"colCreatedAt", 136}, {"colDuration", 105}, {"colFiles", 210}, {"colFileCount", 95}, {"colDiarization", 220}, {"colProcessingDuration", 128},
}

func (v *Meetings) table(c *ui.Context) {
	const actionsWidth = 125
	width := float32(actionsWidth)
	for _, column := range queueColumns {
		width += column.width
	}
	first := max(1, v.Client.Page.From)
	total := max(v.Client.Page.Total, first-1+len(v.Client.Jobs))
	contentScroll(c).Grow(1).MinHeight(0).FillWidth().Background(palette(c).Content).Children(func() {
		ui.ScrollHorizontal(c).FillWidth().Shrink(0).Children(func() {
			ui.Column(c).Width(width).Role(ui.RoleTable).Label(v.msg("queueTitle")).RowCount(total + 1).ColumnCount(len(queueColumns) + 1).Children(func() {
				ui.Row(c).Height(44).Shrink(0).AlignItems(ui.Center).Background(palette(c).Subpanel).Role(ui.RoleRow).RowIndex(1).Children(func() {
					for index, column := range queueColumns {
						ui.Text(c, v.msg(column.key)).Width(column.width).Shrink(0).Padding(0, 12).FontSize(14).TextColor(c.Theme().TextMuted).Role(ui.RoleColumnHeader).RowIndex(1).ColumnIndex(index + 1)
					}
					ui.Text(c, v.msg("colActions")).Width(actionsWidth).Height(44).Shrink(0).Padding(12).Background(palette(c).Subpanel).TextColor(c.Theme().TextMuted).Sticky().Right(0).Role(ui.RoleColumnHeader).RowIndex(1).ColumnIndex(10)
				})
				for index, job := range v.Client.Jobs {
					rowIndex := first + index + 1
					row := ui.Row(c.Key(job.ID)).FillWidth().Height(58).Shrink(0).AlignItems(ui.Center).Role(ui.RoleRow).RowIndex(rowIndex).Label(job.Title).Selected(v.Client.SelectedID == job.ID)
					canRead := meetingOperationAvailable(job.Source, v.RemoteCapabilities, "jobs.read")
					if canRead {
						row.Focusable()
						if row.Hovered() {
							row.Background(c.Theme().Accent.Alpha(.05))
						}
					}
					row.Children(func() {
						for column, definition := range queueColumns {
							ui.Row(c.Key(column)).Width(definition.width).FillHeight().Shrink(0).Padding(0, 12).Clip().AlignItems(ui.Center).FontSize(14).Role(ui.RoleCell).RowIndex(rowIndex).ColumnIndex(column + 1).Children(func() { v.cell(c, job, definition.key) })
						}
						ui.Row(c).Width(actionsWidth).FillHeight().Shrink(0).Padding(0, 6).Gap(4).AlignItems(ui.Center).Background(palette(c).Content).Sticky().Right(0).Role(ui.RoleCell).RowIndex(rowIndex).ColumnIndex(10).Children(func() {
							label := v.msg("details")
							enabled := v.CanReadJob(job)
							if job.OverallStatus == "completed" {
								label = v.msg("viewResult")
								enabled = v.CanReadResult(job)
							}
							button := ui.Button(c, label).FontSize(12).Padding(6, 8).Disabled(!enabled).Label(label + ": " + job.Title)
							if job.OverallStatus == "completed" {
								button.Background(c.Theme().Accent).TextColor(c.Theme().AccentText).Border(0, ui.Transparent)
							}
							if button.Clicked() {
								if job.OverallStatus == "completed" {
									v.OpenResult(job.ID, v.Locale)
								} else {
									v.OpenDetail(job.ID)
								}
							}
							if job.OverallStatus == "completed" || job.OverallStatus == "failed" || v.CanDelete(job) {
								ui.ButtonBase(c).Label(v.msg("moreActions") + ": " + job.Title).Padding(3).Children(func() { ui.Text(c, "···") }).PopoverMenuButton(func(menu *ui.Menu) {
									if job.OverallStatus == "completed" && menu.Item(v.msg("details")).Disabled(!v.CanReadJob(job)).Chosen() {
										v.OpenDetail(job.ID)
									}
									if job.OverallStatus == "failed" {
										if menu.Item(presentation.Message(v.Locale, "common.retry")).Disabled(!v.CanRetry(job)).Chosen() {
											v.retry(job)
										}
									}
									if menu.Item(presentation.Message(v.Locale, "common.delete")).Disabled(!v.CanDelete(job)).Chosen() {
										v.Actions.Delete(job.ID, job.Title)
									}
								})
							}
						})
					})
					if canRead && row.Clicked() {
						v.selected = index
						v.Client.Select(job.ID)
					}
					if canRead && row.DoubleClicked() && v.CanReadJob(job) {
						v.OpenDetail(job.ID)
					}
					separator(c)
				}
			})
		})
	})
}
func (v *Meetings) cell(c *ui.Context, job domain.JobSummary, col string) {
	switch col {
	case "colTask":
		mark := "AV"
		if len(job.SourceFiles) > 0 {
			parts := strings.Split(job.SourceFiles[0].Name, ".")
			if len(parts) > 1 {
				ext := strings.TrimSpace(parts[len(parts)-1])
				if len(ext) > 0 && len(ext) <= 4 {
					mark = strings.ToUpper(ext)
				}
			}
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Size(36, 36).Radius(9).Background(palette(c).Selected).AlignItems(ui.Center).Justify(ui.Center).Children(func() { ui.Text(c, mark).TextColor(palette(c).NavAccent) })
			ui.Text(c, job.Title).FontSize(15).SingleLine().Grow(1).MinWidth(0).Tooltip(job.Title)
		})
	case "colFiles":
		names := make([]string, len(job.SourceFiles))
		for i, f := range job.SourceFiles {
			names[i] = f.Name
		}
		text := strings.Join(names, " · ")
		if text == "" {
			text = presentation.Message(v.Locale, "common.noData")
		}
		ui.Text(c, text).SingleLine().Tooltip(text)
	case "colFileCount":
		ui.Text(c, v.format("filesCount", map[string]any{"count": len(job.SourceFiles)})).FontSize(12).TextColor(c.Theme().TextMuted)
	case "colDuration":
		text := v.msg("pending")
		n := int(job.DurationMinutes)
		if n > 0 {
			if n < 60 {
				text = v.format("minutes", map[string]any{"count": n})
			} else if n%60 == 0 {
				text = v.format("hours", map[string]any{"count": n / 60})
			} else {
				text = v.format("hoursMinutes", map[string]any{"hours": n / 60, "minutes": n % 60})
			}
		}
		ui.Text(c, text).TextColor(c.Theme().TextMuted)
	case "colDiarization":
		label := v.diarization(job)
		ui.Text(c, label).FontSize(12).Padding(3, 8).Radius(12).Background(palette(c).Selected).TextColor(palette(c).NavAccent).SingleLine().Tooltip(label)
	case "colCreatedAt":
		ui.Text(c, createdText(v.Locale, job.CreatedAt)).TextColor(c.Theme().TextMuted).SingleLine()
	case "colProcessingDuration":
		text := v.msg("notCompleted")
		if job.ProcessingDurationSeconds != nil {
			n := *job.ProcessingDurationSeconds
			if n >= 3600 {
				text = v.format("processingWithHours", map[string]any{"hours": n / 3600, "minutes": fmt.Sprintf("%02d", (n%3600)/60)})
			} else {
				text = v.format("processingWithMinutes", map[string]any{"minutes": n / 60, "seconds": fmt.Sprintf("%02d", n%60)})
			}
		}
		ui.Text(c, text).TextColor(c.Theme().TextMuted).SingleLine()
	case "colStatus":
		if reason := jobFailureReason(job); job.OverallStatus == "failed" && reason != "" {
			ui.Row(c).Role(ui.RoleGroup).Label(statusText(v.Locale, job.OverallStatus)).Focusable().Tooltip(reason).Children(func() { statusBadge(c, v.Locale, job.OverallStatus) })
		} else {
			statusBadge(c, v.Locale, job.OverallStatus)
		}
	case "colProgress":
		percent := job.ProgressPercent
		if job.OverallStatus == "completed" {
			value := float64(100)
			percent = &value
		}
		if percent == nil || math.IsNaN(*percent) || math.IsInf(*percent, 0) {
			key := "processRunning"
			if job.OverallStatus == "failed" {
				key = "processFailed"
			}
			ui.Text(c, v.msg(key)).TextColor(c.Theme().TextMuted)
		} else {
			n := max(0, min(100, math.Round(*percent)))
			ui.Column(c).Width(76).Children(func() { progressBar(c, float32(n), fmt.Sprintf("%.0f%%", n)) })
			ui.Text(c, fmt.Sprintf(" %.0f%%", n)).FontSize(12)
		}
	}
}
func (v *Meetings) diarization(job domain.JobSummary) string {
	key := "diarizationEnabled"
	if !job.EnableSpeaker || job.DiarizationStatus == "disabled" {
		key = "transcriptOnly"
	} else {
		switch job.DiarizationStatus {
		case "completed":
			key = "diarizationCompleted"
		case "unavailable":
			key = "diarizationUnavailable"
		case "failed":
			key = "diarizationFailed"
		case "legacy_unverified":
			key = "diarizationUnverified"
		}
	}
	return v.msg(key)
}
