package desktop

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
	"maps"
	"reflect"
	"slices"
)

type NotesView struct {
	Client        *client.ResultController
	Editor        *client.NotesController
	Preferences   domain.UIPreferences
	Close         func()
	OpenSource    func(string)
	sourceQueries map[string]string
	retrying      bool
}

func (v *NotesView) View(c *ui.Context) {
	locale := v.Preferences.Locale
	p := presentation.PreferenceColors(c.Theme().Dark, v.Preferences)
	m := func(key string) string { return presentation.Message(locale, "notes."+key) }
	if v.Client.ResultError != nil || v.Client.SelectedID == "" || (v.retrying && !v.Client.Refreshing()) {
		v.retrying = false
	}
	loading := v.Client.LoadingResult || v.retrying
	ui.Themed(c, &p.Theme).Fill().Children(func() {
		ui.Column(c).Fill().Padding(16).Gap(16).Background(p.Content).Children(func() {
			title, status := m("windowTitle"), "idle"
			summary := domain.EmptySummary()
			if r := v.Client.Result; r != nil {
				title, status, summary = r.Job.Title, r.Job.SummaryStatus, r.Summary
			}
			panel(c, func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
						ui.Text(c, title).FontSize(23).Role(ui.RoleHeading).Level(1)
						ui.Text(c, localized(locale, "可直接编写或校订纪要，人工内容单独保存。", "Write or revise notes. Your edits are saved independently.")).FontSize(12).TextColor(c.Theme().TextMuted)
					})
					ui.Row(c.Key("summary-status")).Role(ui.RoleStatus).Children(func() { statusBadge(c, locale, status) })
					if v.Editor != nil && v.Client.Result != nil && !v.Editor.Editing && ui.Button(c, localized(locale, "编辑纪要", "Edit notes")).Disabled(loading).Clicked() {
						v.Editor.Begin(*v.Client.Result)
					}
					if v.Editor != nil && v.Editor.Editing {
						if ui.PrimaryButton(c, presentation.Message(locale, "common.save")).Disabled(v.Editor.Busy).Clicked() {
							v.Editor.Save()
						}
						if ui.Button(c, presentation.Message(locale, "common.cancel")).Disabled(v.Editor.Busy).Clicked() {
							v.Editor.Cancel()
						}
					}
					if textButton(c, presentation.Message(locale, "common.closeWindow")).Shrink(0).Disabled(v.Close == nil).Clicked() && v.Close != nil {
						v.Close()
					}
				})
			})
			notes := contentScroll(c).FillWidth().Grow(1).MinHeight(0).Label(m("sectionTitle")).Busy(loading)
			if v.Client.Result != nil {
				notes.Focusable()
			}
			notes.Children(func() {
				ui.Column(c).FillWidth().Gap(14).Children(func() {
					if loading && (v.Client.Result == nil || v.retrying) {
						ui.Text(c.Key("loading"), presentation.Message(locale, "workbench.loading")).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
						if v.Client.Result == nil {
							return
						}
					}
					if v.Client.ResultError != nil {
						ui.Text(c.Key("load-error"), presentation.Message(locale, "workbench.loadFailed")).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
						if textButton(c, presentation.Message(locale, "common.retry")).Disabled(v.retrying).Clicked() {
							v.retrying = v.Client.SelectedID != ""
							v.Client.Refresh()
						}
						if v.Client.Result == nil {
							return
						}
					}
					if v.Client.Result == nil {
						ui.Text(c, presentation.Message(locale, "workbench.notFound")).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
						return
					}
					if v.Client.Result.SummarySourceChanged {
						ui.Text(c, localized(locale, "逐字稿已修改，当前纪要基于旧内容，请核对后使用。", "The transcript has changed. These notes use earlier content; review before use.")).FillWidth().TextColor(c.Theme().Warning).Role(ui.RoleStatus)
					}
					if v.Editor != nil && v.Editor.Editing {
						v.editNotes(c)
						return
					}
					ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Center).Gap(12).Children(func() {
						ui.Text(c, m("sectionTitle")).FontSize(18).Role(ui.RoleHeading).Level(2)
						statusBadge(c, locale, status)
					})
					ui.Column(c.Key("notes")).FillWidth().Gap(14).Selectable().Children(func() {
						panel(c.Key("overview"), func() {
							ui.Text(c, m("summary")).Role(ui.RoleHeading).Level(3)
							v.noteSources(c, "summary")
							text := summary.Overview
							if text == "" {
								text = m("emptySummary")
							}
							ui.Text(c, text).PreserveSpaces()
						})
						for _, section := range []struct {
							key   string
							items []string
						}{{"topics", summary.Topics}, {"decisions", summary.Decisions}, {"actionItems", summary.ActionItems}, {"risks", summary.Risks}, {"followUps", summary.FollowUps}} {
							if len(section.items) == 0 {
								continue
							}
							panel(c.Key(section.key), func() {
								ui.Text(c, m(section.key)).Role(ui.RoleHeading).Level(3)
								v.noteSources(c, section.key)
								ui.Column(c).FillWidth().Gap(14).Role(ui.RoleList).Label(m(section.key)).Children(func() {
									for i, item := range section.items {
										ui.Text(c.Key(i), "• "+item).Role(ui.RoleListItem).PositionInSet(i+1, len(section.items))
									}
								})
							})
						}
					})
				})
			})
		})
	})
}
func (w *Workspace) openNotes(parent *application.JobWindow, id, title string) {
	if w.closed {
		return
	}
	if w.notes != nil {
		// Do not revoke an existing editor before its unsaved-change guard runs.
		if w.notesView == nil || w.notesView.Client.SelectedID == id || w.notesView.Editor != nil && (w.notesView.Editor.Busy || w.notesView.Editor.Editing) {
			w.notes.Focus()
			return
		}
		w.notes.Close()
		if w.notes != nil {
			return
		}
	}
	grant, err := parent.OpenChild(application.MeetingNotes)
	if err != nil {
		if w.resultView != nil && w.resultView.Actions != nil {
			w.resultView.Actions.Error = err
		}
		return
	}
	if w.notes != nil {
		w.notes.Close()
	}
	var window *mygo.Window
	controller := client.NewResult(grant, mygo.RunOnMain, func() {
		if window != nil {
			window.Invalidate()
		}
	})
	view := &NotesView{Client: controller, Preferences: w.preferences}
	window = newDesktopWindow(mygo.WindowOptions{Title: presentation.Format(w.preferences.Locale, "windows.meetingNotesTitle", map[string]any{"title": title}), Width: 1120, Height: 860, MinWidth: 920, MinHeight: 720, Content: ui.View(view.View)})
	view.Close = window.Close
	view.OpenSource = func(segmentID string) {
		if w.resultView != nil && w.resultView.Client.SelectedID == id && w.resultView.Client.Result != nil && w.resultView.Client.Result.Job.Source == "local" {
			w.resultView.pendingSourceSegmentID = segmentID
			w.result.Focus()
			w.result.Invalidate()
		} else {
			w.resultSourceJob, w.resultSourceSegment = id, segmentID
			w.openResultFromSource(id, w.preferences.Locale, "local")
		}
	}
	if w.resultEditing {
		view.Editor = client.NewNotes(grant, mygo.RunOnMain, window.Invalidate, func() {
			controller.Refresh()
			if w.resultView != nil {
				w.resultView.Client.Refresh()
			}
		})
	}
	guard := &closeGuard{check: func() closeCheck {
		e := view.Editor
		if e == nil {
			return closeCheck{unchanged: func() bool { return true }}
		}
		text := e.Overview + "\x00" + e.Topics + "\x00" + e.Decisions + "\x00" + e.ActionItems + "\x00" + e.Risks + "\x00" + e.FollowUps
		sources := maps.Clone(e.Sources)
		for key, ids := range sources {
			sources[key] = slices.Clone(ids)
		}
		return closeCheck{busy: e.Busy, dirty: e.Editing, unchanged: func() bool {
			return !e.Busy && reflect.DeepEqual(sources, e.Sources) && text == e.Overview+"\x00"+e.Topics+"\x00"+e.Decisions+"\x00"+e.ActionItems+"\x00"+e.Risks+"\x00"+e.FollowUps
		}}
	}, focus: window.Focus}
	w.closing.register(guard)
	window.OnClose(func(event *mygo.CloseEvent) {
		if !w.closing.allow(guard, w.closeConfirmation(window, false), window.Close, func() {}) {
			event.PreventDefault()
		}
	})
	w.notes, w.notesView, w.notesTitle = window, view, title
	window.OnClosed(func() {
		w.closing.remove(guard)
		if view.Editor != nil {
			view.Editor.Close()
		}
		controller.Close()
		grant.Close()
		if w.notes == window {
			w.notes, w.notesView, w.notesTitle = nil, nil, ""
		}
	})
	controller.Select(id)
	controller.StartPolling()
}
