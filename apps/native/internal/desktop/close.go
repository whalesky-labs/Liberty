package desktop

import (
	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
)

type closeCheck struct {
	busy, dirty bool
	unchanged   func() bool
}

type closeGuard struct {
	check func() closeCheck
	focus func()
}

type closeAttempt struct {
	target *closeGuard
	guards []*closeGuard
	checks []closeCheck
	cancel func()
}

type closeCoordinator struct {
	guards            []*closeGuard
	pending, approved *closeAttempt
	committing        bool
}

type settingsCloseGate struct {
	pending, finished bool
}

func (gate *settingsCloseGate) allow(settings *client.SettingsController, resume func(), failed func(error)) bool {
	if gate.finished {
		return true
	}
	if gate.pending {
		return false
	}
	if settings == nil || !settings.Busy && !settings.Dirty() {
		return true
	}
	gate.pending = true
	settings.Flush(func(err error) {
		gate.pending = false
		if gate.finished {
			return
		}
		if err != nil {
			failed(err)
		} else {
			resume()
		}
	})
	return false
}

func (gate *settingsCloseGate) finish() {
	gate.finished, gate.pending = true, false
}

func (c *closeCoordinator) register(guard *closeGuard) {
	c.guards = append(c.guards, guard)
}

func (c *closeCoordinator) remove(guard *closeGuard) {
	for index, current := range c.guards {
		if current == guard {
			c.guards = append(c.guards[:index], c.guards[index+1:]...)
			return
		}
	}
}

func (c *closeCoordinator) finish() {
	c.committing = true
	c.pending, c.approved, c.guards = nil, nil, nil
}

func (c *closeCoordinator) abort(target *closeGuard) {
	if attempt := c.pending; attempt != nil && attempt.target == target {
		c.pending = nil
		attempt.cancel()
	}
	if attempt := c.approved; attempt != nil && attempt.target == target {
		c.approved = nil
		attempt.cancel()
	}
}

func (c *closeCoordinator) allow(target *closeGuard, confirm func(func(bool)), resume, cancel func()) bool {
	if c.committing {
		return true
	}
	if c.pending != nil {
		if c.pending.target != target {
			cancel()
		}
		return false
	}
	if c.approved != nil && c.approved.target != target {
		cancel()
		return false
	}
	if attempt := c.approved; attempt != nil {
		c.approved = nil
		if !c.valid(attempt) {
			attempt.cancel()
			return false
		}
		c.committing = target == nil
		return true
	}
	guards := append([]*closeGuard(nil), c.guards...)
	if target != nil {
		found := false
		for _, guard := range guards {
			found = found || guard == target
		}
		if !found {
			cancel()
			return false
		}
		guards = []*closeGuard{target}
	}
	attempt := &closeAttempt{target: target, guards: guards, cancel: cancel}
	dirty := false
	for _, guard := range guards {
		check := guard.check()
		if check.busy {
			guard.focus()
			cancel()
			return false
		}
		attempt.checks = append(attempt.checks, check)
		dirty = dirty || check.dirty
	}
	if !dirty {
		c.committing = target == nil
		return true
	}
	c.pending = attempt
	confirm(func(yes bool) {
		if c.pending != attempt {
			return
		}
		c.pending = nil
		if !yes || !c.valid(attempt) {
			cancel()
			return
		}
		c.approved = attempt
		resume()
	})
	return false
}

func (c *closeCoordinator) valid(attempt *closeAttempt) bool {
	if attempt.target == nil && len(attempt.guards) != len(c.guards) {
		return false
	}
	for index, guard := range attempt.guards {
		found := false
		for _, current := range c.guards {
			found = found || current == guard
		}
		if !found {
			return false
		}
		if !attempt.checks[index].unchanged() {
			guard.focus()
			return false
		}
	}
	return true
}

func resourceCloseCheck(editor *client.ResourceEditor) closeCheck {
	draft, sortOrder, password := editor.Draft, editor.MemberSortOrder, editor.Password
	clearCredential, revision := editor.ClearCredential, editor.Revision
	return closeCheck{busy: editor.Busy, dirty: editor.Dirty(), unchanged: func() bool {
		return !editor.Busy && editor.Draft == draft && editor.MemberSortOrder == sortOrder &&
			editor.Password == password && editor.ClearCredential == clearCredential && editor.Revision == revision
	}}
}

func (w *Workspace) installCloseGuards() {
	w.main.OnClose(w.handleMainClose)
	offQuit := mygo.App.OnBeforeQuit(w.handleBeforeQuit)
	w.main.OnClosed(func() {
		w.closing.finish()
		w.settingsClosing.finish()
		offQuit()
	})
}

func (w *Workspace) handleMainClose(event *mygo.CloseEvent) {
	if !w.allowWorkspaceClose(w.main.Close) {
		event.PreventDefault()
	}
}

func (w *Workspace) handleBeforeQuit(event *mygo.QuitEvent) {
	if event.SessionEnding {
		w.closing.finish()
		w.settingsClosing.finish()
		return
	}
	if !w.allowWorkspaceClose(mygo.App.Quit) {
		event.PreventDefault()
	}
}

func (w *Workspace) allowWorkspaceClose(resume func()) bool {
	if w.closed || w.closing.committing {
		return true
	}
	if w.settings != nil && !w.settingsClosing.allow(w.settings.Client, func() {
		if !w.closed {
			resume()
		}
	}, func(error) {
		if w.closed {
			return
		}
		w.closing.abort(nil)
		if w.tab != "/settings" {
			w.lastAppPath = w.tab
		}
		w.tab = "/settings"
		if w.settings.section == "" || w.settings.section == "localRuntime" || w.settings.section == "diagnostics" {
			w.settings.section = "processingDefaults"
		}
		w.main.Focus()
		w.main.Invalidate()
	}) {
		return false
	}
	return w.closing.allow(nil, w.closeConfirmation(w.main, true), resume, func() {})
}

func (w *Workspace) closeConfirmation(parent *mygo.Window, all bool) func(func(bool)) {
	options := closeMessageOptions(parent, w.preferences.Locale, all)
	return func(done func(bool)) {
		go func() {
			result, err := mygo.Dialog.Message(options)
			mygo.RunOnMain(func() { done(result.Button == 1 && err == nil) })
		}()
	}
}

func closeMessageOptions(parent *mygo.Window, locale string, all bool) mygo.MessageOptions {
	message := localized(locale, "放弃未保存的修改并关闭窗口？", "Discard unsaved changes and close this window?")
	if all {
		message = localized(locale, "放弃所有编辑窗口中未保存的修改并退出应用？", "Discard unsaved changes in all editors and quit Liberty?")
	}
	return mygo.MessageOptions{
		Parent: parent, Type: mygo.MessageWarning, Title: localized(locale, "尚未保存", "Unsaved changes"), Message: message,
		Buttons:      []string{presentation.Message(locale, "common.cancel"), localized(locale, "放弃修改", "Discard Changes")},
		CancelButton: 0, DefaultButton: 0, DestructiveButtons: []int{1},
	}
}
