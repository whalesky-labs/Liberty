package desktop

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

type liveModelsSettingsService struct {
	prepared chan struct{}
	options  application.LiveOptions
}

func (s *liveModelsSettingsService) Options(context.Context) (application.LiveOptions, error) {
	return s.options, nil
}
func (s *liveModelsSettingsService) ModelStatus(context.Context) application.LiveModelStatus {
	return application.LiveModelStatus{ModelReady: s.options.ModelReady, TranslationReady: s.options.TranslationReady}
}
func (s *liveModelsSettingsService) Prepare(ctx context.Context, _ func(string)) error {
	close(s.prepared)
	<-ctx.Done()
	return ctx.Err()
}
func (*liveModelsSettingsService) Start(context.Context, domain.LiveRequest) (domain.LiveSession, error) {
	return domain.LiveSession{}, nil
}
func (*liveModelsSettingsService) Control(context.Context, string, string) error { return nil }
func (*liveModelsSettingsService) Snapshot() domain.LiveSnapshot                 { return domain.LiveSnapshot{} }
func (*liveModelsSettingsService) History(context.Context, int) ([]domain.LiveSession, error) {
	return nil, nil
}
func (*liveModelsSettingsService) Segments(context.Context, string, int64) ([]domain.LiveSegment, error) {
	return nil, nil
}
func (*liveModelsSettingsService) RetryTranslations(context.Context, string) error { return nil }
func (*liveModelsSettingsService) Delete(context.Context, string) error            { return nil }
func (*liveModelsSettingsService) Edit(context.Context, domain.LiveSegment) error  { return nil }

func TestRuntimeSettingsOffersLiveModelDownloadAndCancellation(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			service := &liveModelsSettingsService{prepared: make(chan struct{}), options: application.LiveOptions{}}
			callbacks := make(chan func(), 4)
			controller := client.NewLive(service, nil, func(callback func()) { callbacks <- callback }, func() {})
			defer controller.Close()
			view := &RuntimeView{Locale: locale, Live: controller}
			tester := ui.NewTester(func(c *ui.Context) { view.liveModels(c, true, false) }, 1100, 650)

			for _, label := range []string{view.msg("liveModelsGroup"), view.msg("liveASRModel"), view.msg("liveTranslationModels")} {
				if !tester.HasText(label) {
					t.Fatal("live model settings content missing", label)
				}
			}
			if button, ok := tester.AccessByRole(ui.RoleButton, view.msg("liveModelsDownload")); !ok || button.Disabled {
				t.Fatal("live model download action missing", button)
			}
			if err := tester.Click(view.msg("liveModelsDownload")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-service.prepared:
			case <-time.After(time.Second):
				t.Fatal("settings action did not prepare live models")
			}
			controller.CancelPrepare()
			select {
			case callback := <-callbacks:
				callback()
			case <-time.After(time.Second):
				t.Fatal("cancelled model preparation did not finish")
			}
		})
	}
}

func TestRuntimeSettingsRequiresBaseRuntimeBeforeLiveModelDownload(t *testing.T) {
	view := &RuntimeView{Locale: "zh-CN", Live: &client.LiveController{}}
	tester := ui.NewTester(func(c *ui.Context) { view.liveModels(c, false, false) }, 1100, 650)
	button, ok := tester.AccessByRole(ui.RoleButton, view.msg("liveModelsDownload"))
	if !ok || !button.Disabled || !tester.HasText(view.msg("liveModelsNeedsRuntime")) {
		t.Fatal("live models can be downloaded without a ready local runtime", button)
	}
}
