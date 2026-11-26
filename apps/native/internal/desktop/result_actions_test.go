package desktop

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type workbenchPort struct {
	from, to string
	kind     domain.TextExportKind
	locale   string
}

func (p *workbenchPort) RenameSpeaker(_ context.Context, from, to string) error {
	p.from, p.to = from, to
	return nil
}
func (p *workbenchPort) Export(_ context.Context, kind domain.TextExportKind, _ string, locale string) (bool, error) {
	p.kind, p.locale = kind, locale
	return true, nil
}

func TestWorkbenchOriginalEditAndTextExportInteractions(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			speaker := "speaker-0"
			result := &domain.Result{Job: domain.JobSummary{ID: "job-1700000000000-0", Source: "local", Title: "Synthetic result", OverallStatus: "completed", DiarizationStatus: "completed"}, Speakers: []domain.Segment{{ID: "s1", Speaker: &speaker, Text: "Synthetic transcript", EndMS: 1000}}}
			v := NewResultView(&client.ResultController{Result: result}, locale)
			callbacks := make(chan func(), 8)
			port := &workbenchPort{}
			confirmed := false
			renamed := false
			v.Actions = client.NewResultActions(port, port, func(_ context.Context, from, to, language string) (bool, error) {
				confirmed = from == "speaker-0" && to == "speaker-0-updated" && language == locale
				return confirmed, nil
			}, func(fn func()) { callbacks <- fn }, func() {}, func(string, string) { renamed = true })
			defer v.Actions.Close()
			v.EditingEnabled = true
			v.WordExportEnabled = true
			openedNotes := false
			v.OpenNotes = func(title string) { openedNotes = title == "Synthetic result" }
			tt := ui.NewTester(v.View, 1000, 760)
			common := func(key string) string { return presentation.Message(locale, "common."+key) }
			if err := tt.Click(common("edit")); err != nil {
				t.Fatal(err)
			}
			if _, ok := tt.Access(v.msg("speakerInputPlaceholder")); !ok {
				t.Fatal("speaker editor has no accessible label")
			}
			if !tt.Focused(v.msg("speakerInputPlaceholder")) {
				t.Fatal("speaker editor did not receive keyboard focus")
			}
			tt.Key(0, ui.KeyEnd)
			tt.Type("-updated")
			tt.Key(0, ui.KeyEnter)
			flushView(t, callbacks, tt)
			if !confirmed || !renamed || port.from != "speaker-0" || port.to != "speaker-0-updated" {
				t.Fatal("rename confirmation/input differs")
			}
			if err := tt.Click(common("edit")); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click(v.msg("speakerInputPlaceholder")); err != nil {
				t.Fatal(err)
			}
			tt.Key(0, ui.KeyEscape)
			if v.editingSegment != "" {
				t.Fatal("Escape did not cancel speaker edit")
			}
			if err := tt.Click(v.msg("viewNotes")); err != nil || !openedNotes {
				t.Fatal("notes action", err)
			}
			for key, kind := range map[string]domain.TextExportKind{"exportTranscript": domain.ExportTranscript, "exportNotes": domain.ExportNotes, "exportBundle": domain.ExportBundle, "exportWord": domain.ExportWord} {
				if err := tt.Click(v.msg(key)); err != nil {
					t.Fatal(err)
				}
				flushView(t, callbacks, tt)
				if port.kind != kind || port.locale != locale || !v.Actions.Exported {
					t.Fatal("wrong text export action")
				}
			}
			next := *result
			next.Job.DiarizationStatus = "unavailable"
			v.Client.Result = &next
			tt.Frame()
			if node, ok := tt.Access(common("edit")); ok && !node.Disabled {
				t.Fatal("degraded speaker result editable")
			}
		})
	}
}

func TestWorkbenchExportProgressIsBilingualAndDisablesDuplicateActions(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := NewResultView(&client.ResultController{Result: &domain.Result{Job: domain.JobSummary{ID: "job", Source: "local", Title: "Synthetic meeting"}}}, locale)
			view.Actions = &client.ResultActionController{Busy: true, Exporting: true}
			view.WordExportEnabled = true
			tester := ui.NewTester(view.View, 1000, 760)
			for _, key := range []string{"exporting", "exportTranscript", "exportNotes", "exportWord"} {
				assertDisabled(t, tester, view.msg(key), true)
			}
			if tester.HasText(view.msg("exportBundle")) {
				t.Fatal("export remained idle while writing")
			}
		})
	}
}
