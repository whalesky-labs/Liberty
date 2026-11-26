package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func (v *PetView) store(c *ui.Context) {
	s := v.Client.Store
	if s == nil {
		return
	}
	if v.section == "" {
		v.section = "store"
	}
	if v.category == "" {
		v.category = "all"
	}
	petPanel(c, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(14).Children(func() {
			ui.Column(c).Grow(1).Gap(5).Children(func() {
				ui.Text(c, v.text("宠物商店", "Pet Store")).FontSize(24).Bold().Role(ui.RoleHeading).Level(1)
				ui.Text(c, v.text("用每一次专注工作的积累，换来伙伴的小小惊喜。", "Turn your work rewards into little surprises for your companion.")).FontSize(12).TextColor(c.Theme().TextMuted)
			})
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, "LP").FontSize(12)
				ui.Text(c, fmt.Sprint(s.Wallet.Balance)).FontSize(26).Bold()
				ui.Text(c, v.text("当前余额", "Balance")).FontSize(11)
			})
		})
	})
	if g := v.Client.LastGift; g != nil && v.Client.LastAction == "gift" && !v.Client.Busy && (v.Client.Error == nil || v.Client.ErrorIsRefresh) {
		petPanel(c, func() {
			title := v.text("惊喜礼盒开出：", "Gift box opened: ") + v.text(g.Prize.NameZh, g.Prize.NameEn)
			if g.Duplicate {
				title += fmt.Sprintf(v.text(" · 重复补偿 %d LP", " · Duplicate compensation %d LP"), g.DuplicateCompensationLP)
			}
			ui.Text(c, title).FillWidth().TextColor(c.Theme().Success).Role(ui.RoleStatus)
		})
	}
	width, height := c.Size()
	layout := ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Start)
	mainWidth := max(1, shellContentWidth(c)-16-288-14)
	if width <= 1080 {
		layout.Column().AlignItems(ui.Stretch)
		mainWidth = max(1, shellContentWidth(c)-16)
	}
	layout.Children(func() {
		main := ui.Column(c).MinWidth(0).Gap(12)
		if width <= 1080 {
			main.FillWidth()
		} else {
			main.Grow(1).Basis(0)
		}
		main.Children(func() {
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				for _, tab := range []struct{ key, zh, en string }{{"store", "商品商店", "Store"}, {"inventory", "个人仓库", "Inventory"}} {
					if segmentButton(c, v.text(tab.zh, tab.en), v.section == tab.key).Clicked() {
						v.section = tab.key
					}
				}
			})
			ui.Row(c).FillWidth().Wrap().Gap(6).Children(func() {
				for _, category := range []string{"all", "pet", "cosmetic", "theme", "tool", "food", "seed", "badge"} {
					if segmentButton(c, v.typeName(category), v.category == category).Padding(6, 7).Clicked() {
						v.category = category
					}
				}
			})
			items := []domain.PetStoreCatalogItemState{}
			if v.section == "store" {
				for _, item := range s.Catalog {
					if v.category == "all" || v.category == item.Item.ItemType {
						items = append(items, item)
					}
				}
			} else {
				for _, inv := range s.Inventory {
					if inv.Quantity <= 0 || v.category != "all" && v.category != inv.ItemType {
						continue
					}
					item, ok := v.catalog(inv.ItemKey)
					if !ok {
						item = domain.PetStoreCatalogItem{ItemKey: inv.ItemKey, ItemType: inv.ItemType, Slot: inv.Slot, NameZh: inv.ItemKey, NameEn: inv.ItemKey, AssetKey: "gift_box"}
					}
					items = append(items, domain.PetStoreCatalogItemState{Item: item, Owned: true, Equipped: inv.Equipped, Quantity: inv.Quantity, GrowthValue: item.GrowthValue})
				}
			}
			if len(items) == 0 {
				ui.Text(c, v.text("当前分类暂无内容。", "No items in this category yet.")).FillWidth().Padding(30).Role(ui.RoleStatus)
			}
			columns := max(1, int((mainWidth+12)/(148+12)))
			filter := fmt.Sprintf("%s:%s:%d", v.section, v.category, columns)
			if filter != v.storeGridFilter {
				v.storeGrid, v.storeGridFilter = ui.ListState{}, filter
			}
			v.storeGrid.Key = func(row int) any { return items[row*columns].Item.ItemKey }
			ui.List(c, &v.storeGrid, (len(items)+columns-1)/columns, func(row int) {
				ui.Grid(c).FillWidth().Columns(columns).Gap(12).Children(func() {
					for i := row * columns; i < min(len(items), (row+1)*columns); i++ {
						v.storeCard(c, items[i])
					}
				})
			}).FillWidth().Height(max(400, min(850, height-300))).Gap(12).Label(v.text("商品列表", "Products"))
		})
		sidebar := ui.Column(c).Width(288).Shrink(0).Gap(12)
		if width <= 1080 {
			sidebar.FillWidth()
		}
		sidebar.Children(func() {
			petPanel(c, func() {
				ui.Text(c, v.text("商店概览", "Store Overview")).FontSize(15).Bold()
				available, locked := 0, 0
				for _, i := range s.Catalog {
					if i.Purchasable {
						available++
					}
					if i.Status == "locked" {
						locked++
					}
				}
				owned := 0
				for _, item := range s.Inventory {
					if item.Quantity > 0 {
						owned++
					}
				}
				for _, stat := range []struct{ zh, en, value string }{{"等级", "Level", fmt.Sprint(s.Profile.Level)}, {"LP 余额", "LP Balance", fmt.Sprint(s.Wallet.Balance)}, {"个人仓库", "Inventory", fmt.Sprint(owned)}, {"可购买", "Purchasable", fmt.Sprint(available)}, {"未解锁", "Locked", fmt.Sprint(locked)}} {
					v.stat(c, v.text(stat.zh, stat.en), stat.value)
				}
			})
			petPanel(c, func() {
				ui.Text(c, v.text("陪伴阶梯说明", "Bond Tiers")).FontSize(15).Bold()
				for _, stage := range domain.PetStages() {
					ui.Text(c, v.text(stage.LabelZh, stage.LabelEn)).FontSize(11).TextColor(c.Theme().TextMuted)
				}
			})
			petPanel(c, func() {
				ui.Text(c, v.text("当前装备", "Equipped")).FontSize(15).Bold()
				for _, row := range []struct {
					key, zh, en string
					item        *domain.PetInventoryItem
				}{{"accessory", "配饰", "Accessory", s.Equipment.Accessory}, {"scene", "场景", "Scene", s.Equipment.Scene}, {"badge", "徽章", "Badge", s.Equipment.Badge}} {
					ui.Text(c, v.text(row.zh, row.en)).FontSize(11).TextColor(c.Theme().TextMuted)
					if row.item == nil {
						ui.Text(c, v.text("未装备", "None equipped")).FontSize(12)
					} else {
						ui.Text(c, v.itemName(row.item.ItemKey)).FontSize(12)
						if textButton(c, v.text("取消装备", "Unequip")).Label(v.text("取消装备：", "Unequip: ") + v.itemName(row.item.ItemKey)).Disabled(v.Client.Busy).Clicked() {
							v.Client.Unequip(row.key)
						}
					}
				}
			})
			petPanel(c, func() {
				ui.Text(c, v.text("下一个解锁", "Next Unlock")).FontSize(15).Bold()
				locked := false
				for _, item := range s.Catalog {
					if item.Status == "locked" {
						ui.Text(c, v.itemName(item.Item.ItemKey)).FillWidth().FontSize(12).Bold()
						ui.Text(c, v.text(item.LockedReasonZh, item.LockedReasonEn)).FillWidth().FontSize(11).TextColor(c.Theme().TextMuted)
						locked = true
						break
					}
				}
				if !locked {
					ui.Text(c, v.text("暂无锁定目标", "All available")).FontSize(12)
				}
				ui.Text(c, v.text("里程碑", "Milestones")).FontSize(15).Bold()
				for _, counter := range s.Counters {
					v.stat(c, petMilestoneLabel(v.Locale, counter.CounterKey), fmt.Sprint(counter.CounterValue))
				}
			})
		})
	})
}
func (v *PetView) storeCard(c *ui.Context, state domain.PetStoreCatalogItemState) {
	item := state.Item
	card := ui.Column(c.Key(item.ItemKey)).Role(ui.RoleGroup).Label(v.text(item.NameZh, item.NameEn)).FillWidth().FillHeight().MinWidth(0).Radius(8).Clip().Border(1, palette(c).Separator).Background(palette(c).Panel).Children(func() {
		ui.Image(c, presentation.PetArt(item.AssetKey)).FillWidth().Height(150).Fit(ui.Contain).Background(petAccent(item.ItemType).Alpha(.08)).Label(v.text(item.NameZh, item.NameEn))
		ui.Column(c).FillWidth().Padding(12).Gap(7).Grow(1).Children(func() {
			ui.Text(c, v.text(item.NameZh, item.NameEn)).FontSize(14).Bold()
			ui.Text(c, v.typeName(item.ItemType)+" · "+v.stage(item.Rarity)).FontSize(10).TextColor(c.Theme().TextMuted)
			if state.Quantity > 0 {
				ui.Text(c, fmt.Sprintf(v.text("持有 ×%d", "Owned ×%d"), state.Quantity)).FontSize(11)
			}
			if state.GrowthValue > 0 {
				ui.Text(c, fmt.Sprintf(v.text("成长值 +%d", "Growth +%d"), state.GrowthValue)).FontSize(11).TextColor(c.Theme().Success)
			}
			description := v.text(item.DescriptionZh, item.DescriptionEn)
			if state.LockedReasonZh != "" {
				description = v.text(state.LockedReasonZh, state.LockedReasonEn)
			}
			ui.Text(c, description).FillWidth().FontSize(11).TextColor(c.Theme().TextMuted)
			ui.Box(c).Grow(1).MinHeight(0)
			if textButton(c, v.text("查看详情", "View Details")).Label(v.text("查看详情：", "View Details: ") + v.text(item.NameZh, item.NameEn)).Disabled(v.OpenItem == nil).Clicked() {
				v.OpenItem(item.ItemKey)
			}
		})
		separator(c)
		ui.Row(c).FillWidth().Wrap().Gap(4).Padding(6, 10).AlignItems(ui.Center).Children(func() {
			if v.section == "store" {
				price := fmt.Sprintf("%d LP", item.PriceLP)
				if item.ItemKey == domain.PetGiftBox {
					price = fmt.Sprintf(v.text("今日免费 %d/%d", "Free today %d/%d"), state.DailyFreeRemaining, state.DailyFreeLimit)
				}
				ui.Text(c, price).Grow(1).FontSize(11)
				if textButton(c, v.purchaseLabel(state)).Label(v.purchaseLabel(state) + ": " + v.text(item.NameZh, item.NameEn)).Disabled(v.Client.Busy || !state.Purchasable).Clicked() {
					v.openDialog("purchase", item.ItemKey)
				}
			} else {
				label := v.text("装备", "Equip")
				if state.Equipped {
					label = v.text("已装备", "Equipped")
				} else if item.Slot == "consumable" {
					label = v.text("使用", "Use")
				}
				if item.ItemKey == domain.PetMakeupTicket {
					label = v.text("去签到页使用", "Use in Check-in")
				}
				if item.ItemType == "seed" {
					label = v.text("去农场", "Go Farm")
				}
				ui.Text(c, fmt.Sprintf("×%d", state.Quantity)).Grow(1).FontSize(11)
				nav := item.ItemKey == domain.PetMakeupTicket || item.ItemType == "seed"
				if textButton(c, label).Label(label + ": " + v.text(item.NameZh, item.NameEn)).Disabled(v.Client.Busy || state.Equipped || nav && v.Navigate == nil).Clicked() {
					switch {
					case item.ItemKey == domain.PetMakeupTicket:
						v.Navigate("/daily-check-in")
					case item.ItemType == "seed":
						v.Navigate("/farm")
					case item.Slot == "consumable":
						v.openDialog("use", item.ItemKey)
					default:
						v.Client.Equip(item.ItemKey)
					}
				}
			}
		})
	})
	if card.DoubleClicked() && v.OpenItem != nil {
		v.OpenItem(item.ItemKey)
	}
}
func (v *PetView) purchaseLabel(s domain.PetStoreCatalogItemState) string {
	switch s.Status {
	case "achievement":
		return v.text("成就获得", "Achievement")
	case "locked":
		return v.text("未解锁", "Locked")
	case "daily_limit":
		return v.text("今日已领完", "Claimed")
	case "insufficient":
		return v.text("LP 不足", "Need LP")
	case "coming_soon":
		return v.text("即将开放", "Coming Soon")
	}
	if s.Item.ItemKey == domain.PetGiftBox && s.DailyFreeRemaining > 0 {
		return v.text("免费领取", "Free Claim")
	}
	if !s.Purchasable {
		if s.Equipped || s.Status == "equipped" {
			return v.text("已装备", "Equipped")
		}
		if s.Owned || s.Status == "owned" {
			return v.text("已拥有", "Owned")
		}
	}
	return v.text("购买", "Purchase")
}
func petMilestoneLabel(locale, key string) string {
	labels := map[string][2]string{
		"tasks_created": {"创建任务", "Tasks created"}, "transcriptions_completed": {"完成转写", "Transcriptions completed"},
		"summaries_completed": {"完成 AI 总结", "AI summaries completed"}, "exports_completed": {"导出结果", "Exports completed"},
		"active_days": {"活跃天数", "Active days"}, "dark_theme_days": {"深色主题使用天数", "Dark-theme days"},
	}
	if label, ok := labels[key]; ok {
		return localized(locale, label[0], label[1])
	}
	return localized(locale, "未知里程碑", "Unknown milestone")
}
func (v *PetView) typeName(kind string) string {
	labels := map[string][2]string{"all": {"全部", "All"}, "pet": {"宠物", "Pets"}, "cosmetic": {"装扮", "Cosmetics"}, "theme": {"场景", "Scenes"}, "tool": {"道具", "Tools"}, "food": {"食物", "Food"}, "seed": {"种子", "Seeds"}, "badge": {"徽章", "Badges"}, "none": {"未中奖", "No item"}}
	if s, ok := labels[kind]; ok {
		return v.text(s[0], s[1])
	}
	return v.text("物品", "Item")
}
func petAccent(kind string) ui.Color {
	colors := map[string]string{"pet": "#8f96a3", "cosmetic": "#ff7aa8", "theme": "#61b86b", "tool": "#f6c04f", "food": "#f58a4c", "seed": "#78b94c", "badge": "#5f7dff"}
	if color := colors[kind]; color != "" {
		return ui.Hex(color)
	}
	return ui.Hex("#8f96a3")
}
func (v *PetView) confirmation(c *ui.Context) {
	ui.Modal(c, &v.dialogOpen, func() {
		ui.Column(c).Width(360).MaxWidth(520).Gap(14).Children(func() {
			switch v.dialogKind {
			case "name":
				ui.Text(c, v.text("修改宠物名字", "Edit Pet Name")).FontSize(20).Bold()
				ui.Text(c, v.text("宠物名字", "Pet name")).FontSize(12)
				input := ui.TextInput(c, &v.name).FillWidth().Label(v.text("宠物名字", "Pet name")).MaxLength(24).AutoFocus().Disabled(v.Client.Busy)
				ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
					if ui.Button(c, v.text("取消", "Cancel")).Disabled(v.Client.Busy).Clicked() {
						v.dialogOpen = false
					}
					save := ui.PrimaryButton(c, v.text("保存", "Save")).Disabled(v.Client.Busy || strings.TrimSpace(v.name) == "")
					if (save.Clicked() || input.Submitted()) && !v.Client.Busy && strings.TrimSpace(v.name) != "" {
						name := []rune(strings.TrimSpace(v.name))
						if len(name) > 24 {
							name = name[:24]
						}
						v.awaitAction()
						v.Client.SaveName(string(name))
					}
				})
			case "restart":
				ui.Text(c, v.text("重新开始签到", "Restart check-in streak")).FontSize(20).Bold()
				ui.Text(c, v.text("确定从今日重新开始吗？当前断签缺口将不能再补签。", "Start again from today? Missed days in the current gap can no longer be repaired.")).FillWidth()
				ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
					if ui.Button(c, v.text("取消", "Cancel")).Clicked() {
						v.dialogOpen = false
					}
					if ui.PrimaryButton(c, v.text("从今日开始", "Restart Today")).Disabled(v.Client.Busy).Clicked() {
						v.awaitAction()
						v.Client.CheckInNow()
					}
				})
			default:
				item, ok := v.catalog(v.dialogItem)
				if !ok {
					ui.Text(c, v.text("无法找到该商品详情。", "Item details are unavailable."))
					return
				}
				ui.Text(c, v.text(item.NameZh, item.NameEn)).FontSize(20).Bold()
				ui.Image(c, presentation.PetArt(item.AssetKey)).FillWidth().Height(180).Fit(ui.Contain)
				ui.Text(c, v.text(item.DescriptionZh, item.DescriptionEn)).FillWidth().FontSize(12)
				maximum := v.maxQuantity(item, v.dialogKind)
				var quantityInput ui.Element
				if maximum > 1 {
					ui.Text(c, v.text("数量", "Quantity")).FontSize(12)
					quantityInput = ui.TextInput(c, &v.quantity).Width(130).Label(v.text("数量", "Quantity")).AutoFocus().Disabled(v.Client.Busy)
				}
				qty, err := strconv.ParseInt(strings.TrimSpace(v.quantity), 10, 64)
				valid := err == nil && qty > 0 && qty <= maximum
				if v.dialogKind == "purchase" {
					ui.Text(c, fmt.Sprintf(v.text("最多 %d 个 · 单价 %d LP", "Maximum %d · %d LP each"), maximum, item.PriceLP)).FontSize(12)
					if valid {
						v.stat(c, v.text("合计", "Total"), fmt.Sprintf("%d LP", qty*item.PriceLP))
					}
				} else {
					ui.Text(c, fmt.Sprintf(v.text("确认使用「%s」？", "Use %s?"), v.text(item.NameZh, item.NameEn))).FontSize(12)
					if valid && item.ItemType == "food" {
						v.stat(c, v.text("获得成长值", "Growth gained"), fmt.Sprintf("+%d", qty*item.GrowthValue))
					}
				}
				if !valid {
					message := fmt.Sprintf(v.text("请输入 1 至 %d 的整数。", "Enter a whole number from 1 to %d."), maximum)
					if maximum <= 0 {
						message = v.text("当前余额、库存或今日次数已变化，暂不能确认。请取消后刷新。", "Your balance, inventory or daily allowance has changed. Cancel and refresh before continuing.")
					}
					ui.Text(c, message).FillWidth().FontSize(12).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
					quantityInput.Invalid(true).Description(message)
				}
				ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
					if ui.Button(c, v.text("取消", "Cancel")).Disabled(v.Client.Busy).Clicked() {
						v.dialogOpen = false
					}
					label := v.text("确认购买", "Confirm Purchase")
					if v.dialogKind == "use" {
						label = v.text("确认使用", "Confirm Use")
					}
					confirm := ui.PrimaryButton(c, label).Disabled(v.Client.Busy || !valid)
					if (confirm.Clicked() || quantityInput.Submitted()) && !v.Client.Busy && valid {
						v.awaitAction()
						if v.dialogKind == "purchase" {
							v.Client.Purchase(item.ItemKey, qty)
						} else if item.ItemKey == domain.PetGiftBox {
							v.Client.GiftBox()
						} else {
							v.Client.Use(item.ItemKey, qty)
						}
					}
				})
			}
			if v.Client.Error != nil {
				ui.Text(c, v.errorMessage()).FillWidth().FontSize(12).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
			}
		})
	}).Label(v.text("宠物操作确认", "Companion action confirmation"))
}
func (v *PetView) maxQuantity(item domain.PetStoreCatalogItem, action string) int64 {
	if v.Client.Store == nil {
		return 0
	}
	if action == "use" {
		for _, i := range v.Client.Store.Inventory {
			if i.ItemKey == item.ItemKey {
				if item.ItemType == "food" {
					return min(99, i.Quantity)
				}
				return min(1, i.Quantity)
			}
		}
		return 0
	}
	for _, s := range v.Client.Store.Catalog {
		if s.Item.ItemKey == item.ItemKey {
			if !s.Purchasable {
				return 0
			}
			if item.Slot != "consumable" {
				return 1
			}
			if item.ItemKey == domain.PetGiftBox {
				return min(99, s.DailyFreeRemaining)
			}
			if item.PriceLP > 0 {
				return min(99, v.Client.Store.Wallet.Balance/item.PriceLP)
			}
			return 99
		}
	}
	return 0
}
