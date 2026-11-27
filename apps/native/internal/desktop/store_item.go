package desktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type StoreItemView struct {
	key         string
	preferences domain.UIPreferences
	item        *domain.PetStoreCatalogItemState
	source      string
}

var itemTypes = map[string][2]string{
	"pet": {"宠物", "Pet"}, "cosmetic": {"装扮", "Cosmetic"}, "theme": {"场景", "Scene"},
	"tool": {"道具", "Tool"}, "food": {"食物", "Food"}, "seed": {"种子", "Seed"}, "none": {"空奖", "No Prize"},
}

var itemRarityColors = map[string]string{
	"first_meet": "#7b8a9a", "familiar": "#3f9b62", "grow_together": "#2f80ed",
	"deep_bond": "#8b5cf6", "forever_partner": "#d97706", "bond_forever": "#e11d48",
}

func (v *StoreItemView) typeLabel() string {
	label, ok := itemTypes[v.item.Item.ItemType]
	if !ok {
		return localized(v.preferences.Locale, "徽章", "Badge")
	}
	return localized(v.preferences.Locale, label[0], label[1])
}

func (v *StoreItemView) selectItem(store *domain.PetStoreState) {
	v.item, v.source = nil, ""
	if store == nil {
		return
	}
	for _, entry := range store.Catalog {
		if entry.Item.ItemKey == v.key {
			v.item = &entry
			break
		}
	}
	for _, entry := range store.Inventory {
		if entry.ItemKey == v.key {
			v.source = entry.Source
			break
		}
	}
}

func (v *StoreItemView) title() string {
	name := v.key
	if v.item != nil {
		name = localized(v.preferences.Locale, v.item.Item.NameZh, v.item.Item.NameEn)
	}
	return presentation.Format(v.preferences.Locale, "windows.petStoreItemTitle", map[string]any{"title": name})
}

func (v *StoreItemView) preview(c *ui.Context, labels *PetView, colors presentation.Palette, price string, size float32) {
	item := v.item.Item
	card := ui.Box(c.Key(item.ItemKey)).Size(size, size).Focusable().Label(labels.text("3D 商品预览", "3D item preview"))
	target := float32(0)
	if card.Hovered() || card.FocusVisible() {
		target = 180
	}
	angle := card.AnimateWith("flip", target, 720*time.Millisecond, ui.CubicBezier(.2, .72, .18, 1))
	back := angle >= 90
	if back {
		angle -= 180
	}
	accent := petAccent(item.ItemType)
	card.Children(func() {
		face := ui.Column(c).Fill().Center().Radius(8).Border(1, colors.Separator.Mix(accent, .34))
		face.Gradient(colors.Theme.Surface.Mix(ui.Hex("#ffffff"), .08), colors.Subpanel, 145).Shadow(0, 18, 40, 0, ui.RGBA(0, 0, 0, .16))
		face.Transform(ui.RotationY(angle).Then(ui.Perspective(1100))).Children(func() {
			ui.Box(c).Absolute().Fill().Radius(8).ZIndex(-1).RadialGradient(ui.RadialGradient{Center: ui.Point{X: .5, Y: .24}, Stops: []ui.GradientStop{{Color: accent.Alpha(.24)}, {Color: accent.Alpha(0), At: .58}}})
			if back {
				ui.Column(c).Fill().Padding(24).Gap(8).Children(func() {
					ui.Row(c).Grow(1).Center().Children(func() { ui.Text(c, v.typeLabel()).FontSize(12).TextColor(c.Theme().TextMuted) })
					ui.Row(c).Grow(1).Center().Children(func() { ui.Text(c, labels.stage(item.Rarity)).FontSize(24).Bold() })
					ui.Row(c).Grow(1).Center().Children(func() { ui.Text(c, price).FontSize(12).Bold() })
				})
				return
			}
			artSize := min(size*.78, 260)
			ui.Image(c, presentation.PetArt(item.AssetKey)).Size(artSize, artSize).Fit(ui.Contain).Label(labels.text(item.NameZh, item.NameEn))
		})
	})
	ui.Text(c, labels.text("悬停或聚焦图片可查看 3D 翻转背面。", "Hover or focus to flip the item card.")).MaxWidth(320).TextAlign(ui.Center).FontSize(12).TextColor(c.Theme().TextMuted)
}

func (v *StoreItemView) View(c *ui.Context) {
	labels := PetView{Locale: v.preferences.Locale}
	text := labels.text
	colors := presentation.PreferenceColors(c.Theme().Dark, v.preferences)
	ui.Themed(c, &colors.Theme).Fill().Children(func() {
		background := colors.Content
		if !colors.Theme.Dark {
			background = ui.Hex("#f3f5f8")
		}
		ui.Scroll(c).Fill().Background(background).Children(func() {
			if v.item == nil {
				ui.Text(c, text("无法找到该商品详情。", "Item details are unavailable.")).Padding(20)
				return
			}
			item := v.item.Item
			price := text("免费", "Free")
			if item.PriceLP > 0 {
				price = fmt.Sprintf("%d LP", item.PriceLP)
			}
			width, height := c.Size()
			narrow := width <= 760
			var layout ui.Element
			previewWidth := (width - 44 - 26) * .44
			columns := 2
			if narrow {
				layout = ui.Column(c).Padding(14)
				previewWidth = width - 28
				columns = 1
			} else {
				layout = ui.Row(c).Padding(22).MinHeight(height)
			}
			layout.FillWidth().Gap(26).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Width(previewWidth).Padding(26).Gap(14).Center().Children(func() {
					v.preview(c, &labels, colors, price, min(360, max(1, previewWidth-52)))
				})
				ui.Column(c).Grow(1).MinWidth(0).Padding(30).Gap(18).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						badge := func(label string, color ui.Color) {
							ui.Text(c, label).Padding(4, 8).FontSize(12).LineHeight(1.3).TextColor(color).Radius(12).Border(1, color.Alpha(.26)).Background(color.Alpha(.08))
						}
						rarityColor := c.Theme().TextMuted
						if code := itemRarityColors[item.Rarity]; code != "" {
							rarityColor = ui.Hex(code)
						}
						badge(v.typeLabel(), c.Theme().TextMuted)
						badge(labels.stage(item.Rarity), rarityColor)
					})
					ui.Text(c, text(item.NameZh, item.NameEn)).FontSize(28).LineHeight(1.2).Bold().Role(ui.RoleHeading).Level(1)
					ui.Text(c, text(item.DescriptionZh, item.DescriptionEn)).FillWidth().FontSize(14).LineHeight(1.75).TextColor(c.Theme().TextMuted)
					if reason := text(v.item.LockedReasonZh, v.item.LockedReasonEn); reason != "" {
						ui.Text(c, reason).FillWidth().Padding(10, 12).FontSize(13).Radius(8).Border(1, c.Theme().Warning.Alpha(.26)).Background(c.Theme().Warning.Alpha(.09))
					}
					ui.Grid(c).FillWidth().Columns(columns).Gap(10).Children(func() {
						stat := func(label, value string) {
							ui.Column(c).MinWidth(0).MinHeight(58).Padding(10, 12).Justify(ui.Center).Gap(4).Radius(8).Border(1, colors.Separator).Background(c.Theme().Surface).Children(func() {
								ui.Text(c, label).FontSize(12).LineHeight(1.45).TextColor(c.Theme().TextMuted)
								ui.Text(c, value).FontSize(14).LineHeight(1.35).Bold()
							})
						}
						stat(text("分类", "Type"), v.typeLabel())
						stat(text("羁绊阶梯", "Rarity"), labels.stage(item.Rarity))
						status := text("可购买", "Available")
						switch {
						case v.item.Status == "daily_limit":
							status = labels.purchaseLabel(*v.item)
						case v.item.Equipped:
							status = text("已装备", "Equipped")
						case v.item.Owned:
							status = text("已拥有", "Owned")
						case v.item.Status == "locked" || v.item.Status == "achievement" || v.item.Status == "insufficient" || v.item.Status == "coming_soon":
							status = labels.purchaseLabel(*v.item)
						}
						stat(text("状态", "Status"), status)
						stat(text("价格", "Price"), price)
						stat(text("持有", "Owned"), fmt.Sprint(v.item.Quantity))
						stat(text("等级要求", "Required level"), fmt.Sprintf("Lv.%d", item.LevelGate))
						stage, milestone := text("无", "None"), item.MilestoneGate
						if item.StageGate != "" {
							stage = labels.stage(item.StageGate)
						}
						if milestone == "" {
							milestone = text("无", "None")
						}
						stat(text("阶段要求", "Stage gate"), stage)
						stat(text("里程碑", "Milestone"), milestone)
						if v.source != "" {
							stat(text("来源", "Source"), presentation.PetSourceLabel(v.preferences.Locale, v.source))
						}
						if v.item.GrowthValue > 0 {
							stat(text("成长值", "Growth"), fmt.Sprintf("%s +%d", text("成长值", "Growth"), v.item.GrowthValue))
						}
					})
				})
			})
		})
	})
}

func (w *Workspace) refreshStoreItem() {
	if w.itemWindow == nil {
		return
	}
	w.itemView.preferences = w.preferences
	w.itemView.selectItem(w.pet.Client.Store)
	w.itemWindow.SetTitle(w.itemView.title())
	w.itemWindow.Invalidate()
}

func (w *Workspace) openStoreItem(key string) {
	if w.closed || w.pet == nil {
		return
	}
	if w.itemWindow != nil {
		w.itemView.key = key
		w.refreshStoreItem()
		w.itemWindow.Focus()
		return
	}
	view := &StoreItemView{key: key, preferences: w.preferences}
	view.selectItem(w.pet.Client.Store)
	window := newDesktopWindow(mygo.WindowOptions{Title: view.title(), Width: 920, Height: 640, MinWidth: 760, MinHeight: 520, Content: ui.View(view.View)})
	w.itemView, w.itemWindow = view, window
	w.pet.Client.SetVisible(true)
	window.OnClosed(func() {
		if w.itemWindow == window {
			w.itemView, w.itemWindow = nil, nil
			w.pet.Client.SetVisible(PetRouteAvailable(w.tab))
		}
	})
}
