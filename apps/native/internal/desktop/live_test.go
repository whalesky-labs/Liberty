package desktop

import (
	"bytes"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func TestLiveCaptionsBilingualStatesAndAccessibleControls(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprint(locale, "/", dark), func(t *testing.T) {
				controller := &client.LiveController{Request: domain.LiveRequest{DeviceID: "synthetic", SourceLanguage: "zh", TargetLanguage: "en", Translate: true}, Options: application.LiveOptions{ModelReady: true, TranslationReady: true, Devices: []domain.AudioDevice{{ID: "synthetic", Name: "Synthetic microphone"}}}, Snapshot: domain.LiveSnapshot{Session: domain.LiveSession{ID: "synthetic-session", State: "listening", Translate: true}, Level: .055, Segments: []domain.LiveSegment{{SessionID: "synthetic-session", ID: 1, Revision: 1, StartMS: 600, EndMS: 5400, Text: "欢迎体验实时语音识别。", Translation: "Welcome to live speech recognition.", TranslationState: "completed", TranslationMS: 820}}, Partial: &domain.LiveSegment{SessionID: "synthetic-session", ID: 2, Revision: 1, StartMS: 6000, EndMS: 7200, Text: "这是正在识别的下一句"}}}
				view := &LiveView{Client: controller, Locale: locale}
				tester := ui.NewTesterWith(view.View, 1180, 900, ui.TesterOptions{Dark: dark})
				for _, label := range []string{view.text("暂停", "Pause"), view.text("结束", "Finish"), "Welcome to live speech recognition.", "欢迎体验实时语音识别。", view.text("收听设置", "Listening settings"), view.text("麦克风输入电平", "Microphone input level"), view.text("查看完整字幕", "View full captions"), view.text("自动识别 · 中英互译", "Auto-detect · Chinese ↔ English")} {
					if _, ok := tester.Find(label); !ok {
						t.Fatalf("live view missing %q", label)
					}
				}
				for _, hidden := range []string{view.text("输入设备", "Input device"), view.text("历史会话", "Session history"), view.text("字幕列表", "Captions"), view.text("本地处理 · 不保存录音", "On-device · Audio is never saved")} {
					if _, ok := tester.Find(hidden); ok {
						t.Fatalf("listening surface still exposes %q", hidden)
					}
				}

				if directory := os.Getenv("LIBERTY_NATIVE_LIVE_SCREENSHOT_DIR"); filepath.IsAbs(directory) {
					if err := os.MkdirAll(directory, 0700); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("live-%s-dark-%t.png", locale, dark)))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(file, tester.Image())
					closeErr := file.Close()
					if err != nil || closeErr != nil {
						t.Fatal("screenshot encode")
					}
				}
			})
		}
	}
}

func TestLiveSettingsAndHistoryAreDisclosedOnDemand(t *testing.T) {
	controller := client.NewLive(nil, nil, func(f func()) { f() }, func() {})
	defer controller.Close()
	controller.Options = application.LiveOptions{ModelReady: true, TranslationReady: true, Devices: []domain.AudioDevice{{ID: "mic", Name: "Test microphone"}}}
	view := &LiveView{Client: controller, Locale: "zh-CN"}
	tester := ui.NewTester(view.View, 900, 760)
	if err := tester.Click("开始收听"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("输入设备"); !ok || !view.settingsOpen {
		t.Fatal("missing microphone selection did not open settings")
	}
	tester.Key(0, ui.KeyEscape)
	if view.settingsOpen {
		t.Fatal("settings did not close with Escape")
	}
	if err := tester.Click("历史记录"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("历史会话"); !ok {
		t.Fatal("history unavailable")
	}
	if _, ok := tester.Find("麦克风输入电平"); ok {
		t.Fatal("pickup still renders behind history")
	}
	if err := tester.Click("返回收听"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("麦克风输入电平"); !ok {
		t.Fatal("cannot return to listening")
	}
}

func TestLivePickupRespondsToActualInputAndStopsAnimating(t *testing.T) {
	controller := &client.LiveController{Snapshot: domain.LiveSnapshot{Session: domain.LiveSession{State: "listening"}}}
	view := &LiveView{Client: controller, Locale: "en-US"}
	tester := ui.NewTester(func(c *ui.Context) { view.pickup(c, 220) }, 240, 240)
	// Freeze decorative gaze to isolate the real input response.
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	silent := append([]byte(nil), tester.Image().Pix...)
	if tester.FramePending() {
		t.Fatal("silence started continuous animation")
	}
	controller.Snapshot.Level = .16
	tester.Frame()
	tester.Advance(200 * time.Millisecond)
	if bytes.Equal(silent, tester.Image().Pix) {
		t.Fatal("pickup ignored measured input")
	}
	if tester.FramePending() {
		t.Fatal("input interpolation did not settle")
	}
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	controller.Snapshot.Level = .04
	tester.Frame()
	if tester.FramePending() {
		t.Fatal("reduced motion schedules animation")
	}
	controller.Snapshot.Session.State = "paused"
	tester.Frame()
	paused := append([]byte(nil), tester.Image().Pix...)
	controller.Snapshot.Level = .9
	tester.Frame()
	if !sameLiveAvatarImage(paused, tester.Image().Pix) {
		t.Fatal("paused robot changed with microphone input")
	}
	if tester.FramePending() {
		t.Fatal("paused robot schedules animation")
	}
	for _, level := range []float64{math.NaN(), math.Inf(1), -1} {
		if livePickupLevel("listening", level) != 0 {
			t.Fatal("invalid level accepted")
		}
	}
}

// Cached vector masks can round a color channel one step differently from
// their first paint. Larger differences still detect motion or audio response.
func sameLiveAvatarImage(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if delta := int(a[i]) - int(b[i]); delta < -1 || delta > 1 {
			return false
		}
	}
	return true
}

func TestLiveRobotUsesUpstreamGazeWithoutRebuildingPage(t *testing.T) {
	controller := &client.LiveController{}
	view := &LiveView{Client: controller, Locale: "zh-CN"}
	builds := 0
	tester := ui.NewTester(func(c *ui.Context) {
		builds++
		view.pickup(c, 220)
	}, 240, 240)
	open := append([]byte(nil), tester.Image().Pix...)
	initialBuilds := builds
	tester.Advance(1480 * time.Millisecond)
	if sameLiveAvatarImage(open, tester.Image().Pix) {
		t.Fatal("upstream robot did not animate its gaze/blink")
	}
	if builds != initialBuilds {
		t.Fatal("decorative eye animation rebuilt the page")
	}
}

func TestLiveRobotSuspendsMotionAndUsesSessionStates(t *testing.T) {
	for _, phase := range []string{"", "listening", "preparing", "stopping", "paused", "completed", "failed", "interrupted"} {
		t.Run(phase, func(t *testing.T) {
			controller := &client.LiveController{Snapshot: domain.LiveSnapshot{Session: domain.LiveSession{State: phase}}}
			view := &LiveView{Client: controller, Locale: "en-US"}
			tester := ui.NewTester(func(c *ui.Context) { view.pickup(c, 220) }, 240, 240)
			original := append([]byte(nil), tester.Image().Pix...)
			tester.Advance(4600 * time.Millisecond)
			stationary := phase == "paused" || phase == "failed" || phase == "interrupted"
			if sameLiveAvatarImage(original, tester.Image().Pix) != stationary {
				t.Fatal("robot expression does not match session state")
			}
			for _, stopMotion := range []struct {
				name   string
				stop   func()
				resume func()
			}{
				{"inactive", func() { tester.SetWindowActive(false) }, func() { tester.SetWindowActive(true) }},
				{"hidden", func() { tester.SetWindowHidden(true) }, func() { tester.SetWindowHidden(false) }},
				{"reduced motion", func() { tester.SetPreferences(ui.Preferences{ReduceMotion: true}) }, func() { tester.SetPreferences(ui.Preferences{}) }},
			} {
				stopMotion.stop()
				still := append([]byte(nil), tester.Image().Pix...)
				tester.Advance(6 * time.Second)
				if !sameLiveAvatarImage(still, tester.Image().Pix) || tester.FramePending() {
					t.Fatalf("%s robot keeps animating", stopMotion.name)
				}
				stopMotion.resume()
			}
		})
	}
}

func TestLiveListeningStatesFitCompactWindow(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, phase := range []string{"", "listening", "paused", "preparing", "stopping", "completed"} {
			t.Run(locale+"/"+phase, func(t *testing.T) {
				controller := client.NewLive(nil, nil, func(f func()) { f() }, func() {})
				defer controller.Close()
				controller.Options = application.LiveOptions{ModelReady: true, TranslationReady: true}
				controller.Snapshot.Session = domain.LiveSession{State: phase}
				view := &LiveView{Client: controller, Locale: locale}
				tester := ui.NewTester(view.View, 860, 690)
				for _, label := range []string{view.text("历史记录", "History"), view.text("麦克风输入电平", "Microphone input level"), view.text("收听设置", "Listening settings")} {
					r, ok := tester.Find(label)
					if !ok || r.X < 0 || r.X+r.W > 860 || r.Y+r.H > 690 {
						t.Fatalf("compact control not visible: %s %+v", label, r)
					}
				}
				if phase == "paused" {
					if _, ok := tester.Find(view.text("继续收听", "Resume listening")); !ok {
						t.Fatal("paused session lacks resume")
					}
				}
				if directory := os.Getenv("LIBERTY_NATIVE_LIVE_SCREENSHOT_DIR"); filepath.IsAbs(directory) && phase == "" {
					f, err := os.Create(filepath.Join(directory, "live-"+locale+"-compact-idle.png"))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(f, tester.Image())
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
