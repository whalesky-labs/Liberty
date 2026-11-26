package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/repositories/jobdb"
)

func petParityStore() *domain.PetStoreState {
	level := domain.PetLevel(domain.PetExperienceForLevel(7))
	store := &domain.PetStoreState{Profile: domain.PetProfile{Name: "Libby", Level: level.Level, Stage: level.CurrentStage, LevelSnapshot: level, Experience: level.TotalExperience, CurrentMood: "idle"}, Wallet: domain.PetWallet{Balance: 600}}
	for _, item := range domain.PetCatalog()[:5] {
		store.Catalog = append(store.Catalog, domain.PetStoreCatalogItemState{Item: item, Purchasable: true})
	}
	return store
}

func TestPetStoreCardsUsePaneWidthAndKeepFinalRowSize(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{2560, 1920, 1440, 1100, 960, 760} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				store := petParityStore()
				openedItem := ""
				view := &PetView{Locale: locale, Route: "/pet-store", Client: &client.PetController{Store: store}, OpenItem: func(key string) { openedItem = key }}
				shell := &Shell{Locale: locale, Route: view.Route, Preferences: domain.DefaultUIPreferences(), Content: view.View}
				tester := ui.NewTester(shell.View, width, 1400)
				captureParity(t, tester, fmt.Sprintf("pet-store-%s-%d", locale, width))
				first, ok := tester.AccessByRole(ui.RoleGroup, view.itemName(store.Catalog[0].Item.ItemKey))
				if !ok || first.Bounds.W < 148 {
					t.Fatal("missing minimum-width product card", first)
				}
				for _, item := range store.Catalog {
					card, ok := tester.AccessByRole(ui.RoleGroup, view.itemName(item.Item.ItemKey))
					if !ok || math.Abs(float64(first.Bounds.W-card.Bounds.W)) > 1 || card.Bounds.X < first.Bounds.X || card.Bounds.X+card.Bounds.W > float32(width-12) {
						t.Fatal("product card expanded its last row or left the content pane", card)
					}
				}
				if width <= 1080 {
					products, productsOK := tester.AccessByRole(ui.RoleList, view.text("商品列表", "Products"))
					overview, ok := tester.Find(view.text("商店概览", "Store Overview"))
					if !productsOK || !ok || overview.Y <= products.Bounds.Y+products.Bounds.H {
						t.Fatal("stacked overview obscures the scrollable product grid", overview, products)
					}
					// A narrow pane can add rows beyond the list viewport. Check the
					// visible boundary, then prove the clipped final item is reachable.
					lastKey := store.Catalog[len(store.Catalog)-1].Item.ItemKey
					last, _ := tester.AccessByRole(ui.RoleGroup, view.itemName(lastKey))
					if last.Bounds.Y+last.Bounds.H > products.Bounds.Y+products.Bounds.H {
						tester.Scroll(products.Bounds.X+products.Bounds.W/2, products.Bounds.Y+products.Bounds.H/2, 0, 3000)
					}
					label := view.text("查看详情：", "View Details: ") + view.itemName(lastKey)
					if err := tester.Click(label); err != nil || openedItem != lastKey {
						t.Fatal("final product cannot be opened after scrolling", openedItem, err)
					}
					captureParity(t, tester, fmt.Sprintf("pet-store-scrolled-%s-%d", locale, width))
				}
			})
		}
	}
}

func TestPetStoreDetailsShareBottomBaseline(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			store := petParityStore()
			view := &PetView{Locale: locale, section: "store", Client: &client.PetController{}, OpenItem: func(string) {}}
			tester := ui.NewTesterWith(func(context *ui.Context) {
				ui.Grid(context).FillWidth().Columns(len(store.Catalog)).Gap(12).Children(func() {
					for _, item := range store.Catalog {
						view.storeCard(context, item)
					}
				})
			}, 1100, 700, ui.TesterOptions{Dark: locale == "en-US"})
			captureParity(t, tester, "pet-store-card-baseline-"+locale)
			baseline := float32(-1)
			for _, item := range store.Catalog {
				label := view.text("查看详情：", "View Details: ") + view.itemName(item.Item.ItemKey)
				button, ok := tester.AccessByRole(ui.RoleButton, label)
				if !ok || button.Offscreen {
					t.Fatal("product detail action is missing", label, button)
				}
				if baseline < 0 {
					baseline = button.Bounds.Y
				} else if math.Abs(float64(button.Bounds.Y-baseline)) > 1 {
					t.Fatalf("product detail actions do not share a bottom baseline: %q y=%v, expected y=%v", label, button.Bounds.Y, baseline)
				}
			}
		})
	}
}

func TestPetPagesKeepControlsInsideNarrowWindow(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, route := range []string{"/pet", "/daily-check-in", "/redeem-key"} {
			t.Run(locale+route, func(t *testing.T) {
				store := petParityStore()
				reward := domain.PetDailyCheckInRewardPreview{CycleDay: 3, RewardLP: 30, GrowthValue: 5, Items: []domain.PetRewardItem{{ItemKey: domain.PetGiftBox, Quantity: 1}}}
				view := &PetView{Locale: locale, Route: route, Client: &client.PetController{Store: store, Profile: &store.Profile, CheckIn: &domain.PetDailyCheckInState{CheckInDate: "2026-11-19", CycleLength: 14, NextCycleDay: 3, TodayReward: reward, Rewards: []domain.PetDailyCheckInRewardPreview{reward}}}}
				shell := &Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
				tester := ui.NewTester(shell.View, 960, 1600)
				captureParity(t, tester, fmt.Sprintf("pet-page-%s-%s", locale, route[1:]))
				labels := map[string][]string{
					"/pet":            {view.text("修改名字", "Edit Name"), view.text("保存设置", "Save Settings")},
					"/daily-check-in": {view.text("签到领取", "Check In"), view.text("上月", "Previous")},
					"/redeem-key":     {view.text("兑换 Key", "Redemption key"), view.text("立即兑换", "Redeem")},
				}
				for _, label := range labels[route] {
					control, ok := tester.Access(label)
					if !ok || control.Bounds.W <= 0 || control.Bounds.X < 0 || control.Bounds.X+control.Bounds.W > 960 {
						t.Fatal("control left the narrow window", label, control)
					}
				}
			})
		}
	}
}

func TestPetInitialFailureDoesNotPretendToKeepLoading(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := &PetView{Locale: locale, Client: &client.PetController{Error: errors.New("synthetic read failure")}}
		tester := ui.NewTester(view.View, 800, 600)
		if _, ok := tester.AccessByRole(ui.RoleAlert, view.text("无法读取宠物信息，请重试。", "Companion information could not be loaded. Please retry.")); !ok || tester.HasText(view.text("正在加载宠物信息…", "Loading companion…")) {
			t.Fatal("failed first load remains indistinguishable from loading")
		}
	}
}

func TestPetCalendarPreservesAbsoluteDaysAndRewardItemAccessibility(t *testing.T) {
	item := domain.PetRewardItem{ItemKey: domain.PetGiftBox, Quantity: 2}
	milestone := domain.PetDailyCheckInRewardPreview{CycleDay: 14, RewardLP: 60, GrowthValue: 5, Items: []domain.PetRewardItem{{ItemKey: "sprout-badge", Quantity: 1}}}
	state := domain.PetDailyCheckInState{CheckInDate: "2026-11-19", NextCycleDay: 14, CycleLength: 14, TodayReward: milestone,
		Rewards: []domain.PetDailyCheckInRewardPreview{{CycleDay: 3, RewardLP: 30, GrowthValue: 5, Items: []domain.PetRewardItem{item}}, milestone},
		History: []domain.PetDailyCheckInEntry{{CheckInDate: "2026-11-08", StreakCount: 3, CycleDay: 3, RewardLP: 30, GrowthValue: 5, RewardItems: []domain.PetRewardItem{item}}},
	}
	expected := map[string]domain.PetDailyCheckInRewardPreview{
		"2026-11-08": state.Rewards[0],
		"2026-11-19": milestone,
		"2026-11-22": {CycleDay: 17, RewardLP: 20, GrowthValue: 5},
	}
	for _, cell := range petCalendarCells(state, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		if reward, ok := expected[cell.Date]; ok {
			if cell.Reward == nil || cell.Reward.CycleDay != reward.CycleDay || cell.Reward.RewardLP != reward.RewardLP || cell.Reward.GrowthValue != reward.GrowthValue || len(cell.Reward.Items) != len(reward.Items) {
				t.Fatalf("calendar lost the absolute day or recorded reward for %s: %+v", cell.Date, cell.Reward)
			}
			for index, item := range reward.Items {
				if cell.Reward.Items[index] != item {
					t.Fatal("calendar changed the recorded reward item", cell.Date, cell.Reward.Items)
				}
			}
			delete(expected, cell.Date)
		}
	}
	if len(expected) != 0 {
		t.Fatal("calendar omitted expected reward dates", expected)
	}
	if reward := nextPetCheckInMilestone(state); reward == nil || reward.CycleDay != 14 {
		t.Fatal("current one-time milestone is unavailable", reward)
	}
	completed := state
	completed.NextCycleDay = 15
	if reward := nextPetCheckInMilestone(completed); reward != nil {
		t.Fatal("completed one-time track repeated a milestone", reward)
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := &PetView{Locale: locale, Client: &client.PetController{Store: petParityStore()}}
		tester := ui.NewTester(func(ctx *ui.Context) { view.calendar(ctx, state) }, 850, 1000)
		for _, reward := range []domain.PetRewardItem{item, milestone.Items[0]} {
			label := fmt.Sprintf("%s ×%d", view.itemName(reward.ItemKey), reward.Quantity)
			if _, ok := tester.Access(label); !ok {
				t.Fatal("calendar history or milestone item has no localized accessible name", locale, label)
			}
		}
		if today, ok := tester.AccessByRole(ui.RoleGroup, state.CheckInDate); !ok || today.Current != ui.CurrentDate {
			t.Fatal("calendar does not identify today to assistive technology", locale, today)
		}
	}
}

func TestPetCheckInProjectionMatchesRepositoryAfterDay14(t *testing.T) {
	ctx := context.Background()
	repository, _, err := jobdb.OpenPrepared(ctx, filepath.Join(t.TempDir(), "liberty.sqlite3"), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repository.Close(); err != nil {
			t.Error(err)
		}
	})
	start := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.Local)
	results := make([]domain.PetDailyCheckInClaimResult, 31)
	for index := range results {
		result, err := repository.PetClaimCheckIn(ctx, start.AddDate(0, 0, index).Format(time.RFC3339Nano))
		if err != nil {
			t.Fatal(err)
		}
		day := int64(index + 1)
		if result.Duplicate || result.Entry.StreakCount != day || result.Entry.CycleDay != day {
			t.Fatalf("repository did not advance to absolute day %d: %+v", day, result.Entry)
		}
		if day > 14 && (result.Entry.RewardLP != 20 || result.Entry.GrowthValue != 5 || len(result.Entry.RewardItems) != 0) {
			t.Fatalf("repository repeated a one-time milestone on day %d: %+v", day, result.Entry)
		}
		results[index] = result
	}
	t.Run("calendar", func(t *testing.T) {
		for _, anchorDay := range []int{14, 15, 29} {
			state := results[anchorDay-1].State
			for _, result := range results[anchorDay:] {
				entry := result.Entry
				month, err := time.Parse(time.DateOnly, entry.CheckInDate)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, cell := range petCalendarCells(state, month) {
					if cell.Date != entry.CheckInDate {
						continue
					}
					found = true
					reward := cell.Reward
					if cell.Status != "future" || reward == nil || reward.CycleDay != entry.CycleDay || reward.RewardLP != entry.RewardLP || reward.GrowthValue != entry.GrowthValue || len(reward.Items) != 0 {
						t.Errorf("anchor day %d projected %s differently from repository day %d: status=%s reward=%+v", anchorDay, entry.CheckInDate, entry.CycleDay, cell.Status, reward)
					}
				}
				if !found {
					t.Errorf("anchor day %d omitted calendar date %s", anchorDay, entry.CheckInDate)
				}
			}
		}
	})
	t.Run("milestones", func(t *testing.T) {
		for _, sample := range []struct{ day, target int }{{1, 3}, {3, 3}, {4, 5}, {14, 14}, {15, 0}, {17, 0}, {28, 0}, {29, 0}, {31, 0}} {
			reward := nextPetCheckInMilestone(results[sample.day-1].State)
			if sample.target == 0 {
				if reward != nil {
					t.Errorf("absolute day %d has no remaining milestone, got %+v", sample.day, reward)
				}
			} else if reward == nil || reward.CycleDay != int64(sample.target) || len(reward.Items) == 0 {
				t.Errorf("absolute day %d lost milestone %d: %+v", sample.day, sample.target, reward)
			}
		}
	})
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			state := results[14].State
			view := &PetView{Locale: locale, Route: "/daily-check-in", Client: &client.PetController{Store: &state.StoreState, Profile: &state.StoreState.Profile, CheckIn: &state}}
			tester := ui.NewTester(view.View, 1100, 1400)
			if tester.HasText(view.text("下一个里程碑", "Next Milestone")) {
				t.Error("completed one-time track still promises another milestone")
			}
			if !tester.HasText(view.text("第 17 天", "Day 17")) || !tester.HasText("+20 LP · +5 XP") {
				t.Error("calendar or today package lost the absolute day or base reward")
			}
			history := "+60 LP · +5 XP · " + view.itemName("sprout-badge")
			if _, ok := tester.AccessByRole(ui.RoleText, history); !ok {
				t.Error("completed milestone reward lost its localized accessible history name", history)
			}
		})
	}
}

func TestPetRefreshFailureDoesNotSuggestRepeatingCommittedOperation(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		view := &PetView{Locale: locale, Client: &client.PetController{Store: petParityStore(), Error: errors.New("synthetic refresh failure"), ErrorIsRefresh: true}}
		tester := ui.NewTester(view.View, 900, 700)
		if _, ok := tester.AccessByRole(ui.RoleAlert, view.errorMessage()); !ok || tester.HasText(petError(locale, view.Client.Error)) {
			t.Fatal("follow-up read failure is still presented as a failed purchase")
		}
	}
}

func TestPetManagementReducedMotionKeepsSpriteStill(t *testing.T) {
	view := &PetView{}
	tester := ui.NewTester(func(ctx *ui.Context) {
		ui.Image(ctx, view.companionFrame(ctx, "slack")).Size(200, 200)
	}, 240, 240)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	first := bytes.Clone(tester.Image().Pix)
	tester.Advance(1200 * time.Millisecond)
	if !bytes.Equal(first, tester.Image().Pix) {
		t.Fatal("reduced motion still advances the companion sprite")
	}
}

type petFormPort struct {
	client.PetService
	purchased int64
	name      string
}

func (port *petFormPort) Store(context.Context) (domain.PetStoreState, error) {
	return domain.PetStoreState{}, errors.New("synthetic follow-up failure")
}
func (port *petFormPort) Purchase(_ context.Context, _ string, quantity int64) (domain.PetStoreState, error) {
	port.purchased = quantity
	return *petParityStore(), nil
}
func (port *petFormPort) SaveName(_ context.Context, name string) (domain.PetProfile, error) {
	port.name = name
	return domain.PetProfile{Name: name}, nil
}

func TestPetPurchaseShowsTotalValidatesQuantityAndSubmitsWithEnter(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		port := &petFormPort{}
		callbacks := make(chan func(), 2)
		controller := client.NewPet(port, func(callback func()) { callbacks <- callback }, func() {})
		defer controller.Close()
		item := domain.PetStoreCatalogItem{ItemKey: "synthetic-food", NameZh: "测试食物", NameEn: "Test Food", Slot: "consumable", ItemType: "food", PriceLP: 7, GrowthValue: 4, AssetKey: "apple"}
		controller.Store = &domain.PetStoreState{Wallet: domain.PetWallet{Balance: 21}, Catalog: []domain.PetStoreCatalogItemState{{Item: item, Purchasable: true}}}
		view := &PetView{Locale: locale, Client: controller}
		view.openDialog("purchase", item.ItemKey)
		tester := ui.NewTester(view.View, 900, 700)
		view.quantity = "invalid"
		tester.Frame()
		if _, ok := tester.AccessByRole(ui.RoleAlert, view.text("请输入 1 至 3 的整数。", "Enter a whole number from 1 to 3.")); !ok {
			t.Fatal("quantity validation is not announced")
		}
		view.quantity = "3"
		tester.Frame()
		if !tester.HasText(view.text("合计", "Total")) || !tester.HasText("21 LP") {
			t.Fatal("purchase total is missing")
		}
		if err := tester.Click(view.text("数量", "Quantity")); err != nil {
			t.Fatal(err)
		}
		tester.Key(0, ui.KeyEnter)
		flushView(t, callbacks, tester)
		if port.purchased != 3 || view.dialogOpen {
			t.Fatal("Enter failed to confirm the selected quantity")
		}
	}
}

func TestPetNameDialogSupportsEscapeEnterAndNameLimit(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		port := &petFormPort{}
		callbacks := make(chan func(), 2)
		controller := client.NewPet(port, func(callback func()) { callbacks <- callback }, func() {})
		defer controller.Close()
		controller.Store = petParityStore()
		view := &PetView{Locale: locale, Client: controller}
		view.openDialog("name", "")
		tester := ui.NewTester(view.View, 900, 700)
		if !tester.Focused(view.text("宠物名字", "Pet name")) {
			t.Fatal("name dialog did not focus its input")
		}
		tester.Key(0, ui.KeyEscape)
		if view.dialogOpen || port.name != "" {
			t.Fatal("Escape did not cancel without saving")
		}
		view.openDialog("name", "")
		tester.Frame()
		tester.Type("Synthetic companion name exceeding twenty four characters")
		tester.Key(0, ui.KeyEnter)
		flushView(t, callbacks, tester)
		if port.name == "" || len([]rune(port.name)) > 24 || view.dialogOpen {
			t.Fatal("Enter or the name length limit failed")
		}
	}
}

func TestPetInventoryPreservesCheckInAndFarmNavigation(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		store := petParityStore()
		for _, item := range domain.PetCatalog() {
			if item.ItemKey == domain.PetMakeupTicket || item.ItemKey == "wheat-seed" {
				store.Inventory = append(store.Inventory, domain.PetInventoryItem{ItemKey: item.ItemKey, ItemType: item.ItemType, Slot: item.Slot, Quantity: 2})
			}
		}
		if len(store.Inventory) != 2 {
			t.Fatal("navigation fixtures no longer match the catalog")
		}
		var destination string
		view := &PetView{Locale: locale, Route: "/pet-store", Client: &client.PetController{Store: store}, Navigate: func(path string) { destination = path }}
		tester := ui.NewTester(view.View, 960, 900)
		if err := tester.Click(view.text("个人仓库", "Inventory")); err != nil {
			t.Fatal(err)
		}
		for _, sample := range []struct{ key, zh, en, path string }{{domain.PetMakeupTicket, "去签到页使用", "Use in Check-in", "/daily-check-in"}, {"wheat-seed", "去农场", "Go Farm", "/farm"}} {
			label := view.text(sample.zh, sample.en) + ": " + view.itemName(sample.key)
			if err := tester.Click(label); err != nil || destination != sample.path {
				t.Fatal("inventory action did not use the existing route", label, destination, err)
			}
		}
	}
}

func TestPetBlindBoxLayoutAndHistoryCatalogAcrossDates(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1440, 960} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				catalogArchive := domain.PetStoreCatalogItem{ItemKey: "archive-prize", ItemType: "badge", Rarity: "rare", AssetKey: "apple", NameZh: "目录中的历史商品", NameEn: "Catalog History Item"}
				poolArchive := domain.PetStoreCatalogItem{ItemKey: "archive-prize", ItemType: "badge", Rarity: "familiar", AssetKey: "gift_box", NameZh: "奖池回退名称", NameEn: "Pool Fallback Name", DescriptionZh: "旧奖池描述。", DescriptionEn: "Archived pool description."}
				poolFallback := domain.PetStoreCatalogItem{ItemKey: "pool-history", ItemType: "food", Rarity: "familiar", AssetKey: "apple", NameZh: "奖池历史商品", NameEn: "Pool History Item"}
				todayPrize := domain.PetStoreCatalogItem{ItemKey: "today-prize", ItemType: "food", Rarity: "familiar", AssetKey: "apple", NameZh: "今日苹果奖励", NameEn: "Today's Apple Reward", DescriptionZh: "补充伙伴活力的中文说明。", DescriptionEn: "English description restores companion energy."}
				state := &domain.PetBlindBoxState{
					DrawDate: "2026-11-19", DailyLimit: 10, UsedToday: 1, RemainingToday: 9,
					Pool: []domain.PetBlindBoxPoolItem{{Item: todayPrize}, {Item: poolArchive}, {Item: poolFallback}},
					History: []domain.PetBlindBoxDrawEntry{
						{ID: "today", ItemKey: "today-prize", ItemType: "food", Quantity: 1, DrawDate: "2026-11-19", CreatedAt: "2026-11-19T09:00:00+08:00"},
						{ID: "catalog-history", ItemKey: "archive-prize", ItemType: "badge", Quantity: 1, DrawDate: "2026-11-18", CreatedAt: "2026-11-18T12:00:00+08:00"},
						{ID: "pool-history", ItemKey: "pool-history", ItemType: "food", Quantity: 2, DrawDate: "2026-11-17", CreatedAt: "2026-11-17T12:00:00+08:00"},
					},
				}
				store := &domain.PetStoreState{Catalog: []domain.PetStoreCatalogItemState{{Item: catalogArchive}}}
				view := &PetView{Locale: locale, Client: &client.PetController{Store: store, BlindBox: state}}
				catalogItem, ok := view.blindBoxHistoryItem(state, "archive-prize")
				if !ok || catalogItem.NameZh != catalogArchive.NameZh || catalogItem.NameEn != catalogArchive.NameEn {
					t.Fatal("historical item did not prefer the current store catalog", catalogItem, ok)
				}
				fallbackItem, ok := view.blindBoxHistoryItem(state, "pool-history")
				if !ok || fallbackItem.NameZh != poolFallback.NameZh || fallbackItem.NameEn != poolFallback.NameEn {
					t.Fatal("historical item did not fall back to the blind-box pool", fallbackItem, ok)
				}

				tester := ui.NewTester(func(ctx *ui.Context) {
					ui.Scroll(ctx).Fill().Children(func() { view.blindBox(ctx) })
				}, width, 1800)
				today, hasToday := tester.Find(view.text("今日抽取结果", "Today Results"))
				pool, hasPool := tester.Find(view.text("完整奖池", "Full Prize Pool"))
				rules, hasRules := tester.Find(view.text("规则", "Rules"))
				recent, hasRecent := tester.Find(view.text("最近开启", "Recent Opens"))
				if !hasToday || !hasPool || !hasRules || !hasRecent || today.Y >= pool.Y {
					t.Fatal("blind-box content sections are missing or incorrectly ordered", today, pool, rules, recent)
				}
				if !tester.HasText("1/10") {
					t.Fatal("today count includes draws from earlier dates")
				}
				if !tester.HasText(view.text(todayPrize.DescriptionZh, todayPrize.DescriptionEn)) || tester.HasText(view.text(todayPrize.DescriptionEn, todayPrize.DescriptionZh)) {
					t.Fatal("full prize pool does not show the selected locale's item description", locale)
				}
				if !tester.HasText(view.text(catalogArchive.NameZh, catalogArchive.NameEn)) || tester.HasText("archive-prize") {
					t.Fatal("historical catalog item lost its localized display name", locale)
				}
				if !tester.HasText(view.text(catalogArchive.NameZh, catalogArchive.NameEn)) {
					t.Fatal("previous-date item is missing from recent history", locale)
				}
				if width > 1080 {
					if today.X >= rules.X || pool.X >= rules.X || rules.X > recent.X {
						t.Fatal("wide layout does not keep draw/results/pool left and rules/history right", today, pool, rules, recent)
					}
				} else if pool.Y >= rules.Y || rules.Y >= recent.Y {
					t.Fatal("narrow layout does not stack the sidebar after the main column", pool, rules, recent)
				}
			})
		}
	}
}

func TestStoreItemDailyLimitAndRepeatPurchaseUseActualState(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		labels := &PetView{Locale: locale}
		state := domain.PetStoreCatalogItemState{Item: domain.PetStoreCatalogItem{ItemKey: domain.PetGiftBox, NameZh: "礼盒", NameEn: "Gift Box", AssetKey: "gift_box"}, Owned: true, Status: "daily_limit"}
		preferences := domain.DefaultUIPreferences()
		preferences.Locale = locale
		view := &StoreItemView{item: &state, preferences: preferences}
		tester := ui.NewTester(view.View, 920, 900)
		if !tester.HasText(labels.text("今日已领完", "Claimed")) {
			t.Fatal("detail window omitted the daily limit")
		}
		state.Status, state.Purchasable = "owned", true
		if labels.purchaseLabel(state) != labels.text("购买", "Purchase") {
			t.Fatal("owned consumables can no longer be repurchased")
		}
	}
}

func petTabTo(t *testing.T, tester *ui.Tester, label string, width, height int) ui.AccessInfo {
	t.Helper()
	for attempt := 0; attempt < 60; attempt++ {
		tester.Key(0, ui.KeyTab)
		for _, control := range tester.AccessAll() {
			if control.Name == label && control.Focused {
				if !control.Focusable || control.Offscreen || control.Bounds.W < 20 || control.Bounds.H < 10 || control.Bounds.X < 0 || control.Bounds.Y < 0 || control.Bounds.X+control.Bounds.W > float32(width) || control.Bounds.Y+control.Bounds.H > float32(height) {
					t.Fatal("Tab reached a clipped pet control", control)
				}
				return control
			}
		}
	}
	t.Fatal("Tab could not reach the pet control", label)
	return ui.AccessInfo{}
}

func TestPetManagementLongNamesKeepPointerAndKeyboardEditing(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1100, 1440} {
			for _, name := range []string{strings.Repeat("W", 24), strings.Repeat("长", 24)} {
				t.Run(fmt.Sprintf("%s-%d-%c", locale, width, []rune(name)[0]), func(t *testing.T) {
					store := petParityStore()
					store.Profile.Name = name
					view := &PetView{Locale: locale, Route: "/pet", Client: &client.PetController{Store: store, Profile: &store.Profile}, DesktopAvailable: true}
					shell := &Shell{Locale: locale, Route: view.Route, Preferences: domain.DefaultUIPreferences(), Content: view.View}
					tester := ui.NewTesterWith(shell.View, width, 760, ui.TesterOptions{Dark: locale == "en-US"})
					edit := view.text("修改名字", "Edit Name")
					if err := tester.Click(edit); err != nil {
						t.Fatal("valid long name obstructs its edit action", err)
					}
					input, ok := tester.AccessByRole(ui.RoleTextField, view.text("宠物名字", "Pet name"))
					if !ok || !input.Focused || input.Value != name {
						t.Fatal("name editor lost focus or the full stored name", input)
					}
					tester.Command("selectAll")
					tester.Command("copy")
					if tester.Clipboard() != name {
						t.Fatal("keyboard copy truncated the pet name")
					}
					tester.Key(0, ui.KeyEscape)
					if view.dialogOpen || store.Profile.Name != name {
						t.Fatal("Escape failed to cancel without changing the name")
					}
					text, ok := tester.AccessByRole(ui.RoleText, name)
					button, buttonOK := tester.AccessByRole(ui.RoleButton, edit)
					if !ok || !buttonOK || text.Offscreen || text.Bounds.X < 252 || text.Bounds.X+text.Bounds.W > button.Bounds.X || text.Bounds.H > 30 {
						t.Fatal("full accessible name is missing or its visual row overlaps the action", text, button)
					}
					if width == 1100 && name == strings.Repeat("W", 24) {
						captureParity(t, tester, "pet-management-long-name-"+locale)
					}
					petTabTo(t, tester, edit, width, 760)
					tester.Key(0, ui.KeyEnter)
					if !view.dialogOpen || !tester.Focused(view.text("宠物名字", "Pet name")) {
						t.Fatal("keyboard activation did not focus the name editor")
					}
					tester.Key(0, ui.KeyEscape)
					if view.dialogOpen || store.Profile.Name != name {
						t.Fatal("keyboard cancellation changed the name or kept the dialog open")
					}
					petTabTo(t, tester, edit, width, 760)
				})
			}
		}
	}
}

func TestPetCheckInPreservesHistoryRewardsAndOverviewValues(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, milestone := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s-milestone-%t", locale, milestone), func(t *testing.T) {
				store := petParityStore()
				store.Inventory = []domain.PetInventoryItem{{ItemKey: domain.PetGiftBox, Quantity: 20}, {ItemKey: domain.PetMakeupTicket, Quantity: 3}}
				state := &domain.PetDailyCheckInState{CheckInDate: "2026-11-20", CurrentStreak: 4, CycleLength: 14, NextCycleDay: 5, MakeupTicketQuantity: 3, StoreState: *store,
					TodayReward: domain.PetDailyCheckInRewardPreview{CycleDay: 5, RewardLP: 20, GrowthValue: 5},
					History:     []domain.PetDailyCheckInEntry{{ID: "synthetic-check-in", CheckInDate: "2026-10-31", StreakCount: 12, CycleDay: 12, RewardLP: 60, GrowthValue: 8, RewardItems: []domain.PetRewardItem{{ItemKey: domain.PetGiftBox, Quantity: 1}}}},
				}
				target := int64(14)
				if milestone {
					target = 7
					state.Rewards = []domain.PetDailyCheckInRewardPreview{{CycleDay: 7, RewardLP: 40, GrowthValue: 5, Items: []domain.PetRewardItem{{ItemKey: domain.PetMakeupTicket, Quantity: 2}}}}
				}
				view := &PetView{Locale: locale, Route: "/daily-check-in", Client: &client.PetController{Store: store, Profile: &store.Profile, CheckIn: state}}
				shell := &Shell{Locale: locale, Route: view.Route, Preferences: domain.DefaultUIPreferences(), Content: view.View}
				tester := ui.NewTesterWith(shell.View, 1100, 1400, ui.TesterOptions{Dark: locale == "en-US"})
				for _, label := range []string{view.text("连续 12 天", "Streak 12"), "+60 LP · +8 XP · " + view.itemName(domain.PetGiftBox)} {
					if !tester.HasText(label) {
						t.Error("check-in history lost its own streak or reward item", label)
					}
				}
				for label, value := range map[string]string{view.text("仓库物品", "Inventory"): "2", view.text("特别奖励", "Special Reward"): fmt.Sprintf("4/%d", target)} {
					row, ok := tester.Find(label)
					found := false
					for _, control := range tester.AccessAll() {
						if control.Name == value && control.Bounds.X > row.X && math.Abs(float64(control.Bounds.Y-row.Y)) < 3 {
							found = true
						}
					}
					if !ok || !found {
						t.Error("overview lost the server snapshot's value", label, value)
					}
				}
				if milestone {
					tester.SetSize(1100, 760)
					captureParity(t, tester, "pet-check-in-overview-"+locale)
				}
			})
		}
	}
}

type petRefreshPort struct {
	client.PetService
	store                  domain.PetStoreState
	reads, redeems, claims int
	actionError, readError error
	checkedIn              bool
}

func (port *petRefreshPort) Store(context.Context) (domain.PetStoreState, error) {
	port.reads++
	return port.store, port.readError
}
func (*petRefreshPort) Settings(context.Context) (domain.PetSettings, error) {
	return domain.PetSettings{}, nil
}
func (*petRefreshPort) Events(context.Context, int) ([]domain.PetEventLedgerEntry, error) {
	return nil, nil
}
func (*petRefreshPort) Cosmetics(context.Context) ([]domain.PetCosmeticUnlock, error) {
	return nil, nil
}
func (port *petRefreshPort) CheckIn(context.Context) (domain.PetDailyCheckInState, error) {
	return domain.PetDailyCheckInState{CheckInDate: "2026-11-20", CheckedInToday: port.checkedIn, CycleLength: 14, NextCycleDay: 1}, nil
}
func (*petRefreshPort) BlindBox(context.Context) (domain.PetBlindBoxState, error) {
	return domain.PetBlindBoxState{}, nil
}
func (*petRefreshPort) Redemptions(context.Context, int) ([]domain.PetRedeemKeyRedemption, error) {
	return nil, nil
}
func (port *petRefreshPort) Redeem(context.Context, string) (domain.PetRedeemKeyResult, error) {
	port.redeems++
	return domain.PetRedeemKeyResult{State: port.store}, port.actionError
}
func (port *petRefreshPort) ClaimCheckIn(context.Context) (domain.PetDailyCheckInClaimResult, error) {
	port.claims++
	if port.actionError == nil {
		port.checkedIn = true
	}
	return domain.PetDailyCheckInClaimResult{State: domain.PetDailyCheckInState{CheckInDate: "2026-11-20", CheckedInToday: true, CycleLength: 14, StoreState: port.store}}, port.actionError
}

func TestPetRefreshKeepsOperationAlertsAndNeverReplaysWrites(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, route := range []string{"/redeem-key", "/daily-check-in"} {
			for _, accepted := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s%s-accepted-%t", locale, route, accepted), func(t *testing.T) {
					port := &petRefreshPort{store: *petParityStore(), actionError: domain.ErrPetRedemption}
					if route == "/daily-check-in" {
						port.actionError = domain.ErrPet
					}
					if accepted {
						port.actionError, port.readError = nil, errors.New("synthetic read failure")
					}
					callbacks := make(chan func(), 4)
					controller := client.NewPet(port, func(callback func()) { callbacks <- callback }, func() {})
					defer controller.Close()
					controller.Store, controller.Profile = &port.store, &port.store.Profile
					state, _ := port.CheckIn(context.Background())
					controller.CheckIn = &state
					view := &PetView{Locale: locale, Route: route, Client: controller, key: "synthetic-invalid-key"}
					shell := &Shell{Locale: locale, Route: route, Preferences: domain.DefaultUIPreferences(), Content: view.View}
					tester := ui.NewTesterWith(shell.View, 1100, 760, ui.TesterOptions{Dark: locale == "en-US"})
					action := view.text("立即兑换", "Redeem")
					if route == "/daily-check-in" {
						action = view.text("签到领取", "Check In")
					}
					petTabTo(t, tester, action, 1100, 760)
					tester.Key(0, ui.KeyEnter)
					flushView(t, callbacks, tester)
					message := view.errorMessage()
					if _, ok := tester.AccessByRole(ui.RoleAlert, message); !ok || controller.ErrorIsRefresh != accepted {
						t.Fatal("operation and read failures are not distinguished")
					}
					if !accepted && route == "/redeem-key" {
						captureParity(t, tester, "pet-redeem-operation-error-"+locale)
					}
					if _, ok := tester.AccessByRole(ui.RoleButton, view.text("重试", "Retry")); ok {
						t.Error("read-only refresh is still named Retry")
					}
					petTabTo(t, tester, view.text("刷新", "Refresh"), 1100, 760)
					port.readError = nil
					tester.Key(0, ui.KeyEnter)
					flushView(t, callbacks, tester)
					if port.redeems+port.claims != 1 || port.reads != 2 {
						t.Fatal("refresh replayed a write or skipped reading", port.redeems, port.claims, port.reads)
					}
					if accepted {
						if controller.Error != nil || controller.ActionRevision != 1 {
							t.Fatal("successful refresh did not clear only the read error")
						}
					} else if _, ok := tester.AccessByRole(ui.RoleAlert, message); !ok || controller.ErrorIsRefresh || controller.ActionRevision != 0 {
						t.Fatal("refresh erased the unresolved operation failure")
					}
				})
			}
		}
	}
}
