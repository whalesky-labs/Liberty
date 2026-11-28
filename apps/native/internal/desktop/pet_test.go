package desktop

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestStoreCardsExposeDetailButtonsInAccessibilityTree(t *testing.T) {
	store := &domain.PetStoreState{}
	for _, item := range domain.PetCatalog() {
		store.Catalog = append(store.Catalog, domain.PetStoreCatalogItemState{Item: item})
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		var opened string
		view := &PetView{Locale: locale, Route: "/pet-store", Client: &client.PetController{Store: store}, OpenItem: func(key string) { opened = key }}
		tester := ui.NewTester(view.View, 948, 688)
		item := store.Catalog[0].Item
		if _, ok := tester.AccessByRole(ui.RoleGroup, localized(locale, item.NameZh, item.NameEn)); !ok {
			t.Fatal("missing accessible product group", locale)
		}
		label := localized(locale, "查看详情："+item.NameZh, "View Details: "+item.NameEn)
		if _, ok := tester.AccessByRole(ui.RoleButton, label); !ok {
			t.Fatal("missing accessible detail button", locale)
		}
		if err := tester.Click(label); err != nil || opened != item.ItemKey {
			t.Fatal("detail button did not open the selected item", err)
		}
	}
}

func TestStoreVirtualizesRowsAndCanReachLastProduct(t *testing.T) {
	store := &domain.PetStoreState{}
	for _, item := range domain.PetCatalog() {
		store.Catalog = append(store.Catalog, domain.PetStoreCatalogItemState{Item: item})
	}
	view := &PetView{Locale: "en-US", Route: "/pet-store", Client: &client.PetController{Store: store}, OpenItem: func(string) {}}
	shell := &Shell{Locale: "en-US", Preferences: domain.DefaultUIPreferences(), Content: view.View}
	tester := ui.NewTester(shell.View, 1100, 900)
	tester.Frame()
	first, last := store.Catalog[0].Item, store.Catalog[len(store.Catalog)-1].Item
	if _, ok := tester.AccessByRole(ui.RoleGroup, first.NameEn); !ok {
		t.Fatal("first product missing")
	}
	if _, ok := tester.AccessByRole(ui.RoleGroup, last.NameEn); ok {
		t.Fatal("offscreen products were eagerly constructed")
	}
	view.storeGrid.ScrollToEnd()
	tester.Frame()
	tester.Frame()
	if _, ok := tester.AccessByRole(ui.RoleGroup, last.NameEn); !ok {
		t.Fatal("last product unreachable")
	}
}

func TestStoreItemWindowReadsOnlySelectedItemInBothLocales(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		preferences := domain.DefaultUIPreferences()
		preferences.Locale = locale
		view := &StoreItemView{key: "test", preferences: preferences}
		store := &domain.PetStoreState{Catalog: []domain.PetStoreCatalogItemState{{Item: domain.PetStoreCatalogItem{ItemKey: "test", NameZh: "验收商品", NameEn: "Test Item", ItemType: "food", AssetKey: "apple"}, Owned: true, Quantity: 7}}, Inventory: []domain.PetInventoryItem{{ItemKey: "test", Source: "daily_check_in"}}}
		view.selectItem(store)
		tester := ui.NewTester(view.View, 920, 640)
		for _, label := range []string{localized(locale, "验收商品", "Test Item"), localized(locale, "已拥有", "Owned"), localized(locale, "免费", "Free"), localized(locale, "每日签到", "Daily Check-in"), "7"} {
			if !tester.HasText(label) {
				t.Fatalf("%s missing %q", locale, label)
			}
		}
		if tester.HasText(localized(locale, "确认购买", "Confirm Purchase")) {
			t.Fatal("item reader displayed a purchase action")
		}
		for _, width := range []int{920, 760} {
			tester.SetSize(width, 640)
			first, hasFirst := tester.Find(localized(locale, "分类", "Type"))
			second, hasSecond := tester.Find(localized(locale, "羁绊阶梯", "Rarity"))
			if !hasFirst || !hasSecond || width > 760 && (first.Y != second.Y || first.X >= second.X) || width <= 760 && (first.X != second.X || first.Y >= second.Y) {
				t.Fatalf("%s at %d: unexpected detail grid %v, %v", locale, width, first, second)
			}
		}
		view.key = "missing"
		view.selectItem(store)
		tester.Frame()
		if !tester.HasText(localized(locale, "无法找到该商品详情。", "Item details are unavailable.")) || tester.HasText("7") {
			t.Fatal("previous item leaked into missing selection")
		}
	}
}

func TestStoreItemPreviewFlipsOnHoverAndKeyboardFocus(t *testing.T) {
	item := domain.PetCatalog()[0]
	view := &StoreItemView{item: &domain.PetStoreCatalogItemState{Item: item}}
	labels := &PetView{Locale: "en-US"}
	tester := ui.NewTester(func(ctx *ui.Context) {
		ui.Column(ctx).Children(func() {
			view.preview(ctx, labels, presentation.Colors(false, ctx.Theme().Accent), "31 LP", 180)
		})
	}, 320, 280)
	card, ok := tester.Find("3D item preview")
	if !ok || card.W != card.H || !tester.HasText("Hover or focus") || tester.HasText("31 LP") {
		t.Fatal("missing square front face or instructions")
	}
	front := bytes.Clone(tester.Image().Pix)
	tester.Move(card.X+card.W/2, card.Y+card.H/2)
	tester.Advance(75 * time.Millisecond)
	middle := bytes.Clone(tester.Image().Pix)
	tester.Advance(720 * time.Millisecond)
	if !tester.HasText("31 LP") || bytes.Equal(front, middle) || bytes.Equal(middle, tester.Image().Pix) {
		t.Fatal("expected a rendered intermediate frame before the back face")
	}
	tester.Move(310, 260)
	tester.Advance(750 * time.Millisecond)
	if tester.HasText("31 LP") {
		t.Fatal("leaving did not restore the front face")
	}
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Key(0, ui.KeyTab)
	if !tester.Focused("3D item preview") || !tester.HasText("31 LP") {
		t.Fatal("keyboard focus with reduced motion must show the back immediately")
	}
}

func TestPetAnimationReadsWorkflowSourceFromLedger(t *testing.T) {
	now := time.UnixMilli(1700000000000)
	for source, want := range map[string]string{"job_created": "drive", "transcription_started": "work", "transcription_completed": "snow", "ai_summary_completed": "snow", "export_completed": "snow", "daily_open": "slack"} {
		events := []domain.PetEventLedgerEntry{{EventType: "workflow", EventSource: source, EventTime: now.Format(time.RFC3339Nano)}}
		if got := petVisualAction("", "sleepy", events, now); got != want {
			t.Errorf("%s: got %s, want %s", source, got, want)
		}
	}
}

func TestPetEnvironmentFollowsActiveQueueStatus(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		want   string
	}{
		{name: "queued", status: "queued", want: "queued"},
		{name: "transcribing", status: "transcribing", want: "transcribing"},
		{name: "speaker processing", status: "speaker_processing", want: "speaker_processing"},
		{name: "summarizing", status: "summarizing", want: "summarizing"},
		{name: "completed", status: "completed", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			jobs := []domain.JobSummary{{OverallStatus: test.status}}
			if got := petEnvironment(jobs); got != test.want {
				t.Fatalf("pet environment = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWorkspacePublishesQueueEnvironmentToPet(t *testing.T) {
	workspace := &Workspace{
		controller: &client.Controller{State: client.State{Jobs: []domain.JobSummary{{OverallStatus: "summarizing"}}}},
		pet:        &PetView{},
	}
	workspace.onJobsChanged()
	if workspace.pet.Environment != "summarizing" {
		t.Fatalf("pet environment = %q, want summarizing", workspace.pet.Environment)
	}

	workspace.controller.Jobs = []domain.JobSummary{{OverallStatus: "completed"}}
	workspace.onJobsChanged()
	if workspace.pet.Environment != "" {
		t.Fatalf("finished queue left pet environment %q", workspace.pet.Environment)
	}
}

func TestBlindBoxUsesPoolArtAndConvergesInDepthOrder(t *testing.T) {
	pool := []domain.PetBlindBoxPoolItem{{Item: domain.PetStoreCatalogItem{AssetKey: "apple"}}, {Item: domain.PetStoreCatalogItem{AssetKey: "gift_box"}}}
	cards := orbitCards(pool, 4, 1, 1)
	if len(cards) != 2 || cards[0].z > cards[1].z {
		t.Fatal("prize card depth order")
	}
	seen := map[string]bool{}
	for _, card := range cards {
		seen[card.asset] = true
		if math.Hypot(card.x, card.z) > .34 || math.Abs(card.y) > .001 {
			t.Fatal("draw did not converge")
		}
	}
	if !seen["apple"] || !seen["gift_box"] {
		t.Fatal("missing pool art")
	}
}

func TestBlindBoxStagePositionsButtonAndRendersDrawPhases(t *testing.T) {
	item, ok := domain.FindPetItem("bookshelf-scene")
	if !ok {
		t.Fatal("missing catalog fixture")
	}
	state := &domain.PetBlindBoxState{DailyLimit: 10, RemainingToday: 1, Pool: []domain.PetBlindBoxPoolItem{{Item: item}}}
	view := &PetView{Locale: "en-US", Client: &client.PetController{BlindBox: state}}
	tester := ui.NewTester(func(ctx *ui.Context) {
		ui.Scroll(ctx).Fill().Children(func() { view.blindBox(ctx) })
	}, 820, 720)
	stage, hasStage := tester.Find("Companion blind box")
	control, hasButton := tester.AccessByRole(ui.RoleButton, "Start Draw")
	button := control.Bounds
	if !hasStage || !hasButton || stage.H != 430 || math.Abs(float64(button.X+button.W/2-stage.X-stage.W/2)) > 1 || math.Abs(float64(stage.Y+stage.H-button.Y-button.H-30)) > 1 {
		t.Fatalf("unexpected draw control placement: stage %v button %v", stage, button)
	}
	idle := bytes.Clone(tester.Image().Pix)
	view.Client.Busy = true
	view.drawStart = tester.Now().Add(-4950 * time.Millisecond)
	tester.Frame()
	if !tester.HasText("Drawing") || tester.HasText("Latest Reward") || bytes.Equal(idle, tester.Image().Pix) {
		t.Fatal("last draw phase must remain visible until settlement")
	}
	view.Client.LastDraw = &domain.PetBlindBoxDrawResult{Prize: item}
	view.Client.Busy = false
	state.RemainingToday = 0
	tester.SetPreferences(ui.Preferences{ReduceMotion: true})
	tester.Frame()
	if !tester.HasText("No Attempts Left") || tester.HasText("Latest Reward") || !tester.HasText(item.NameEn) {
		t.Fatal("missing settled scene or daily limit, or result text is still inline")
	}
	settled := bytes.Clone(tester.Image().Pix)
	tester.Advance(200 * time.Millisecond)
	if !bytes.Equal(settled, tester.Image().Pix) {
		t.Fatal("reduced motion must leave the settled scene still")
	}
}
