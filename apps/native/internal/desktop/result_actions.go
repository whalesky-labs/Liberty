package desktop

import (
	"context"
	"strings"
	"unicode"

	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/fileout"
	"liberty.local/liberty/internal/infrastructure/minutesdocx"
)

type NativeResultFiles struct {
	Parent  *mygo.Window
	Grant   *application.JobWindow
	dialogs resultFileDialogs
}

type resultFileDialogs interface {
	Save(mygo.SaveDialogOptions) (string, error)
}

func (f *NativeResultFiles) fileDialogs() resultFileDialogs {
	if f.dialogs != nil {
		return f.dialogs
	}
	return mygo.Dialog
}

func (f *NativeResultFiles) Export(ctx context.Context, kind domain.TextExportKind, title, locale string) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	ext, err := kind.Extension()
	if err != nil {
		return false, err
	}
	filename, err := f.fileDialogs().Save(mygo.SaveDialogOptions{Parent: f.Parent, DefaultPath: exportName(title, kind, ext), Filters: []mygo.FileFilter{{Name: strings.ToUpper(ext), Extensions: []string{ext}}}})
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return false, fileout.ErrOutput
	}
	if filename == "" {
		return false, nil
	}
	if kind == domain.ExportWord {
		err = f.Grant.ExportWord(ctx, "", func(document domain.MinutesDocument) error {
			data, err := minutesdocx.Encode(document)
			if err != nil {
				return err
			}
			return fileout.Write(ctx, filename, data)
		})
	} else {
		err = f.Grant.ExportText(ctx, kind, exportLabels(locale), func(data []byte) error { return fileout.Write(ctx, filename, data) })
	}
	return true, err
}
func exportName(title string, kind domain.TextExportKind, extension string) string {
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, title)
	name = strings.Trim(name, " .")
	characters := []rune(name)
	if len(characters) > 100 {
		name = string(characters[:100])
	}
	if name == "" {
		name = "Liberty"
	}
	suffix := string(kind)
	if kind == domain.ExportWord {
		suffix = "meeting-minutes"
	}
	return name + "-" + suffix + "." + extension
}
func exportLabels(locale string) domain.TextExportLabels {
	m := func(key string) string { return presentation.Message(locale, "export."+key) }
	return domain.TextExportLabels{UnknownSpeaker: presentation.Message(locale, "common.unknownSpeaker"), TranscriptHeading: m("transcriptHeading"), SummaryHeading: m("summaryHeading"), TopicsHeading: m("topicsHeading"), DecisionsHeading: m("decisionsHeading"), ActionItemsHeading: m("actionItemsHeading"), RisksHeading: m("risksHeading"), FollowUpsHeading: m("followUpsHeading"), EmptySummary: m("emptySummary")}
}

func (w *Workspace) AttachResultEditing(writer application.SpeakerWriter) {
	w.scopes.EnableEditing(writer)
	w.resultEditing = true
}

func (w *Workspace) AttachPlayback(player application.SegmentPlayer) { w.scopes.EnablePlayback(player) }

func (w *Workspace) AttachWordExport(exporter *application.WordExport) {
	w.scopes.EnableWordExport(exporter)
	w.wordExportEnabled = true
}

func (w *Workspace) AttachSummary(service *application.SummaryService) {
	w.summaryService = service
	w.scopes.EnableSummary(service)
}
func (w *Workspace) resultActions(window *mygo.Window, view *ResultView, grant *application.JobWindow) *client.ResultActionController {
	return client.NewResultActions(grant, &NativeResultFiles{Parent: window, Grant: grant}, func(ctx context.Context, from, to, locale string) (bool, error) {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		label := strings.TrimSpace(from)
		if label == "" {
			label = presentation.Message(locale, "common.unknownSpeaker")
		}
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: window, Type: mygo.MessageWarning, Title: presentation.Message(locale, "workbench.renameSpeakerTitle"), Message: presentation.Format(locale, "workbench.renameSpeakerConfirm", map[string]any{"source": label, "target": to}), Buttons: []string{presentation.Message(locale, "common.cancel"), presentation.Message(locale, "workbench.replace")}, CancelButton: 0, DefaultButton: 0})
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return answer.Button == 1, err
	}, mygo.RunOnMain, window.Invalidate, func(from, to string) {
		label := strings.TrimSpace(from)
		if label == "" {
			label = presentation.Message(view.Locale, "common.unknownSpeaker")
		}
		if view.speaker == label {
			view.speaker = to
		}
		view.Client.Refresh()
		if !w.closed {
			w.controller.ResultController.Refresh()
		}
	})
}
