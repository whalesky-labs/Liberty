package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

type closeFixture struct {
	coordinator closeCoordinator
	editors     []*client.ResourceEditor
	guards      []*closeGuard
	focused     []int
	confirm     func(bool)
	prompts     int
	resumes     int
	canceled    int
}

func newCloseFixture(t *testing.T, kinds ...application.ResourceKind) *closeFixture {
	t.Helper()
	fixture := &closeFixture{}
	for index, kind := range kinds {
		draft := application.NewResourceDraft(kind)
		editor := client.NewResourceEditor(&resourceEditorPort{}, draft, func(callback func()) { callback() }, func() {}, func() {})
		t.Cleanup(editor.Close)
		switch kind {
		case application.Models:
			editor.Draft.Model.Name = "Synthetic draft model"
		case application.Templates:
			editor.Draft.Template.Name = "Synthetic draft template"
		case application.Members:
			editor.Draft.Member.Name = "Synthetic draft member"
		}
		fixture.editors = append(fixture.editors, editor)
		guard := &closeGuard{check: func() closeCheck { return resourceCloseCheck(editor) }, focus: func() { fixture.focused = append(fixture.focused, index) }}
		fixture.guards = append(fixture.guards, guard)
		fixture.coordinator.register(guard)
	}
	return fixture
}

func (fixture *closeFixture) request(target *closeGuard) bool {
	return fixture.coordinator.allow(target, func(done func(bool)) {
		fixture.prompts++
		fixture.confirm = done
	}, func() { fixture.resumes++ }, func() { fixture.canceled++ })
}

func TestWorkspaceCloseChecksAllEditorsBeforeDestroyingAnything(t *testing.T) {
	fixture := newCloseFixture(t, application.Models, application.Templates, application.Members)
	fixture.editors[0].Draft.Model.Name = "Synthetic unsaved model"
	fixture.editors[1].Draft.Template.Prompt = "Synthetic unsaved prompt"
	fixture.editors[2].MemberSortOrder = ""
	if fixture.request(nil) || fixture.resumes != 0 || fixture.prompts != 1 || fixture.coordinator.committing {
		t.Fatal("workspace started destruction before aggregate confirmation")
	}
	fixture.confirm(false)
	if fixture.resumes != 0 || fixture.canceled != 1 || fixture.coordinator.committing || len(fixture.coordinator.guards) != 3 {
		t.Fatal("cancel removed an editor or authorized workspace destruction")
	}
	for _, editor := range fixture.editors {
		if !editor.Dirty() {
			t.Fatal("cancel discarded an unsaved draft")
		}
	}
	if fixture.request(nil) || fixture.prompts != 2 {
		t.Fatal("cancel prevented a fresh close request")
	}
	fixture.confirm(true)
	if fixture.resumes != 1 || fixture.coordinator.committing {
		t.Fatal("approval did not resume exactly once or committed outside the resumed event")
	}
	if !fixture.request(nil) || !fixture.coordinator.committing {
		t.Fatal("resumed workspace close did not consume aggregate approval")
	}
	for _, guard := range fixture.guards {
		if !fixture.request(guard) {
			t.Fatal("approved teardown prompted or refused an editor a second time")
		}
	}
	if fixture.prompts != 2 {
		t.Fatal("workspace teardown emitted duplicate confirmation dialogs")
	}
}

func TestWorkspaceCloseRefusesBusyEditorBeforeAnyDiscardDialog(t *testing.T) {
	fixture := newCloseFixture(t, application.Templates, application.Members)
	fixture.editors[0].Draft.Template.Name = "Synthetic draft"
	fixture.editors[1].Busy = true
	if fixture.request(nil) || fixture.prompts != 0 || fixture.resumes != 0 || fixture.coordinator.committing {
		t.Fatal("workspace closed or prompted while an editor was saving")
	}
	if len(fixture.focused) != 1 || fixture.focused[0] != 1 {
		t.Fatal("saving editor was not brought forward")
	}
	fixture.editors[1].Busy = false
	if fixture.request(nil) || fixture.prompts != 1 {
		t.Fatal("editor completion did not permit a fresh discard request")
	}
}

func TestWorkspaceCloseAbortRevokesPendingAndApprovedGlobalAttempts(t *testing.T) {
	for _, approved := range []bool{false, true} {
		fixture := newCloseFixture(t, application.Members)
		if fixture.request(nil) {
			t.Fatal("dirty editor bypassed confirmation")
		}
		oldCallback := fixture.confirm
		if approved {
			oldCallback(true)
		}
		resumes := fixture.resumes
		fixture.coordinator.abort(nil)
		oldCallback(true)
		if fixture.resumes != resumes || fixture.coordinator.pending != nil || fixture.coordinator.approved != nil || fixture.coordinator.committing || len(fixture.coordinator.guards) != 1 || !fixture.editors[0].Dirty() {
			t.Fatal("aborted global close retained authorization or discarded an editor")
		}
		if fixture.request(fixture.guards[0]) || fixture.prompts != 2 {
			t.Fatal("aborted global close prevented an individual close confirmation")
		}
		fixture.confirm(false)
	}
}

func TestWorkspaceCloseAbortDoesNotCancelIndividualEditorAttempt(t *testing.T) {
	fixture := newCloseFixture(t, application.Templates)
	guard := fixture.guards[0]
	if fixture.request(guard) {
		t.Fatal("dirty editor bypassed confirmation")
	}
	fixture.coordinator.abort(nil)
	if fixture.coordinator.pending == nil || fixture.canceled != 0 {
		t.Fatal("global abort canceled an unrelated editor confirmation")
	}
	fixture.confirm(true)
	if !fixture.request(guard) || fixture.coordinator.committing {
		t.Fatal("individual approval was lost or authorized global teardown")
	}
}

func TestWorkspaceAndEditorCloseRequestsShareOnePendingDialog(t *testing.T) {
	fixture := newCloseFixture(t, application.Templates, application.Members)
	if fixture.request(fixture.guards[0]) {
		t.Fatal("dirty editor closed before confirmation")
	}
	for range 3 {
		if fixture.request(nil) || fixture.request(fixture.guards[0]) || fixture.request(fixture.guards[1]) {
			t.Fatal("repeated close bypassed the pending editor dialog")
		}
	}
	if fixture.prompts != 1 || fixture.canceled != 6 || fixture.coordinator.committing {
		t.Fatal("overlapping close requests created dialogs or retained another editor switch")
	}
	fixture.confirm(false)
	if fixture.request(nil) || fixture.prompts != 2 {
		t.Fatal("workspace close could not be retried after cancel")
	}
	if fixture.request(fixture.guards[0]) || fixture.prompts != 2 {
		t.Fatal("individual editor closed during aggregate confirmation")
	}
	fixture.confirm(true)
	if fixture.request(fixture.guards[0]) {
		t.Fatal("individual close consumed aggregate approval")
	}
	if !fixture.request(nil) {
		t.Fatal("aggregate approval was lost to an unrelated close request")
	}
}

func TestEditorApprovalDoesNotAuthorizeOtherEditorsOrWholeWorkspace(t *testing.T) {
	fixture := newCloseFixture(t, application.Templates, application.Members)
	if fixture.request(fixture.guards[0]) {
		t.Fatal("dirty editor closed immediately")
	}
	fixture.confirm(true)
	if fixture.request(nil) || fixture.request(fixture.guards[1]) {
		t.Fatal("one editor's discard approval escaped its scope")
	}
	if !fixture.request(fixture.guards[0]) || fixture.coordinator.committing {
		t.Fatal("editor close did not consume only its own approval")
	}
	fixture.coordinator.remove(fixture.guards[0])
	if fixture.request(nil) || fixture.prompts != 2 {
		t.Fatal("remaining unsaved editor lost workspace close protection")
	}
}

func TestDiscardApprovalCannotCloseChangedOrSavingEditor(t *testing.T) {
	changes := map[string]func(*client.ResourceEditor){
		"draft":      func(editor *client.ResourceEditor) { editor.Draft.Member.Name = "Changed after prompt" },
		"raw-sort":   func(editor *client.ResourceEditor) { editor.MemberSortOrder = "invalid" },
		"credential": func(editor *client.ResourceEditor) { editor.Password = "synthetic-editor-value" },
		"clear":      func(editor *client.ResourceEditor) { editor.ClearCredential = true },
		"revision":   func(editor *client.ResourceEditor) { editor.Revision++ },
		"busy":       func(editor *client.ResourceEditor) { editor.Busy = true },
	}
	for name, change := range changes {
		for _, queued := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "-during-dialog", true: "-before-resumed-event"}[queued], func(t *testing.T) {
				fixture := newCloseFixture(t, application.Members)
				if fixture.request(nil) {
					t.Fatal("dirty editor did not prompt")
				}
				if queued {
					fixture.confirm(true)
				}
				change(fixture.editors[0])
				if queued {
					if fixture.request(nil) {
						t.Fatal("queued quit accepted stale discard approval")
					}
				} else {
					fixture.confirm(true)
					if fixture.resumes != 0 {
						t.Fatal("changed draft resumed destruction")
					}
				}
				if fixture.canceled != 1 || fixture.coordinator.committing || len(fixture.coordinator.guards) != 1 || len(fixture.focused) != 1 {
					t.Fatal("stale approval did not preserve and focus the changed editor")
				}
			})
		}
	}
}

func TestAggregateCloseRechecksCleanEditorsAndWindowRoster(t *testing.T) {
	for _, change := range []string{"clean-became-dirty", "opened", "closed", "replaced"} {
		t.Run(change, func(t *testing.T) {
			fixture := newCloseFixture(t, application.Templates)
			clean := &client.ResourceEditor{}
			cleanGuard := &closeGuard{check: func() closeCheck { return resourceCloseCheck(clean) }, focus: func() {}}
			fixture.coordinator.register(cleanGuard)
			if fixture.request(nil) {
				t.Fatal("workspace bypassed its dirty editor")
			}
			switch change {
			case "clean-became-dirty":
				clean.Draft.Member.Name = "New draft"
			case "opened":
				fixture.coordinator.register(&closeGuard{check: func() closeCheck { return resourceCloseCheck(&client.ResourceEditor{}) }, focus: func() {}})
			case "closed":
				fixture.coordinator.remove(cleanGuard)
			case "replaced":
				fixture.coordinator.remove(cleanGuard)
				fixture.coordinator.register(&closeGuard{check: cleanGuard.check, focus: func() {}})
			}
			fixture.confirm(true)
			if fixture.resumes != 0 || fixture.canceled != 1 || fixture.coordinator.committing {
				t.Fatal("aggregate discard approval survived a changed editor roster or draft")
			}
		})
	}
}

func TestCleanWorkspaceClosesWithoutDialogAndLateCallbacksCannotResume(t *testing.T) {
	fixture := newCloseFixture(t)
	if !fixture.request(nil) || fixture.prompts != 0 {
		t.Fatal("empty workspace needed confirmation")
	}
	fixture = newCloseFixture(t, application.Models)
	fixture.editors[0].Draft.Model.Name = ""
	if !fixture.request(nil) || fixture.prompts != 0 {
		t.Fatal("a clean editor needed discard confirmation")
	}
	fixture = newCloseFixture(t, application.Members)
	if fixture.request(nil) {
		t.Fatal("dirty editor did not prompt")
	}
	fixture.coordinator.finish()
	fixture.confirm(true)
	if fixture.resumes != 0 || fixture.coordinator.pending != nil || fixture.coordinator.approved != nil {
		t.Fatal("a late dialog callback reopened shutdown")
	}
	if !fixture.request(nil) || !fixture.request(fixture.guards[0]) || fixture.prompts != 1 {
		t.Fatal("session ending teardown started a new dialog or refused termination")
	}
}

func TestNativeCloseEventsPreserveBusyEditorsAndCannotBlockSessionEnd(t *testing.T) {
	fixture := newCloseFixture(t, application.Members)
	fixture.editors[0].Busy = true
	workspace := &Workspace{closing: fixture.coordinator}
	quit := &mygo.QuitEvent{}
	workspace.handleBeforeQuit(quit)
	closeEvent := &mygo.CloseEvent{}
	workspace.handleMainClose(closeEvent)
	if !quit.DefaultPrevented() || !closeEvent.DefaultPrevented() || workspace.closing.committing {
		t.Fatal("native quit or main close ignored the busy editor preflight")
	}
	if len(fixture.focused) != 2 {
		t.Fatal("native close events failed to focus the busy editor")
	}
	ending := &mygo.QuitEvent{SessionEnding: true}
	workspace.handleBeforeQuit(ending)
	if ending.DefaultPrevented() || !workspace.closing.committing || len(workspace.closing.guards) != 0 {
		t.Fatal("OS session ending was prevented or retained asynchronous close checks")
	}
	closeEvent = &mygo.CloseEvent{}
	workspace.handleMainClose(closeEvent)
	if closeEvent.DefaultPrevented() || len(fixture.focused) != 2 {
		t.Fatal("session ending attempted asynchronous editor confirmation")
	}
}

func TestCloseDialogsUseCurrentLanguageAndSafeKeyboardDefaults(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, all := range []bool{false, true} {
			options := closeMessageOptions(nil, locale, all)
			if options.DefaultButton != 0 || options.CancelButton != 0 || len(options.Buttons) != 2 ||
				len(options.DestructiveButtons) != 1 || options.DestructiveButtons[0] != 1 {
				t.Fatal("Enter/Escape or the destructive action targets the wrong close button")
			}
			cancel, discard := "取消", "放弃修改"
			if locale == "en-US" {
				cancel, discard = "Cancel", "Discard Changes"
			}
			if options.Buttons[0] != cancel || options.Buttons[1] != discard || options.Title == "" || options.Message == "" {
				t.Fatal("close dialog did not use the current language")
			}
			other := closeMessageOptions(nil, locale, !all)
			if options.Message == other.Message {
				t.Fatal("close dialog does not distinguish one editor from the entire workspace")
			}
		}
	}
}

func TestCloseCancelLeavesEditorAbleToSave(t *testing.T) {
	draft := application.NewResourceDraft(application.Members)
	draft.Member.Name = "Synthetic member"
	callbacks := make(chan func(), 2)
	port := &resourceEditorPort{draft: draft, saved: make(chan domain.CredentialUpdate, 1)}
	editor := client.NewResourceEditor(port, draft, func(callback func()) { callbacks <- callback }, func() {}, func() {})
	defer editor.Close()
	editor.Draft.Member.Department = "Changed department"
	fixture := &closeFixture{}
	guard := &closeGuard{check: func() closeCheck { return resourceCloseCheck(editor) }, focus: func() {}}
	fixture.coordinator.register(guard)
	if fixture.request(nil) {
		t.Fatal("workspace did not ask before discarding the editor")
	}
	fixture.confirm(false)
	editor.Save()
	select {
	case callback := <-callbacks:
		callback()
	case <-time.After(5 * time.Second):
		t.Fatal("canceling workspace close canceled the still-open editor service")
	}
	if editor.Error != nil || editor.Busy || editor.Dirty() || editor.Draft.Member.Department != "Changed department" {
		t.Fatal("editor could not save after workspace close was canceled")
	}
	if !fixture.request(nil) || fixture.prompts != 1 {
		t.Fatal("saved editor still blocked workspace close")
	}
}
