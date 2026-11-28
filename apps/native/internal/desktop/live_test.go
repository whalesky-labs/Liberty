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
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestLiveCaptionsBilingualStatesAndAccessibleControls(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprint(locale, "/", dark), func(t *testing.T) {
				controller := &client.LiveController{Request: domain.LiveRequest{DeviceID: "synthetic", SourceLanguage: "zh", TargetLanguage: "en", Translate: true}, Options: application.LiveOptions{ModelReady: true, TranslationReady: true, Devices: []domain.AudioDevice{{ID: "synthetic", Name: "Synthetic microphone"}}}, Snapshot: domain.LiveSnapshot{Session: domain.LiveSession{ID: "synthetic-session", State: "listening", Translate: true}, Level: .055, Segments: []domain.LiveSegment{{SessionID: "synthetic-session", ID: 1, Revision: 1, StartMS: 600, EndMS: 5400, Text: "欢迎体验实时语音识别。", Translation: "Welcome to live speech recognition.", TranslationState: "completed", TranslationMS: 820}}, Partial: &domain.LiveSegment{SessionID: "synthetic-session", ID: 2, Revision: 1, StartMS: 6000, EndMS: 7200, Text: "这是正在识别的下一句"}}}
				view := &LiveView{Client: controller, Locale: locale}
				tester := ui.NewTesterWith(view.View, 1180, 900, ui.TesterOptions{Dark: dark})
				for _, label := range []string{view.text("暂停", "Pause"), view.text("结束", "Finish"), "Welcome to live speech recognition.", "欢迎体验实时语音识别。", view.text("收听设置", "Listening settings"), view.text("音频输入电平", "Audio input level"), view.text("查看完整字幕", "View full captions"), view.text("自动识别 · 中英互译", "Auto-detect · Chinese ↔ English")} {
					if _, ok := tester.Find(label); !ok {
						t.Fatalf("live view missing %q", label)
					}
				}
				for _, hidden := range []string{view.text("实时翻译", "Live Translation"), view.text("输入设备", "Input device"), view.text("历史会话", "Session history"), view.text("字幕列表", "Captions"), view.text("本地处理 · 不保存录音", "On-device · Audio is never saved")} {
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
	controller.Error = fmt.Errorf("synthetic live error")
	session := domain.LiveSession{ID: "synthetic-history", CreatedAt: time.Date(2026, time.November, 27, 9, 30, 0, 0, time.Local).Format(time.RFC3339Nano), State: "completed", SegmentCount: 2}
	controller.History = []domain.LiveSession{session}
	view := &LiveView{Client: controller, Locale: "zh-CN"}
	shell := &Shell{Locale: view.Locale, Route: "/live", Preferences: domain.DefaultUIPreferences(), Content: view.View, TitlebarActions: view.titlebarActions}
	shell.TrailingPane = &view.historyPanel
	tester := ui.NewTester(shell.View, 1180, 900)
	github, ok := tester.Find(presentation.Message(view.Locale, "shell.github"))
	if !ok {
		t.Fatal("titlebar is missing GitHub link")
	}
	historyToggle, ok := tester.Find(view.text("历史记录", "History"))
	if !ok || historyToggle.X <= github.X+github.W {
		t.Fatalf("history toggle should follow GitHub in titlebar: github=%+v toggle=%+v", github, historyToggle)
	}
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
	const wideWindow, windowHeight = 2560, 900
	tester.SetSize(wideWindow, windowHeight)
	closedAlert, ok := tester.Find(liveError(view.Locale, controller.Error, ""))
	if !ok {
		t.Fatal("missing main content alert")
	}
	if err := tester.Click("历史记录"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("历史会话"); !ok || !view.historyOpen || view.mode != "" {
		t.Fatal("history panel did not open alongside listening")
	}
	if _, ok := tester.Find(createdText(view.Locale, session.CreatedAt)); !ok {
		t.Fatal("history panel did not render the stored session")
	}
	panel, ok := tester.AccessByRole(ui.RoleComplementary, view.text("历史记录", "History"))
	if !ok {
		t.Fatal("history panel is missing its complementary landmark")
	}
	alert, ok := tester.Find(liveError(view.Locale, controller.Error, ""))
	if !ok || panel.Bounds.Y >= alert.Y {
		t.Fatalf("history panel should start at the content top above the main content alert: panel=%+v alert=%+v", panel.Bounds, alert)
	}
	if _, ok := tester.Find("音频输入电平"); !ok {
		t.Fatal("listening view disappeared behind history panel")
	}
	divider, ok := tester.AccessByRole(ui.RoleSplitter, "")
	if !ok {
		t.Fatal("history panel is missing a draggable divider")
	}
	if top, bottom := divider.Bounds.Y, divider.Bounds.Y+divider.Bounds.H; top != presentation.TitlebarHeight || bottom != windowHeight-presentation.StatusbarHeight {
		t.Fatalf("history divider spans %v..%v, want %v..%v", top, bottom, presentation.TitlebarHeight, windowHeight-presentation.StatusbarHeight)
	}
	checkDividerEnds := func() {
		t.Helper()
		d, ok := tester.AccessByRole(ui.RoleSplitter, "")
		if !ok {
			t.Fatal("missing divider")
		}
		x := int(math.Round(float64(d.Bounds.X)))
		frame := tester.Image()
		for _, sampleX := range []int{presentation.SidebarWidth + 2, wideWindow - 2} {
			y := int(d.Bounds.Y) - 1
			if frame.RGBAAt(sampleX, y) != frame.RGBAAt(x, windowHeight/2) || frame.RGBAAt(sampleX, y) == frame.RGBAAt(sampleX, y-2) {
				t.Fatalf("missing header divider at (%d, %d)", sampleX, y)
			}
		}
		for _, y := range []int{0, windowHeight - 1} {
			if frame.RGBAAt(x, y) == frame.RGBAAt(x-2, y) || frame.RGBAAt(x, y) != frame.RGBAAt(x, windowHeight/2) {
				t.Fatalf("divider at x=%d does not continue to y=%d", x, y)
			}
		}
	}
	checkDividerEnds()
	if right := panel.Bounds.X + panel.Bounds.W; math.Abs(float64(right-(wideWindow-shellContentPaddingX))) > 1 {
		t.Fatalf("history panel ends at %v instead of the workspace right edge", right)
	}
	mainLeft := float32(presentation.SidebarWidth + shellContentPaddingX)
	wantCenter := (mainLeft + divider.Bounds.X) / 2
	if alert.W != closedAlert.W || math.Abs(float64(alert.X+alert.W/2-wantCenter)) > 2 {
		t.Fatalf("main content should retain its width and center in the left column: closed=%+v open=%+v want center=%v", closedAlert, alert, wantCenter)
	}
	initialSplit := view.mainSplitPercent
	x := divider.Bounds.X + divider.Bounds.W/2
	y := divider.Bounds.Y + divider.Bounds.H/2
	tester.Press(x, y)
	tester.Move(x-50, y)
	tester.Release(x-50, y)
	if view.mainSplitPercent >= initialSplit {
		t.Fatalf("dragging the divider left did not widen history panel: before=%v after=%v", initialSplit, view.mainSplitPercent)
	}
	tester.Move(0, 0)
	checkDividerEnds()
	movedDivider, _ := tester.AccessByRole(ui.RoleSplitter, "")
	if err := tester.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tester.Find("历史会话"); ok || view.historyOpen {
		t.Fatal("history panel did not collapse")
	}
	closedFrame := tester.Image()
	if y := presentation.TitlebarHeight - 1; closedFrame.RGBAAt(500, y) != closedFrame.RGBAAt(500, y-2) {
		t.Fatal("header divider remained after closing history")
	}
	previousX := int(math.Round(float64(movedDivider.Bounds.X)))
	if closedFrame.RGBAAt(previousX, 0) != closedFrame.RGBAAt(previousX-2, 0) {
		t.Fatal("history divider remained in the titlebar after closing")
	}
	if _, ok := tester.Find("音频输入电平"); !ok {
		t.Fatal("listening view disappeared after collapsing history")
	}
	if restored, ok := tester.Find(liveError(view.Locale, controller.Error, "")); !ok || restored != closedAlert {
		t.Fatalf("closing history did not restore the centered content: got %+v, want %+v", restored, closedAlert)
	}
}

func TestHistoryToggleKeepsNeutralColorsAcrossAccents(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, open := range []bool{false, true} {
			var reference []byte
			for _, accent := range []string{"#2f6dff", "#ffb020"} {
				p := presentation.Colors(dark, ui.Hex(accent))
				view := &LiveView{Locale: "en-US", historyOpen: open}
				tester := ui.NewTesterWith(func(c *ui.Context) {
					ui.Themed(c, &p.Theme).Fill().Center().Children(func() { view.titlebarActions(c) })
				}, 60, 60, ui.TesterOptions{Dark: dark, Preferences: ui.Preferences{ReduceMotion: true}})
				tester.Key(0, ui.KeyTab)
				tester.Move(30, 30)
				pixels := tester.Image().Pix
				if reference != nil && !bytes.Equal(reference, pixels) {
					t.Fatalf("accent changed history toggle appearance: dark=%t open=%t", dark, open)
				}
				reference = bytes.Clone(pixels)
			}
		}
	}
}

func TestHistoryPanelLayout(t *testing.T) {
	s := &client.LiveController{}
	v := &LiveView{Client: s, Locale: "en-US"}
	tester := ui.NewTester(v.history, 320, 600)
	if empty, ok := tester.Find("No sessions yet"); !ok || math.Abs(float64(empty.Y-300)) > 80 {
		t.Fatalf("empty state should be centered in the panel body: %+v", empty)
	}
	if _, ok := tester.Find("Next"); ok {
		t.Fatal("empty first page should omit pagination")
	}
	stamp := time.Date(2026, 11, 27, 9, 30, 0, 0, time.Local)
	s.History = []domain.LiveSession{{ID: "one", CreatedAt: stamp.Format(time.RFC3339Nano), State: "completed", SegmentCount: 3}}
	tester.Frame()
	for _, label := range []string{"Nov 27, 2026", "9:30 AM", "Completed · Captions: 3"} {
		if _, ok := tester.Find(label); !ok {
			t.Fatalf("missing session detail %q", label)
		}
	}
	s.History, s.HistoryOffset = nil, 20
	tester.Frame()
	for _, label := range []string{"No sessions on this page", "Page 2", "Previous"} {
		if _, ok := tester.Find(label); !ok {
			t.Fatalf("missing last-page recovery control or label %q", label)
		}
	}
}

func TestLiveModelButtonOpensRuntimeSettings(t *testing.T) {
	controller := client.NewLive(nil, nil, func(f func()) { f() }, func() {})
	defer controller.Close()
	controller.Options = application.LiveOptions{Devices: []domain.AudioDevice{{ID: "mic", Name: "Test microphone"}}}
	opened := false
	view := &LiveView{Client: controller, Locale: "zh-CN", OpenSettings: func() { opened = true }}
	tester := ui.NewTester(view.View, 900, 760)
	if err := tester.Click("下载本地模型"); err != nil {
		t.Fatal(err)
	}
	if !opened {
		t.Fatal("model button did not open runtime settings")
	}
	if controller.Preparing {
		t.Fatal("model button started a download in the live view")
	}
}

func TestOpenRuntimeSettingsSelectsEnvironmentAndModels(t *testing.T) {
	workspace := &Workspace{
		tab:      "/live",
		view:     &Meetings{Source: "local"},
		settings: &SettingsView{Client: &client.SettingsController{}, section: "appearance"},
	}

	workspace.openRuntimeSettings()

	if workspace.tab != "/settings" || workspace.settings.section != "speechModels" {
		t.Fatalf("runtime settings target = route %q, section %q", workspace.tab, workspace.settings.section)
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
				for _, label := range []string{view.text("音频输入电平", "Audio input level"), view.text("收听设置", "Listening settings")} {
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
