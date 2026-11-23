package desktop

import (
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func receiveAssemblyDispatch(t *testing.T, dispatches <-chan func()) func() {
	t.Helper()
	select {
	case dispatch := <-dispatches:
		return dispatch
	case <-time.After(3 * time.Second):
		t.Fatal("work controller callback was not dispatched")
		return nil
	}
}

func assemblyTestClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

func TestAssemblyMouseAndKeyboardPlayThrough(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := assemblyGameView{Locale: locale}
			tt := ui.NewTester(view.View, 960, 560)
			assemblyTestClick(t, tt, view.text("急单", "Rush Order"))
			assemblyTestClick(t, tt, view.text("开始装配", "Start Assembly"))
			state := view.round.Snapshot()
			if state.Phase != domain.AssemblyPlaying || state.Mode != domain.AssemblyRush || len(state.Order) != 10 {
				t.Fatal("mode selection or start did not reach the active round")
			}
			tt.Advance(1100 * time.Millisecond)
			if state = view.round.Snapshot(); state.Elapsed < time.Second || state.Phase != domain.AssemblyPlaying {
				t.Fatalf("mounted game loop did not advance: %+v", state)
			}
			wrong := domain.AssemblyPart(int(state.Order[0])%3 + 1)
			assemblyTestClick(t, tt, fmt.Sprintf("%d · %s", wrong, view.partName(wrong)))
			if state = view.round.Snapshot(); state.Mistakes != 1 || state.Position != 0 || state.Elapsed < 6*time.Second {
				t.Fatalf("mouse did not apply a single wrong-part penalty: %+v", state)
			}
			// Alternate pointer and keyboard input through the same visible order.
			for i, part := range state.Order {
				if i%2 == 0 {
					assemblyTestClick(t, tt, fmt.Sprintf("%d · %s", part, view.partName(part)))
				} else {
					tt.Key(0, ui.Key1+ui.Key(part-1))
					tt.Frame()
				}
				if got := view.round.Snapshot().Position; got != i+1 {
					t.Fatalf("part %d left progress at %d, want %d", part, got, i+1)
				}
			}
			if view.round.Snapshot().Phase != domain.AssemblyCompleted || !tt.HasText(view.text("订单完成", "Order Complete")) {
				t.Fatal("terminal result was not rebuilt from the game state")
			}
			completed := view.round.Snapshot()
			tt.Key(0, ui.Key1)
			tt.Advance(2 * time.Second)
			if !reflect.DeepEqual(completed, view.round.Snapshot()) {
				t.Fatal("result screen continued the game or accepted another part")
			}
			assemblyTestClick(t, tt, view.text("再来一局", "Play Again"))
			if state = view.round.Snapshot(); state.Phase != domain.AssemblyPlaying || state.Position != 0 || state.Mistakes != 0 || state.Elapsed != 0 || reflect.DeepEqual(state.Order, completed.Order) {
				t.Fatal("replay did not initialize a new order")
			}
		})
	}
}

func TestAssemblyStatusAnnouncesCurrentFeedbackAcrossPhases(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := assemblyGameView{Locale: locale}
			tt := ui.NewTester(view.View, 960, 620)
			assertStatus := func(want string) {
				t.Helper()
				if _, ok := tt.AccessByRole(ui.RoleStatus, want); !ok {
					t.Fatalf("accessible status missing %q", want)
				}
			}

			assemblyTestClick(t, tt, view.text("开始装配", "Start Assembly"))
			state := view.round.Snapshot()
			first := state.Order[0]
			assertStatus(fmt.Sprintf(view.text("当前步骤：%d/%d，安装%s。", "Current step: %d of %d, fit %s."), 1, len(state.Order), view.partName(first)))

			wrong := domain.AssemblyPart(int(first)%3 + 1)
			assemblyTestClick(t, tt, fmt.Sprintf("%d · %s", wrong, view.partName(wrong)))
			state = view.round.Snapshot()
			wrongStatus := fmt.Sprintf(view.text("配件不对：%s。扣 %d 秒；当前步骤：%s。", "Wrong part: %s. -%ds; current step: %s."), view.partName(wrong), int(state.Penalty/time.Second), view.partName(first))
			assertStatus(wrongStatus)

			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			state = view.round.Snapshot()
			assertStatus(fmt.Sprintf(view.text("练习已暂停，已完成 %d/%d。", "Practice paused. %d of %d parts fitted."), state.Position, len(state.Order)))
			assemblyTestClick(t, tt, view.text("继续装配", "Resume Assembly"))
			state = view.round.Snapshot()
			resumeStatus := fmt.Sprintf(view.text("当前步骤：%d/%d，安装%s。", "Current step: %d of %d, fit %s."), state.Position+1, len(state.Order), view.partName(state.Order[state.Position]))
			assertStatus(resumeStatus)

			for state = view.round.Snapshot(); state.Position < len(state.Order); state = view.round.Snapshot() {
				part := state.Order[state.Position]
				assemblyTestClick(t, tt, fmt.Sprintf("%d · %s", part, view.partName(part)))
				state = view.round.Snapshot()
				if state.Phase == domain.AssemblyCompleted {
					break
				}
				next := state.Order[state.Position]
				assertStatus(fmt.Sprintf(view.text("%s 已安装。进度 %d/%d；下一步：%s。", "%s fitted. Progress %d of %d; next: %s."), view.partName(part), state.Position, len(state.Order), view.partName(next)))
			}
			state = view.round.Snapshot()
			assertStatus(fmt.Sprintf(view.text("订单完成：完成 %d/%d，准确率 %d%%。", "Order complete: %d of %d parts fitted, %d%% accuracy."), state.Position, len(state.Order), state.AccuracyPercent))
			if _, stale := tt.AccessByRole(ui.RoleStatus, wrongStatus); stale {
				t.Fatal("completed round still exposed the previous wrong-part status")
			}

			timedView := assemblyGameView{Locale: locale}
			timedTester := ui.NewTester(timedView.View, 960, 620)
			assemblyTestClick(t, timedTester, view.text("开始装配", "Start Assembly"))
			state = timedView.round.Snapshot()
			wrong = domain.AssemblyPart(int(state.Order[0])%3 + 1)
			assemblyTestClick(t, timedTester, fmt.Sprintf("%d · %s", wrong, timedView.partName(wrong)))
			timedTester.Advance(61 * time.Second)
			state = timedView.round.Snapshot()
			if state.Phase != domain.AssemblyTimedOut {
				t.Fatalf("timeout status setup did not time out: %+v", state)
			}
			assertTimedStatus := fmt.Sprintf(timedView.text("时间用尽：完成 %d/%d，准确率 %d%%。", "Time's up: %d of %d parts fitted, %d%% accuracy."), state.Position, len(state.Order), state.AccuracyPercent)
			if _, ok := timedTester.AccessByRole(ui.RoleStatus, assertTimedStatus); !ok {
				t.Fatalf("accessible timeout status missing %q", assertTimedStatus)
			}
			if _, stale := timedTester.AccessByRole(ui.RoleStatus, wrongStatus); stale {
				t.Fatal("timed-out round still exposed a previous wrong-part status")
			}
		})
	}
}

func TestAssemblyPauseFocusHiddenAndTab(t *testing.T) {
	for _, loss := range []string{"escape", "keyboard-focus", "inactive", "hidden"} {
		t.Run(loss, func(t *testing.T) {
			view := assemblyGameView{Locale: "en-US"}
			tt := ui.NewTester(view.View, 820, 620)
			assemblyTestClick(t, tt, "Start Assembly")
			tt.Advance(100 * time.Millisecond)
			// Tab must leave the canvas, reach a native control and keep play live.
			tt.Key(0, ui.KeyTab)
			tt.Frame()
			if view.round.Snapshot().Phase != domain.AssemblyPlaying {
				t.Fatal("Tab trapped focus or paused despite another game control having focus")
			}
			switch loss {
			case "escape":
				tt.Key(0, ui.KeyEscape)
			case "keyboard-focus":
				tt.SetFocused(false)
			case "inactive":
				tt.SetWindowActive(false)
			case "hidden":
				tt.SetWindowHidden(true)
			}
			tt.Frame()
			paused := view.round.Snapshot()
			if paused.Phase != domain.AssemblyPaused {
				t.Fatalf("%s did not pause: %+v", loss, paused)
			}
			tt.Key(0, ui.Key1)
			tt.Advance(70 * time.Second)
			if !reflect.DeepEqual(paused, view.round.Snapshot()) {
				t.Fatal("paused round accepted input or spent background time")
			}
			tt.SetWindowHidden(false)
			tt.SetWindowActive(true)
			tt.SetFocused(true)
			tt.Frame()
			if view.round.Snapshot().Phase != domain.AssemblyPaused {
				t.Fatal("regaining focus silently resumed play")
			}
			assemblyTestClick(t, tt, "Resume Assembly")
			tt.Advance(100 * time.Millisecond)
			state := view.round.Snapshot()
			if state.Phase != domain.AssemblyPlaying || state.Elapsed-paused.Elapsed > 150*time.Millisecond {
				t.Fatalf("resume replayed background time: %+v vs %+v", state, paused)
			}
		})
	}
}

func TestAssemblyRepeatTimeoutAndReducedMotion(t *testing.T) {
	view := assemblyGameView{Locale: "en-US"}
	tt := ui.NewTesterWith(view.View, 820, 620, ui.TesterOptions{Dark: true, Preferences: ui.Preferences{ReduceMotion: true}})
	assemblyTestClick(t, tt, "Rush Order")
	assemblyTestClick(t, tt, "Start Assembly")
	part := view.round.Snapshot().Order[0]
	key := ui.Key1 + ui.Key(part-1)
	tt.KeyDown(key)
	tt.Frame()
	once := view.round.Snapshot()
	for range 6 {
		tt.KeyRepeat(0, key)
		tt.Advance(20 * time.Millisecond)
	}
	tt.KeyUp(key)
	if state := view.round.Snapshot(); state.Position != once.Position || state.Mistakes != once.Mistakes {
		t.Fatal("a held key repeated part placement")
	}
	if view.visualTime != 0 {
		t.Fatal("reduced motion advanced decorative animation")
	}
	tt.Advance(46 * time.Second)
	if state := view.round.Snapshot(); state.Phase != domain.AssemblyTimedOut || state.Remaining != 0 || !tt.HasText("Time's Up") {
		t.Fatalf("game clock did not produce a visible timeout: %+v", state)
	}
}

func TestAssemblyPartButtonIgnoresActivationKeyRepeat(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  ui.Key
	}{
		{"space", ui.KeySpace},
		{"enter", ui.KeyEnter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := assemblyGameView{Locale: "en-US"}
			tt := ui.NewTester(view.View, 960, 560)
			assemblyTestClick(t, tt, "Start Assembly")
			part := view.round.Snapshot().Order[0]
			label := fmt.Sprintf("%d · %s", part, view.partName(part))
			for count := 0; count < 12 && !tt.Focused(label); count++ {
				tt.Key(0, ui.KeyTab)
			}
			if !tt.Focused(label) {
				t.Fatal("Tab did not reach the correct part button")
			}
			tt.KeyDown(tc.key)
			once := view.round.Snapshot()
			if once.Position != 1 || once.Mistakes != 0 {
				t.Fatalf("initial activation did not place one correct part: %+v", once)
			}
			for range 6 {
				tt.KeyRepeat(0, tc.key)
				tt.Advance(20 * time.Millisecond)
			}
			tt.KeyUp(tc.key)
			if state := view.round.Snapshot(); state.Position != once.Position || state.Mistakes != once.Mistakes {
				t.Fatalf("a held activation key repeated part placement: %+v", state)
			}
			tt.Key(0, tc.key)
			if state := view.round.Snapshot(); state.Position+state.Mistakes != once.Position+once.Mistakes+1 {
				t.Fatal("a fresh activation after key release was ignored")
			}
		})
	}
}

func TestAssemblyLayoutAndControlSemantics(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{820, 1180} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				view := assemblyGameView{Locale: locale}
				tt := ui.NewTesterWith(view.View, width, 620, ui.TesterOptions{Dark: locale == "en-US"})
				capture := func(phase string) {
					t.Helper()
					if directory := os.Getenv("LIBERTY_ASSEMBLY_SCREENSHOT_DIR"); directory != "" {
						if err := os.MkdirAll(directory, 0o755); err != nil {
							t.Fatal(err)
						}
						file, err := os.Create(filepath.Join(directory, fmt.Sprintf("%s-%d-%s.png", locale, width, phase)))
						if err != nil {
							t.Fatal(err)
						}
						err = png.Encode(file, tt.Image())
						closeErr := file.Close()
						if err != nil || closeErr != nil {
							t.Fatalf("capture failed: %v / %v", err, closeErr)
						}
					}
				}
				capture("ready")
				assemblyTestClick(t, tt, view.text("开始装配", "Start Assembly"))
				capture("playing")
				for _, part := range []domain.AssemblyPart{domain.AssemblyBolt, domain.AssemblyWasher, domain.AssemblyGear} {
					label := fmt.Sprintf("%d · %s", part, view.partName(part))
					access, ok := tt.Access(label)
					if !ok || access.Role != ui.RoleButton || !access.Focusable || access.Disabled || access.Offscreen || access.Bounds.H < 44 || access.Bounds.X < 0 || access.Bounds.X+access.Bounds.W > float32(width) {
						t.Fatalf("inaccessible or clipped part control %s: %+v", label, access)
					}
				}
				assemblyTestClick(t, tt, view.text("暂停", "Pause"))
				capture("paused")
				if !tt.HasText(view.text("已暂停", "Paused")) {
					t.Fatal("pause overlay missing")
				}
			})
		}
	}
}

func TestAssemblyFactoryEntryKeepsWorkState(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, key := range []string{"factory", "mine", "convenience-store"} {
			t.Run(locale+"/"+key, func(t *testing.T) {
				state := domain.WorkGameState{GameKey: key, NameEn: "Factory", NameZh: "工厂打螺丝"}
				for _, game := range domain.WorkGameCatalog() {
					if game.Key == key {
						state.NameEn, state.NameZh, state.Jobs = game.NameEn, game.NameZh, game.Jobs
						job := game.Jobs[0]
						state.Tasks = []domain.WorkGameTask{{ID: key + "-slot-1", GameKey: key, JobKey: job.JobKey, SlotIndex: 1, Status: workIdle, Job: &job}}
					}
				}
				// With no service supplied, any accidental start/care/claim call
				// fails. Practice is reachable without a work-task mutation.
				controller := &client.WorkController{GameStates: map[string]domain.WorkGameState{key: state}}
				opened := 0
				view := WorkGameView{Client: controller, Locale: locale, GameKey: key, OpenAssembly: func() { opened++ }}
				tt := ui.NewTester(view.View, 960, 560)
				tt.Frame()
				label := workText(locale, "Practice Basic Assembly", "练习基础装配")
				if key != "factory" {
					if _, ok := tt.Find(label); ok {
						t.Fatal("assembly practice appeared outside the factory")
					}
					return
				}
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if opened != 1 || !reflect.DeepEqual(controller.GameStates[key], state) || controller.BusyKey != "" || controller.LastReward != nil {
					t.Fatal("practice changed persistent work state or failed to open")
				}
			})
		}
	}
}

func TestAssemblyCarePracticeReportsOnlyCompletedRound(t *testing.T) {
	completed := 0
	view := assemblyGameView{
		Locale:         "en-US",
		CareTaskID:     "factory-task-1",
		OnCareComplete: func() { completed++ },
	}
	tester := ui.NewTester(view.View, 960, 560)
	assemblyTestClick(t, tester, "Start Assembly")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if completed != 0 || view.round.Snapshot().Phase != domain.AssemblyPaused {
		t.Fatal("pausing a care practice round changed the task")
	}
	assemblyTestClick(t, tester, "Resume Assembly")
	for _, part := range view.round.Snapshot().Order {
		assemblyTestClick(t, tester, fmt.Sprintf("%d · %s", part, view.partName(part)))
	}
	if completed != 1 || view.round.Snapshot().Phase != domain.AssemblyCompleted {
		t.Fatalf("completed care practice did not report exactly once: count=%d state=%+v", completed, view.round.Snapshot())
	}
	assemblyTestClick(t, tester, "Play Again")
	for _, part := range view.round.Snapshot().Order {
		assemblyTestClick(t, tester, fmt.Sprintf("%d · %s", part, view.partName(part)))
	}
	if completed != 1 {
		t.Fatal("replaying a completed care entry reported a second care action")
	}
	failed := assemblyGameView{Locale: "en-US", CareTaskID: "factory-task-2", OnCareComplete: func() { completed++ }}
	failedTester := ui.NewTester(failed.View, 960, 560)
	assemblyTestClick(t, failedTester, "Rush Order")
	assemblyTestClick(t, failedTester, "Start Assembly")
	failedTester.Advance(46 * time.Second)
	if failed.round.Snapshot().Phase != domain.AssemblyTimedOut || completed != 1 {
		t.Fatal("timed-out care practice changed the task")
	}
}

func TestWorkCarePracticeEntryPreservesDirectCareAction(t *testing.T) {
	game := factoryWorkDefinition(t)
	task := domain.WorkGameTask{ID: "factory-task-1", GameKey: game.Key, SlotIndex: 1, Status: workNeedsCare, Job: &game.Jobs[0]}
	state := domain.WorkGameState{GameKey: game.Key, NameEn: game.NameEn, NameZh: game.NameZh, Jobs: game.Jobs, Tasks: []domain.WorkGameTask{task}}
	openedTask := ""
	view := &WorkGameView{
		Client:           &client.WorkController{GameStates: map[string]domain.WorkGameState{game.Key: state}},
		Locale:           "en-US",
		GameKey:          game.Key,
		OpenAssemblyCare: func(taskID string) { openedTask = taskID },
	}
	tester := ui.NewTester(view.View, 960, 700)
	if !tester.HasText("Care Job") {
		t.Fatal("direct care action disappeared when optional practice was available")
	}
	if !tester.HasText("Care with Assembly Practice") {
		t.Fatalf("optional care entry not rendered, game=%s state=%+v text=%v", game.Key, state, tester.Texts())
	}
	if err := tester.Click("Care with Assembly Practice"); err != nil {
		t.Fatalf("optional care entry not clickable: %v; text=%v", err, tester.Texts())
	}
	tester.Frame()
	if openedTask != task.ID {
		t.Fatalf("optional care practice opened task %q, want %q", openedTask, task.ID)
	}
}

func TestAssemblyCareSuccessUsesWorkControllerOnce(t *testing.T) {
	game := factoryWorkDefinition(t)
	task := domain.WorkGameTask{ID: "factory-task-1", GameKey: game.Key, SlotIndex: 1, Status: workNeedsCare}
	state := domain.WorkGameState{GameKey: game.Key, NameEn: game.NameEn, NameZh: game.NameZh, Jobs: game.Jobs, Tasks: []domain.WorkGameTask{task}}
	nextCareAt := "2026-11-20T12:40:00Z"
	advancedTask := task
	advancedTask.Status = workRunning
	advancedTask.StageIndex = 1
	advancedTask.NextCareAt = &nextCareAt
	advancedTask.RemainingSeconds = 60
	advanced := state
	advanced.Tasks = []domain.WorkGameTask{advancedTask}
	calls := make(chan workUIRequest, 8)
	service := &workUIService{calls: calls, game: state, afterCare: &advanced}
	dispatches := make(chan func(), 8)
	controller := client.NewWorkController(service, func(run func()) { dispatches <- run }, func() {})
	defer controller.Close()
	meetings := &Meetings{Locale: "en-US"}
	workspace := Workspace{
		tab:        assemblyRoute,
		view:       meetings,
		workClient: controller,
		workGame:   &WorkGameView{Client: controller},
	}
	workspace.assemblyGame = &assemblyGameView{
		Locale:     "en-US",
		CareTaskID: task.ID,
		OnCareComplete: func() {
			workspace.completeAssemblyCare(task.ID)
		},
	}
	tester := ui.NewTester(workspace.assemblyGame.View, 960, 560)
	assemblyTestClick(t, tester, "Start Assembly")
	for _, part := range workspace.assemblyGame.round.Snapshot().Order {
		assemblyTestClick(t, tester, fmt.Sprintf("%d · %s", part, workspace.assemblyGame.partName(part)))
	}
	care := receiveWorkUIRequest(t, calls)
	if care.action != "care" || care.game != "factory" || care.target != task.ID || workspace.tab != factoryRoute {
		t.Fatalf("successful care practice did not return through the work controller: %+v route=%s", care, workspace.tab)
	}
	careDone := receiveAssemblyDispatch(t, dispatches)
	careDone()
	refresh := receiveWorkUIRequest(t, calls)
	if refresh.action != "game" {
		t.Fatalf("care completion did not refresh the factory state: %+v", refresh)
	}
	refreshDone := receiveAssemblyDispatch(t, dispatches)
	refreshDone()
	if refreshed := controller.GameStates["factory"].Tasks[0]; refreshed.Status != workRunning || refreshed.StageIndex != 1 || refreshed.NextCareAt == nil || *refreshed.NextCareAt != nextCareAt {
		t.Fatalf("care practice did not refresh the persisted next stage: %+v", refreshed)
	}
	workView := &WorkGameView{Locale: "en-US", GameKey: "factory", Client: controller}
	workTester := ui.NewTester(workView.View, 960, 700)
	if !workTester.HasText("Next care in 1 min") || workTester.HasText("Care with Assembly Practice") {
		t.Fatalf("factory page did not render the post-care state: %v", workTester.Texts())
	}
	select {
	case extra := <-calls:
		t.Fatalf("care practice repeated a business action: %+v", extra)
	default:
	}
}

func TestAssemblyCareFailureRemainsVisibleAfterReturnToFactory(t *testing.T) {
	game := factoryWorkDefinition(t)
	task := domain.WorkGameTask{ID: "factory-task-1", GameKey: game.Key, SlotIndex: 1, Status: workNeedsCare}
	state := domain.WorkGameState{GameKey: game.Key, NameEn: game.NameEn, NameZh: game.NameZh, Jobs: game.Jobs, Tasks: []domain.WorkGameTask{task}}
	failure := errors.New("synthetic care rejection")
	calls := make(chan workUIRequest, 8)
	service := &workUIService{calls: calls, game: state, careError: failure}
	dispatches := make(chan func(), 8)
	controller := client.NewWorkController(service, func(run func()) { dispatches <- run }, func() {})
	defer controller.Close()
	workspace := Workspace{
		tab:        assemblyRoute,
		view:       &Meetings{Locale: "en-US"},
		workClient: controller,
		workGame:   &WorkGameView{Client: controller},
	}
	workspace.assemblyGame = &assemblyGameView{
		Locale:     "en-US",
		CareTaskID: task.ID,
		OnCareComplete: func() {
			workspace.completeAssemblyCare(task.ID)
		},
	}
	tester := ui.NewTester(workspace.assemblyGame.View, 960, 560)
	assemblyTestClick(t, tester, "Start Assembly")
	for _, part := range workspace.assemblyGame.round.Snapshot().Order {
		assemblyTestClick(t, tester, fmt.Sprintf("%d · %s", part, workspace.assemblyGame.partName(part)))
	}
	care := receiveWorkUIRequest(t, calls)
	if care.action != "care" || workspace.tab != factoryRoute {
		t.Fatalf("failed care practice did not return to the factory: %+v route=%s", care, workspace.tab)
	}
	receiveAssemblyDispatch(t, dispatches)()
	if refresh := receiveWorkUIRequest(t, calls); refresh.action != "game" {
		t.Fatalf("factory return did not schedule a state refresh: %+v", refresh)
	}
	receiveAssemblyDispatch(t, dispatches)()
	if !errors.Is(controller.Error, failure) || controller.ErrorIsRefresh || controller.GameStates[game.Key].Tasks[0].Status != workNeedsCare {
		t.Fatalf("failed care action was hidden by the route refresh: error=%v refresh=%v state=%+v", controller.Error, controller.ErrorIsRefresh, controller.GameStates[game.Key])
	}
	view := &WorkGameView{Locale: "en-US", GameKey: game.Key, Client: controller}
	workTester := ui.NewTester(view.View, 960, 700)
	if _, ok := workTester.AccessByRole(ui.RoleAlert, "The action could not be completed. Refresh the map before trying again."); !ok || !workTester.HasText("Care Job") {
		t.Fatalf("care failure was not visible or retryable after returning to the factory: %v", workTester.Texts())
	}
}

func factoryWorkDefinition(t *testing.T) domain.WorkGameDefinition {
	t.Helper()
	for _, game := range domain.WorkGameCatalog() {
		if game.Key == "factory" {
			return game
		}
	}
	t.Fatal("factory work definition is missing")
	return domain.WorkGameDefinition{}
}

func TestAssemblyWorkspaceLeaveDiscardsRoundAndBlurPauses(t *testing.T) {
	w := Workspace{tab: factoryRoute, view: &Meetings{Locale: "en-US"}, workGame: &WorkGameView{GameKey: "factory"}}
	w.navigate(assemblyRoute)
	if w.tab != assemblyRoute || w.assemblyGame == nil || w.workGame.GameKey != "factory" {
		t.Fatal("practice did not open independently of the factory work task")
	}
	first := w.assemblyGame
	first.round.Start(domain.AssemblySteady, 20)
	first.round.Advance(time.Second)
	w.pauseAssemblyGame()
	if first.round.Snapshot().Phase != domain.AssemblyPaused {
		t.Fatal("workspace lifecycle did not pause the round")
	}
	first.Back()
	if w.tab != factoryRoute || w.assemblyGame != nil || w.workGame.GameKey != "factory" {
		t.Fatal("return left a live round or changed the work game")
	}
	w.navigate(assemblyRoute)
	if w.assemblyGame == first || w.assemblyGame.round.Snapshot().Phase != domain.AssemblyReady {
		t.Fatal("reopening practice resumed a discarded round")
	}
	w.navigate("/")
	if w.assemblyGame != nil {
		t.Fatal("sidebar navigation kept the game loop alive")
	}
}
