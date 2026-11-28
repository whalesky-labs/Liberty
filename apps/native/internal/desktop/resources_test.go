package desktop

import (
	"context"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

type resourceEditorPort struct {
	draft application.ResourceDraft
	saved chan domain.CredentialUpdate
}

func (p *resourceEditorPort) Save(_ context.Context, d application.ResourceDraft, u domain.CredentialUpdate) (application.ResourceDraft, application.MutationOutcome, error) {
	p.saved <- u
	return d, application.MutationOutcome{}, nil
}
func (p *resourceEditorPort) Delete(context.Context) (application.MutationOutcome, error) {
	return application.MutationOutcome{}, nil
}
func (p *resourceEditorPort) Reset() (application.ResourceDraft, error) {
	return application.NewResourceDraft(p.draft.Kind), nil
}
func (p *resourceEditorPort) Duplicate(_ context.Context, d application.ResourceDraft, suffix string) (application.ResourceDraft, error) {
	d.Template.Builtin = false
	d.Template.ID = "synthetic-persisted-copy"
	d.Template.Name += suffix
	return d, nil
}
func (p *resourceEditorPort) Close() {}
func TestResourceViewsKeepBilingualFieldsAndCredentialIntent(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
			t.Run(locale+"/"+string(kind), func(t *testing.T) {
				callbacks := make(chan func(), 4)
				draft := application.NewResourceDraft(kind)
				draft.Model.Name, draft.Model.BaseURL, draft.Model.Model = "Synthetic model", "https://example.test/v1", "synthetic-model"
				draft.Template.Name, draft.Template.Prompt = "Synthetic template", "Summarize the synthetic meeting."
				draft.Member.Name = "Synthetic member"
				port := &resourceEditorPort{draft: draft, saved: make(chan domain.CredentialUpdate, 1)}
				c := client.NewResourceEditor(port, draft, func(f func()) { callbacks <- f }, func() {}, func() {})
				defer c.Close()
				v := &ResourceEditorView{Client: c, Locale: locale, Preferences: domain.DefaultUIPreferences()}
				tt := ui.NewTester(v.View, 960, 820)
				if !tt.HasText(v.msg("name")) || !tt.HasText(v.msg("save")) {
					t.Fatal("missing original editor fields")
				}
				if kind == application.Models {
					c.Password = "synthetic-editor-value"
				}
				if kind == application.Members {
					c.MemberSortOrder = "9223372036854775807"
				}
				tt.Frame()
				if err := tt.Click(v.msg("save")); err != nil {
					t.Fatal(err)
				}
				flushView(t, callbacks, tt)
				update := <-port.saved
				if kind == application.Models && (update.Action != domain.SetCredential || c.Password != "") {
					t.Fatal("credential intention lost or password not cleared")
				}
				if kind == application.Members && c.Draft.Member.SortOrder != 9223372036854775807 {
					t.Fatal("int64 sort order lost")
				}
			})
		}
	}
}
func TestResourcesManagementRendersOriginalMetricsAndActions(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, kind := range []application.ResourceKind{application.Models, application.Templates, application.Members} {
			c := &client.ResourceController{Models: []domain.AIModel{{ID: "model", Name: "Synthetic model", Enabled: true, IsDefault: true}}, Templates: []domain.SummaryTemplate{{ID: "template", Name: "Synthetic template", Builtin: true}}, Members: []domain.MeetingMember{{ID: "member", Name: "Synthetic member", IsRecorder: true}}}
			opened := false
			v := &ResourcesView{Client: c, Locale: locale, Kind: kind, Open: func(k application.ResourceKind, id string) { opened = k == kind && id == "" }}
			tt := ui.NewTester(v.View, 1180, 900)
			if !tt.HasText(v.msg("title")) || !tt.HasText(v.msg("total")) {
				t.Fatal("missing resource metrics", kind)
			}
			if err := tt.Click(v.msg("add")); err != nil {
				t.Fatal(err)
			}
			if !opened {
				t.Fatal("add did not open scoped editor")
			}
		}
	}
}
func TestSettingsSectionsUseExistingBilingualMessages(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, section := range []string{"appearance", "languageDisplay", "processingDefaults", "performance"} {
			t.Run(fmt.Sprint(locale, section), func(t *testing.T) {
				c := &client.SettingsController{Draft: domain.DefaultSettings(), Loaded: true}
				v := &SettingsView{Client: c, Locale: locale, section: section}
				tt := ui.NewTester(v.View, 1180, 900)
				if !tt.HasText(v.msg(section)) {
					t.Fatal("settings section missing")
				}
			})
		}
	}
}
