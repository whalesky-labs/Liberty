package desktop

import (
	"fmt"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func assertDefaultSegmentedHeight(t *testing.T, tester *ui.Tester, label string, themes ...ui.Theme) {
	t.Helper()
	selected := 0
	reference := ui.NewTester(func(c *ui.Context) {
		render := func() { ui.Segmented(c, &selected, "First", "Second").Label("Reference") }
		if len(themes) > 0 {
			ui.Themed(c, &themes[0]).Children(render)
		} else {
			render()
		}
	}, 600, 200)
	want, _ := reference.Find("Reference")
	got, ok := tester.AccessByRole(ui.RoleRadioGroup, label)
	if !ok || got.Bounds.H != want.H {
		t.Fatalf("%s height = %v, want MyGo default %v", label, got.Bounds.H, want.H)
	}
}

func TestStoreSegmentedFiltersPreserveCategoryAndInventory(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			store := &domain.PetStoreState{}
			for _, item := range domain.PetCatalog() {
				store.Catalog = append(store.Catalog, domain.PetStoreCatalogItemState{Item: item})
			}
			view := &PetView{Locale: locale, Route: "/pet-store", Client: &client.PetController{Store: store}}
			tester := ui.NewTester(view.View, 760, 900)
			assertDefaultSegmentedHeight(t, tester, view.text("商店内容", "Store content"))
			assertDefaultSegmentedHeight(t, tester, view.text("商品分类", "Product category"))
			captureParity(t, tester, "segmented-store-"+locale)
			if err := tester.Click(view.typeName("all")); err != nil {
				t.Fatal(err)
			}
			tester.Key(0, ui.KeyEnd)
			if view.category != "badge" || !tester.Focused(view.typeName("badge")) {
				t.Fatal("last category cannot be selected and focused")
			}
			if err := tester.Click(view.text("个人仓库", "Inventory")); err != nil {
				t.Fatal(err)
			}
			if view.section != "inventory" || view.category != "badge" || !tester.HasText(view.text("当前分类暂无内容。", "No items in this category yet.")) {
				t.Fatal("inventory selection lost the current filter")
			}
		})
	}
}

func TestRuntimeSegmentedFilterSelectsComponentLogs(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		t.Run(locale, func(t *testing.T) {
			view := &RuntimeView{Locale: locale, logsExpanded: true}
			panel := domain.RuntimePanel{Logs: map[string]string{"python": "synthetic python log", "ffmpeg": "synthetic ffmpeg log", "model": "synthetic model log"}}
			tester := ui.NewTester(func(c *ui.Context) { view.installLogs(c, panel) }, 760, 500)
			assertDefaultSegmentedHeight(t, tester, localized(locale, "日志组件", "Log component"))
			captureParity(t, tester, "segmented-runtime-"+locale)
			for _, component := range []string{"python", "ffmpeg", "model"} {
				if err := tester.Click(view.msg(runtimeNameKey(component))); err != nil {
					t.Fatal(err)
				}
				if view.logComponent != component || !tester.HasText("synthetic "+component+" log") {
					t.Fatal("component selection did not update logs", component)
				}
			}
		})
	}
}

func TestQueueSegmentedFiltersAndNumberedPages(t *testing.T) {
	callbacks := make(chan func(), 32)
	controller := client.New(viewMeetings{jobs: queueParityJobs("local", 80)}, func(callback func()) { callbacks <- callback }, func() {})
	t.Cleanup(controller.Close)
	view := NewMeetings(controller, "en-US")
	tester := ui.NewTester(view.View, 1100, 900)
	view.refresh()
	flushView(t, callbacks, tester)
	assertDefaultSegmentedHeight(t, tester, view.msg("filterLabel"))
	assertDefaultSegmentedHeight(t, tester, view.msg("paginationLabel"))
	captureParity(t, tester, "segmented-queue")
	for _, page := range []int{3, 5, 7, 8, 6} {
		if err := tester.Click(fmt.Sprint(page)); err != nil {
			t.Fatal(err)
		}
		flushView(t, callbacks, tester)
		if controller.Page.Page != page || view.page != page {
			t.Fatal("numbered page selected an incorrect offset", controller.Page.Page, page)
		}
	}
	if err := tester.Click(fmt.Sprintf("%s %d", view.msg("filterCompleted"), controller.Page.Counts.Completed)); err != nil {
		t.Fatal(err)
	}
	flushView(t, callbacks, tester)
	if view.status != "completed" || controller.Page.Page != 1 {
		t.Fatal("filter change did not reset pagination")
	}
}

func TestResultSegmentedFilterRecoversWhenSpeakerIsRemoved(t *testing.T) {
	view := resultParityView("en-US", 4)
	tester := ui.NewTester(view.View, 960, 720)
	if err := tester.Click("Speaker 0 2"); err != nil {
		t.Fatal(err)
	}
	if view.speaker != "Speaker 0" {
		t.Fatal("speaker selection did not apply")
	}
	next := *view.Client.Result
	next.Speakers = slices.Clone(next.Speakers)
	for i := range next.Speakers {
		label := "Renamed speaker"
		next.Speakers[i].Speaker = &label
	}
	view.Client.Result = &next
	tester.Frame()
	all, ok := tester.AccessByRole(ui.RoleRadio, fmt.Sprintf("%s 4", view.msg("allSpeakers")))
	if !ok || !all.Checked || view.speaker != "" || !tester.HasText("Synthetic transcript 0000") {
		t.Fatal("removed speaker left the filter and transcript inconsistent", all, view.speaker)
	}
}
