package desktop

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func resourceTabTo(t *testing.T, tester *ui.Tester, label string, width, height int) ui.AccessInfo {
	t.Helper()
	for attempt := 0; attempt < 30; attempt++ {
		tester.Key(0, ui.KeyTab)
		for _, node := range tester.AccessAll() {
			if node.Name == label && node.Focused {
				if node.Offscreen || node.Bounds.W < 20 || node.Bounds.H < 10 || node.Bounds.X < 0 || node.Bounds.Y < 0 || node.Bounds.X+node.Bounds.W > float32(width) || node.Bounds.Y+node.Bounds.H > float32(height) {
					t.Fatal("keyboard reached a clipped resource control", node)
				}
				return node
			}
		}
	}
	t.Fatal("keyboard could not reach resource control", label)
	return ui.AccessInfo{}
}

type resourceParityPort struct {
	resourceEditorPort
	saves      chan application.ResourceDraft
	duplicates chan application.ResourceDraft
	failSave   bool
	deletes    chan bool
	failDelete bool
}

func (port *resourceParityPort) Save(_ context.Context, draft application.ResourceDraft, update domain.CredentialUpdate) (application.ResourceDraft, application.MutationOutcome, error) {
	port.saves <- draft
	port.saved <- update
	if port.failSave {
		return application.ResourceDraft{}, application.MutationOutcome{}, domain.ErrCredentialStore
	}
	return draft, application.MutationOutcome{}, nil
}

func (port *resourceParityPort) Duplicate(ctx context.Context, draft application.ResourceDraft, suffix string) (application.ResourceDraft, error) {
	port.duplicates <- draft
	return port.resourceEditorPort.Duplicate(ctx, draft, suffix)
}

func (port *resourceParityPort) Delete(context.Context) (application.MutationOutcome, error) {
	port.deletes <- true
	if port.failDelete {
		return application.MutationOutcome{}, domain.ErrResourceInvalid
	}
	return application.MutationOutcome{}, nil
}

type resourceTemplateRepository struct {
	application.ResourceRepository
	values     []domain.SummaryTemplate
	writeError error
}

func (repository *resourceTemplateRepository) Templates(ctx context.Context) ([]domain.SummaryTemplate, error) {
	return append([]domain.SummaryTemplate(nil), repository.values...), ctx.Err()
}

func (repository *resourceTemplateRepository) SaveTemplate(ctx context.Context, value domain.SummaryTemplate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if repository.writeError != nil {
		return repository.writeError
	}
	for index := range repository.values {
		if repository.values[index].ID == value.ID {
			repository.values[index] = value
			return nil
		}
	}
	repository.values = append(repository.values, value)
	return nil
}

func TestResourceEditorCopiesSavedTemplateAfterKeyboardEditsAndFailure(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 4)
			source := domain.SummaryTemplate{ID: "synthetic-template", Name: "Saved template", Description: "Saved description", Prompt: "Saved prompt", IncludeSpeaker: true}
			repository := &resourceTemplateRepository{values: []domain.SummaryTemplate{source}, writeError: domain.ErrResourceInvalid}
			service := application.NewResources(repository, nil)
			defer service.Close()
			grant, draft, err := service.OpenEditor(context.Background(), application.Templates, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			controller := client.NewResourceEditor(grant, draft, func(callback func()) { callbacks <- callback }, func() {}, func() {})
			defer controller.Close()
			view := &ResourceEditorView{Client: controller, Locale: locale, Preferences: domain.DefaultUIPreferences()}
			tester := ui.NewTester(view.View, 820, 720)
			resourceTabTo(t, tester, view.msg("name"), 820, 720)
			tester.Command("selectAll")
			tester.Type("Unsaved name")
			resourceTabTo(t, tester, view.msg("prompt"), 820, 720)
			tester.Command("selectAll")
			tester.Type("Unsaved prompt")
			source.Prompt = "Latest persisted prompt"
			repository.values[0] = source
			resourceTabTo(t, tester, view.msg("duplicate"), 820, 720)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			if controller.Draft.Template.Name != "Unsaved name" || controller.Draft.Template.Prompt != "Unsaved prompt" || !controller.Dirty() || len(repository.values) != 1 || repository.values[0] != source {
				t.Fatal("failed copy discarded the unsaved editor")
			}
			if _, ok := tester.AccessByRole(ui.RoleAlert, resourceError(locale, domain.ErrResourceInvalid)); !ok {
				t.Fatal("copy failure is not exposed as an alert")
			}
			repository.writeError = nil
			resourceTabTo(t, tester, view.msg("duplicate"), 820, 720)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			if len(repository.values) != 2 || repository.values[0] != source || controller.Draft.Template != repository.values[1] || controller.Draft.Template.Prompt != source.Prompt || controller.Draft.Template.Name != source.Name+localized(locale, " - 副本", " - Copy") || controller.Draft.ID() == source.ID || controller.Draft.ID() == "" || controller.Dirty() || controller.Error != nil {
				t.Fatal("copy retry failed to select a clean persisted copy of the saved template")
			}
			resourceTabTo(t, tester, view.msg("name"), 820, 720)
			tester.Command("selectAll")
			tester.Type("Edited persisted copy")
			resourceTabTo(t, tester, view.msg("save"), 820, 720)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			if len(repository.values) != 2 || repository.values[0] != source || repository.values[1].Name != "Edited persisted copy" || controller.Error != nil || controller.Dirty() {
				t.Fatal("editing the selected copy changed the source or lost its transferred editor scope")
			}
		})
	}
}

func TestResourceEditorModelHelpAndCredentialStateFollowKeyboardSaveRetry(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 4)
			draft := application.NewResourceDraft(application.Models)
			draft.Model = domain.AIModel{ID: "synthetic-model", Name: strings.Repeat("合成模型 Synthetic model ", 12), BaseURL: "https://example.test/v1", Model: strings.Repeat("synthetic-model-", 30), CredentialPresent: true, Enabled: true}
			port := &resourceParityPort{resourceEditorPort: resourceEditorPort{draft: draft, saved: make(chan domain.CredentialUpdate, 2)}, saves: make(chan application.ResourceDraft, 2), failSave: true}
			controller := client.NewResourceEditor(port, draft, func(callback func()) { callbacks <- callback }, func() {}, func() {})
			defer controller.Close()
			view := &ResourceEditorView{Client: controller, Locale: locale, Preferences: domain.DefaultUIPreferences()}
			tester := ui.NewTesterWith(view.View, 760, 680, ui.TesterOptions{Dark: locale == "en-US"})
			model := resourceTabTo(t, tester, view.msg("model"), 760, 680)
			if model.Description != view.msg("modelHelp") || len(model.DescribedBy) != 1 {
				t.Fatal("model field lost the legacy help association", model)
			}
			tester.Command("selectAll")
			tester.Command("copy")
			if tester.Clipboard() != draft.Model.Model {
				t.Fatal("long model value was truncated during keyboard copy")
			}
			resourceTabTo(t, tester, view.msg("clearCredential"), 760, 680)
			tester.Key(0, ui.KeyEnter)
			credential := resourceTabTo(t, tester, view.msg("apiKey"), 760, 680)
			if !credential.Password || credential.Description != view.msg("credentialClearPending") {
				t.Fatal("credential field does not expose its pending retention state", credential)
			}
			resourceTabTo(t, tester, view.msg("keepCredential"), 760, 680)
			tester.Key(0, ui.KeyEnter)
			resourceTabTo(t, tester, view.msg("defaultSwitch"), 760, 680)
			tester.Key(0, ui.KeySpace)
			resourceTabTo(t, tester, view.msg("save"), 760, 680)
			tester.Key(0, ui.KeyEnter)
			region, ok := tester.AccessByRole(ui.RoleRegion, view.msg("editorEditTitle"))
			if !ok || !region.Busy {
				t.Fatal("busy editor has no named busy region", region)
			}
			flushView(t, callbacks, tester)
			<-port.saves
			if update := <-port.saved; update.Action != domain.KeepCredential || !controller.Dirty() {
				t.Fatal("save failure changed the intended credential retention or cleared edits")
			}
			if _, ok := tester.AccessByRole(ui.RoleAlert, resourceError(locale, domain.ErrCredentialStore)); !ok {
				t.Fatal("save failure has no readable alert")
			}
			captureParity(t, tester, "resources-model-error-"+locale)
			port.failSave = false
			resourceTabTo(t, tester, view.msg("save"), 760, 680)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			if saved := <-port.saves; !saved.Model.IsDefault || saved.Model.Model != draft.Model.Model || controller.Dirty() || controller.Error != nil {
				t.Fatal("keyboard save retry lost the long model value or default selection")
			}
			if update := <-port.saved; update.Action != domain.KeepCredential {
				t.Fatal("save retry changed credential intent")
			}
		})
	}
}

func TestResourceListsExposeBusyExistingRowsAndTemplateHeadings(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
			t.Run(fmt.Sprint(kind, "/", locale), func(t *testing.T) {
				view := resourceParityView(kind, locale, 1)
				view.Client.Busy = true
				shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
				tester := ui.NewTesterWith(shell.View, 1100, 760, ui.TesterOptions{Dark: locale == "en-US"})
				role := ui.RoleList
				if kind == application.Members {
					role = ui.RoleTable
				}
				list, ok := tester.AccessByRole(role, view.msg("listTitle"))
				if !ok || !list.Busy {
					t.Fatal("existing rows hide an in-progress resource refresh from accessibility", list)
				}
				if kind == application.Templates {
					heading, ok := tester.AccessByRole(ui.RoleHeading, view.Client.Templates[0].Name)
					if !ok || heading.Level != 3 {
						t.Fatal("template card lost its legacy heading navigation", heading)
					}
				}
				view.Client.Busy = false
				tester.Frame()
				list, _ = tester.AccessByRole(role, view.msg("listTitle"))
				if list.Busy {
					t.Fatal("resource list remained busy after the refresh completed")
				}
				edit := presentation.Message(locale, "common.edit") + ": Synthetic resource 00"
				resourceTabTo(t, tester, edit, 1100, 760)
			})
		}
	}
}

func TestResourceTemplateLongReadonlyPromptWrapsCopiesAndKeepsFooterReachable(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, minimum := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/minimum=%t", locale, minimum), func(t *testing.T) {
				paragraph := strings.Repeat("合成会议提示词需要在编辑器内完整折行并允许保留全部正文复制。", 30)
				if locale == "en-US" {
					paragraph = strings.Repeat("Synthetic meeting prompts must wrap within the editor and preserve the entire text when copied. ", 30)
				}
				draft := application.NewResourceDraft(application.Templates)
				draft.Template = domain.SummaryTemplate{ID: "synthetic-builtin", Name: strings.Repeat("合成模板 Synthetic template ", 18), Description: strings.Repeat("Synthetic description 合成说明 ", 15), Prompt: paragraph + "\n  " + strings.Repeat("unbroken", 60) + "\nFinal synthetic line.  ", Builtin: true, IncludeSpeaker: true}
				callbacks := make(chan func(), 4)
				port := &resourceParityPort{resourceEditorPort: resourceEditorPort{draft: draft}, duplicates: make(chan application.ResourceDraft, 1)}
				controller := client.NewResourceEditor(port, draft, func(callback func()) { callbacks <- callback }, func() {}, func() {})
				defer controller.Close()
				view := &ResourceEditorView{Client: controller, Locale: locale, Preferences: domain.DefaultUIPreferences()}
				options := resourceEditorOptions(application.Templates, locale, draft.ID())
				width, height := options.Width, options.Height
				if minimum {
					width, height = options.MinWidth, options.MinHeight
				}
				tester := ui.NewTesterWith(view.View, width, height, ui.TesterOptions{Dark: locale == "en-US"})
				name := resourceTabTo(t, tester, view.msg("name"), width, height)
				if name.Value != draft.Template.Name || !name.ReadOnly {
					t.Fatal("long built-in name is truncated or editable")
				}
				tester.Command("selectAll")
				tester.Command("copy")
				if tester.Clipboard() != draft.Template.Name {
					t.Fatal("long template name cannot be copied in full")
				}
				prompt := resourceTabTo(t, tester, view.msg("prompt"), width, height)
				if !prompt.ReadOnly || !prompt.Multiline || prompt.Value != draft.Template.Prompt {
					t.Fatal("prompt lost its read-only multiline value", prompt)
				}
				tester.Command("selectAll")
				tester.Command("copy")
				if tester.Clipboard() != draft.Template.Prompt {
					t.Fatal("long prompt copy lost whitespace or offscreen text")
				}
				tester.Key(ui.Ctrl|ui.Shift, ui.KeyHome)
				tester.Key(ui.Shift, ui.KeyDown)
				tester.Command("copy")
				visualLine := tester.Clipboard()
				if len([]rune(visualLine)) < 10 || len(visualLine) >= len(paragraph) || !strings.HasPrefix(paragraph, visualLine) || strings.Contains(visualLine, "\n") {
					t.Fatal("long paragraph does not wrap into keyboard-selectable visual lines", len(visualLine))
				}
				tester.Type("must not edit")
				if controller.Draft != draft {
					t.Fatal("read-only keyboard interaction modified the built-in template")
				}
				if minimum {
					captureParity(t, tester, "resources-template-long-"+locale)
				}
				resourceTabTo(t, tester, view.msg("duplicate"), width, height)
				tester.Key(0, ui.KeyEnter)
				flushView(t, callbacks, tester)
				<-port.duplicates
				prompt = resourceTabTo(t, tester, view.msg("prompt"), width, height)
				if prompt.ReadOnly || controller.Draft.Template.Builtin || controller.Dirty() {
					t.Fatal("duplicate is not a clean editable saved copy")
				}
				tester.Command("selectAll")
				tester.Type("Synthetic replacement prompt")
				tester.Key(0, ui.KeyEscape)
				if controller.Draft.Template.Prompt != "Synthetic replacement prompt" || !controller.Dirty() {
					t.Fatal("Escape in a text field discarded unsaved prompt edits")
				}
			})
		}
	}
}

type resourceParityLists struct {
	models       []domain.AIModel
	readError    error
	deleteError  error
	reads        chan bool
	deletes      chan string
	confirmation chan bool
	confirmed    bool
}

func (service *resourceParityLists) Models(context.Context) ([]domain.AIModel, error) {
	service.reads <- true
	return service.models, service.readError
}
func (service *resourceParityLists) Templates(context.Context) ([]domain.SummaryTemplate, error) {
	return nil, nil
}
func (service *resourceParityLists) Members(context.Context) ([]domain.MeetingMember, error) {
	return nil, nil
}
func (service *resourceParityLists) DeleteModel(_ context.Context, id string) (application.MutationOutcome, error) {
	service.deletes <- id
	return application.MutationOutcome{}, service.deleteError
}
func (service *resourceParityLists) DeleteTemplate(context.Context, string) error { return nil }
func (service *resourceParityLists) DeleteMember(context.Context, string) error   { return nil }

func TestResourceListRefreshRecoversReadWithoutRepeatingDelete(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 8)
			service := &resourceParityLists{models: []domain.AIModel{{ID: "synthetic-model", Name: strings.Repeat("合成模型 Synthetic model ", 15), Model: strings.Repeat("synthetic-model-", 30), Enabled: true, IsDefault: true}}, reads: make(chan bool, 8), deletes: make(chan string, 4), confirmation: make(chan bool, 4)}
			controller := client.NewResources(service, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			controller.Models = service.models
			view := &ResourcesView{Client: controller, Kind: application.Models, Locale: locale, ContentWidth: shellContentWidth, Confirm: func(kind application.ResourceKind, name string) func(context.Context) (bool, error) {
				if kind != application.Models || name != service.models[0].Name {
					t.Fatal("delete confirmation names an unrelated resource")
				}
				return func(context.Context) (bool, error) { service.confirmation <- true; return service.confirmed, nil }
			}}
			shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
			tester := ui.NewTesterWith(shell.View, 1100, 760, ui.TesterOptions{Dark: locale == "en-US"})
			remove := presentation.Message(locale, "common.delete") + ": " + service.models[0].Name
			resourceTabTo(t, tester, remove, 1100, 760)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			<-service.confirmation
			<-service.reads
			if len(service.deletes) != 0 {
				t.Fatal("canceled confirmation deleted a resource")
			}
			service.confirmed, service.deleteError = true, domain.ErrCredentialStore
			resourceTabTo(t, tester, remove, 1100, 760)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			<-service.confirmation
			<-service.deletes
			<-service.reads
			if _, ok := tester.AccessByRole(ui.RoleAlert, resourceError(locale, domain.ErrCredentialStore)); !ok {
				t.Fatal("delete failure is unreadable")
			}
			refresh := localized(locale, "刷新", "Refresh")
			resourceTabTo(t, tester, refresh, 1100, 760)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			<-service.reads
			if len(service.deletes) != 0 || len(service.confirmation) != 0 || controller.Error != nil {
				t.Fatal("list refresh retried a mutation or did not recover the list")
			}
			service.deleteError, service.readError = nil, domain.ErrResourceInvalid
			resourceTabTo(t, tester, remove, 1100, 760)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			<-service.confirmation
			if id := <-service.deletes; id != "synthetic-model" {
				t.Fatal("delete retry targeted an unrelated resource")
			}
			<-service.reads
			service.readError, service.models = nil, nil
			resourceTabTo(t, tester, refresh, 1100, 760)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			<-service.reads
			if len(service.deletes) != 0 || len(service.confirmation) != 0 || controller.Error != nil || len(controller.Models) != 0 || !tester.HasText(view.msg("empty")) {
				t.Fatal("read recovery after an accepted delete repeated the action or kept stale rows")
			}
		})
	}
}

func TestResourceMemberEditorKeyboardResetAndDeleteKeepConfirmation(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			draft := application.NewResourceDraft(application.Members)
			draft.Member = domain.MeetingMember{ID: "synthetic-member", Name: strings.Repeat("合成人员 Synthetic member ", 12), Department: strings.Repeat("Synthetic department 合成部门 ", 12), SortOrder: 17, IsRecorder: true}
			callbacks := make(chan func(), 4)
			port := &resourceParityPort{resourceEditorPort: resourceEditorPort{draft: draft}, deletes: make(chan bool, 2), failDelete: true}
			controller := client.NewResourceEditor(port, draft, func(callback func()) { callbacks <- callback }, func() {}, func() {})
			defer controller.Close()
			confirmed := false
			confirmations := make(chan bool, 4)
			view := &ResourceEditorView{Client: controller, Locale: locale, Preferences: domain.DefaultUIPreferences(), Confirm: func(title, message string) func(context.Context) (bool, error) {
				return func(context.Context) (bool, error) { confirmations <- true; return confirmed, nil }
			}}
			tester := ui.NewTesterWith(view.View, 640, 560, ui.TesterOptions{Dark: locale == "en-US"})
			resourceTabTo(t, tester, view.msg("department"), 640, 560)
			tester.Command("selectAll")
			tester.Command("copy")
			if tester.Clipboard() != draft.Member.Department {
				t.Fatal("long member department was truncated on copy")
			}
			tester.Type("Synthetic replacement department")
			resourceTabTo(t, tester, view.msg("reset"), 640, 560)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			<-confirmations
			if !controller.Dirty() || controller.Draft.Member.Department != "Synthetic replacement department" {
				t.Fatal("canceled reset lost unsaved member data")
			}
			remove := presentation.Message(locale, "common.delete")
			resourceTabTo(t, tester, remove, 640, 560)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			<-confirmations
			if len(port.deletes) != 0 || controller.Draft.ID() != draft.ID() {
				t.Fatal("canceled editor delete bypassed confirmation")
			}
			confirmed = true
			resourceTabTo(t, tester, remove, 640, 560)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			<-confirmations
			<-port.deletes
			if controller.Draft.ID() != draft.ID() || !controller.Dirty() {
				t.Fatal("failed delete discarded the selected member or edits")
			}
			if _, ok := tester.AccessByRole(ui.RoleAlert, resourceError(locale, domain.ErrResourceInvalid)); !ok {
				t.Fatal("editor delete failure has no readable alert")
			}
			port.failDelete = false
			resourceTabTo(t, tester, remove, 640, 560)
			tester.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tester)
			<-confirmations
			<-port.deletes
			if controller.Draft.ID() != "" || controller.Dirty() || controller.Error != nil || controller.MemberSortOrder != "0" {
				t.Fatal("confirmed delete retry did not reset the member editor")
			}
		})
	}
}

type resourceParityFiles struct{}

func (resourceParityFiles) Import(context.Context) (domain.MemberImportResult, bool, error) {
	return domain.MemberImportResult{}, false, nil
}

func (resourceParityFiles) Export(context.Context) (bool, error) { return false, nil }

func TestResourceMemberLongRowsKeepKeyboardActionsAtMainWindowSizes(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, sample := range []struct{ width, height int }{{1100, 760}, {1440, 960}} {
			t.Run(fmt.Sprintf("%s/%d", locale, sample.width), func(t *testing.T) {
				view := resourceParityView(application.Members, locale, 8)
				view.Files = resourceParityFiles{}
				for index := range view.Client.Members {
					view.Client.Members[index].Name = fmt.Sprintf("%02d %s", index, strings.Repeat("合成人员 Synthetic member ", 15))
					view.Client.Members[index].Department = strings.Repeat("Synthetic department 合成部门 ", 20)
				}
				opened := ""
				view.Open = func(kind application.ResourceKind, id string) { opened = id }
				shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
				tester := ui.NewTesterWith(shell.View, sample.width, sample.height, ui.TesterOptions{Dark: locale == "en-US"})
				first := view.Client.Members[0]
				if name, ok := tester.AccessByRole(ui.RoleRow, first.Name); !ok || name.Offscreen {
					t.Fatal("long member name disappeared from the table")
				}
				resourceTabTo(t, tester, presentation.Message(locale, "common.import"), sample.width, sample.height)
				resourceTabTo(t, tester, presentation.Message(locale, "common.export"), sample.width, sample.height)
				edit := presentation.Message(locale, "common.edit") + ": " + first.Name
				resourceTabTo(t, tester, edit, sample.width, sample.height)
				if sample.width == 1100 {
					captureParity(t, tester, "resources-members-long-"+locale)
				}
				tester.Key(0, ui.KeyEnter)
				if opened != first.ID {
					t.Fatal("keyboard edit with a long name selected the wrong member")
				}
				last := view.Client.Members[len(view.Client.Members)-1]
				resourceTabTo(t, tester, presentation.Message(locale, "common.edit")+": "+last.Name, sample.width, sample.height)
				tester.Key(0, ui.KeyEnter)
				if opened != last.ID {
					t.Fatal("keyboard could not open the last member after vertical scrolling")
				}
			})
		}
	}
}
