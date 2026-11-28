package desktop

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type detailLogTone struct {
	start, end int
	tone       string
}

type detailLogDocument struct {
	source, id, raw string
	buffer          *ui.TextBuffer
	tones           []detailLogTone
}

var detailLogANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")

func (document *detailLogDocument) update(job domain.JobSummary, raw *string) bool {
	value := ""
	if raw != nil {
		value = *raw
	}
	if document.buffer != nil && document.source == job.Source && document.id == job.ID && document.raw == value {
		return false
	}
	document.source, document.id, document.raw = job.Source, job.ID, value
	document.tones = document.tones[:0]
	clean := detailLogANSI.ReplaceAllString(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"), "")
	classified := client.DetailLogs(&clean)
	var text strings.Builder
	if len(classified) > 0 {
		text.Grow(len(clean))
		lines := strings.Split(strings.TrimSuffix(clean, "\n"), "\n")
		position, entry := 0, 0
		for index := len(lines) - 1; index >= 0; index-- {
			if index != len(lines)-1 {
				text.WriteByte('\n')
				position++
			}
			line := lines[index]
			text.WriteString(line)
			end := position + utf8.RuneCountInString(line)
			if strings.TrimSpace(line) != "" {
				tone := classified[entry].Tone
				entry++
				if tone != "info" {
					last := len(document.tones) - 1
					if last >= 0 && document.tones[last].tone == tone && document.tones[last].end+1 == position {
						document.tones[last].end = end
					} else {
						document.tones = append(document.tones, detailLogTone{start: position, end: end, tone: tone})
					}
				}
			}
			position = end
		}
		if strings.HasSuffix(clean, "\n") {
			text.WriteByte('\n')
		}
	}
	if document.buffer == nil {
		document.buffer = ui.NewTextBuffer(text.String())
	} else {
		document.buffer.Set(text.String())
	}
	return true
}

type detailSourceReader struct {
	service client.Meetings
	source  string
}

func (reader detailSourceReader) Result(ctx context.Context, id string) (domain.Result, error) {
	var result domain.Result
	var err error
	switch service := reader.service.(type) {
	case client.SourceJobReader:
		result, err = service.JobForSource(ctx, reader.source, id)
	case client.SourceResultReader:
		result, err = service.ResultForSource(ctx, reader.source, id)
	default:
		result, err = reader.service.Result(ctx, id)
	}
	if err == nil && result.Job.Source != reader.source {
		return domain.Result{}, client.ErrResultSourceMismatch
	}
	return result, err
}

func (w *Workspace) closeDetail() {
	if w.detail != nil {
		w.detail.Client.ResultController.Close()
		w.detail = nil
	}
}

func (w *Workspace) openDetailFromSource(id, source string) {
	if w.closed || id == "" || w.meetings == nil || source != "local" && source != "remote" {
		return
	}
	w.navigation.ReplaceCurrent(w.navigationEntry())
	w.showDetailFromSource(id, source)
	w.navigation.Visit(w.navigationEntry())
}

func (w *Workspace) showDetailFromSource(id, source string) {
	w.navigateTo("/jobs/" + id)
	w.closeDetail()
	dispatch := w.jobDispatch
	if dispatch == nil {
		dispatch = mygo.RunOnMain
	}
	controller := client.NewResult(detailSourceReader{service: w.meetings, source: source}, dispatch, func() {
		if !w.closed && w.main != nil {
			w.main.Invalidate()
		}
	})
	w.detail = NewMeetings(&client.Controller{ResultController: controller}, w.view.Locale)
	w.detail.Source = source
	w.detail.confirmLocalRuntime = w.confirmJobRuntime
	w.detail.OpenResult = func(id, locale string) { w.openResultFromSource(id, locale, source) }
	controller.Select(id)
	controller.StartPolling()
}

func (w *Workspace) warnJobRuntime() {
	parent, locale := w.main, w.preferences.Locale
	go func() {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{Parent: parent, Type: mygo.MessageWarning, Title: presentation.Message(locale, "common.modelUnavailableTitle"), Message: presentation.Message(locale, "common.modelUnavailableMessage")})
	}()
}

func (w *Workspace) confirmJobRuntime() bool {
	if w.runtime != nil && localJobRuntimeReady(w.runtime.Client) {
		return true
	}
	if w.newJob != nil && w.newJob.WarnRuntime != nil {
		w.newJob.WarnRuntime()
	} else {
		w.warnJobRuntime()
	}
	return false
}

func panel(c *ui.Context, body func()) {
	ui.Column(c).FillWidth().Padding(16).Gap(14).Border(1, palette(c).Separator).Radius(10).Background(palette(c).Panel).Children(body)
}
func (v *Meetings) Detail(c *ui.Context, back func()) {
	msg := func(key string) string { return presentation.Message(v.Locale, "jobDetail."+key) }
	width, _ := c.Size()
	contentScroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(shellContentMaxWidth).AlignSelf(ui.Center).Gap(14).Children(func() {
			if textButton(c, msg("backToJobs")).Clicked() {
				back()
			}
			if v.Client.LoadingResult {
				ui.Text(c, v.text("正在读取…", "Loading…")).Role(ui.RoleStatus)
				return
			}
			if v.Client.ResultError != nil {
				ui.Text(c, v.text("详情刷新失败，请重试。", "Could not refresh details. Please retry.")).FillWidth().TextColor(c.Theme().Danger).Role(ui.RoleAlert)
				if textButton(c, presentation.Message(v.Locale, "common.retry")).Clicked() {
					v.Client.ResultController.Refresh()
				}
				if v.Client.Result == nil {
					return
				}
			}
			if v.Actions != nil && v.Actions.Error != nil {
				message := v.text("任务操作失败，请重试。", "Job action failed. Please retry.")
				if errors.Is(v.Actions.Error, domain.ErrRuntime) {
					message = presentation.Message(v.Locale, "common.modelUnavailableMessage")
				}
				ui.Text(c, message).FillWidth().TextColor(c.Theme().Danger).Role(ui.RoleAlert)
			}
			if v.OpenError {
				ui.Text(c, v.text("无法打开结果窗口，请重试。", "Could not open result. Please retry.")).FillWidth().TextColor(c.Theme().Danger).Role(ui.RoleAlert)
			}
			if v.Client.Result == nil {
				ui.Text(c, msg("notFound")).Role(ui.RoleStatus)
				return
			}
			r := v.Client.Result
			job := r.Job
			if !meetingOperationAvailable(job.Source, v.RemoteCapabilities, "jobs.read") {
				ui.Text(c, presentation.Message(v.Locale, "workbench.remoteOperationUnavailable")).TextColor(c.Theme().Warning)
				return
			}
			panel(c, func() {
				header := ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Start)
				if width <= 760 {
					header.Column()
				}
				header.Children(func() {
					ui.Text(c, job.Title).FontSize(23).Grow(1).MinWidth(0).FillWidth()
					statusBadge(c, v.Locale, job.OverallStatus)
				})
				names := []string{}
				for _, file := range job.SourceFiles {
					names = append(names, file.Name)
				}
				ui.Text(c, strings.Join(names, " · ")).FillWidth().TextColor(c.Theme().TextMuted).FontSize(12)
				columns := 4
				if width <= 680 {
					columns = 2
				}
				ui.Grid(c).FillWidth().Columns(columns).Gap(10).Children(func() {
					for _, stage := range []struct{ key, status string }{{"stageUploaded", job.UploadStatus}, {"stageAsr", job.ASRStatus}, {"stageSummary", job.SummaryStatus}, {"stageOverall", job.OverallStatus}} {
						ui.Column(c.Key(stage.key)).FillWidth().MinWidth(0).Padding(10).Gap(8).Radius(7).Background(palette(c).Subpanel).Children(func() {
							ui.Text(c, msg(stage.key)).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
							statusBadge(c, v.Locale, stage.status)
						})
					}
				})
				speaker := "common.disabled"
				if job.EnableSpeaker {
					speaker = "common.enabled"
				}
				ui.Text(c, fmt.Sprintf("%s %d · %s %d · %s %s", msg("inputFiles"), len(job.SourceFiles), msg("hotwords"), len(job.Hotwords), msg("speaker"), presentation.Message(v.Locale, speaker))).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
				if job.DiarizationStatus != "completed" && job.DiarizationStatus != "disabled" {
					ui.Text(c, detailDiarization(v.Locale, job.DiarizationStatus)).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
				}
				if job.OverallStatus == "completed" {
					if ui.PrimaryButton(c, msg("viewWorkbench")).Disabled(!v.CanReadResult(job)).Clicked() {
						v.OpenResult(job.ID, v.Locale)
					}
				}
				if job.OverallStatus == "failed" {
					if ui.Button(c, msg("retryJob")).Disabled(!v.CanRetry(job)).Clicked() {
						v.retry(job)
					}
				}
			})
			context := ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Stretch)
			if width <= 1180 {
				context.Column()
			}
			context.Children(func() {
				files := ui.Column(c).MinWidth(0)
				if width <= 1180 {
					files.FillWidth()
				} else {
					files.Grow(1.35).Basis(0)
				}
				files.Children(func() {
					panel(c, func() {
						ui.Text(c, msg("filesSection"))
						for _, file := range job.SourceFiles {
							ui.Column(c.Key(file.ID)).FillWidth().Padding(12).Gap(5).Radius(8).Background(palette(c).Subpanel).Children(func() {
								ui.Text(c, file.Name).FillWidth().Selectable()
								kind := "common.video"
								if file.Kind == "audio" {
									kind = "common.audio"
								}
								ui.Text(c, presentation.Message(v.Locale, kind)+" · "+file.SizeLabel).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
							})
						}
					})
				})
				settings := ui.Column(c).MinWidth(280)
				if width <= 1180 {
					settings.MinWidth(0).FillWidth()
				} else {
					settings.Grow(.65).Basis(0)
				}
				settings.Children(func() {
					panel(c, func() {
						ui.Text(c, msg("settingsSection"))
						ui.Text(c, msg("language")+"  "+job.Lang)
						ui.Text(c, msg("speakerDiarization")).FontSize(12).TextColor(c.Theme().TextMuted)
						ui.Text(c, detailDiarization(v.Locale, job.DiarizationStatus)).FillWidth().FontSize(12)
						ui.Text(c, fmt.Sprintf("%s  %d", msg("hotwords"), len(job.Hotwords)))
					})
				})
			})
			panel(c, func() {
				progress := v.detailProgress.Update(job)
				ui.Row(c).FillWidth().Children(func() {
					ui.Text(c, msg("progressSection")).Grow(1)
					ui.Text(c, fmt.Sprintf("%.0f%%", progress))
				})
				progressBar(c, float32(progress), msg("progressSection"))
				text := statusText(v.Locale, job.OverallStatus)
				if job.ProgressMessage != nil && strings.TrimSpace(*job.ProgressMessage) != "" {
					text = strings.TrimSpace(*job.ProgressMessage)
				}
				ui.Text(c, text).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
			})
			panel(c, func() {
				ui.Text(c, msg("logSection"))
				if reason := jobFailureReason(job); reason != "" {
					ui.Text(c, reason).FillWidth().TextColor(c.Theme().Danger).Selectable().Role(ui.RoleAlert)
				}
				for _, warning := range job.Warnings {
					ui.Text(c, warning.Message).FillWidth().TextColor(c.Theme().Warning).Selectable()
				}
				changed := v.detailLog.update(job, r.ProcessLog)
				if v.detailLog.buffer.Len() == 0 {
					ui.Text(c, msg("noLog")).TextColor(c.Theme().TextMuted)
					return
				}
				field := ui.TextAreaBuffer(c.Key(job.Source+":"+job.ID), v.detailLog.buffer).FillWidth().Lines(1, 26).MinHeight(0).MaxHeight(420).Padding(12).FontSize(12).TextColor(c.Theme().TextMuted).Background(palette(c).Subpanel).ReadOnly(true).Label(msg("logSection"))
				if changed {
					field.SetTextSelection(0, 0)
				}
				for _, span := range v.detailLog.tones {
					var color ui.Color
					switch span.tone {
					case "error":
						color = c.Theme().Danger
					case "warning":
						color = c.Theme().Warning
					case "success":
						color = c.Theme().Success
					}
					field.TextRanges(ui.TextRange{Start: span.start, End: span.end, Color: color})
				}
			})
		})
	})
}

func detailDiarization(locale, status string) string {
	key := "diarizationPending"
	switch status {
	case "disabled":
		return presentation.Message(locale, "common.disabled")
	case "completed":
		key = "diarizationCompleted"
	case "unavailable":
		key = "diarizationUnavailable"
	case "failed":
		key = "diarizationFailed"
	case "legacy_unverified":
		key = "diarizationUnverified"
	}
	return presentation.Message(locale, "jobDetail."+key)
}
