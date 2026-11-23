package desktop

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

type DashboardView struct {
	Client               *client.DashboardController
	Locale               string
	period               string
	section              string
	metric               string
	OpenResult           func(string, string)
	OpenResultFromSource func(string, string, string)
	OpenDetail           func(string)
	OpenDetailFromSource func(string, string)
	Navigate             func(string)
	CanNavigate          func(string) bool
	RuntimeReady         bool
	RemoteCapabilities   *domain.RemoteCapabilities
}

func NewDashboardView(controller *client.DashboardController, locale string) *DashboardView {
	return &DashboardView{Client: controller, Locale: locale, period: "7d", section: "activity", metric: "jobs"}
}
func (v *DashboardView) Period() string            { return v.period }
func (v *DashboardView) text(zh, en string) string { return localized(v.Locale, zh, en) }
func (v *DashboardView) available(path string) bool {
	return v.Navigate != nil && (v.CanNavigate == nil || v.CanNavigate(path))
}
func (v *DashboardView) link(c *ui.Context, label, path string) {
	if textButton(c, label).Disabled(!v.available(path)).Clicked() {
		v.Navigate(path)
	}
}

func (v *DashboardView) View(c *ui.Context) {
	width, _ := c.Size()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(1180).AlignSelf(ui.Center).Padding(12, 8, 32).Gap(26).Children(func() {
			header := ui.Row(c).FillWidth().MinHeight(32).Padding(0, 2).Gap(10).AlignItems(ui.Center)
			if width <= 980 {
				header.Column().AlignItems(ui.Start)
			}
			header.Children(func() {
				ui.Text(c, v.text("👋 · 好久不见，欢迎回来！", "👋 · Welcome back! It's been a while.")).FontSize(24).LineHeight(1.25).Bold()
				if v.Client.Overview != nil && v.Client.Overview.Source == "remote" {
					ui.Text(c, v.text("远端任务快照", "Remote snapshot")).Padding(4, 7).FontSize(10).Radius(10).Background(palette(c).Selected).TextColor(palette(c).NavAccent)
				}
			})
			if v.Client.Error != nil {
				if v.Client.Overview == nil {
					ui.Text(c, v.text("无法加载工作台数据", "Could not load dashboard data"))
					if textButton(c, v.text("重试", "Retry")).Clicked() {
						v.Client.Refresh(v.period)
					}
					return
				}
				ui.Text(c, v.text("刷新失败，当前显示上一次可用数据。", "Refresh failed. Showing the last available snapshot.")).TextColor(c.Theme().Warning)
			}
			o := v.Client.Overview
			if o == nil {
				ui.Text(c, v.text("正在聚合工作台数据…", "Aggregating dashboard data…"))
				return
			}
			m := o.Metrics
			columns := 4
			if width <= 760 {
				columns = 2
			}
			ui.Grid(c).FillWidth().Columns(columns).GapX(max(28, min(72, width*.05))).GapY(20).Padding(0, 2).Label(v.text("会议核心指标", "Core meeting metrics")).Children(func() {
				v.kpi(c, v.text("会议任务", "Meeting jobs"), fmt.Sprint(m.TotalJobs), fmt.Sprintf(v.text("%d 个正在处理", "%d currently processing"), m.ActiveJobs), false)
				v.kpi(c, v.text("音视频时长", "Media duration"), minutesText(v.Locale, m.MediaDurationMinutes), fmt.Sprintf(v.text("累计处理 %s", "%s processing time"), minutesText(v.Locale, int64(math.Round(float64(m.ProcessingDurationSeconds)/60)))), false)
				v.kpi(c, v.text("任务完成率", "Completion rate"), ratio(m.CompletedJobs, m.TotalJobs), fmt.Sprintf(v.text("%d 个完成 · %d 个失败", "%d completed · %d failed"), m.CompletedJobs, m.FailedJobs), m.FailedJobs > 0)
				v.kpi(c, v.text("AI 总结覆盖率", "AI summary coverage"), ratio(m.SummaryReadyJobs, m.CompletedJobs), fmt.Sprintf(v.text("已完成任务中 %d / %d", "%d of %d completed jobs"), m.SummaryReadyJobs, m.CompletedJobs), false)
			})
			ui.Column(c).FillWidth().Gap(22).Children(func() {
				ui.Row(c).Gap(4).Label(v.text("工作台主内容", "Dashboard workspace")).Children(func() {
					if segmentButton(c, v.text("任务动态", "Task activity"), v.section == "activity").Clicked() {
						v.section = "activity"
					}
					if segmentButton(c, v.text("分析概览", "Analysis overview"), v.section == "insights").Clicked() {
						v.section = "insights"
					}
				})
				if v.section == "activity" {
					v.activity(c, o)
				} else {
					v.insights(c, o)
				}
			})
		})
	})
}
func (v *DashboardView) kpi(c *ui.Context, label, value, meta string, warning bool) {
	width, _ := c.Size()
	ui.Column(c.Key(label)).FillWidth().MinWidth(0).MinHeight(64).Justify(ui.Center).Gap(5).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
		e := ui.Text(c, value).FillWidth().FontSize(max(21, min(27, width*.02))).LineHeight(1.05).Bold().SingleLine()
		if warning {
			e.TextColor(c.Theme().Warning)
		}
		ui.Text(c, meta).FontSize(9).TextColor(c.Theme().TextMuted).SingleLine()
	})
}
func (v *DashboardView) activity(c *ui.Context, o *domain.DashboardOverview) {
	m := o.Metrics
	width, _ := c.Size()
	columns := 2
	if width <= 760 {
		columns = 1
	}
	ui.Grid(c).FillWidth().MaxWidth(736).Columns(columns).Gap(16).Children(func() {
		v.summary(c, v.text("待关注", "Needs attention"), v.text("全部任务", "All jobs"), "/jobs", []string{v.text("处理中", "Active"), v.text("异常与告警", "Exceptions")}, []int64{m.ActiveJobs, m.FailedJobs + m.WarningJobs})
		v.summary(c, v.text("最近结果", "Recent results"), v.text("全部结果", "All results"), "/jobs?status=completed", []string{v.text("已完成", "Completed"), v.text("已总结", "Summarized")}, []int64{m.CompletedJobs, m.SummaryReadyJobs})
	})
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		toolbar := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(20).Padding(8, 0, 0)
		if width <= 980 {
			toolbar.Column().AlignItems(ui.Stretch).Gap(10)
		}
		toolbar.Children(func() {
			ui.Text(c, v.text("最近任务", "Recent jobs")).FontSize(15).Bold().Grow(1)
			ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
				v.ranges(c)
				if textButton(c, v.text("刷新", "Refresh")).Disabled(v.Client.Loading).Clicked() {
					v.Client.Refresh(v.period)
				}
				if ui.PrimaryButton(c, v.text("新建任务", "New Job")).Disabled(o.Source == "remote" || !v.available("/jobs/new")).Clicked() {
					v.Navigate("/jobs/new")
				}
			})
		})
		jobs := dashboardActivity(o)
		if len(jobs) == 0 {
			ui.Column(c).FillWidth().MinHeight(98).Padding(20, 16).Center().Children(func() {
				ui.Text(c, v.text("当前时间范围内还没有任务。", "No jobs in this range.")).FontSize(11).TextColor(c.Theme().TextMuted)
			})
			return
		}
		ui.ScrollHorizontal(c).FillWidth().Children(func() {
			ui.Column(c).FillWidth().MinWidth(780).Role(ui.RoleTable).RowCount(len(jobs) + 1).ColumnCount(5).Label(v.text("最近任务", "Recent jobs")).Children(func() {
				v.tableRow(c, true, 1, func() {
					ui.Text(c, v.text("任务名称", "Title")).Grow(1).MinWidth(220).Role(ui.RoleColumnHeader).RowIndex(1).ColumnIndex(1)
					ui.Text(c, v.text("时长", "Duration")).Width(90).Role(ui.RoleColumnHeader).RowIndex(1).ColumnIndex(2)
					ui.Text(c, v.text("状态", "Status")).Width(180).Role(ui.RoleColumnHeader).RowIndex(1).ColumnIndex(3)
					ui.Text(c, v.text("创建时间", "Created")).Width(130).Role(ui.RoleColumnHeader).RowIndex(1).ColumnIndex(4)
					ui.Text(c, v.text("操作", "Action")).Width(64).Role(ui.RoleColumnHeader).RowIndex(1).ColumnIndex(5)
				})
				for index, job := range jobs {
					rowIndex := index + 2
					v.tableRow(c.Key(job.ID), false, rowIndex, func() {
						ui.Row(c).Grow(1).MinWidth(220).Gap(10).AlignItems(ui.Center).Role(ui.RoleCell).RowIndex(rowIndex).ColumnIndex(1).Label(job.Title).Children(func() {
							ui.Row(c).Size(24, 24).Radius(5).Background(palette(c).Selected)
							ui.Text(c, job.Title).FontSize(12).Bold().SingleLine().Grow(1).MinWidth(0)
						})
						ui.Text(c, minutesText(v.Locale, job.DurationMinutes)).Width(90).TextColor(c.Theme().TextMuted).Role(ui.RoleCell).RowIndex(rowIndex).ColumnIndex(2)
						ui.Row(c).Width(180).Gap(5).AlignItems(ui.Center).Clip().Role(ui.RoleCell).RowIndex(rowIndex).ColumnIndex(3).Children(func() {
							statusBadge(c, v.Locale, job.OverallStatus)
							if job.WarningCount > 0 {
								ui.Text(c, fmt.Sprintf(v.text("%d 条告警", "%d warnings"), job.WarningCount)).FontSize(10).TextColor(c.Theme().Warning)
							}
							if job.OverallStatus == "completed" && !job.HasSummary {
								ui.Text(c, v.text("待总结", "No summary")).FontSize(10).TextColor(c.Theme().TextMuted)
							}
						})
						ui.Text(c, createdText(v.Locale, job.CreatedAt)).Width(130).FontSize(11).TextColor(c.Theme().TextMuted).Role(ui.RoleCell).RowIndex(rowIndex).ColumnIndex(4)
						label := v.text("详情", "Details")
						source := dashboardSource(o)
						enabled := (v.OpenDetail != nil || v.OpenDetailFromSource != nil) && meetingOperationAvailable(source, v.RemoteCapabilities, "jobs.read")
						if job.OverallStatus == "completed" {
							label = v.text("查看结果", "View result")
							enabled = (v.OpenResult != nil || v.OpenResultFromSource != nil) && meetingOperationAvailable(source, v.RemoteCapabilities, "jobs.read") && meetingOperationAvailable(source, v.RemoteCapabilities, "jobs.result.read")
						}
						ui.Row(c).Width(64).Role(ui.RoleCell).RowIndex(rowIndex).ColumnIndex(5).Children(func() {
							if textButton(c, label).Width(64).Label(label + ": " + job.Title).Disabled(!enabled).Clicked() {
								if job.OverallStatus == "completed" {
									if v.OpenResultFromSource != nil {
										v.OpenResultFromSource(source, job.ID, v.Locale)
									} else {
										v.OpenResult(job.ID, v.Locale)
									}
								} else {
									if v.OpenDetailFromSource != nil {
										v.OpenDetailFromSource(source, job.ID)
									} else {
										v.OpenDetail(job.ID)
									}
								}
							}
						})
					})
				}
			})
		})
	})
}
func dashboardSource(overview *domain.DashboardOverview) string {
	if overview == nil || overview.Source == "" {
		return "local"
	}
	return overview.Source
}
func dashboardActivity(o *domain.DashboardOverview) []domain.DashboardJob {
	// JS Map keeps first insertion order but the last value for duplicate keys.
	jobs := []domain.DashboardJob{}
	indices := map[string]int{}
	for _, group := range [][]domain.DashboardJob{o.AttentionJobs, o.RecentResults} {
		for _, job := range group {
			if i, ok := indices[job.ID]; ok {
				jobs[i] = job
			} else {
				indices[job.ID] = len(jobs)
				jobs = append(jobs, job)
			}
		}
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].CreatedAt > jobs[j].CreatedAt })
	return jobs[:min(8, len(jobs))]
}
func (v *DashboardView) tableRow(c *ui.Context, header bool, index int, body func()) {
	height := float32(54)
	font := float32(11)
	color := c.Theme().Text
	if header {
		height = 32
		font = 10
		color = c.Theme().TextMuted
	}
	ui.Row(c).FillWidth().MinHeight(height).Padding(8, 10).Gap(16).AlignItems(ui.Center).FontSize(font).TextColor(color).Role(ui.RoleRow).RowIndex(index).Children(body)
	separator(c)
}
func (v *DashboardView) summary(c *ui.Context, title, action, path string, labels []string, values []int64) {
	p := palette(c)
	ui.Column(c).FillWidth().MinWidth(0).Padding(14, 16).Gap(16).Radius(7).Border(1, p.Separator).Background(p.Panel.Mix(c.Theme().Accent, 0.04)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Row(c).Size(9, 9).Radius(3).Background(c.Theme().Accent)
			ui.Text(c, title).FontSize(13).Bold().Grow(1)
			v.link(c, action, path)
		})
		ui.Row(c).Gap(24).Children(func() {
			for i, label := range labels {
				ui.Column(c.Key(label)).Grow(1).Gap(5).Children(func() {
					ui.Text(c, label).FontSize(10).TextColor(c.Theme().TextMuted)
					ui.Text(c, fmt.Sprint(values[i])).FontSize(20).Bold()
				})
			}
		})
	})
}
func (v *DashboardView) ranges(c *ui.Context) {
	ui.Row(c).Padding(2).Radius(7).Background(palette(c).Subpanel).Label(v.text("统计时间范围", "Statistics range")).Children(func() {
		for _, r := range []struct{ key, zh, en string }{{"today", "今日", "Today"}, {"7d", "近 7 天", "7 days"}, {"30d", "近 30 天", "30 days"}, {"all", "全部", "All"}} {
			if segmentButton(c, v.text(r.zh, r.en), v.period == r.key).Padding(4, 9).MinHeight(26).Clicked() {
				v.period = r.key
				v.Client.Refresh(v.period)
			}
		}
	})
}
func (v *DashboardView) insights(c *ui.Context, o *domain.DashboardOverview) {
	width, _ := c.Size()
	gap := max(28, min(56, width*.04))
	primary := ui.Grid(c).FillWidth().ColumnTracks(ui.Fr(1.65), ui.Fr(.85).Min(280)).Gap(gap).AlignItems(ui.Start)
	if width <= 980 {
		primary.Columns(1)
	}
	primary.Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Children(func() {
			ui.Row(c).FillWidth().MinHeight(42).Padding(0, 2, 10).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, v.text("工作量趋势", "Workload trend")).FontSize(14).FontWeight(650).Grow(1)
				ui.Row(c).Padding(2).Radius(7).Background(palette(c).Subpanel.Mix(c.Theme().Accent, .08)).Label(v.text("趋势指标", "Trend metric")).Children(func() {
					for _, metric := range []struct{ key, zh, en string }{{"jobs", "任务", "Jobs"}, {"media", "音视频", "Media"}, {"processing", "处理耗时", "Processing"}} {
						if segmentButton(c, v.text(metric.zh, metric.en), v.metric == metric.key).Padding(3, 7).MinHeight(22).FontSize(10).Clicked() {
							v.metric = metric.key
						}
					}
				})
			})
			v.chart(c, o.Trend)
		})
		ui.Column(c).FillWidth().MinWidth(0).Gap(12).Children(func() {
			ui.Text(c, v.text("结果完善度", "Result completeness")).FillWidth().MinHeight(42).Padding(10, 2).FontSize(14).FontWeight(650)
			m := o.Metrics
			for _, r := range []struct {
				label        string
				value, total int64
			}{
				{v.text("逐字稿", "Transcript"), m.TranscriptReadyJobs, m.CompletedJobs},
				{v.text("讲话人分离", "Speaker separation"), m.SpeakerReadyJobs, m.SpeakerEligibleJobs},
				{v.text("AI 总结", "AI summary"), m.SummaryReadyJobs, m.CompletedJobs},
				{v.text("已导出", "Exported"), m.ExportedJobs, m.CompletedJobs},
			} {
				ui.Row(c.Key(r.label)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					percent := float32(0)
					count := "—"
					if r.total > 0 {
						percent = float32(r.value) * 100 / float32(r.total)
						count = fmt.Sprintf("%d / %d", r.value, r.total)
					}
					ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
						ui.Row(c).FillWidth().Gap(10).Children(func() {
							ui.Text(c, r.label).Grow(1).FontSize(10).TextColor(c.Theme().TextMuted)
							ui.Text(c, ratio(r.value, r.total)).FontSize(11).Bold()
						})
						progressBar(c, percent, r.label+" "+ratio(r.value, r.total))
					})
					ui.Text(c, count).Width(42).TextAlign(ui.End).FontSize(10).TextColor(c.Theme().TextMuted)
				})
			}
		})
	})
	if o.Source == "remote" {
		return
	}
	secondary := ui.Grid(c).FillWidth().Columns(2).Gap(gap).Padding(4, 0, 0).AlignItems(ui.Start)
	if width <= 980 {
		secondary.Columns(1)
	}
	secondary.Children(func() {
		ui.Column(c).FillWidth().MinWidth(0).Children(func() {
			ui.Text(c, v.text("处理资源", "Processing resources")).FillWidth().MinHeight(42).Padding(10, 2).FontSize(14).FontWeight(650)
			columns := 4
			if width <= 760 {
				columns = 2
			}
			ui.Grid(c).FillWidth().Columns(columns).MinHeight(98).Gap(8).Padding(4, 0, 0).Children(func() {
				runtimeValue, runtimeColor := v.text("待配置", "Needs setup"), c.Theme().Warning
				if v.RuntimeReady {
					runtimeValue, runtimeColor = v.text("已就绪", "Ready"), c.Theme().Success
				}
				v.insightLink(c, v.text("本地运行时", "Local runtime"), runtimeValue, "/settings", runtimeColor, false)
				v.insightLink(c, v.text("AI 模型", "AI models"), fmt.Sprintf(v.text("%d / %d 已启用", "%d / %d enabled"), o.Resources.EnabledAIModels, o.Resources.AIModels), "/models", c.Theme().Text, false)
				v.insightLink(c, v.text("总结模板", "Templates"), fmt.Sprint(o.Resources.Templates), "/templates", c.Theme().Text, false)
				v.insightLink(c, v.text("人员库", "Members"), fmt.Sprint(o.Resources.Members), "/members", c.Theme().Text, false)
			})
		})
		ui.Column(c).FillWidth().MinWidth(0).Children(func() {
			ui.Row(c).FillWidth().MinHeight(42).Padding(0, 2, 10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, v.text("伙伴与福利", "Companion & benefits")).FontSize(14).FontWeight(650).Grow(1)
				v.link(c, v.text("查看伙伴", "Open companion"), "/pet")
			})
			pet := o.Companion
			if pet == nil {
				ui.Column(c).FillWidth().MinHeight(98).Padding(20, 16).Center().Children(func() {
					ui.Text(c, v.text("伙伴数据尚未初始化。", "Companion data has not been initialized.")).FontSize(11).TextColor(c.Theme().TextMuted)
				})
				return
			}
			companion := ui.Grid(c).FillWidth().MinHeight(98).ColumnTracks(ui.Fr(.85), ui.Fr(1.15)).Gap(12).Padding(4, 0, 0)
			if width <= 760 {
				companion.Columns(1)
			}
			companion.Children(func() {
				ui.Column(c).FillWidth().MinWidth(0).Padding(12, 2).Gap(8).Justify(ui.Center).Children(func() {
					ui.Row(c).FillWidth().Gap(10).Children(func() {
						ui.Text(c, pet.Name).SingleLine().MinWidth(0).FontSize(12).Bold().Grow(1)
						ui.Text(c, fmt.Sprintf("Lv.%d", pet.Level)).FontSize(9).TextColor(c.Theme().TextMuted)
					})
					progressBar(c, float32(pet.LevelProgressPercent), v.text("等级进度", "Level progress"))
					exp := v.text("已满级", "Maximum level")
					if pet.NextLevelExperience > 0 {
						exp = fmt.Sprintf("%d / %d EXP", pet.CurrentLevelExperience, pet.NextLevelExperience)
					}
					ui.Text(c, exp).FontSize(9).TextColor(c.Theme().TextMuted)
				})
				ui.Grid(c).FillWidth().Columns(3).Gap(8).Children(func() {
					check := v.text("待签到", "Pending")
					if pet.CheckedInToday {
						check = v.text("已完成", "Done")
					}
					v.insightLink(c, "LP", fmt.Sprint(pet.LPBalance), "/pet-store", c.Theme().Text, true)
					v.insightLink(c, v.text("今日签到", "Check-in"), check, "/daily-check-in", c.Theme().Text, true)
					v.insightLink(c, v.text("待处理", "Needs action"), fmt.Sprint(pet.ClaimableActivities), "/work-market", c.Theme().Text, true)
				})
			})
		})
	})
}

func (v *DashboardView) insightLink(c *ui.Context, label, value, path string, color ui.Color, centered bool) {
	button := ui.ButtonBase(c.Key(path)).Column().FillWidth().MinWidth(0).Padding(12, 14).Gap(7).Radius(7).Justify(ui.Center).AlignItems(ui.Start).Background(palette(c).Subpanel.Mix(c.Theme().Accent, .07)).Label(label).Tooltip(label + ": " + value).Disabled(!v.available(path))
	if centered {
		button.Padding(10).Gap(5).AlignItems(ui.Center)
	}
	if button.Hovered() {
		button.Background(palette(c).Subpanel.Mix(c.Theme().Accent, .14))
	}
	button.Children(func() {
		ui.Text(c, label).FillWidth().FontSize(9).TextColor(c.Theme().TextMuted)
		ui.Text(c, value).FillWidth().MinWidth(0).FontSize(12).Bold().TextColor(color).SingleLine()
	})
	if button.Clicked() {
		v.Navigate(path)
	}
}

func (v *DashboardView) chart(c *ui.Context, points []domain.DashboardTrend) {
	if len(points) == 0 {
		ui.Column(c).FillWidth().MinHeight(174).Padding(20, 16).Center().Children(func() {
			ui.Text(c, v.text("当前时间范围内还没有趋势数据。", "No trend data in this range.")).FontSize(11).TextColor(c.Theme().TextMuted)
		})
		return
	}
	values := make([]int64, len(points))
	maximum := int64(1)
	descriptions := make([]string, len(points))
	unit := v.text("个", "jobs")
	if v.metric != "jobs" {
		unit = v.text("分钟", "min")
	}
	for i, p := range points {
		value := p.TotalJobs
		if v.metric == "media" {
			value = p.MediaDurationMinutes
		} else if v.metric == "processing" {
			value = int64(math.Round(float64(p.ProcessingDurationSeconds) / 60))
		}
		values[i] = value
		maximum = max(maximum, value)
		descriptions[i] = fmt.Sprintf("%s: %d %s", p.Period, value, unit)
	}
	ui.Column(c).FillWidth().MinHeight(174).Padding(12, 2, 4).Gap(4).Role(ui.RoleImage).Label(v.text("工作量趋势：", "Workload trend: ") + strings.Join(descriptions, "; ")).Children(func() {
		ui.Row(c).Height(132).FillWidth().Gap(8).Children(func() {
			ui.Column(c).Height(132).Width(28).AlignItems(ui.End).Children(func() {
				ui.Text(c, fmt.Sprint(maximum)).FontSize(9).TextColor(c.Theme().TextMuted)
				ui.Box(c).Grow(1)
				ui.Text(c, "0").FontSize(9).TextColor(c.Theme().TextMuted)
			})
			ui.Box(c).Grow(1).MinWidth(0).Height(132).Children(func() {
				for _, bottom := range []float32{0, 44, 88} {
					ui.Box(c).Absolute().Bottom(bottom).FillWidth().Height(1).Background(palette(c).Separator)
				}
				ui.Grid(c).Absolute().Fill().Columns(len(points)).Gap(4).Children(func() {
					for index, value := range values {
						height := float32(value) * 132 / float32(maximum)
						if value > 0 {
							height = max(6.6, height)
						}
						slot := ui.Column(c.Key(points[index].Period)).FillWidth().Height(132).Justify(ui.End).AlignItems(ui.Center).Tooltip(descriptions[index])
						color := c.Theme().Accent.Mix(c.Theme().Text, .22)
						if slot.Hovered() {
							color = c.Theme().Accent
						}
						slot.Children(func() {
							ui.Box(c).WidthPercent(72).MaxWidth(24).Height(height).Radius(3, 3, 0, 0).Background(color).Label(descriptions[index])
						})
					}
				})
			})
		})
		ui.Row(c).FillWidth().Height(18).Padding(0, 0, 0, 36).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, points[0].Period).FontSize(9).TextColor(c.Theme().TextMuted)
			ui.Text(c, points[len(points)-1].Period).FontSize(9).TextColor(c.Theme().TextMuted)
		})
	})
}
