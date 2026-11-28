package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func syntheticDrawResult() domain.PetBlindBoxDrawResult {
	prize, _ := domain.FindPetItem("wheat-harvest-food")
	return domain.PetBlindBoxDrawResult{
		Prize: prize,
		Draw:  domain.PetBlindBoxDrawEntry{ID: "synthetic-draw", ItemType: "food", Quantity: 2},
		State: domain.PetBlindBoxState{DailyLimit: 10, RemainingToday: 9, UsedToday: 1},
	}
}

func TestBlindBoxResultDialogShowsSettlementAndDismissesOnce(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, outcome := range []string{"item", "duplicate", "empty"} {
				t.Run(fmt.Sprintf("%s-%t-%s", locale, dark, outcome), func(t *testing.T) {
					view := benefitsLayoutView(locale, "/daily-blind-box")
					result := syntheticDrawResult()
					wantAmount := "×2"
					switch outcome {
					case "duplicate":
						result.Prize.ItemType, result.Duplicate = "cosmetic", true
						result.Draw.DuplicateCompensationLP = 12
						wantAmount = "+12 LP"
					case "empty":
						result.Prize.ItemType, result.Draw.ItemType = "none", "none"
						result.Prize.NameZh, result.Prize.NameEn = "什么都没抽中", "Nothing This Time"
						result.Draw.Quantity = 0
						wantAmount = view.text("本次未获得物品", "No item this time")
					}
					shell := Shell{Locale: locale, Route: view.Route, Preferences: domain.DefaultUIPreferences(), Content: view.View}
					tester := ui.NewTesterWith(shell.View, 1100, 760, ui.TesterOptions{Dark: dark})
					tester.SetPreferences(ui.Preferences{ReduceMotion: true})
					view.Client.Busy, view.Client.PendingAction = true, "draw"
					view.drawStart = tester.Now()
					tester.Frame()
					title, done := view.text("抽奖结果", "Blind-box result"), view.text("知道了", "Done")
					if _, ok := tester.AccessByRole(ui.RoleDialog, title); ok {
						t.Fatal("draw showed a result before settlement")
					}
					view.Client.Busy, view.Client.PendingAction = false, ""
					view.Client.LastAction, view.Client.ActionRevision, view.Client.LastDraw = "draw", 1, &result
					tester.Frame()
					dialog, ok := tester.AccessByRole(ui.RoleDialog, title)
					if !ok || dialog.Offscreen || dialog.Bounds.X < 0 || dialog.Bounds.Y < 0 || dialog.Bounds.X+dialog.Bounds.W > 1100 || dialog.Bounds.Y+dialog.Bounds.H > 760 {
						t.Fatal("settled reward dialog is missing or clipped", dialog)
					}
					if !tester.HasText(wantAmount) || !tester.HasText(view.text(result.Prize.NameZh, result.Prize.NameEn)) || !tester.Focused(done) {
						t.Fatal("dialog lost localized prize, amount or initial focus")
					}
					if tester.HasText(view.text("最新奖励", "Latest Reward")) {
						t.Fatal("the inline result remains behind the dialog")
					}
					captureParity(t, tester, fmt.Sprintf("blind-result-%s-%t-%s", locale, dark, outcome))
					if outcome == "item" {
						tester.Key(0, ui.KeyEnter)
					} else if outcome == "duplicate" {
						tester.Key(0, ui.KeyEscape)
					} else if err := tester.Click(done); err != nil {
						t.Fatal(err)
					}
					tester.Frame()
					if view.drawResultOpen {
						t.Fatal("result could not be dismissed")
					}
					// A fresh pointer from the same accepted result must not be a new event.
					copy := result
					view.Client.LastDraw = &copy
					view.Route = "/daily-check-in"
					tester.Frame()
					view.Route = "/daily-blind-box"
					tester.Frame()
					if view.drawResultOpen || view.Client.ActionRevision != 1 {
						t.Fatal("redraw or route return replayed a dismissed result")
					}
					view.Client.ActionRevision++
					tester.Frame()
					if !view.drawResultOpen {
						t.Fatal("a second accepted draw did not present its result")
					}
				})
			}
		}
	}
}

func TestBlindBoxResultDialogLongTextStaysWithinWindow(t *testing.T) {
	view := benefitsLayoutView("en-US", "/daily-blind-box")
	result := syntheticDrawResult()
	result.Prize.NameEn = strings.Repeat("Long reward name ", 10)
	result.Prize.DescriptionEn = strings.Repeat("Long reward description. ", 30)
	view.Client.LastAction, view.Client.ActionRevision, view.Client.LastDraw = "draw", 1, &result
	shell := Shell{Locale: view.Locale, Route: view.Route, Preferences: domain.DefaultUIPreferences(), Content: view.View}
	tester := ui.NewTester(shell.View, 1100, 760)
	done, ok := tester.AccessByRole(ui.RoleButton, "Done")
	if !ok || done.Offscreen || done.Bounds.Y+done.Bounds.H > 760 || !done.Focused {
		t.Fatal("long result text pushed its dismissal outside the window", done)
	}
}

type drawResultPort struct {
	petRefreshPort
	draws  int
	result domain.PetBlindBoxDrawResult
}

func (port *drawResultPort) DrawBlindBox(context.Context) (domain.PetBlindBoxDrawResult, error) {
	port.draws++
	return port.result, port.actionError
}

func (port *drawResultPort) BlindBox(context.Context) (domain.PetBlindBoxState, error) {
	return port.result.State, nil
}

func TestBlindBoxResultFollowsCommittedDrawAndNotFailedRetry(t *testing.T) {
	port := &drawResultPort{petRefreshPort: petRefreshPort{store: *petParityStore(), readError: errors.New("synthetic refresh failure")}, result: syntheticDrawResult()}
	port.result.State.StoreState = port.store
	callbacks := make(chan func(), 4)
	controller := client.NewPet(port, func(callback func()) { callbacks <- callback }, func() {})
	defer controller.Close()
	controller.Store, controller.BlindBox = &port.store, &port.result.State
	view := &PetView{Client: controller, Route: "/daily-blind-box", Locale: "en-US", ContentWidth: shellContentWidth}
	shell := Shell{Locale: view.Locale, Route: view.Route, Preferences: domain.DefaultUIPreferences(), Content: view.View}
	tester := ui.NewTester(shell.View, 1100, 760)
	settle := func() {
		t.Helper()
		select {
		case callback := <-callbacks:
			callback()
		case <-time.After(8 * time.Second):
			t.Fatal("draw did not finish")
		}
		tester.Frame()
	}
	if err := tester.Click("Start Draw"); err != nil {
		t.Fatal(err)
	}
	settle()
	if !view.drawResultOpen || port.draws != 1 || !controller.ErrorIsRefresh || !tester.HasText("×2") {
		t.Fatal("committed result was hidden by its follow-up read failure")
	}
	if err := tester.Click("Done"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.Focused("Start Draw") {
		t.Fatal("dismissing the result did not restore focus to its trigger")
	}
	port.readError = nil
	controller.Refresh()
	settle()
	if view.drawResultOpen || port.draws != 1 {
		t.Fatal("refresh replayed the result or the draw")
	}
	port.actionError = domain.ErrPet
	if err := tester.Click("Start Draw"); err != nil {
		t.Fatal(err)
	}
	settle()
	if view.drawResultOpen || port.draws != 2 || controller.ActionRevision != 1 || controller.LastDraw != nil || controller.ErrorIsRefresh {
		t.Fatal("failed draw reused a previous prize or advanced settlement")
	}
}
