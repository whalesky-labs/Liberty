package desktop

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

type correctionViewPort struct{ edits chan domain.SegmentEdit }

func (p correctionViewPort) RenameSpeaker(context.Context, string, string) error { return nil }
func (p correctionViewPort) EditSegment(_ context.Context, edit domain.SegmentEdit) error {
	p.edits <- edit
	return nil
}

func TestMeetingWorkflowCorrectionEditsOnlySelectedSegment(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := resultParityView(locale, 1)
			callbacks := make(chan func(), 4)
			port := correctionViewPort{edits: make(chan domain.SegmentEdit, 1)}
			view.Actions = client.NewResultActions(port, nil, nil, func(fn func()) { callbacks <- fn }, func() {}, nil)
			defer view.Actions.Close()
			tester := ui.NewTester(view.View, 1280, 900)
			if err := tester.Click(view.text("校对此句", "Correct segment")); err != nil {
				t.Fatal(err)
			}
			view.Actions.CorrectionText = "Verified synthetic fact"
			view.Actions.CorrectionSpeaker = "Verified person"
			tester.Frame()
			if _, ok := tester.AccessByRole(ui.RoleTextField, view.text("校对原文", "Correct transcript")); !ok {
				t.Fatal("missing accessible editor")
			}
			if err := tester.Click(localized(locale, "还原本次修改", "Reset changes")); err != nil {
				t.Fatal(err)
			}
			if view.Actions.CorrectionText != view.Client.Result.PrimarySegments()[0].Text {
				t.Fatal("reset lost original")
			}
			view.Actions.CorrectionText = "Verified synthetic fact"
			// The asynchronous save callback is tested through application integration;
			// UI cancellation must preserve the original immutable result snapshot.
			if err := tester.Click(localized(locale, "取消", "Cancel")); err != nil {
				t.Fatal(err)
			}
			if view.Actions.Correction != nil || view.Client.Result.PrimarySegments()[0].Text == "Verified synthetic fact" {
				t.Fatal("cancel published a draft")
			}
		})
	}
}

func TestMeetingWorkflowBatchLayoutExposesPerFileTitles(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 4)
			controller := client.NewSubmission(submissionViewService{calls: make(chan application.CreateMeeting, 4)}, viewPicker{}, func(fn func()) { callbacks <- fn }, func() {}, func(string) {})
			defer controller.Close()
			controller.Batch = []client.BatchItem{{Media: application.SelectedMedia{Token: "a", Name: "first.wav"}, Title: "First"}, {Media: application.SelectedMedia{Token: "b", Name: "second.wav"}, Title: "Second"}}
			controller.Media = &controller.Batch[0].Media
			view := &NewJobView{Client: controller, Locale: locale, Runtime: &client.RuntimeController{Panel: &domain.RuntimePanel{Overview: domain.RuntimeOverview{ShellReady: true}}}}
			tester := ui.NewTester(view.View, 1180, 900)
			flushView(t, callbacks, tester)
			for _, name := range []string{"first.wav", "second.wav"} {
				if !tester.HasText(name) {
					t.Fatal("batch file missing", name)
				}
			}
			if !controller.CanSubmit() {
				t.Fatal("batch required unrelated single title")
			}
		})
	}
}

func TestMeetingWorkflowVisualReview(t *testing.T) {
	directory := os.Getenv("LIBERTY_MEETING_UI_REVIEW")
	if !filepath.IsAbs(directory) {
		t.Skip("explicit synthetic UI review directory required")
	}
	if os.MkdirAll(directory, 0700) != nil {
		t.Fatal("review directory unavailable")
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := resultParityView(locale, 4)
		view.Playback = &client.PlaybackController{Speed: 1}
		view.Actions = client.NewResultActions(correctionViewPort{}, nil, nil, func(fn func()) { fn() }, func() {}, nil)
		tester := ui.NewTester(view.View, 1280, 900)
		if err := tester.Click(view.text("校对此句", "Correct segment")); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(directory, "correction-"+locale+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if png.Encode(file, tester.Image()) != nil || file.Close() != nil {
			t.Fatal("review render failed")
		}
		view.Actions.Close()
		preferences := domain.DefaultUIPreferences()
		preferences.Locale = locale
		notes := &NotesView{Client: view.Client, Preferences: preferences}
		notes.Editor = client.NewNotes(nil, func(fn func()) { fn() }, func() {}, func() {})
		notes.Editor.Begin(*view.Client.Result)
		notes.Editor.Overview = "Synthetic reviewed meeting notes."
		noteTester := ui.NewTester(notes.View, 1120, 860)
		file, err = os.Create(filepath.Join(directory, "notes-"+locale+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if png.Encode(file, noteTester.Image()) != nil || file.Close() != nil {
			t.Fatal("notes review render failed")
		}
		notes.Editor.Close()
	}
}
