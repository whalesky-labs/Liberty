package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type SummaryView struct {
	Client                                       *client.SummaryController
	Locale                                       string
	Preferences                                  domain.UIPreferences
	Close                                        func()
	Confirm                                      func(context.Context, string) (bool, error)
	modelID, templateID, extra                   string
	includeSpeaker, includeTimestamp, useMembers bool
}

func (v *SummaryView) msg(key string) string { return presentation.Message(v.Locale, "aiSummary."+key) }
func (v *SummaryView) format(key string, values map[string]any) string {
	return presentation.Format(v.Locale, "aiSummary."+key, values)
}
func (v *SummaryView) View(c *ui.Context) {
	p := presentation.PreferenceColors(c.Theme().Dark, v.Preferences)
	width, _ := c.Size()
	title, count, files := v.msg("heroTitle"), 0, 0
	latest := latestSummary(v.Client.Runs)
	status := summaryDisplayStatus(v.Client.Result, latest)
	currentStatus := v.msg("activeLabelEmpty")
	if r := v.Client.Result; r != nil {
		title, count, files = r.Job.Title, len(r.PrimarySegments()), len(r.Job.SourceFiles)
	}
	if latest != nil {
		switch status {
		case "summarizing":
			currentStatus = v.msg("activeLabelRunning")
		case "failed":
			currentStatus = v.msg("activeLabelFailed")
		default:
			currentStatus = v.msg("activeLabelSaved")
		}
	}
	ui.Themed(c, &p.Theme).Fill().Children(func() {
		ui.Column(c).Fill().Padding(16).Gap(14).Background(p.Content).Children(func() {
			panel(c, func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, title).FontSize(21).Bold().Role(ui.RoleHeading).Level(1).Selectable().PreserveSpaces()
						ui.Text(c, v.format("heroCopy", map[string]any{"count": count})).FontSize(12).TextColor(c.Theme().TextMuted)
					})
					statusBadge(c, v.Locale, status)
					if textButton(c, presentation.Message(v.Locale, "common.closeWindow")).Shrink(0).Disabled(v.Close == nil).Clicked() && v.Close != nil {
						v.Close()
					}
				})
				ui.Row(c).FillWidth().Wrap().Gap(12).Children(func() {
					ui.Text(c, v.format("transcriptCount", map[string]any{"count": count})).FontSize(11)
					current := ui.Text(c, v.format("currentStatus", map[string]any{"status": currentStatus})).FontSize(11)
					if v.Client.Result != nil {
						current.Role(ui.RoleStatus)
					}
					ui.Text(c, v.format("inputFiles", map[string]any{"count": files})).FontSize(11)
					if v.Client.Result == nil {
						ui.Text(c, v.msg("jobMissing")).FontSize(11).TextColor(c.Theme().TextMuted)
					} else {
						fileNames := make([]string, 0, len(v.Client.Result.Job.SourceFiles))
						for _, file := range v.Client.Result.Job.SourceFiles {
							if name := strings.TrimSpace(file.Name); name != "" {
								fileNames = append(fileNames, name)
							}
						}
						if len(fileNames) > 0 {
							ui.Text(c, strings.Join(fileNames, " · ")).FontSize(11).TextColor(c.Theme().TextMuted).Selectable().PreserveSpaces()
						}
					}
				})
			})
			if v.Client.Error != nil && !(v.Client.Loading && v.Client.Result == nil) {
				ui.Text(c.Key("error"), summaryError(v.Locale, v.Client.Error)).TextColor(c.Theme().Danger).Role(ui.RoleAlert).Selectable()
				if textButton(c, presentation.Message(v.Locale, "common.retry")).Disabled(v.Client.Busy).Clicked() {
					v.Client.Retry()
				}
			}
			body := ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Label(v.msg("heroTitle")).Busy(v.Client.Loading || v.Client.Busy)
			if v.Client.Result != nil {
				body.Focusable()
			}
			body.Children(func() {
				ui.Column(c).FillWidth().Gap(14).Children(func() {
					if v.Client.Loading && v.Client.Result == nil {
						ui.Text(c.Key("loading"), presentation.Message(v.Locale, "workbench.loading")).Role(ui.RoleStatus)
						return
					}
					if v.Client.Error != nil && v.Client.Result == nil {
						return
					}
					var layout ui.Element
					if width <= 980 {
						layout = ui.Column(c.Key("layout")).FillWidth().Gap(14)
					} else {
						layout = ui.Row(c.Key("layout")).FillWidth().Gap(14).AlignItems(ui.Start)
					}
					layout.Children(func() {
						side := ui.Column(c.Key("side")).Gap(14)
						if width <= 980 {
							side.FillWidth()
						} else {
							side.Width(320).Shrink(0)
						}
						side.Children(func() {
							panel(c, func() { v.configuration(c, count) })
							panel(c, func() {
								ui.Text(c, v.msg("history")).FontSize(16).Bold().Role(ui.RoleHeading).Level(2)
								if len(v.Client.Runs) == 0 {
									ui.Text(c, v.msg("emptyRuns")).FontSize(12)
								}
								ui.Column(c).FillWidth().Gap(10).Role(ui.RoleList).Label(v.msg("history")).Children(func() {
									for index, run := range v.Client.Runs {
										ui.Column(c.Key(run.ID)).FillWidth().Role(ui.RoleListItem).PositionInSet(index+1, len(v.Client.Runs)).Children(func() { v.history(c, run, index+1) })
									}
								})
							})
						})
						preview := ui.Column(c.Key("preview")).MinWidth(0)
						if width <= 980 {
							preview.FillWidth()
						} else {
							preview.Grow(1)
						}
						preview.Children(func() { panel(c, func() { v.preview(c) }) })
					})
				})
			})
		})
	})
}
func (v *SummaryView) configuration(c *ui.Context, count int) {
	busy := v.Client.Busy || v.Client.Running()
	speaker := v.Client.Result != nil && v.Client.Result.Job.DiarizationStatus == "completed" && len(v.Client.Result.Speakers) > 0
	ui.Text(c, v.msg("currentConfig")).FontSize(16).Bold().Role(ui.RoleHeading).Level(2)
	labels, ids := []string{v.msg("chooseModel")}, []string{""}
	for _, m := range v.Client.Options.Models {
		if m.Enabled {
			labels = append(labels, m.Name+" · "+m.Model)
			ids = append(ids, m.ID)
			if v.modelID == "" && m.IsDefault {
				v.modelID = m.ID
			}
		}
	}
	if v.modelID == "" && len(ids) > 1 {
		v.modelID = ids[1]
	}
	v.selectOption(c, &v.modelID, labels, ids, "model", busy)
	labels, ids = []string{v.msg("chooseTemplate")}, []string{""}
	for _, t := range v.Client.Options.Templates {
		labels = append(labels, t.Name)
		ids = append(ids, t.ID)
	}
	if v.templateID == "" && len(ids) > 1 {
		v.chooseTemplate(ids[1])
	}
	previous := v.templateID
	v.selectOption(c, &v.templateID, labels, ids, "template", busy)
	if previous != v.templateID {
		v.chooseTemplate(v.templateID)
	}
	includeSpeaker := v.includeSpeaker && speaker
	if ui.Checkbox(c, &includeSpeaker, v.msg("includeSpeaker")).Disabled(busy || !speaker).Changed() {
		v.includeSpeaker = includeSpeaker
	}
	ui.Checkbox(c, &v.includeTimestamp, v.msg("includeTimestamp")).Disabled(busy)
	useMembers := v.useMembers && speaker
	if ui.Checkbox(c, &useMembers, v.msg("useMemberMapping")).Disabled(busy || !speaker).Changed() {
		v.useMembers = useMembers
	}
	if !speaker && v.Client.Result != nil && v.Client.Result.Job.EnableSpeaker {
		ui.Text(c, v.msg("speakerUnavailable")).FontSize(11).TextColor(c.Theme().TextMuted)
	}
	ui.Text(c, v.msg("extraInstructions")).FontSize(12)
	ui.TextArea(c, &v.extra).FillWidth().Height(90).Label(v.msg("extraInstructions")).Placeholder(v.msg("extraInstructionsPlaceholder")).Disabled(busy)
	caption := v.msg("submit")
	if v.Client.Running() {
		caption = v.msg("submitting")
	}
	resumable := latestSummary(v.Client.Runs)
	resume := resumable != nil && resumable.Status == "failed"
	if ui.Button(c, caption).FillWidth().Disabled(busy || (!resume && (v.modelID == "" || v.templateID == "" || count == 0))).Clicked() {
		if resume {
			v.Client.Generate(application.SummaryForm{RunID: resumable.ID})
		} else {
			v.Client.Generate(application.SummaryForm{ModelID: v.modelID, TemplateID: v.templateID, IncludeSpeaker: v.includeSpeaker && speaker, IncludeTimestamp: v.includeTimestamp, UseMembers: v.useMembers && speaker, Extra: v.extra})
		}
	}
}
func (v *SummaryView) chooseTemplate(id string) {
	v.templateID = id
	for _, t := range v.Client.Options.Templates {
		if t.ID == id {
			v.includeSpeaker, v.includeTimestamp = t.IncludeSpeaker, t.IncludeTimestamp
			return
		}
	}
}
func (v *SummaryView) selectOption(c *ui.Context, id *string, labels, ids []string, key string, disabled bool) {
	// Duplicate display names remain individually selectable without exposing IDs.
	reserved, used := map[string]bool{}, map[string]bool{}
	for _, label := range labels {
		reserved[label] = true
	}
	for index, label := range labels {
		candidate := label
		if used[label] {
			for suffix := 1; ; suffix++ {
				candidate = fmt.Sprintf("%s (%d)", label, suffix)
				if !reserved[candidate] && !used[candidate] {
					break
				}
			}
		}
		labels[index], used[candidate] = candidate, true
	}
	selected := labels[0]
	for i, value := range ids {
		if value == *id {
			selected = labels[i]
		}
	}
	ui.Text(c, v.msg(key)).FontSize(12)
	if ui.Select(c, &selected, labels).FillWidth().Label(v.msg(key)).Disabled(disabled).Changed() {
		for i, label := range labels {
			if selected == label {
				*id = ids[i]
				break
			}
		}
	}
}
func (v *SummaryView) names(run domain.SummaryRun) (string, string) {
	model, template := v.msg("unknownModel"), v.msg("unknownTemplate")
	for _, m := range v.Client.Options.Models {
		if m.ID == run.ModelConfigID {
			model = m.Name
		}
	}
	for _, t := range v.Client.Options.Templates {
		if t.ID == run.TemplateID {
			template = t.Name
		}
	}
	return model, template
}
func summaryStatus(run *domain.SummaryRun) string {
	if run == nil {
		return "idle"
	}
	if run.Status == "running" {
		return "summarizing"
	}
	return run.Status
}
func summaryDisplayStatus(result *domain.Result, latest *domain.SummaryRun) string {
	hasSummary := false
	if result != nil {
		summary := result.Summary
		hasSummary = strings.TrimSpace(summary.Overview) != "" || len(summary.Topics)+len(summary.Decisions)+len(summary.ActionItems)+len(summary.Risks)+len(summary.FollowUps) > 0
	}
	if latest != nil && latest.Status == "running" {
		return "summarizing"
	}
	if latest != nil && latest.Status == "failed" && !hasSummary {
		return "failed"
	}
	if hasSummary || (latest != nil && latest.Status == "completed") {
		return "completed"
	}
	return "idle"
}
func (v *SummaryView) history(c *ui.Context, run domain.SummaryRun, position int) {
	model, template := v.names(run)
	marker := ""
	if v.Client.Result != nil && v.Client.Result.Job.ActiveSummaryRunID != nil && *v.Client.Result.Job.ActiveSummaryRunID == run.ID {
		marker = v.msg("currentResult")
	} else if latest := latestSummary(v.Client.Runs); latest != nil && latest.ID == run.ID {
		marker = v.msg("latest")
	}
	label := []string{fmt.Sprintf(localized(v.Locale, "记录 %d", "Record %d"), position), template, model, createdText(v.Locale, run.CreatedAt), statusText(v.Locale, summaryStatus(&run))}
	if marker != "" {
		label = append(label, marker)
	}
	if run.ErrorMessage != nil {
		label = append(label, summaryError(v.Locale, errors.New(*run.ErrorMessage)))
	}
	b := ui.ButtonBase(c.Key(run.ID)).FillWidth().Padding(10).Radius(8).Label(strings.Join(label, " · ")).Role(ui.RoleToggleButton).Selected(v.Client.SelectedID == run.ID)
	if v.Client.SelectedID == run.ID {
		b.Background(palette(c).Selected).TextColor(c.Theme().Text)
	}
	b.Children(func() {
		ui.Column(c).FillWidth().Gap(5).Children(func() {
			ui.Row(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, template).Grow(1).MinWidth(0).FontSize(12).Bold()
				if marker != "" {
					ui.Text(c, marker).FontSize(10)
				}
				statusBadge(c, v.Locale, summaryStatus(&run))
			})
			ui.Text(c, model+" · "+createdText(v.Locale, run.CreatedAt)).FontSize(11).TextColor(c.Theme().TextMuted)
			if run.Result != nil {
				ui.Text(c, run.Result.Overview).SingleLine().FontSize(11)
			}
			if run.ErrorMessage != nil {
				ui.Text(c, summaryError(v.Locale, errors.New(*run.ErrorMessage))).FontSize(11).TextColor(c.Theme().Danger)
			}
		})
	})
	if b.Clicked() {
		v.Client.SelectedID = run.ID
	}
}
func latestSummary(runs []domain.SummaryRun) *domain.SummaryRun {
	if len(runs) == 0 {
		return nil
	}
	latest := 0
	for i := 1; i < len(runs); i++ {
		if runs[i].UpdatedAt > runs[latest].UpdatedAt || (runs[i].UpdatedAt == runs[latest].UpdatedAt && runs[i].ID > runs[latest].ID) {
			latest = i
		}
	}
	return &runs[latest]
}
func (v *SummaryView) preview(c *ui.Context) {
	run := v.Client.Selected()
	previewStatus := summaryStatus(run)
	if run == nil {
		previewStatus = summaryDisplayStatus(v.Client.Result, latestSummary(v.Client.Runs))
	}
	ui.Row(c).FillWidth().Gap(12).Justify(ui.Center).Children(func() {
		ui.Text(c, v.msg("preview")).FontSize(16).Bold().Role(ui.RoleHeading).Level(2)
		statusBadge(c, v.Locale, previewStatus)
	})
	if run != nil {
		model, template := v.names(*run)
		ui.Text(c, template+" · "+model+" · "+createdText(v.Locale, run.CreatedAt)).FontSize(11).TextColor(c.Theme().TextMuted)
		active := v.Client.Result != nil && v.Client.Result.Job.ActiveSummaryRunID != nil && *v.Client.Result.Job.ActiveSummaryRunID == run.ID
		applyable := active && run.Status == "completed"
		caption := v.msg("setCurrent")
		if applyable {
			caption = v.msg("usingCurrent")
		}
		ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
			if textButton(c, caption).Disabled(v.Client.Busy || active || run.Status != "completed" || run.Result == nil).Clicked() {
				v.Client.Select(run.ID)
			}
			if textButton(c, v.msg("deleteCurrent")).Disabled(v.Client.Busy || v.Confirm == nil).Clicked() {
				locale := v.Locale
				v.Client.Delete(run.ID, func(ctx context.Context) (bool, error) { return v.Confirm(ctx, locale) })
			}
			if run.Status == "failed" && textButton(c, presentation.Message(v.Locale, "common.retry")).Disabled(v.Client.Busy || v.Client.Running()).Clicked() {
				v.Client.Generate(application.SummaryForm{RunID: run.ID})
			}
		})
	}
	summary := domain.EmptySummary()
	if v.Client.Result != nil {
		summary = v.Client.Result.Summary
	}
	if run != nil && run.Result != nil {
		r := domain.Result{SummaryRuns: []domain.SummaryRun{*run}}
		r.HydrateSummary()
		summary = r.Summary
	}
	ui.Column(c.Key("notes")).FillWidth().Gap(14).Selectable().Children(func() {
		for _, section := range []struct {
			key   string
			items []string
		}{{"summary", []string{summary.Overview}}, {"topics", summary.Topics}, {"decisions", summary.Decisions}, {"actionItems", summary.ActionItems}, {"risks", summary.Risks}, {"followUps", summary.FollowUps}} {
			if len(section.items) == 0 {
				continue
			}
			label := presentation.Message(v.Locale, "notes."+section.key)
			ui.Text(c, label).Bold().Role(ui.RoleHeading).Level(3)
			items := ui.Column(c.Key(section.key)).FillWidth().Gap(14)
			if section.key != "summary" {
				items.Role(ui.RoleList).Label(label)
			}
			items.Children(func() {
				for index, text := range section.items {
					if text == "" {
						text = presentation.Message(v.Locale, "notes.emptySummary")
					}
					item := ui.Text(c.Key(index), text).PreserveSpaces()
					if section.key != "summary" {
						item.Role(ui.RoleListItem).PositionInSet(index+1, len(section.items))
					}
				}
			})
		}
	})
}
func summaryError(locale string, err error) string {
	key := "requestFailed"
	switch err.Error() {
	case application.ErrJobNotFound.Error():
		key = "jobNotFound"
	case domain.ErrResourceMissing.Error(), domain.ErrCredentialRequired.Error():
		key = "modelMissing"
	case application.ErrWindowScope.Error():
		key = "jobSourceMismatch"
	case domain.ErrAIStructured.Error():
		key = "invalidStructuredJson"
	}
	if errors.Is(err, domain.ErrSummaryEndpoint) || err.Error() == "ai_model_endpoint_changed" {
		return localized(locale, "模型服务地址已变更，请恢复原配置或重新生成总结。", "The model endpoint changed. Restore its original configuration or generate a new summary.")
	}
	return presentation.Message(locale, "aiSummary."+key)
}
func (w *Workspace) openSummary(parent *application.JobWindow, title string) {
	if w.summaryService == nil || w.closed {
		return
	}
	grant, err := parent.OpenChild(application.AISummary)
	if err != nil {
		return
	}
	if w.summaryWindow != nil {
		w.summaryWindow.Close()
	}
	var window *mygo.Window
	controller := client.NewSummaryController(grant, mygo.RunOnMain, func() {
		if window != nil {
			window.Invalidate()
		}
	})
	view := &SummaryView{Client: controller, Locale: w.preferences.Locale, Preferences: w.preferences, useMembers: true}
	window = newDesktopWindow(mygo.WindowOptions{Title: presentation.Format(w.preferences.Locale, "windows.aiSummaryTitle", map[string]any{"title": title}), Width: 1120, Height: 860, MinWidth: 960, MinHeight: 720, Content: ui.View(view.View)})
	view.Close = window.Close
	view.Confirm = func(ctx context.Context, locale string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: window, Type: mygo.MessageWarning, Title: presentation.Message(locale, "aiSummary.deleteTitle"), Message: presentation.Message(locale, "aiSummary.deleteConfirm"), Buttons: []string{presentation.Message(locale, "common.cancel"), presentation.Message(locale, "common.delete")}, CancelButton: 0, DefaultButton: 0, DestructiveButtons: []int{1}})
		return result.Button == 1 && ctx.Err() == nil, err
	}
	w.summaryWindow, w.summaryView, w.summaryTitle = window, view, title
	window.OnClosed(func() {
		controller.Close()
		grant.Close()
		if w.summaryWindow == window {
			w.summaryWindow = nil
			w.summaryView = nil
			w.summaryTitle = ""
		}
	})
	controller.Refresh()
	controller.StartPolling()
}
