package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type submissionViewService struct {
	calls chan application.CreateMeeting
}

func (s submissionViewService) Defaults(context.Context) (application.JobDefaults, error) {
	return application.JobDefaults{Hotwords: "Synthetic", SummaryTemplate: "Synthetic summary"}, nil
}
func (s submissionViewService) Create(_ context.Context, f application.CreateMeeting) (string, error) {
	s.calls <- f
	return "job-1700000000000-9", nil
}

type viewPicker struct{}

func (viewPicker) Pick(context.Context) (application.SelectedMedia, error) {
	return application.SelectedMedia{Token: "grant", Name: "synthetic.wav", Kind: "audio"}, nil
}
func (viewPicker) Release(string) {}
func TestNewJobSelectionDefaultsReadinessAndCreation(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			callbacks := make(chan func(), 10)
			s := submissionViewService{calls: make(chan application.CreateMeeting, 1)}
			created := ""
			c := client.NewSubmission(s, viewPicker{}, func(f func()) { callbacks <- f }, func() {}, func(id string) { created = id })
			defer c.Close()
			status := &client.RuntimeController{Panel: &domain.RuntimePanel{}}
			warned := false
			v := &NewJobView{Client: c, Runtime: status, Locale: locale, WarnRuntime: func() { warned = true }}
			tt := ui.NewTester(v.View, 1180, 900)
			flushView(t, callbacks, tt)
			if err := tt.Click(v.msg("addFiles")); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tt)
			if c.Form.Title != "synthetic" || c.Form.Hotwords != "Synthetic" || !tt.HasText("synthetic.wav") {
				t.Fatal("selected media/defaults missing")
			}
			if err := tt.Click(v.msg("createJob")); err != nil {
				t.Fatal(err)
			}
			if !warned {
				t.Fatal("missing runtime did not warn")
			}
			select {
			case <-s.calls:
				t.Fatal("unready UI submitted")
			default:
			}
			status.Panel.Overview.ShellReady = true
			tt.Frame()
			if err := tt.Click(v.msg("createJob")); err != nil {
				t.Fatal(err)
			}
			flushView(t, callbacks, tt)
			flushView(t, callbacks, tt)
			form := <-s.calls
			if created == "" || form.Selection != "grant" || form.Language != "zh-CN" || !form.Speaker || form.SizeLabel != presentation.Message(locale, "common.localPath") {
				t.Fatal("job configuration changed")
			}
		})
	}
}
func TestRuntimeViewKeepsOriginalBilingualComponentLabels(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		state := domain.RuntimeOverview{Status: "missing", Python: domain.NewRuntimeComponent("python", "managed", "0"), FFmpeg: domain.NewRuntimeComponent("ffmpeg", "managed", "0"), Models: domain.NewRuntimeComponent("model", "managed", "0")}
		v := &RuntimeView{Client: &client.RuntimeController{Panel: &domain.RuntimePanel{Overview: state, Sources: []domain.RuntimeSourceOption{{ID: "official", Name: "官方源"}}}}, Locale: locale}
		tt := ui.NewTester(v.View, 1180, 900)
		for section, keys := range map[string][]string{
			"localRuntime":     {"runtimeResourcePython", "runtimeResourceFfmpeg"},
			"speechModels":     {"runtimeResourceFunasr"},
			"runtimeDownloads": {"runtimeInstallLog", "runtimeDownloadSource"},
		} {
			v.section = section
			tt.Frame()
			for _, key := range keys {
				if !tt.HasText(v.msg(key)) {
					t.Fatal("missing runtime text", section, key)
				}
			}
		}
		v.section = "runtimeDownloads"
		tt.Frame()
		if err := tt.Click(v.msg("runtimeInstallLog")); err != nil {
			t.Fatal(err)
		}
		if !tt.HasText(v.msg("runtimeInstallLogEmpty")) {
			t.Fatal("expanded logs lost the bilingual empty state")
		}
	}
}

func TestInstallLogIncludesNonemptyComponentsInLegacyOrder(t *testing.T) {
	panel := domain.RuntimePanel{Logs: map[string]string{"model": "ready", "python": "validated\n", "ffmpeg": " \n "}}
	if got := panel.InstallLog(); got != "[python]\nvalidated\n\n[model]\nready" {
		t.Fatalf("unexpected combined diagnostic: %q", got)
	}
}

func TestNewJobLegacyResponsiveLayoutAndBilingualFields(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1440, 1180, 980, 680, 420} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				controller := &client.SubmissionController{Form: application.CreateMeeting{Language: "zh-CN", Speaker: true}}
				view := &NewJobView{Client: controller, Locale: locale}
				tester := ui.NewTesterWith(view.View, width, 2400, ui.TesterOptions{Dark: locale == "en-US"})
				for _, label := range []string{view.msg("addFiles"), view.msg("jobTitle"), view.msg("language"), view.msg("speaker"), view.msg("createJob")} {
					access, ok := tester.Access(label)
					if !ok || access.Offscreen || access.Bounds.X < 0 || access.Bounds.X+access.Bounds.W > float32(width)+1 || access.Bounds.W < 40 {
						t.Fatalf("field clipped at legacy breakpoint: %s %+v", label, access)
					}
				}
				picker, _ := tester.Find(view.msg("addFiles"))
				title, _ := tester.Find(view.msg("jobTitle"))
				if width <= 980 && title.Y < picker.Y+picker.H || width > 980 && title.X <= picker.X+picker.W {
					t.Fatalf("configuration panel did not follow the legacy column breakpoint: picker=%+v title=%+v", picker, title)
				}
				language, _ := tester.AccessByRole(ui.RolePopUpButton, view.msg("language"))
				if width <= 680 && language.Bounds.W < title.W-1 {
					t.Fatal("narrow language selector retained its fixed width", language)
				}
				if !tester.HasText(view.msg("defaultSummaryTemplateName")) {
					t.Fatal("empty default template lost the legacy fallback")
				}
				if width == 1440 && math.Abs(float64(picker.X-154)) > 20 {
					t.Fatal("new job content no longer centers within the legacy maximum width", picker)
				}
				captureParity(t, tester, fmt.Sprintf("new-job-%s-%d", locale, width))
			})
		}
	}
}

func TestNewJobTitleKeepsKeyboardFocusAndEnterUsesSubmissionPreflight(t *testing.T) {
	callbacks := make(chan func(), 8)
	service := submissionViewService{calls: make(chan application.CreateMeeting, 2)}
	created := ""
	controller := client.NewSubmission(service, viewPicker{}, func(callback func()) { callbacks <- callback }, func() {}, func(id string) { created = id })
	defer controller.Close()
	view := &NewJobView{Client: controller, Locale: "en-US", Runtime: &client.RuntimeController{Panel: &domain.RuntimePanel{Overview: domain.RuntimeOverview{ShellReady: true}}}}
	tester := ui.NewTester(view.View, 1180, 1200)
	flushView(t, callbacks, tester)
	if !tester.Focused(view.msg("jobTitle")) {
		t.Fatal("new task title did not receive initial keyboard focus")
	}
	tester.Type("Keyboard title")
	if err := tester.Click(view.msg("addFiles")); err != nil {
		t.Fatal(err)
	}
	flushView(t, callbacks, tester)
	if controller.Form.Title != "Keyboard title" {
		t.Fatal("media selection overwrote the typed title")
	}
	if err := tester.Click(view.msg("jobTitle")); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyEnter)
	if !controller.Busy {
		t.Fatal("Enter did not start the same guarded submission as the button")
	}
	flushView(t, callbacks, tester)
	flushView(t, callbacks, tester)
	if created == "" || len(service.calls) != 1 || controller.Media != nil || controller.Form.Title != "" {
		t.Fatal("keyboard submission did not finish exactly once and reset the committed draft")
	}
}

func TestNewJobSelectedMediaRemovalAndModeFeedback(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		callbacks := make(chan func(), 8)
		controller := client.NewSubmission(submissionViewService{}, viewPicker{}, func(callback func()) { callbacks <- callback }, func() {}, func(string) {})
		view := &NewJobView{Client: controller, Locale: locale}
		tester := ui.NewTester(view.View, 420, 1800)
		flushView(t, callbacks, tester)
		controller.Pick()
		flushView(t, callbacks, tester)
		label := presentation.Format(locale, "newJob.removeFile", map[string]any{"name": controller.Media.Name})
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
		if controller.Media != nil || controller.Form.Selection != "" || !tester.HasText(view.msg("addFiles")) {
			t.Fatal("per-file removal did not clear the selection")
		}
		view.Remote = true
		tester.Frame()
		if !tester.HasText(view.msg("remoteService")) || tester.HasText(view.msg("localPython")) || tester.HasText(view.msg("pendingEnvHint")) {
			t.Fatal("remote creation still claims a local runtime mode or missing local environment")
		}
		if _, ok := tester.AccessByRole(ui.RoleAlert, view.msg("remoteUploadUnavailable")); !ok {
			t.Fatal("unsupported remote upload did not expose its actual capability error")
		}
		controller.Close()
	}
}

func TestNewJobDefaultErrorsAndRuntimeWarningAreNotSubmitted(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		controller := &client.SubmissionController{Error: &client.JobDefaultsError{Cause: errors.New("synthetic read failure")}, Media: &application.SelectedMedia{Token: "synthetic-selection", Name: "synthetic.wav", Kind: "audio"}, Form: application.CreateMeeting{Title: "Synthetic meeting"}}
		view := &NewJobView{Client: controller, Locale: locale}
		tester := ui.NewTester(view.View, 1180, 1400)
		message := localized(locale, "无法读取任务默认设置，请重试。", "Could not load job defaults. Please retry.")
		if _, ok := tester.AccessByRole(ui.RoleAlert, message); !ok || tester.HasText(view.msg("createFailed")) {
			t.Fatal("defaults error was mislabeled as job creation failure")
		}
		if err := tester.Click(view.msg("createJob")); err != nil {
			t.Fatal(err)
		}
		if controller.Busy {
			t.Fatal("an unavailable runtime without a dialog callback bypassed the preflight")
		}
	}
}

func TestNewJobMediaPickFailureShowsBilingualRetryFeedback(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		controller := &client.SubmissionController{
			Error: &client.MediaPickError{Cause: application.ErrJobInput},
			Media: &application.SelectedMedia{Token: "media-grant", Name: "synthetic.wav", Kind: "audio"},
			Form:  application.CreateMeeting{Selection: "media-grant", Title: "Synthetic meeting"},
		}
		view := &NewJobView{Client: controller, Locale: locale}
		tester := ui.NewTester(view.View, 1180, 1400)
		message := localized(locale, "无法读取所选媒体，请重新选择文件。", "Could not read the selected media. Reselect the file.")
		if _, ok := tester.AccessByRole(ui.RoleAlert, message); !ok || tester.HasText(view.msg("createFailed")) || !tester.HasText(view.msg("reselect")) {
			t.Fatalf("media picker failure lost its bilingual retry feedback for %s", locale)
		}
	}
}

func TestNewJobDoesNotSubmitWithStaleReadyRuntimeSnapshot(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		controller := &client.SubmissionController{
			Media: &application.SelectedMedia{Name: "synthetic.wav", Kind: "audio"},
			Form:  application.CreateMeeting{Title: "Synthetic meeting"},
		}
		warnings := 0
		view := &NewJobView{
			Client: controller, Locale: locale,
			Runtime:     &client.RuntimeController{Panel: &domain.RuntimePanel{Overview: domain.RuntimeOverview{ShellReady: true}}, Error: errors.New("synthetic refresh failure")},
			WarnRuntime: func() { warnings++ },
		}
		tester := ui.NewTester(view.View, 1180, 1400)
		if !tester.HasText(view.msg("pendingEnvHint")) || tester.HasText(view.msg("readyToCreate")) {
			t.Fatal("failed runtime refresh retained a ready creation hint")
		}
		if err := tester.Click(view.msg("createJob")); err != nil || warnings != 1 || controller.Busy {
			t.Fatal("a stale ready snapshot bypassed runtime preflight", err)
		}
	}
}
