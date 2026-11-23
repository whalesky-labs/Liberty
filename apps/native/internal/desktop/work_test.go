package desktop

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func TestWorkViewMissingStateDoesNotRequestDuringRender(t *testing.T) {
	// A controller without a service deliberately panics if rendering attempts
	// to load. Workspace route activation is the only initial-load owner.
	v := WorkGameView{Client: &client.WorkController{}, Locale: "en-US", GameKey: "mine"}
	tt := ui.NewTester(v.View, 960, 560)
	for range 3 {
		tt.Frame()
	}
	if !tt.HasText("Loading map...") {
		t.Fatal("loading state missing")
	}
}

func TestWorkMapAssetsAndHotspotsForEveryGame(t *testing.T) {
	for _, game := range domain.WorkGameCatalog() {
		for _, locale := range []string{"en-US", "zh-CN"} {
			t.Run(game.Key+locale, func(t *testing.T) {
				state := domain.WorkGameState{GameKey: game.Key, NameEn: game.NameEn, NameZh: game.NameZh, Jobs: game.Jobs}
				for _, job := range game.Jobs {
					j := job
					state.Tasks = append(state.Tasks, domain.WorkGameTask{ID: fmt.Sprintf("%s-slot-%d", game.Key, j.SlotIndex), SlotIndex: j.SlotIndex, GameKey: game.Key, Job: &j, Status: workRunning})
				}
				v := WorkGameView{Client: &client.WorkController{GameStates: map[string]domain.WorkGameState{game.Key: state}}, Locale: locale, GameKey: game.Key}
				tt := ui.NewTester(v.View, 960, 560)
				tt.Frame()
				if !tt.HasText(workText(locale, game.NameEn, game.NameZh)) {
					t.Fatal("map heading missing")
				}
				for frame := range 9 {
					for _, job := range game.Jobs {
						presentation.WorkArt(workWorkerArt(game.Key, job.SlotIndex, frame))
					}
				}
				for _, task := range state.Tasks {
					label := gameJobLabel(locale, task.Job) + ": " + workStatusLabel(locale, task.Status)
					r, ok := tt.Find(label)
					if !ok {
						t.Fatalf("missing hotspot %s", label)
					}
					a := workArea(game.Key, task.SlotIndex)
					wantX, wantY := v.scene.x+a[0]*v.scene.scale, v.scene.y+a[1]*v.scene.scale
					if math.Abs(float64(r.X-wantX)) > 1 || math.Abs(float64(r.Y-wantY)) > 1 || math.Abs(float64(r.W-a[2]*v.scene.scale)) > 1 {
						t.Fatalf("hotspot misaligned: %+v want %.2f %.2f scale %.3f", r, wantX, wantY, v.scene.scale)
					}
				}
				// Paint all nine frames to exercise the actual image decoder path.
				for range 9 {
					tt.Advance(170 * time.Millisecond)
				}
				tt.SetSize(1180, 720)
				tt.Frame()
				if v.scene.width != 1180 || v.scene.height != 720 {
					t.Fatal("scene did not resize")
				}
			})
		}
	}
}

func TestFarmSceneSelectionAndCropAssets(t *testing.T) {
	for _, crop := range domain.FarmCropCatalog() {
		presentation.WorkArt(farmCropArt(crop.CropKey))
	}
	for _, locale := range []string{"en-US", "zh-CN"} {
		state := domain.FarmState{Crops: domain.FarmCropCatalog()}
		for index := int64(1); index <= 3; index++ {
			state.Plots = append(state.Plots, domain.FarmPlot{ID: fmt.Sprintf("farm-plot-%d", index), PlotIndex: index, Status: "empty"})
		}
		chosen := ""
		v := FarmView{Client: &client.WorkController{FarmState: &state}, Locale: locale, Choose: func(p domain.FarmPlot, _ []domain.FarmCropConfig) { chosen = p.ID }}
		tt := ui.NewTester(v.View, 960, 560)
		tt.Frame()
		if err := tt.Click(fmt.Sprintf("%s 1: %s", workText(locale, "Plot", "地块"), farmActionLabel(locale, "empty"))); err != nil {
			t.Fatal(err)
		}
		if chosen != "farm-plot-1" {
			t.Fatal("default selected plot differs", chosen)
		}
		label := fmt.Sprintf("%s 3: %s", workText(locale, "Plot", "地块"), farmStatusLabel(locale, "empty"))
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		if chosen != "farm-plot-3" {
			t.Fatal("map click lost plot identity", chosen)
		}
		initialScale := v.scene.scale
		tt.Pinch(40, 220, 1.2)
		if v.scene.scale <= initialScale {
			t.Fatal("map background did not zoom")
		}
		initialX := v.scene.x
		tt.Press(40, 220)
		tt.Move(20, 220)
		tt.Release(20, 220)
		if v.scene.x >= initialX || v.scene.dragging {
			t.Fatal("map background did not pan and release")
		}
	}
}

func TestWorkScenePanZoomKeepsOriginalWorldAndBounds(t *testing.T) {
	var scene workScene
	scene.resize(960, 560)
	if scene.scale != workCoverScale(960, 560) {
		t.Fatal("cover scale differs")
	}
	x, y := float32(400), float32(250)
	worldX, worldY := (x-scene.x)/scene.scale, (y-scene.y)/scene.scale
	scene.zoom(scene.scale*1.2, x, y)
	if math.Abs(float64((x-scene.x)/scene.scale-worldX)) > .01 || math.Abs(float64((y-scene.y)/scene.scale-worldY)) > .01 {
		t.Fatal("zoom moved anchor")
	}
	scene.input(ui.InputEvent{Kind: ui.InputPointerDown, Button: 0, X: 100, Y: 100})
	scene.input(ui.InputEvent{Kind: ui.InputPointerMove, Button: 0, X: 10000, Y: 10000})
	if scene.x != 0 || scene.y != 0 {
		t.Fatal("pan escaped world")
	}
	scene.input(ui.InputEvent{Kind: ui.InputPointerUp, Canceled: true})
	if scene.dragging {
		t.Fatal("cancelled drag retained capture")
	}
	scene.resize(1400, 1000)
	if scene.scale < workCoverScale(1400, 1000) {
		t.Fatal("resize exposed outside map")
	}
}
