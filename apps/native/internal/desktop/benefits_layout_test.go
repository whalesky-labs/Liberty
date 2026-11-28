package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func benefitsLayoutView(locale, route string) *PetView {
	store := petParityStore()
	store.Wallet.Balance = 240
	rewards := []domain.PetDailyCheckInRewardPreview{
		{CycleDay: 3, RewardLP: 20, GrowthValue: 5, Items: []domain.PetRewardItem{{ItemKey: domain.PetGiftBox, Quantity: 1}}},
		{CycleDay: 7, RewardLP: 20, GrowthValue: 5, Items: []domain.PetRewardItem{{ItemKey: "clover-badge", Quantity: 1}}},
	}
	pool := []domain.PetBlindBoxPoolItem{}
	for _, key := range []string{domain.PetGiftBox, "apple", "bookshelf-scene"} {
		if item, ok := domain.FindPetItem(key); ok {
			pool = append(pool, domain.PetBlindBoxPoolItem{Item: item})
		}
	}
	return &PetView{Locale: locale, Route: route, ContentWidth: shellContentWidth, Client: &client.PetController{
		Store: store, Profile: &store.Profile,
		CheckIn:  &domain.PetDailyCheckInState{CheckInDate: "2026-11-28", CurrentStreak: 2, NextCycleDay: 3, CycleLength: 14, TodayReward: rewards[0], Rewards: rewards, StoreState: *store, MakeupTicketQuantity: 1},
		BlindBox: &domain.PetBlindBoxState{DrawDate: "2026-11-28", DailyLimit: 10, RemainingToday: 10, Pool: pool},
	}}
}

func TestBenefitsPagesKeepPrimaryActionsVisibleAcrossThemesAndWidths(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{1100, 1440} {
				for _, route := range []string{"/redeem-key", "/daily-check-in", "/daily-blind-box"} {
					t.Run(fmt.Sprintf("%s-%s-%t-%d", route, locale, dark, width), func(t *testing.T) {
						view := benefitsLayoutView(locale, route)
						shell := Shell{Locale: locale, Route: route, Preferences: domain.DefaultUIPreferences(), Content: view.View}
						tester := ui.NewTesterWith(shell.View, width, 900, ui.TesterOptions{Dark: dark})
						tester.SetPreferences(ui.Preferences{ReduceMotion: true})
						tester.Frame()
						label := view.text("立即兑换", "Redeem")
						if route == "/daily-check-in" {
							label = view.text("签到领取", "Check In")
						} else if route == "/daily-blind-box" {
							label = view.text("开始抽奖", "Start Draw")
						}
						action, ok := tester.AccessByRole(ui.RoleButton, label)
						if !ok || action.Offscreen || action.Bounds.W < 40 || action.Bounds.X < 0 || action.Bounds.X+action.Bounds.W > float32(width) || action.Bounds.Y+action.Bounds.H > 740 {
							t.Fatal("primary benefits action is clipped at the minimum height", action)
						}
						if route == "/redeem-key" && !action.Disabled {
							t.Fatal("empty redemption key can be submitted")
						}
						if route == "/daily-check-in" {
							view.Client.CheckIn.CheckedInToday = true
							tester.Frame()
							assertDisabled(t, tester, view.text("今日已签到", "Checked In Today"), true)
							view.Client.CheckIn.CheckedInToday = false
							tester.Frame()
						}
						if route == "/daily-blind-box" {
							view.Client.BlindBox.RemainingToday = 0
							tester.Frame()
							assertDisabled(t, tester, view.text("今日次数已用完", "No Attempts Left"), true)
							view.Client.BlindBox.RemainingToday = 10
							tester.Frame()
						}
						captureParity(t, tester, fmt.Sprintf("benefits-%s-%s-%t-%d", route[1:], locale, dark, width))
					})
				}
			}
		}
	}
}

func TestBenefitsCalendarNavigationPreservesTodayAndClaimState(t *testing.T) {
	view := benefitsLayoutView("en-US", "/daily-check-in")
	shell := Shell{Locale: view.Locale, Route: view.Route, Preferences: domain.DefaultUIPreferences(), Content: view.View}
	tester := ui.NewTester(shell.View, 1100, 900)
	for _, label := range []string{"Previous", "This Month"} {
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
	}
	if view.calendarOffset != 0 || view.Client.CheckIn.CheckedInToday || view.Client.Busy {
		t.Fatal("browsing the calendar changed the claim state")
	}
	day, ok := tester.AccessByRole(ui.RoleGroup, view.Client.CheckIn.CheckInDate)
	if !ok || day.Current != ui.CurrentDate {
		t.Fatal("returning to this month lost today's accessible state")
	}
}
