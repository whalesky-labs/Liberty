package desktop

import (
	"fmt"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"
)

func (v *SettingsView) performanceDisplay(c *ui.Context) {
	settingsGroup(c, v.msg("performanceDisplay"), func() {
		v.row(c, "performanceIndicators", "performanceIndicatorsHint", func() {
			shown := !v.Client.Draft.HidePerformanceIndicators
			if ui.Switch(c, &shown).Label(v.msg("performanceIndicators")).Changed() {
				v.Client.Draft.HidePerformanceIndicators = !shown
				v.Client.Save()
			}
		})
	})
}

func (v *SettingsView) experiments(c *ui.Context) {
	settingsGroup(c, v.msg("memoryUsage"), func() {
		ui.Column(c).FillWidth().Padding(14, 0).Gap(12).Children(func() {
			ui.Text(c, v.msg("memoryScope")).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
			m := v.memoryUsage.Read(time.Now())
			heap, stack, metadata, other := c.Theme().Accent, c.Theme().Accent.Mix(c.Theme().Danger, .5), c.Theme().Success, c.Theme().Warning
			tiles := []memoryTile{
				{v.msg("memoryObjects"), m.Objects, heap},
				{v.msg("memoryFree"), m.Free, heap},
				{v.msg("memoryUnused"), m.Unused, heap},
				{v.msg("memoryStack"), m.Stacks, stack},
				{v.msg("memoryMetadata"), m.Metadata, metadata},
				{v.msg("memoryOther"), m.Other, other},
			}
			sort.SliceStable(tiles, func(i, j int) bool { return tiles[i].bytes > tiles[j].bytes })
			for len(tiles) > 0 && tiles[len(tiles)-1].bytes == 0 {
				tiles = tiles[:len(tiles)-1]
			}
			chart := ui.Column(c).FillWidth().Height(250).Clip().Role(ui.RoleGroup).Label(v.msg("memoryUsage"))
			chart.Children(func() {
				width := chart.Bounds().W
				if width <= 0 {
					width = shellContentWidth(c)
				}
				memoryTreemap(c, tiles, ui.Rect{W: 100, H: 100}, width, 250)
			})
			ui.Row(c).FillWidth().Wrap().Gap(14).Children(func() {
				for _, item := range []memoryTile{
					{v.msg("memoryHeap"), m.Objects + m.Free + m.Unused, heap},
					{v.msg("memoryStack"), m.Stacks, stack},
					{v.msg("memoryRuntime"), m.Metadata, metadata},
					{v.msg("memoryOther"), m.Other, other},
				} {
					ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(9, 9).Radius(2).Background(item.color)
						ui.Text(c, item.name+"  "+item.size()).FontSize(12)
					})
				}
			})
		})
	})
}

type memoryTile struct {
	name  string
	bytes uint64
	color ui.Color
}

func (t memoryTile) size() string { return fmt.Sprintf("%.1f MiB", float64(t.bytes)/(1<<20)) }

// Bisect the longer side near half the remaining bytes, preserving area.
func memoryTreemap(c *ui.Context, tiles []memoryTile, rect ui.Rect, width, height float32) {
	if len(tiles) == 0 {
		return
	}
	if len(tiles) == 1 {
		t := tiles[0]
		label := t.name + " · " + t.size()
		ui.Box(c).Absolute().LeftPercent(rect.X).TopPercent(rect.Y).WidthPercent(rect.W).HeightPercent(rect.H).Padding(1.5).Clip().Children(func() {
			box := ui.Column(c).Fill().Clip().Radius(7).Background(t.color.Alpha(.16)).Border(1, t.color.Alpha(.55)).Role(ui.RoleGroup).Label(label).Tooltip(label)
			if width*rect.W/100 >= 62 && height*rect.H/100 >= 40 {
				box.Padding(7).Children(func() {
					ui.Text(c, t.name).SingleLine().FontSize(12)
					ui.Text(c, t.size()).SingleLine().FontSize(11).TextColor(c.Theme().TextMuted)
				})
			}
		})
		return
	}
	var total uint64
	for _, t := range tiles {
		total += t.bytes
	}
	split, left := 1, tiles[0].bytes
	for split < len(tiles)-1 && 2*left+tiles[split].bytes < total {
		left += tiles[split].bytes
		split++
	}
	fraction := float32(left) / float32(total)
	first, second := rect, rect
	if width*rect.W >= height*rect.H {
		first.W *= fraction
		second.X += first.W
		second.W -= first.W
	} else {
		first.H *= fraction
		second.Y += first.H
		second.H -= first.H
	}
	memoryTreemap(c, tiles[:split], first, width, height)
	memoryTreemap(c, tiles[split:], second, width, height)
}
