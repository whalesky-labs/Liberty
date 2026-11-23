package desktop

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type NotesView struct {
	Client         *client.ResultController
	Preferences    domain.UIPreferences
	Close          func()
	retrying       bool
	retryingResult *domain.Result
}

func (v *NotesView) View(c *ui.Context) {
	locale := v.Preferences.Locale
	p := presentation.PreferenceColors(c.Theme().Dark, v.Preferences)
	m := func(key string) string { return presentation.Message(locale, "notes."+key) }
	if v.Client.ResultError != nil || v.Client.SelectedID == "" || (v.retrying && v.Client.Result != v.retryingResult) {
		v.retrying = false
		v.retryingResult = nil
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
						ui.Text(c, title).FontSize(23).Bold().Role(ui.RoleHeading).Level(1)
						ui.Text(c, m("windowCopy")).FontSize(12).TextColor(c.Theme().TextMuted)
					})
					ui.Row(c.Key("summary-status")).Role(ui.RoleStatus).Children(func() { statusBadge(c, locale, status) })
					if textButton(c, presentation.Message(locale, "common.closeWindow")).Shrink(0).Disabled(v.Close == nil).Clicked() && v.Close != nil {
						v.Close()
					}
				})
			})
			notes := ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Label(m("sectionTitle")).Busy(loading)
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
							v.retryingResult = v.Client.Result
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
					ui.Row(c).FillWidth().Justify(ui.Center).AlignItems(ui.Center).Gap(12).Children(func() {
						ui.Text(c, m("sectionTitle")).FontSize(18).Bold().Role(ui.RoleHeading).Level(2)
						statusBadge(c, locale, status)
					})
					ui.Column(c.Key("notes")).FillWidth().Gap(14).Selectable().Children(func() {
						panel(c.Key("overview"), func() {
							ui.Text(c, m("summary")).Bold().Role(ui.RoleHeading).Level(3)
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
								ui.Text(c, m(section.key)).Bold().Role(ui.RoleHeading).Level(3)
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
	window = mygo.NewWindow(mygo.WindowOptions{Title: presentation.Format(w.preferences.Locale, "windows.meetingNotesTitle", map[string]any{"title": title}), Width: 1120, Height: 860, MinWidth: 920, MinHeight: 720, Content: ui.View(view.View)})
	view.Close = window.Close
	w.notes, w.notesView = window, view
	window.OnClosed(func() {
		controller.Close()
		grant.Close()
		if w.notes == window {
			w.notes, w.notesView = nil, nil
		}
	})
	controller.Select(id)
	controller.StartPolling()
}
