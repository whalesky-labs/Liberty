package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type ResultView struct {
	Preferences                                   domain.UIPreferences
	Client                                        *client.ResultController
	Locale                                        string
	transcript                                    ui.ListState
	lastJob                                       string
	lastSource                                    string
	query, speaker                                string
	Close                                         func()
	OpenNotes                                     func(string)
	OpenSummary                                   func(string)
	Actions                                       *client.ResultActionController
	EditingEnabled                                bool
	WordExportEnabled                             bool
	RemoteCapabilities                            *domain.RemoteCapabilities
	editingSegment, originalSpeaker, draftSpeaker string
	focusSpeakerSegment                           string
	transcriptCache                               transcriptCache
	displayResult                                 *domain.Result
	displaySegments                               []domain.Segment
	canRenameSpeaker                              bool
}

type transcriptFilterOption struct {
	label string
	count int
}

type transcriptCache struct {
	locale       string
	segmentData  *domain.Segment
	segmentCount int
	options      []transcriptFilterOption
	searchable   []string
	query        string
	speaker      string
	visible      []domain.Segment
	valid        bool
}

func NewResultView(c *client.ResultController, locale string) *ResultView {
	return &ResultView{Client: c, Locale: locale}
}
func (v *ResultView) text(zh, en string) string { return localized(v.Locale, zh, en) }
func (v *ResultView) msg(key string) string     { return presentation.Message(v.Locale, "workbench."+key) }
func (v *ResultView) format(key string, values map[string]any) string {
	return presentation.Format(v.Locale, "workbench."+key, values)
}
func (v *ResultView) View(c *ui.Context) {
	p := presentation.PreferenceColors(c.Theme().Dark, v.Preferences)
	ui.Themed(c, &p.Theme).Fill().Children(func() { ui.Column(c).Fill().Padding(12).Gap(14).Background(p.Content).Children(func() { v.Body(c) }) })
}
func (v *ResultView) Body(c *ui.Context) {
	if v.Client.ResultError != nil {
		ui.Text(c, v.msg("loadFailed")).Role(ui.RoleAlert).TextColor(c.Theme().Danger).Selectable()
		if textButton(c, presentation.Message(v.Locale, "common.retry")).Clicked() {
			v.Client.Select(v.Client.SelectedID)
		}
		v.closeAction(c)
		return
	}
	if v.Client.LoadingResult && v.Client.Result == nil {
		ui.Text(c, v.msg("loading")).Role(ui.RoleStatus).Busy(true)
		v.closeAction(c)
		return
	}
	r := v.Client.Result
	if r == nil {
		ui.Text(c, v.msg("notFound")).Role(ui.RoleStatus)
		v.closeAction(c)
		return
	}
	if v.lastJob != r.Job.ID || v.lastSource != r.Job.Source {
		v.lastJob, v.lastSource = r.Job.ID, r.Job.Source
		v.transcript = ui.ListState{}
		v.query = ""
		v.speaker = ""
		v.editingSegment, v.originalSpeaker, v.draftSpeaker = "", "", ""
		v.focusSpeakerSegment = ""
		v.transcriptCache = transcriptCache{}
	}
	job := r.Job
	if job.Source == "remote" && (!meetingOperationAvailable(job.Source, v.RemoteCapabilities, "jobs.read") || !meetingOperationAvailable(job.Source, v.RemoteCapabilities, "jobs.result.read")) {
		if v.Close != nil && textButton(c, presentation.Message(v.Locale, "common.closeWindow")).Clicked() {
			v.Close()
		}
		ui.Text(c, v.msg("remoteOperationUnavailable")).TextColor(c.Theme().Warning).Role(ui.RoleStatus)
		return
	}
	// Controllers publish immutable result snapshots. Legacy speaker matching
	// belongs to a changed snapshot, not every repaint of the same transcript.
	if v.displayResult != r {
		v.displayResult, v.displaySegments = r, r.DisplaySegments()
		v.canRenameSpeaker = r.CanRenameSpeaker()
	}
	segments := v.displaySegments
	panel(c, func() {
		ui.Row(c).Children(func() {
			if v.Close != nil && textButton(c, presentation.Message(v.Locale, "common.closeWindow")).Clicked() {
				v.Close()
			}
		})
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, job.Title).FontSize(23).Bold().Grow(1).MinWidth(0).Role(ui.RoleHeading).Level(1)
			statusBadge(c, v.Locale, job.OverallStatus)
		})
		ui.Text(c, v.msg("heroCopy")).FontSize(12).TextColor(c.Theme().TextMuted)
		ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
			for _, key := range []string{"aiSummary", "viewNotes", "exportBundle", "exportTranscript", "exportNotes", "exportWord"} {
				kind := map[string]domain.TextExportKind{"exportBundle": domain.ExportBundle, "exportTranscript": domain.ExportTranscript, "exportNotes": domain.ExportNotes, "exportWord": domain.ExportWord}[key]
				disabled := kind == "" || v.Actions == nil || (v.Actions != nil && v.Actions.Busy)
				if key == "viewNotes" {
					disabled = v.OpenNotes == nil || job.Source != "local"
				}
				if key == "aiSummary" {
					disabled = v.OpenSummary == nil || job.Source != "local" || len(segments) == 0
				}
				if key == "exportWord" {
					disabled = disabled || !v.WordExportEnabled || job.Source != "local"
				}
				caption := v.msg(key)
				if key == "exportBundle" && v.Actions != nil && v.Actions.Exporting {
					caption = v.msg("exporting")
				}
				button := ui.Button(c, caption).Disabled(disabled).FontSize(12)
				if key == "aiSummary" || key == "exportBundle" {
					button.Background(c.Theme().Accent).TextColor(c.Theme().AccentText)
				}
				if button.Clicked() {
					if key == "viewNotes" {
						v.OpenNotes(job.Title)
					} else if key == "aiSummary" {
						v.OpenSummary(job.Title)
					} else {
						v.Actions.Export(kind, job.Title, v.Locale)
					}
				}
			}
		})
		if v.Actions != nil {
			if v.Actions.Error != nil {
				ui.Text(c.Key("operation-error"), v.text("操作失败，请重试。", "The operation failed. Please retry.")).TextColor(c.Theme().Danger).Role(ui.RoleAlert).Selectable()
			}
			if v.Actions.Exported {
				ui.Text(c.Key("exported"), v.text("文件已导出。", "File exported.")).TextColor(c.Theme().Success).Role(ui.RoleStatus)
			}
		}
		ui.Row(c).FillWidth().Gap(12).Wrap().Children(func() {
			for _, item := range []struct {
				key   string
				value int
			}{{"transcriptCount", len(segments)}, {"summaryCount", len(r.SummaryRuns)}, {"fileCount", len(job.SourceFiles)}, {"durationMinutes", int(job.DurationMinutes)}} {
				ui.Text(c, v.format(item.key, map[string]any{"count": item.value})).FontSize(11).TextColor(c.Theme().TextMuted)
			}
			hotwords := strings.Join(job.Hotwords, "、")
			if hotwords == "" {
				hotwords = v.msg("notConfigured")
			}
			ui.Text(c, v.format("hotwords", map[string]any{"value": hotwords})).FontSize(11).TextColor(c.Theme().TextMuted)
			note := "notesEmpty"
			if r.Summary.Overview != "" {
				note = "notesReady"
			}
			ui.Text(c, v.msg(note)).FontSize(11).TextColor(c.Theme().TextMuted)
			activeSummary := false
			if activeID := r.Job.ActiveSummaryRunID; activeID != nil {
				for _, run := range r.SummaryRuns {
					if run.ID == *activeID {
						activeSummary = true
						break
					}
				}
			}
			activeSummaryStatus := "summaryEmpty"
			if activeSummary {
				activeSummaryStatus = "activeResultReady"
			}
			ui.Text(c, v.msg(activeSummaryStatus)).FontSize(11).TextColor(c.Theme().TextMuted)
		})
	})
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(12).Gap(12).Border(1, palette(c).Separator).Radius(10).Background(palette(c).Panel).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, v.msg("transcript")).FontSize(16).Bold().Grow(1).Role(ui.RoleHeading).Level(2)
			if ui.TextInput(c, &v.query).Width(280).Placeholder(v.msg("searchPlaceholder")).Label(v.msg("searchPlaceholder")).Changed() {
				v.transcript = ui.ListState{}
			}
		})
		if job.DiarizationStatus == "legacy_unverified" && len(segments) > 0 && segments[0].Speaker != nil {
			ui.Text(c, v.msg("legacySpeakerNotice")).FontSize(12).TextColor(c.Theme().Warning)
		}
		options, visible := v.transcriptData(segments)
		ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
			if segmentButton(c, fmt.Sprintf("%s %d", v.msg("allSpeakers"), len(segments)), v.speaker == "").MaxWidthPercent(100).MinWidth(0).Role(ui.RoleToggleButton).Checked(v.speaker == "").Clicked() {
				v.speaker = ""
				v.transcript = ui.ListState{}
			}
			for _, option := range options {
				if segmentButton(c.Key(option.label), fmt.Sprintf("%s %d", option.label, option.count), v.speaker == option.label).MaxWidthPercent(100).MinWidth(0).Role(ui.RoleToggleButton).Checked(v.speaker == option.label).Clicked() {
					v.speaker = option.label
					v.transcript = ui.ListState{}
				}
			}
		})
		if len(visible) == 0 {
			ui.Text(c, v.msg("emptyFilteredTranscript")).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
			return
		}
		ui.List(c, &v.transcript, len(visible), func(i int) {
			segment := visible[i]
			ui.Column(c.Key(segment.ID)).FillWidth().Padding(12).Gap(10).Children(func() {
				tools := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10)
				tools.Children(func() {
					busy := !v.EditingEnabled || v.Actions == nil || !v.canRenameSpeaker || !meetingOperationAvailable(job.Source, v.RemoteCapabilities, "transcript.speakers.rename") || (v.Actions != nil && v.Actions.Busy)
					if v.editingSegment == segment.ID {
						input := ui.TextInput(c, &v.draftSpeaker).Width(190).Placeholder(v.msg("speakerInputPlaceholder")).Label(v.msg("speakerInputPlaceholder")).Disabled(busy).AutoFocus()
						if input.Shortcut(0, ui.KeyEscape) {
							v.clearSpeakerEdit()
						}
						save := textButton(c, presentation.Message(v.Locale, "common.save")).Disabled(busy || strings.TrimSpace(v.draftSpeaker) == "")
						if (save.Clicked() || input.Submitted()) && !busy && strings.TrimSpace(v.draftSpeaker) != "" {
							v.Actions.Rename(v.originalSpeaker, v.draftSpeaker, v.Locale)
							v.clearSpeakerEdit()
						}
						if textButton(c, presentation.Message(v.Locale, "common.cancel")).Disabled(busy).Clicked() {
							v.clearSpeakerEdit()
						}
					} else {
						ui.Text(c, v.speakerLabel(segment)).MinWidth(0).FontSize(12).Bold().Padding(3, 8).Radius(5).Background(palette(c).Selected).TextColor(palette(c).NavAccent)
						edit := textButton(c.Key("edit-speaker"), presentation.Message(v.Locale, "common.edit")).Shrink(0).Disabled(busy)
						if v.focusSpeakerSegment == segment.ID {
							edit.Focus()
							v.focusSpeakerSegment = ""
						}
						if edit.Clicked() {
							v.editingSegment = segment.ID
							v.originalSpeaker = ""
							if segment.Speaker != nil {
								v.originalSpeaker = strings.TrimSpace(*segment.Speaker)
							}
							v.draftSpeaker = v.originalSpeaker
						}
					}
					ui.Row(c).Grow(1)
					// Preserve the original timeline's mm:ss clock (wraps after 60 minutes).
					ui.Text(c, fmt.Sprintf("%02d:%02d - %02d:%02d", segment.StartMS/60000%60, segment.StartMS/1000%60, segment.EndMS/60000%60, segment.EndMS/1000%60)).Shrink(0).FontSize(12).TextColor(c.Theme().TextMuted)
				})
				if v.editingSegment == segment.ID && tools.Shortcut(0, ui.KeyEscape) {
					v.clearSpeakerEdit()
				}
				ui.Text(c, segment.Text).PreserveSpaces().Selectable()
				separator(c)
			})
		}).Grow(1).MinHeight(0).Focusable().Label(v.msg("transcript"))
	})
}
func (v *ResultView) clearSpeakerEdit() {
	v.focusSpeakerSegment = v.editingSegment
	v.editingSegment, v.originalSpeaker, v.draftSpeaker = "", "", ""
}
func (v *ResultView) closeAction(c *ui.Context) {
	if v.Close != nil && textButton(c, presentation.Message(v.Locale, "common.closeWindow")).Clicked() {
		v.Close()
	}
}
func (v *ResultView) speakerLabel(segment domain.Segment) string {
	if segment.Speaker != nil && strings.TrimSpace(*segment.Speaker) != "" {
		return strings.TrimSpace(*segment.Speaker)
	}
	return presentation.Message(v.Locale, "common.unknownSpeaker")
}

func (v *ResultView) transcriptData(segments []domain.Segment) ([]transcriptFilterOption, []domain.Segment) {
	query := strings.ToLower(strings.TrimSpace(v.query))
	var segmentData *domain.Segment
	if len(segments) > 0 {
		segmentData = &segments[0]
	}
	cache := &v.transcriptCache
	if cache.locale != v.Locale || cache.segmentData != segmentData || cache.segmentCount != len(segments) {
		options := make([]transcriptFilterOption, 0, 4)
		optionIndexes := make(map[string]int, 4)
		for _, segment := range segments {
			label := v.speakerLabel(segment)
			if optionIndex, ok := optionIndexes[label]; ok {
				options[optionIndex].count++
			} else {
				optionIndexes[label] = len(options)
				options = append(options, transcriptFilterOption{label: label, count: 1})
			}
		}
		*cache = transcriptCache{locale: v.Locale, segmentData: segmentData, segmentCount: len(segments), options: options}
	}
	if cache.valid && cache.query == query && cache.speaker == v.speaker {
		return cache.options, cache.visible
	}
	if query == "" && v.speaker == "" {
		cache.query, cache.speaker, cache.visible, cache.valid = query, v.speaker, segments, true
		return cache.options, cache.visible
	}
	if query != "" && cache.searchable == nil {
		cache.searchable = make([]string, len(segments))
		for index, segment := range segments {
			speaker := ""
			if segment.Speaker != nil {
				speaker = *segment.Speaker
			}
			cache.searchable[index] = strings.ToLower(speaker + " " + segment.Text)
		}
	}
	visible := make([]domain.Segment, 0, len(segments))
	for index, segment := range segments {
		if v.speaker != "" && v.speakerLabel(segment) != v.speaker {
			continue
		}
		if query != "" && !strings.Contains(cache.searchable[index], query) {
			continue
		}
		visible = append(visible, segment)
	}
	cache.query, cache.speaker, cache.visible, cache.valid = query, v.speaker, visible, true
	return cache.options, cache.visible
}
