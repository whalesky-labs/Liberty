package desktop

import (
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func marketFixture() domain.WorkMarketState {
	return domain.WorkMarketState{Maps: []domain.WorkMap{
		{ID: "farm", NameZh: "农场种菜", NameEn: "Farm", DescriptionZh: "播种、浇水、等待成熟，收获宠物商品。", DescriptionEn: "Plant, water and harvest items for your companion.", Status: workIdle, Enabled: true, Route: "/farm", Summary: domain.WorkMapSummary{ActivePlots: 1}},
		{ID: "mine", NameZh: "矿场挖矿", NameEn: "Mine", DescriptionZh: "选择矿脉、照看矿点，收获工具和 LP。", DescriptionEn: "Choose a vein, tend the mine and collect tools and LP.", Status: workNeedsCare, Enabled: true, Route: "/work-game/mine", Summary: domain.WorkMapSummary{ActivePlots: 2, NeedsCarePlots: 1}},
		{ID: "factory", NameZh: "工厂打螺丝", NameEn: "Factory", DescriptionZh: "接装配订单，处理生产线。", DescriptionEn: "Take assembly orders and manage the production line.", Status: workClaimable, Enabled: true, Route: "/work-game/factory", Summary: domain.WorkMapSummary{ActivePlots: 3, MaturePlots: 2}},
		{ID: "convenience-store", NameZh: "便利店值班", NameEn: "Convenience Store", DescriptionZh: "完成收银、补货和清洁。", DescriptionEn: "Complete checkout, restocking and cleaning duties.", Status: "locked", Route: "/work-game/convenience-store"},
	}}
}

func captureParity(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	directory := os.Getenv("LIBERTY_UI_SCREENSHOT_DIR")
	if directory == "" {
		return
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(directory, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	encodeErr := png.Encode(file, tester.Image())
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		t.Fatalf("capture failed: %v / %v", encodeErr, closeErr)
	}
}

func TestMarketMatchesLegacyResponsiveCardsAndHeroBounds(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, width := range []int{1440, 1100, 760} {
			t.Run(fmt.Sprintf("%s-%d", locale, width), func(t *testing.T) {
				state := marketFixture()
				view := WorkMarketView{Client: &client.WorkController{MarketState: &state}, Locale: locale, Open: func(string) {}}
				shell := Shell{Locale: locale, Preferences: domain.DefaultUIPreferences(), Content: view.View}
				height := 1200
				if width == 760 {
					height = 3400
				}
				tester := ui.NewTesterWith(shell.View, width, height, ui.TesterOptions{Dark: locale == "en-US"})
				tester.Frame()
				hero, heroOK := tester.Find(workText(locale, "Work map overview", "打工地图概览"))
				art, artOK := tester.Find(workText(locale, "Niuma Market map lobby", "牛马市场地图大厅"))
				if !heroOK || !artOK || art.W <= 0 || art.H < 240 || art.X < hero.X || art.Y < hero.Y || art.X+art.W > hero.X+hero.W || art.Y+art.H > hero.Y+hero.H {
					t.Fatalf("hero art escaped its container: hero=%+v art=%+v", hero, art)
				}
				cards := make([]ui.Rect, len(state.Maps))
				for index, entry := range state.Maps {
					name := workText(locale, entry.NameEn, entry.NameZh)
					card, ok := tester.Find(name)
					if !ok || card.W <= 0 || card.X < presentation.SidebarWidth || card.X+card.W > float32(width) {
						t.Fatalf("card clipped: %s %+v", name, card)
					}
					cards[index] = card
					cover, ok := tester.Find(workText(locale, entry.NameEn+" map", entry.NameZh+"地图"))
					if !ok || math.Abs(float64(cover.W/cover.H-4.0/3)) > .02 || math.Abs(float64(cover.W-card.W)) > 1 || math.Abs(float64(cover.Y-card.Y)) > .1 || cover.Y < hero.Y+hero.H {
						t.Fatalf("map cover lost its 4:3 ratio or overlapped the hero: %+v card=%+v", cover, card)
					}
					if !tester.HasText(marketStatusLabel(locale, entry.Status)) {
						t.Fatal("map status missing", entry.Status)
					}
				}
				switch width {
				case 1440:
					if cards[0].Y != cards[3].Y || cards[0].X >= cards[3].X {
						t.Fatal("wide market must have four columns", cards)
					}
				case 1100:
					if cards[0].Y != cards[1].Y || cards[2].Y != cards[3].Y || cards[2].Y < cards[0].Y+cards[0].H || cards[0].X != cards[2].X {
						t.Fatal("compact market must have two columns", cards)
					}
				case 760:
					for index := 1; index < len(cards); index++ {
						if cards[index].X != cards[0].X || cards[index].Y < cards[index-1].Y+cards[index-1].H {
							t.Fatal("narrow market must have one column", cards)
						}
					}
				}
				tester.SetSize(width, 960)
				tester.Frame()
				captureParity(t, tester, fmt.Sprintf("market-%s-%d", locale, width))
			})
		}
	}
}

func TestMarketMapStatsShareBottomBaseline(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			state := marketFixture()
			for index := range state.Maps[:3] {
				base := int64(index * 3)
				state.Maps[index].Summary = domain.WorkMapSummary{ActivePlots: base + 1, NeedsCarePlots: base + 2, MaturePlots: base + 3}
			}
			view := &WorkMarketView{Client: &client.WorkController{MarketState: &state}, Locale: locale, Open: func(string) {}}
			tester := ui.NewTesterWith(func(context *ui.Context) {
				ui.Grid(context).FillWidth().Columns(4).Gap(14).Children(func() {
					for _, entry := range state.Maps {
						view.mapCard(context, entry)
					}
				})
			}, 1200, 600, ui.TesterOptions{Dark: locale == "en-US"})
			captureParity(t, tester, "market-map-stats-alignment-"+locale)
			baseline := float32(-1)
			for index := range state.Maps[:3] {
				label := fmt.Sprintf("%s %d", workText(locale, "Active", "进行中"), int64(index*3+1))
				stat, ok := tester.Find(label)
				if !ok {
					t.Fatal("map activity statistic is missing", label)
				}
				if baseline < 0 {
					baseline = stat.Y
				} else if math.Abs(float64(stat.Y-baseline)) > 1 {
					t.Fatalf("map statistics do not share a bottom baseline: %q y=%v, expected y=%v", label, stat.Y, baseline)
				}
			}
			unavailable, ok := tester.Find(workText(locale, "Coming soon", "筹备中"))
			if !ok || math.Abs(float64(unavailable.Y-baseline)) > 1 {
				t.Fatalf("unavailable map label does not share the statistics baseline: %+v, expected y=%v", unavailable, baseline)
			}
		})
	}
}

func TestMarketStatusDisabledAndNavigationSemantics(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		state := marketFixture()
		opened := ""
		view := WorkMarketView{Client: &client.WorkController{MarketState: &state}, Locale: locale, Open: func(path string) { opened = path }}
		tester := ui.NewTester(view.View, 1440, 1000)
		for _, entry := range state.Maps {
			name := workText(locale, entry.NameEn, entry.NameZh)
			access, ok := tester.Access(name)
			if !ok || access.Role != ui.RoleButton || access.Disabled == entry.Enabled || access.Description != marketStatusLabel(locale, entry.Status) {
				t.Fatalf("map state is not exposed to accessibility: %+v", access)
			}
			if entry.Enabled {
				if err := tester.Click(name); err != nil || opened != entry.Route {
					t.Fatalf("map opened wrong route: %s %v", opened, err)
				}
			}
		}
		if !tester.HasText(workText(locale, "Coming soon", "筹备中")) {
			t.Fatal("locked map lacks its unavailable explanation")
		}
		state.Maps[0].Route = ""
		tester.Frame()
		assertDisabled(t, tester, workText(locale, state.Maps[0].NameEn, state.Maps[0].NameZh), true)
		view.Client.Error = errors.New("synthetic private failure")
		tester.Frame()
		if tester.HasText("synthetic private failure") || !tester.HasText(workText(locale, "Could not refresh the maps. Please retry.", "地图刷新失败，请重试。")) {
			t.Fatal("market failure is missing or exposes raw diagnostic content")
		}
	}
}

func TestMarketUnavailableEntriesExplainWhyTheyCannotOpen(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, missingRoute := range []bool{false, true} {
			state := marketFixture()
			entry := &state.Maps[0]
			entry.Status = "locked"
			if missingRoute {
				entry.Status, entry.Route = workIdle, ""
			}
			opened := 0
			view := &WorkMarketView{Client: &client.WorkController{MarketState: &state}, Locale: locale, Open: func(string) { opened++ }}
			tester := ui.NewTester(view.View, 1440, 1000)
			name := workText(locale, entry.NameEn, entry.NameZh)
			access, ok := tester.AccessByRole(ui.RoleButton, name)
			if !ok || !access.Disabled || access.Description == marketStatusLabel(locale, workIdle) {
				t.Fatal("unavailable map is active or its reason is missing", access)
			}
			_ = tester.Click(name)
			if opened != 0 {
				t.Fatal("unavailable map still navigates")
			}
		}
	}
}

func TestMarketNavigationIgnoresHeldActivationKeys(t *testing.T) {
	for _, key := range []ui.Key{ui.KeyEnter, ui.KeySpace} {
		state := marketFixture()
		opened := 0
		view := &WorkMarketView{Client: &client.WorkController{MarketState: &state}, Locale: "en-US", Open: func(string) { opened++ }}
		tester := ui.NewTester(view.View, 1440, 1000)
		focusWorkControl(t, tester, state.Maps[0].NameEn)
		tester.KeyDown(key)
		tester.KeyRepeat(0, key)
		tester.KeyRepeat(0, key)
		tester.KeyUp(key)
		if opened != 1 {
			t.Fatal("held key repeatedly navigated to the map", key, opened)
		}
	}
}
