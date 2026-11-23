package desktop

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

const workWorldW, workWorldH = 1536, 1024

const (
	workIdle      = "idle"
	workRunning   = "running"
	workNeedsCare = "needsCare"
	workClaimable = "claimable"
)

var farmPlotAreas = map[int64][4]float32{1: {210, 410, 330, 330}, 2: {505, 438, 330, 330}, 3: {800, 410, 330, 330}}
var workHotspots = map[string]map[int64][4]float32{
	"mine":              {1: {390, 392, 190, 230}, 2: {1012, 438, 190, 232}, 3: {652, 620, 220, 234}},
	"factory":           {1: {465, 230, 166, 188}, 2: {565, 338, 166, 188}, 3: {749, 170, 166, 188}},
	"convenience-store": {1: {459, 336, 166, 188}, 2: {739, 391, 166, 188}, 3: {1031, 386, 166, 188}},
}

type WorkMarketView struct {
	Client *client.WorkController
	Locale string
	Open   func(string)
}

func (v *WorkMarketView) View(c *ui.Context) {
	locale := v.Locale
	width, _ := c.Size()
	columns := 4
	if width <= 1180 {
		columns = 2
	}
	if width <= 820 {
		columns = 1
	}
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Gap(14).Children(func() {
			hero := ui.Grid(c).FillWidth().MinHeight(280).ColumnTracks(ui.Fr(.86), ui.Fr(1.14).Min(320)).Gap(12).Padding(14, 16).Radius(10).Border(1, palette(c).Separator).Background(palette(c).Panel).Clip().Label(workText(locale, "Work map overview", "打工地图概览"))
			if width <= 820 {
				hero.Columns(1)
			}
			hero.Children(func() {
				ui.Column(c).MinWidth(0).Justify(ui.Center).Gap(8).Children(func() {
					ui.Text(c, workText(locale, "Work Games", "打工地图")).FontSize(13).TextColor(c.Theme().TextMuted)
					ui.Text(c, workText(locale, "Niuma Market", "牛马市场")).FontSize(23).FontWeight(720).Role(ui.RoleHeading).Level(1)
					ui.Text(c, workText(locale, "Pick a mini-game map and turn small breaks into pet items.", "选择一个打工小游戏地图，把碎片时间变成宠物商品收益。")).FillWidth().FontSize(13).LineHeight(1.55).TextColor(c.Theme().TextMuted)
				})
				ui.Box(c).FillWidth().MinHeight(240).Radius(22).Clip().Children(func() {
					ui.Image(c, presentation.WorkArt("niuma-market.webp")).Absolute().Fill().Fit(ui.Cover).Label(workText(locale, "Niuma Market map lobby", "牛马市场地图大厅"))
				})
			})
			if v.Client.Error != nil {
				ui.Column(c).FillWidth().Gap(8).Padding(16).Children(func() {
					ui.Text(c, workText(locale, "Could not refresh the maps. Please retry.", "地图刷新失败，请重试。")).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
					if textButton(c, workText(locale, "Retry", "重试")).Disabled(v.Client.BusyKey != "").HandleInput(workIgnoreRepeat).Clicked() {
						v.Client.LoadMarket()
					}
				})
			}
			if v.Client.MarketState == nil {
				if v.Client.Error == nil {
					ui.Text(c, workText(locale, "Loading maps…", "正在加载地图…")).Padding(20, 16).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
				}
				return
			}
			if len(v.Client.MarketState.Maps) == 0 {
				ui.Text(c, workText(locale, "No maps are available yet.", "暂无可用地图。")).Padding(20, 16).Role(ui.RoleStatus)
			}
			ui.Grid(c).FillWidth().Columns(columns).Gap(14).Children(func() {
				for _, entry := range v.Client.MarketState.Maps {
					v.mapCard(c.Key(entry.ID), entry)
				}
			})
		})
	})
}

func (v *WorkMarketView) mapCard(c *ui.Context, entry domain.WorkMap) {
	locale := v.Locale
	name := workText(locale, entry.NameEn, entry.NameZh)
	status := marketStatusLabel(locale, entry.Status)
	available := entry.Enabled && entry.Status != "locked" && entry.Route != "" && v.Open != nil
	if !entry.Enabled {
		status = marketStatusLabel(locale, "locked")
	} else if entry.Status != "locked" && !available {
		status = workText(locale, "Map unavailable", "地图不可用")
	}
	button := ui.ButtonBase(c).Column().FillWidth().MinWidth(0).Padding(0).Justify(ui.Start).Radius(10).Clip().Background(palette(c).Panel).Label(name).Tooltip(status).Disabled(!available).HandleInput(workIgnoreRepeat)
	if !available {
		button.Opacity(.68).DimDisabled(false)
	}
	if button.Hovered() && available {
		button.Background(c.Theme().SurfaceHover.Mix(palette(c).Panel, .94))
	}
	button.Children(func() {
		ui.Box(c).FillWidth().AspectRatio(4.0 / 3).Clip().Background(palette(c).Subpanel).Children(func() {
			if art := workMapArt(entry.ID); art != "" {
				ui.Image(c, presentation.WorkArt(art)).Absolute().Fill().Fit(ui.Cover).Label(workText(locale, entry.NameEn+" map", entry.NameZh+"地图"))
			}
			background, color := ui.RGBA(255, 255, 255, .88), ui.Hex("#2f3c2f")
			switch entry.Status {
			case workClaimable, workNeedsCare:
				background, color = ui.Hex("#e9f7d5"), ui.Hex("#234b2b")
			case "locked":
				background, color = ui.RGBA(239, 241, 242, .9), ui.Hex("#59616a")
			}
			ui.Text(c, status).Absolute().Top(12).Left(12).MinHeight(30).Padding(6, 12).Radius(15).Background(background).TextColor(color).FontSize(13).FontWeight(800)
		})
		ui.Column(c).FillWidth().Padding(16).Gap(14).Children(func() {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, name).FillWidth().FontSize(18).Bold()
				ui.Text(c, workText(locale, entry.DescriptionEn, entry.DescriptionZh)).FillWidth().FontSize(14).LineHeight(1.55).TextColor(c.Theme().TextMuted)
			})
			ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
				if !available {
					reason := workText(locale, "Coming soon", "筹备中")
					if entry.Enabled && entry.Status != "locked" {
						reason = status
					}
					ui.Text(c, reason).MinHeight(28).Padding(6, 10).Radius(14).Background(palette(c).Subpanel).FontSize(12).TextColor(c.Theme().TextMuted)
					return
				}
				for _, stat := range []struct {
					label string
					value int64
				}{{workText(locale, "Active", "进行中"), entry.Summary.ActivePlots}, {workText(locale, "Care", "需照看"), entry.Summary.NeedsCarePlots}, {workText(locale, "Ready", "可收获"), entry.Summary.MaturePlots}} {
					ui.Text(c, fmt.Sprintf("%s %d", stat.label, stat.value)).MinHeight(28).Padding(6, 10).Radius(14).Background(palette(c).Subpanel.Mix(c.Theme().Accent, .08)).FontSize(12).Bold()
				}
			})
		})
	})
	if button.Clicked() {
		v.Open(entry.Route)
	}
}

func marketStatusLabel(locale, status string) string {
	switch status {
	case "locked":
		return workText(locale, "Locked", "未开放")
	case workClaimable:
		return workText(locale, "Ready", "可收获")
	default:
		return workStatusLabel(locale, status)
	}
}

type FarmView struct {
	Client   *client.WorkController
	Locale   string
	Back     func()
	Choose   func(domain.FarmPlot, []domain.FarmCropConfig)
	selected string
	focused  string
	scene    workScene
}

func (v *FarmView) View(c *ui.Context) {
	locale := v.Locale
	state := v.Client.FarmState
	if state == nil {
		workLoading(c, locale, v.Client, v.Client.LoadFarm, v.Back)
		return
	}
	active, care, ready := int64(0), int64(0), int64(0)
	for _, p := range state.Plots {
		if p.Status != "empty" {
			active++
		}
		if p.Status == "needs_water" {
			care++
		}
		if p.Status == "mature" {
			ready++
		}
	}
	selected := selectedFarmPlot(state.Plots, v.selected)
	animationTime := c.Now()
	if c.Preferences().ReduceMotion {
		animationTime = time.Unix(0, 0)
	} else {
		c.After(90 * time.Millisecond)
	}
	frame := int((animationTime.UnixNano() / int64(90*time.Millisecond)) % 8)
	root := ui.Box(c).Fill().MinHeight(0).Radius(8).Clip().Background(ui.Hex("#6fa943"))
	root.Children(func() {
		focused := ""
		v.scene.world(c, root, locale, func() {
			ui.Image(c, presentation.WorkArt("farm-background.png")).Absolute().Fill().Fit(ui.Cover).PassThrough()
			for _, plot := range state.Plots {
				p := plot
				a := farmArea(p.PlotIndex)
				button := ui.ButtonBase(c.Key(p.ID)).Absolute().Left(a[0]).Top(a[1]).Size(a[2], a[3]).Padding(0).Background(ui.Transparent).Border(0, ui.Transparent).
					Label(fmt.Sprintf("%s %d: %s", workText(locale, "Plot", "地块"), p.PlotIndex, farmStatusLabel(locale, p.Status))).Disabled(v.Client.BusyKey != "").HandleInput(v.scene.hotspotInput(a))
				button.Children(func() {
					soil := map[int64]string{1: "plot-wheat-soil.png", 2: "plot-carrot-soil.png", 3: "plot-tomato-soil.png"}[p.PlotIndex]
					if soil != "" {
						ui.Image(c, presentation.WorkArt(soil)).Absolute().Fill().Fit(ui.Contain).PassThrough()
					}
					if cropArt := farmCropArt(p.CropKey); p.Status != "empty" && cropArt != "" {
						scale, offset, opacity := farmCropStyle(p)
						ui.Image(c, presentation.WorkArt(cropArt)).Absolute().Fill().Fit(ui.Contain).Scale(scale, scale).Translate(0, offset).Opacity(opacity).PassThrough()
					}
					if p.Status != "planted" {
						ui.Text(c, farmActionLabel(locale, p.Status)).Absolute().Left(85).Bottom(26).Width(160).Padding(7).Radius(12).Background(ui.RGBA(28, 43, 23, .8)).TextColor(ui.RGB(255, 255, 255)).FontSize(18).PassThrough()
					}
				})
				if button.FocusVisible() {
					focused = p.ID
					v.selected = p.ID
					if v.focused != p.ID {
						v.scene.focus(a)
						c.After(0)
					}
				}
				if button.Clicked() {
					v.selected = p.ID
					v.scene.focus(a)
					v.run(p, state.Crops)
				}
			}
			dogX, dogY, facing := farmDogPosition(animationTime)
			ui.Image(c, presentation.WorkArt(fmt.Sprintf("dog-walk-%02d.png", frame+1))).Absolute().Left(dogX).Top(dogY).Size(180, 150).Scale(.78*facing, .78).Fit(ui.Contain).PassThrough()
		})
		v.focused = focused
		header := workHeader(c, locale, workText(locale, "Farm", "农场种菜"), active, care, ready, true, v.Back)
		hud := workHUD(c, root, true, func() {
			ui.Text(c, workText(locale, "Selected Plot", "当前地块")).FontSize(10).TextColor(ui.RGBA(255, 255, 255, .64))
			if selected == nil {
				ui.Text(c, workText(locale, "No plots", "暂无地块")).TextColor(ui.RGB(255, 255, 255))
				workError(c, locale, v.Client, v.Client.LoadFarm)
				return
			}
			p := *selected
			ui.Text(c, fmt.Sprintf("%s %d", workText(locale, "Plot", "地块"), p.PlotIndex)).FontSize(16).Bold().TextColor(ui.RGB(255, 255, 255))
			ui.Text(c, farmStatusLabel(locale, p.Status)).FontSize(13).TextColor(ui.RGB(255, 255, 255))
			ui.Text(c, farmCropLabel(locale, p)).FontSize(11).TextColor(ui.RGBA(255, 255, 255, .7))
			label := farmActionLabel(locale, p.Status)
			if p.Status == "planted" {
				label = workText(locale, "Next care in ", "距离下次照看 ") + workSeconds(locale, p.RemainingSeconds)
			}
			if v.Client.BusyKey == "plot:"+p.ID {
				label = workText(locale, "Working…", "处理中…")
			}
			actionName := fmt.Sprintf("%s %d: %s", workText(locale, "Plot", "地块"), p.PlotIndex, label)
			if ui.PrimaryButton(c, label).Label(actionName).FillWidth().MinHeight(38).Disabled(p.Status == "planted" || v.Client.BusyKey != "").HandleInput(workIgnoreRepeat).Clicked() {
				v.run(p, state.Crops)
			}
			if harvest := v.Client.LastHarvest; harvest != nil {
				title := workText(locale, "Harvest collected", "收获已领取")
				summary := workRewardSummary(locale, harvest.Rewards, harvest.LPReward)
				ui.Text(c, title).FontSize(12).Bold().TextColor(ui.RGB(255, 255, 255))
				ui.Text(c, summary).FillWidth().FontSize(11).TextColor(ui.RGB(255, 255, 255)).Role(ui.RoleStatus).Label(title + ": " + summary)
			}
			workError(c, locale, v.Client, v.Client.LoadFarm)
		})
		if v.scene.overlays(root.LayoutBounds(), header.LayoutBounds(), hud.LayoutBounds()) {
			c.After(0)
		}
	})
}

func (v *FarmView) run(plot domain.FarmPlot, crops []domain.FarmCropConfig) {
	if v.Client.BusyKey != "" {
		return
	}
	switch plot.Status {
	case "empty":
		if v.Choose != nil {
			v.Choose(plot, crops)
		}
	case "needs_water":
		v.Client.Water(plot.ID)
	case "mature":
		v.Client.Harvest(plot.ID)
	}
}

func selectedFarmPlot(plots []domain.FarmPlot, id string) *domain.FarmPlot {
	for i := range plots {
		if plots[i].ID == id {
			return &plots[i]
		}
	}
	for _, status := range []string{"mature", "needs_water"} {
		for i := range plots {
			if plots[i].Status == status {
				return &plots[i]
			}
		}
	}
	if len(plots) > 0 {
		return &plots[0]
	}
	return nil
}

func farmArea(index int64) [4]float32 {
	if a, ok := farmPlotAreas[index]; ok {
		return a
	}
	return [4]float32{360 + float32(index)*360, 430, 330, 330}
}
func farmCropArt(key string) string {
	for _, crop := range domain.FarmCropCatalog() {
		if crop.CropKey == key {
			return "crop-" + key + "-overlay.png"
		}
	}
	return ""
}
func farmCropStyle(p domain.FarmPlot) (scale, offset, opacity float32) {
	switch {
	case p.Status == "mature":
		return .96, 0, 1
	case p.Status == "needs_water":
		return .68, -10, .9
	case p.ProgressRatio > .62:
		return .84, -4, 1
	default:
		return .48, -18, .78
	}
}
func farmDogPosition(now time.Time) (x, y, facing float32) {
	phase := float32(now.UnixNano()%int64(18*time.Second)) / float32(18*time.Second)
	points := [][3]float32{{0, 0, 0}, {.18, 330, -44}, {.34, 640, -12}, {.5, 880, 74}, {.68, 610, 142}, {.84, 255, 96}, {1, 0, 0}}
	for i := 1; i < len(points); i++ {
		if phase <= points[i][0] {
			a, b := points[i-1], points[i]
			t := (phase - a[0]) / (b[0] - a[0])
			x, y = 190+a[1]+(b[1]-a[1])*t, 700+a[2]+(b[2]-a[2])*t
			break
		}
	}
	facing = 1
	if phase >= .5 {
		facing = -1
	}
	return
}

type WorkGameView struct {
	Client           *client.WorkController
	Locale           string
	GameKey          string
	Back             func()
	OpenAssembly     func()
	OpenAssemblyCare func(string)
	selected         int64
	focused          string
	scene            workScene
}

func (v *WorkGameView) View(c *ui.Context) {
	locale := v.Locale
	art := workMapArt(v.GameKey)
	if art == "" {
		ui.Column(c).Fill().Padding(20).Gap(12).Children(func() {
			ui.Text(c, workText(locale, "Map unavailable", "地图不可用")).Role(ui.RoleHeading).Level(1)
			if ui.Button(c, workText(locale, "Back", "返回牛马市场")).Disabled(v.Back == nil).HandleInput(workIgnoreRepeat).Clicked() {
				v.Back()
			}
		})
		return
	}
	state, ok := v.Client.GameStates[v.GameKey]
	if !ok {
		workLoading(c, locale, v.Client, func() { v.Client.LoadGame(v.GameKey) }, v.Back)
		return
	}
	active, care, ready := int64(0), int64(0), int64(0)
	for _, task := range state.Tasks {
		if task.Status != workIdle {
			active++
		}
		if task.Status == workNeedsCare {
			care++
		}
		if task.Status == workClaimable {
			ready++
		}
	}
	selected := selectedWorkTask(state.Tasks, v.selected)
	frame := 0
	if !c.Preferences().ReduceMotion {
		frame = int((c.Now().UnixNano() / int64(170*time.Millisecond)) % 9)
		c.After(170 * time.Millisecond)
	}
	root := ui.Box(c).Fill().MinHeight(0).Radius(8).Clip().Background(ui.Hex("#9cc9e2"))
	root.Children(func() {
		focused := ""
		v.scene.world(c, root, locale, func() {
			ui.Image(c, presentation.WorkArt(art)).Absolute().Fill().Fit(ui.Cover).PassThrough()
			for _, task := range state.Tasks {
				t := task
				a := workArea(v.GameKey, t.SlotIndex)
				button := ui.ButtonBase(c.Key(t.ID)).Absolute().Left(a[0]).Top(a[1]).Size(a[2], a[3]).Padding(0).Background(ui.Transparent).Border(0, ui.Transparent).
					Label(gameJobLabel(locale, t.Job) + ": " + workStatusLabel(locale, t.Status)).Disabled(v.Client.BusyKey != "").HandleInput(v.scene.hotspotInput(a))
				button.Children(func() { v.workHotspot(c, t, frame, a) })
				if button.FocusVisible() {
					focused = t.ID
					v.selected = t.SlotIndex
					if v.focused != t.ID {
						v.scene.focus(a)
						c.After(0)
					}
				}
				if button.Clicked() {
					v.selected = t.SlotIndex
					v.scene.focus(a)
					v.run(t, state.Jobs)
				}
			}
		})
		v.focused = focused
		header := workHeader(c, locale, workText(locale, state.NameEn, state.NameZh), active, care, ready, false, v.Back)
		hud := workHUD(c, root, false, func() {
			ui.Text(c, workText(locale, "Current Job", "当前岗位")).FontSize(10).TextColor(ui.RGBA(255, 255, 255, .64))
			if selected == nil {
				ui.Text(c, workText(locale, "No jobs yet.", "暂无岗位。")).TextColor(ui.RGB(255, 255, 255))
				workError(c, locale, v.Client, func() { v.Client.LoadGame(v.GameKey) })
				return
			}
			t := *selected
			ui.Text(c, gameJobLabel(locale, t.Job)).FontSize(16).Bold().TextColor(ui.RGB(255, 255, 255))
			ui.Text(c, workStatusLabel(locale, t.Status)).FontSize(12).TextColor(ui.RGBA(255, 255, 255, .8))
			ui.Text(c, fmt.Sprintf("%s  %d%%", workText(locale, "Progress", "进度"), int(t.ProgressRatio*100+.5))).FontSize(11).TextColor(ui.RGBA(255, 255, 255, .8))
			progressBar(c, float32(t.ProgressRatio*100), workText(locale, "Work task progress", "工作进度"))
			ui.Text(c, workProgressHint(locale, t)).FillWidth().FontSize(11).TextColor(ui.RGBA(255, 255, 255, .8))
			if t.Status == workNeedsCare && v.GameKey == "factory" && v.OpenAssemblyCare != nil {
				if ui.Button(c.Key("assembly-care"), workText(locale, "Care with Assembly Practice", "用装配练习完成照看")).FillWidth().MinHeight(34).Disabled(v.Client.BusyKey != "").HandleInput(workIgnoreRepeat).Clicked() {
					v.OpenAssemblyCare(t.ID)
				}
			}
			label := workTaskAction(locale, t.Status)
			if t.Status == workRunning {
				label = workText(locale, "Next care in ", "距离下次照看 ") + workSeconds(locale, t.RemainingSeconds)
			}
			if v.Client.BusyKey == "task:"+t.ID {
				label = workText(locale, "Working…", "处理中…")
			}
			if ui.PrimaryButton(c, label).FillWidth().MinHeight(38).Disabled(t.Status == workRunning || v.Client.BusyKey != "").HandleInput(workIgnoreRepeat).Clicked() {
				v.run(t, state.Jobs)
			}
			if v.Client.LastReward != nil && v.Client.LastReward.GameKey == v.GameKey {
				title := workText(locale, "Reward claimed", "奖励已领取")
				summary := workRewardSummary(locale, v.Client.LastReward.Rewards, v.Client.LastReward.LPReward)
				ui.Text(c, title).FontSize(12).Bold().TextColor(ui.RGB(255, 255, 255))
				ui.Text(c, summary).FillWidth().FontSize(11).TextColor(ui.RGB(255, 255, 255)).Role(ui.RoleStatus).Label(title + ": " + summary)
			}
			if v.GameKey == "factory" && v.OpenAssembly != nil {
				if ui.Button(c.Key("assembly-practice"), workText(locale, "Practice Basic Assembly", "练习基础装配")).FillWidth().MinHeight(34).HandleInput(workIgnoreRepeat).Clicked() {
					v.OpenAssembly()
				}
			}
			workError(c, locale, v.Client, func() { v.Client.LoadGame(v.GameKey) })
		})
		if v.scene.overlays(root.LayoutBounds(), header.LayoutBounds(), hud.LayoutBounds()) {
			c.After(0)
		}
	})
}

func selectedWorkTask(tasks []domain.WorkGameTask, index int64) *domain.WorkGameTask {
	for i := range tasks {
		if tasks[i].SlotIndex == index {
			return &tasks[i]
		}
	}
	for _, status := range []string{workClaimable, workNeedsCare} {
		for i := range tasks {
			if tasks[i].Status == status {
				return &tasks[i]
			}
		}
	}
	if len(tasks) > 0 {
		return &tasks[0]
	}
	return nil
}
func workArea(key string, index int64) [4]float32 {
	if area, ok := workHotspots[key][index]; ok {
		return area
	}
	return [4]float32{180 + float32(index)*330, 380, 280, 280}
}
func workMapArt(key string) string {
	return map[string]string{"farm": "farm-isometric.webp", "mine": "mine-map.webp", "factory": "factory-map.webp", "convenience-store": "convenience-store-map.webp"}[key]
}
func (v *WorkGameView) run(task domain.WorkGameTask, jobs []domain.WorkGameJobConfig) {
	if v.Client.BusyKey != "" {
		return
	}
	switch task.Status {
	case workIdle:
		if task.Job != nil {
			v.Client.Start(v.GameKey, task.ID, task.Job.JobKey)
			return
		}
		for _, job := range jobs {
			if job.SlotIndex == task.SlotIndex {
				v.Client.Start(v.GameKey, task.ID, job.JobKey)
				return
			}
		}
	case workNeedsCare:
		v.Client.Care(v.GameKey, task.ID)
	case workClaimable:
		v.Client.Claim(v.GameKey, task.ID)
	}
}
func workWorkerArt(key string, slot int64, frame int) string {
	prefix := map[string]string{"mine": "mine", "factory": "factory", "convenience-store": "store"}[key]
	if prefix == "" {
		return ""
	}
	frame = (frame + int(max(slot-1, 0))*3) % 9
	if frame < 0 {
		frame += 9
	}
	return fmt.Sprintf("%s-worker-frame-%d.png", prefix, frame+1)
}
func (v *WorkGameView) workHotspot(c *ui.Context, task domain.WorkGameTask, frame int, area [4]float32) {
	x, y, size := area[2]*.5, area[3]*.62, float32(138)
	if v.GameKey == "mine" {
		switch task.SlotIndex {
		case 1:
			x, y, size = area[2]*.46, area[3]*.52, 176
		case 2:
			x, y, size = area[2]*.5, area[3]*.54, 168
		default:
			x, y, size = area[2]*.5, area[3]*.5, 184
		}
	}
	if art := workWorkerArt(v.GameKey, task.SlotIndex, frame); art != "" {
		ui.Image(c, presentation.WorkArt(art)).Absolute().Left(x-size/2).Top(y-size/2).Size(size, size).Fit(ui.Contain).PassThrough()
	}
	if v.GameKey == "convenience-store" && task.SlotIndex == 1 {
		ui.Box(c).Absolute().Left(area[2]*.5-48).Top(area[3]*.7-28).Size(112, 56).Radius(6).Background(ui.Hex("#b46f2f")).PassThrough().Children(func() {
			ui.Box(c).Absolute().Bottom(0).FillWidth().Height(18).Background(ui.Hex("#387264")).PassThrough()
		})
	}
}

func workOverlayLayout(c *ui.Context, farm bool) (width, inset float32, compact bool) {
	viewportWidth, _ := c.Size()
	width, inset = 390, 14
	breakpoint := float32(860)
	if farm {
		width, breakpoint = 360, 760
	}
	compact = viewportWidth <= breakpoint
	if compact {
		width, inset = 360, 10
		if farm {
			width = 340
		}
	}
	return
}

func workHeader(c *ui.Context, locale, title string, active, care, ready int64, farm bool, back func()) ui.Element {
	careLabel, readyLabel := "需照看", "可领取"
	_, inset, compact := workOverlayLayout(c, farm)
	if farm {
		careLabel, readyLabel = "需浇水", "可收获"
	}
	header := ui.Row(c).Absolute().Left(inset).Top(inset).Right(inset).Gap(12).AlignItems(ui.Start)
	header.Children(func() {
		ui.Row(c).MinWidth(0).Wrap().Padding(6, 10).Gap(10).Radius(8).Background(ui.RGBA(24, 27, 29, .72)).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, workText(locale, "Back", "返回牛马市场")).Disabled(back == nil).MinHeight(30).Background(ui.RGBA(255, 255, 255, .12)).TextColor(ui.RGB(255, 255, 255)).HandleInput(workIgnoreRepeat).Clicked() {
				back()
			}
			ui.Text(c, title).MinWidth(0).FontSize(17).Bold().TextColor(ui.RGB(255, 255, 255)).Role(ui.RoleHeading).Level(1)
		})
		if compact {
			return
		}
		ui.Box(c).Grow(1)
		ui.Row(c).MinWidth(0).Wrap().Gap(6).Justify(ui.End).Children(func() {
			for _, metric := range []struct {
				label string
				value int64
			}{{workText(locale, "Active", "进行中"), active}, {workText(locale, "Care", careLabel), care}, {workText(locale, "Ready", readyLabel), ready}} {
				ui.Text(c, fmt.Sprintf("%s %d", metric.label, metric.value)).FontSize(11).Padding(10).Radius(8).Background(ui.RGBA(24, 27, 29, .68)).TextColor(ui.RGB(255, 255, 255))
			}
		})
	})
	return header
}
func workHUD(c *ui.Context, root ui.Element, farm bool, children func()) ui.Element {
	width, inset, _ := workOverlayLayout(c, farm)
	viewportWidth, height := root.LayoutBounds().W, root.LayoutBounds().H
	if height <= 0 {
		_, height = c.Size()
	}
	if viewportWidth <= 0 {
		viewportWidth, _ = c.Size()
	}
	hud := ui.Scroll(c).Absolute().Left(inset).Bottom(inset).Width(width).MaxWidth(max(1, viewportWidth-2*inset)).MaxHeight(max(1, min(height-112, height*.6))).Radius(8).Background(ui.RGBA(24, 27, 29, .76))
	hud.Children(func() {
		ui.Column(c).FillWidth().Padding(12).Gap(9).Children(children)
	})
	return hud
}
func workError(c *ui.Context, locale string, controller *client.WorkController, retry func()) {
	if controller.Error == nil {
		return
	}
	message := workActionError(locale, controller.Error)
	if controller.ErrorIsRefresh {
		message = workText(locale, "Could not refresh the map. Completed actions remain applied. Retry the refresh.", "地图刷新失败，已完成的操作仍然生效，请重试刷新。")
	}
	ui.Text(c, message).FillWidth().FontSize(12).TextColor(ui.Hex("#ffd0c9")).Role(ui.RoleAlert)
	if retry != nil && ui.Button(c, workText(locale, "Retry", "重试")).Disabled(controller.BusyKey != "").HandleInput(workIgnoreRepeat).Clicked() {
		retry()
	}
}
func workActionError(locale string, err error) string {
	for _, crop := range domain.FarmCropCatalog() {
		if err.Error() == fmt.Sprintf("缺少%s，请先去宠物商店购买。", crop.NameZh+"种子") {
			return fmt.Sprintf(workText(locale, "Not enough %s seeds. Buy seeds in the Pet Store.", "缺少%s种子，请先去宠物商店购买。"), workText(locale, crop.NameEn, crop.NameZh))
		}
	}
	switch err.Error() {
	case "这块地还不能播种。", "这块地暂时不需要浇水。", "作物还没有成熟。":
		return workText(locale, "The plot has changed. Refresh the map before trying again.", "地块状态已变化，请刷新地图后重试。")
	case "这个岗位还没有空闲。", "这个岗位暂时不需要照看。", "这个岗位还不能领奖。":
		return workText(locale, "The job has changed. Refresh the map before trying again.", "岗位状态已变化，请刷新地图后重试。")
	}
	return workText(locale, "The action could not be completed. Refresh the map before trying again.", "操作未完成，请刷新地图后重试。")
}
func workLoading(c *ui.Context, locale string, controller *client.WorkController, retry, back func()) {
	ui.Column(c).Fill().Padding(20).Gap(12).Children(func() {
		if ui.Button(c, workText(locale, "Back", "返回牛马市场")).Disabled(back == nil).HandleInput(workIgnoreRepeat).Clicked() {
			back()
		}
		if controller.Error == nil {
			ui.Text(c, workText(locale, "Loading map...", "正在加载地图…")).FontSize(18).Role(ui.RoleStatus)
		} else {
			ui.Text(c, workText(locale, "Could not load the map. Please retry.", "无法读取地图，请重试。")).Role(ui.RoleAlert).TextColor(c.Theme().Danger)
			if retry != nil && ui.Button(c, workText(locale, "Retry", "重试")).Disabled(controller.BusyKey != "").HandleInput(workIgnoreRepeat).Clicked() {
				retry()
			}
		}
	})
}

func workRewardSummary(locale string, rewards []domain.FarmRewardItem, lp int64) string {
	parts := make([]string, 0, len(rewards)+1)
	for _, reward := range rewards {
		name := workText(locale, "Item", "物品")
		if item, ok := domain.FindPetItem(reward.ItemKey); ok {
			name = workText(locale, item.NameEn, item.NameZh)
		}
		parts = append(parts, fmt.Sprintf("%s ×%d", name, reward.Quantity))
	}
	if lp > 0 {
		parts = append(parts, fmt.Sprintf("LP +%d", lp))
	}
	return strings.Join(parts, " · ")
}

func workProgressHint(locale string, task domain.WorkGameTask) string {
	switch task.Status {
	case workIdle:
		return workText(locale, "Start this slot when you are ready.", "准备好后即可开始这个岗位。")
	case workNeedsCare:
		return workText(locale, "A short care action is waiting.", "现在需要完成一次短照看动作。")
	case workClaimable:
		return workText(locale, "Reward is ready to claim.", "奖励已经可以领取。")
	default:
		return workText(locale, "Next checkpoint in ", "下个节点还需 ") + workSeconds(locale, task.RemainingSeconds)
	}
}

func workText(locale, en, zh string) string {
	if locale == "en-US" {
		return en
	}
	return zh
}
func workIgnoreRepeat(ev ui.InputEvent) bool {
	return ev.Kind == ui.InputKeyDown && ev.Repeat && (ev.Key == ui.KeyEnter || ev.Key == ui.KeySpace)
}
func farmStatusLabel(locale, status string) string {
	switch status {
	case "planted":
		return workText(locale, "Growing", "成长中")
	case "needs_water":
		return workText(locale, "Needs water", "需要浇水")
	case "mature":
		return workText(locale, "Ready", "可收获")
	default:
		return workText(locale, "Empty", "空地")
	}
}
func farmActionLabel(locale, status string) string {
	switch status {
	case "empty":
		return workText(locale, "Choose Crop", "选择农作物")
	case "needs_water":
		return workText(locale, "Water", "浇水")
	case "mature":
		return workText(locale, "Harvest", "收获")
	default:
		return workText(locale, "Growing", "成长中")
	}
}
func farmCropLabel(locale string, p domain.FarmPlot) string {
	if p.Crop == nil {
		return workText(locale, "Empty Plot", "空地")
	}
	return workText(locale, p.Crop.NameEn, p.Crop.NameZh)
}
func workStatusLabel(locale, status string) string {
	switch status {
	case workRunning:
		return workText(locale, "Running", "进行中")
	case workNeedsCare:
		return workText(locale, "Needs Care", "需要照看")
	case workClaimable:
		return workText(locale, "Ready", "可领取")
	default:
		return workText(locale, "Idle", "空闲")
	}
}
func workTaskAction(locale, status string) string {
	switch status {
	case workNeedsCare:
		return workText(locale, "Care Job", "照看岗位")
	case workClaimable:
		return workText(locale, "Claim Reward", "领取奖励")
	case workIdle:
		return workText(locale, "Start Job", "开始打工")
	default:
		return workText(locale, "Next care in", "距离下次照看")
	}
}
func gameJobLabel(locale string, j *domain.WorkGameJobConfig) string {
	if j == nil {
		return workText(locale, "Open Slot", "空闲岗位")
	}
	return workText(locale, j.NameEn, j.NameZh)
}
func workSeconds(locale string, seconds int64) string {
	if seconds <= 0 {
		return workText(locale, "soon", "即将到达")
	}
	minutes := int64(math.Ceil(float64(seconds) / 60))
	if locale == "en-US" {
		if minutes < 60 {
			return fmt.Sprintf("%d min", minutes)
		}
		return fmt.Sprintf("%d hr", int64(math.Ceil(float64(minutes)/60)))
	}
	if minutes < 60 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	return fmt.Sprintf("%d 小时", int64(math.Ceil(float64(minutes)/60)))
}
