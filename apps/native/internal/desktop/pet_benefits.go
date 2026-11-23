package desktop

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func (v *PetView) blindBox(c *ui.Context) {
	s := v.Client.BlindBox
	if s == nil {
		ui.Text(c, v.text("正在加载每日盲盒…", "Loading blind box…"))
		return
	}
	petPanel(c, func() {
		ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(6).Children(func() {
				ui.Text(c, v.text("每日盲盒", "Daily Blind Box")).FontSize(24).Bold().Role(ui.RoleHeading).Level(1)
				ui.Text(c, v.text("每天最多开启 10 次伙伴盲盒，奖励来自宠物商店内容，但不包含宠物本体。", "Open up to ten companion boxes each day. Rewards come from the pet store, excluding pets.")).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
			})
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, v.text("今日剩余", "Remaining Today")).FontSize(11)
				ui.Text(c, fmt.Sprint(s.RemainingToday)).FontSize(30).Bold()
				ui.Text(c, fmt.Sprintf("%d/%d", s.UsedToday, s.DailyLimit)).FontSize(12)
			})
		})
	})
	width, _ := c.Size()
	layout := ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Start)
	if width <= 1080 {
		layout.Column().AlignItems(ui.Stretch)
	}
	layout.Children(func() {
		main := ui.Column(c).MinWidth(0)
		if width <= 1080 {
			main.FillWidth()
		} else {
			main.Grow(1).Basis(0)
		}
		main.Children(func() {
			petPanel(c, func() {
				ui.Column(c).FillWidth().Gap(14).Children(func() {
					ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
						if !v.Client.Busy {
							v.drawStart = time.Time{}
						}
						drawing := !v.drawStart.IsZero()
						label := v.text("开始抽奖", "Start Draw")
						if drawing {
							label = v.text("抽奖中…", "Drawing…")
						} else if s.RemainingToday <= 0 {
							label = v.text("今日次数已用完", "No Attempts Left")
						}
						ui.Box(c).FillWidth().Height(430).Children(func() {
							v.drawScene(c, s.Pool, drawing)
							gold := ui.Hex("#f6c04f")
							button := ui.Button(c, label).Absolute().LeftPercent(50).TranslatePercent(-50, 0).Bottom(30).MinWidth(128).MinHeight(38)
							button.Background(palette(c).Panel.Mix(gold, .14)).Border(1, palette(c).Separator.Mix(gold, .38)).Shadow(0, 10, 28, 0, gold.Alpha(.18))
							if button.Disabled(v.Client.Busy || s.RemainingToday <= 0).Clicked() {
								v.drawStart = c.Now()
								v.Client.Draw()
							}
						})
					})
					if r := v.Client.LastDraw; r != nil && v.drawStart.IsZero() {
						ui.Text(c, v.text("最新奖励", "Latest Reward")).FontSize(12).TextColor(c.Theme().TextMuted)
						ui.Text(c, v.text(r.Prize.NameZh, r.Prize.NameEn)).FontSize(22).Bold().Role(ui.RoleStatus)
						copy := v.text(r.Prize.DescriptionZh, r.Prize.DescriptionEn)
						if r.Duplicate {
							copy = fmt.Sprintf(v.text("已拥有，自动转为 %d LP 补偿。", "Already owned. Converted to %d LP."), r.Draw.DuplicateCompensationLP)
						}
						ui.Text(c, copy).FontSize(12).FillWidth()
					}
					ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
						ui.Text(c, v.text("今日抽取结果", "Today Results")).FontSize(16).Bold()
						ui.Text(c, fmt.Sprintf("%d/%d", blindBoxTodayDrawCount(s), s.DailyLimit)).FontSize(12).TextColor(c.Theme().TextMuted)
					})
					count := 0
					history := slices.Clone(s.History)
					slices.SortStableFunc(history, func(left, right domain.PetBlindBoxDrawEntry) int {
						return petDrawTimeCompare(left.CreatedAt, right.CreatedAt)
					})
					for _, draw := range history {
						if draw.DrawDate != s.DrawDate {
							continue
						}
						count++
						item, ok := v.blindBoxHistoryItem(s, draw.ItemKey)
						name, asset := v.itemName(draw.ItemKey), "gift_box"
						if ok {
							name, asset = v.text(item.NameZh, item.NameEn), item.AssetKey
						}
						ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
							ui.Image(c, presentation.PetArt(asset)).Size(36, 36).Fit(ui.Contain).Label(name)
							ui.Text(c, name).Grow(1).MinWidth(0).FontSize(12)
							ui.Text(c, createdText(v.Locale, draw.CreatedAt)).FontSize(11)
							if draw.DuplicateCompensationLP > 0 {
								ui.Text(c, fmt.Sprintf("+%d LP", draw.DuplicateCompensationLP)).FontSize(11)
							}
						})
					}
					if count == 0 {
						ui.Text(c, v.text("今日还没有抽取记录。", "No opens today.")).FontSize(12).TextColor(c.Theme().TextMuted)
					}
				})
			})
			petPanel(c, func() {
				ui.Text(c, v.text("完整奖池", "Full Prize Pool")).FontSize(16).Bold()
				cols := max(1, int((shellContentWidth(c)-16-28+8)/160))
				ui.Grid(c).FillWidth().Columns(cols).Gap(8).Children(func() {
					for _, prize := range s.Pool {
						item := prize.Item
						if catalogItem, ok := v.blindBoxHistoryItem(s, prize.Item.ItemKey); ok {
							item = catalogItem
						}
						ui.Column(c.Key(prize.Item.ItemKey)).FillWidth().FillHeight().MinWidth(0).Padding(10).Gap(5).Radius(6).Background(palette(c).Subpanel).Children(func() {
							ui.Image(c, presentation.PetArt(item.AssetKey)).FillWidth().Height(80).Fit(ui.Contain)
							ui.Text(c, v.text(item.NameZh, item.NameEn)).FontSize(11).Bold()
							ui.Text(c, v.typeName(item.ItemType)+" · "+v.stage(item.Rarity)).FontSize(10).TextColor(c.Theme().TextMuted)
							ui.Text(c, v.text(item.DescriptionZh, item.DescriptionEn)).FillWidth().FontSize(10).LineHeight(1.4).TextColor(c.Theme().TextMuted)
							if prize.Owned {
								ui.Text(c, v.text("已拥有", "Owned")).FontSize(10)
							}
						})
					}
				})
			})
		})
		sidebar := ui.Column(c).Width(320).Shrink(0).Gap(14)
		if width <= 1080 {
			sidebar.FillWidth()
		}
		sidebar.Children(func() {
			petPanel(c, func() {
				ui.Text(c, v.text("规则", "Rules")).FontSize(16).Bold()
				v.stat(c, v.text("每日次数", "Daily Attempts"), fmt.Sprint(s.DailyLimit))
				v.stat(c, v.text("奖池数量", "Pool Items"), fmt.Sprint(len(s.Pool)))
				v.stat(c, v.text("不包含", "Excluded"), v.text("宠物", "Pets"))
				separator(c)
				ui.Text(c, v.text("重复规则", "Duplicate Rule")).FontSize(11).TextColor(c.Theme().TextMuted)
				ui.Text(c, v.text("消耗品叠加，已拥有收藏品转 LP。", "Consumables stack, owned collectibles convert to LP.")).FillWidth().FontSize(11).LineHeight(1.45)
			})
			petPanel(c, func() {
				ui.Text(c, v.text("最近开启", "Recent Opens")).FontSize(16).Bold()
				history := slices.Clone(s.History)
				slices.SortStableFunc(history, func(left, right domain.PetBlindBoxDrawEntry) int {
					return petDrawTimeCompare(right.CreatedAt, left.CreatedAt)
				})
				if len(history) == 0 {
					ui.Text(c, v.text("暂无开启记录。", "No history yet.")).FontSize(12).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
				}
				for index, draw := range history {
					item, ok := v.blindBoxHistoryItem(s, draw.ItemKey)
					name := v.itemName(draw.ItemKey)
					asset := "gift_box"
					meta := v.typeName(draw.ItemType)
					if ok {
						name = v.text(item.NameZh, item.NameEn)
						asset = item.AssetKey
						meta = v.typeName(item.ItemType) + " · " + v.stage(item.Rarity)
					}
					key := draw.ID
					if key == "" {
						key = fmt.Sprintf("%s-%d", draw.ItemKey, index)
					}
					ui.Row(c.Key(key)).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Image(c, presentation.PetArt(asset)).Size(36, 36).Fit(ui.Contain).Label(name)
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, name).FillWidth().FontSize(12).Bold().SingleLine().Tooltip(name)
							ui.Text(c, meta).FillWidth().FontSize(10).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(meta)
							ui.Text(c, createdText(v.Locale, draw.CreatedAt)).FontSize(10).TextColor(c.Theme().TextMuted)
						})
						amount := v.text("空", "Empty")
						if draw.DuplicateCompensationLP > 0 {
							amount = fmt.Sprintf("+%d LP", draw.DuplicateCompensationLP)
						} else if draw.ItemType != "none" {
							amount = fmt.Sprintf("×%d", draw.Quantity)
						}
						ui.Text(c, amount).FontSize(10).TextColor(c.Theme().TextMuted)
					})
				}
			})
		})
	})
}

func blindBoxPrize(pool []domain.PetBlindBoxPoolItem, key string) (domain.PetBlindBoxPoolItem, bool) {
	for _, prize := range pool {
		if prize.Item.ItemKey == key {
			return prize, true
		}
	}
	return domain.PetBlindBoxPoolItem{}, false
}

func (v *PetView) blindBoxHistoryItem(state *domain.PetBlindBoxState, key string) (domain.PetStoreCatalogItem, bool) {
	if v.Client != nil && v.Client.Store != nil {
		for _, entry := range v.Client.Store.Catalog {
			if entry.Item.ItemKey == key {
				return entry.Item, true
			}
		}
	}
	if prize, ok := blindBoxPrize(state.Pool, key); ok {
		return prize.Item, true
	}
	return domain.PetStoreCatalogItem{}, false
}

func blindBoxTodayDrawCount(state *domain.PetBlindBoxState) int {
	count := 0
	for _, draw := range state.History {
		if draw.DrawDate == state.DrawDate {
			count++
		}
	}
	return count
}

func petDrawTimeCompare(left, right string) int {
	leftTime, leftErr := time.Parse(time.RFC3339Nano, left)
	rightTime, rightErr := time.Parse(time.RFC3339Nano, right)
	if leftErr != nil || rightErr != nil {
		return 0
	}
	return leftTime.Compare(rightTime)
}
func (v *PetView) redeem(c *ui.Context) {
	petPanel(c, func() {
		ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(6).Children(func() {
				ui.Text(c, v.text("兑换中心", "Redeem Center")).FontSize(24).Bold().Role(ui.RoleHeading).Level(1)
				ui.Text(c, v.text("LP、成长值与道具奖励", "LP, growth, and item rewards")).FontSize(12).TextColor(c.Theme().TextMuted)
			})
			ui.Text(c, fmt.Sprintf("%d LP", v.Client.Store.Wallet.Balance)).FontSize(26).Bold()
		})
	})
	width, _ := c.Size()
	layout := ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Start)
	if width <= 1080 {
		layout.Column().AlignItems(ui.Stretch)
	}
	layout.Children(func() {
		main := ui.Column(c).MinWidth(0)
		if width <= 1080 {
			main.FillWidth()
		} else {
			main.Grow(1).Basis(0)
		}
		main.Children(func() {
			petPanel(c, func() {
				ui.Text(c, v.text("兑换 Key", "Redeem Key")).FontSize(17).Bold()
				ui.TextArea(c, &v.key).FillWidth().Height(150).Label(v.text("兑换 Key", "Redemption key")).Placeholder("LIB1… / LIB2…").Disabled(v.Client.Busy)
				ui.Text(c, v.text("短码请由发行方使用本地工具换发为签名 Key。", "Ask the issuer to locally reissue short codes as signed keys.")).FillWidth().FontSize(11).TextColor(c.Theme().TextMuted)
				label := v.text("立即兑换", "Redeem")
				if v.Client.PendingAction == "redeem" {
					label = v.text("兑换中…", "Redeeming…")
				}
				key := domain.NormalizePetRedemption(v.key)
				if ui.PrimaryButton(c, label).Disabled(v.Client.Busy || key == "").Clicked() {
					v.awaitAction()
					v.Client.Redeem(key)
				}
				if result := v.Client.LastRedeem; result != nil {
					separator(c)
					status := v.text("兑换成功。", "Redeemed successfully.")
					if result.Duplicate {
						status = v.text("此 Key 已经兑换过，不会重复发放奖励。", "This key has already been redeemed; rewards were not granted again.")
					}
					ui.Text(c, status).TextColor(c.Theme().Success).FontSize(12).Role(ui.RoleStatus)
					v.stat(c, v.text("活动", "Campaign"), result.Redemption.CampaignID)
					v.redeemRewards(c, result.Rewards)
				}
			})
		})
		sidebar := ui.Column(c).Width(320).Shrink(0)
		if width <= 1080 {
			sidebar.FillWidth()
		}
		sidebar.Children(func() {
			petPanel(c, func() {
				ui.Text(c, v.text("历史记录", "History")).FontSize(17).Bold()
				if len(v.Client.Redemptions) == 0 {
					ui.Text(c, v.text("暂无兑换记录。", "No records yet.")).FontSize(12).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
				}
				for _, redemption := range v.Client.Redemptions {
					ui.Column(c.Key(redemption.ID)).FillWidth().Gap(5).Padding(6, 0).Children(func() {
						ui.Text(c, redemption.CampaignID).FontSize(13).Bold()
						ui.Text(c, createdText(v.Locale, redemption.RedeemedAt)).FontSize(11).TextColor(c.Theme().TextMuted)
						ui.Text(c, redemption.CodePrefix).FontSize(10)
						var rewards domain.PetRedeemKeyRewards
						if json.Unmarshal([]byte(redemption.RewardJson), &rewards) == nil {
							v.redeemRewards(c, rewards)
						}
						ui.Text(c, v.text("已兑换", "Redeemed")).FontSize(10).TextColor(c.Theme().Success)
						separator(c)
					})
				}
			})
		})
	})
}
func (v *PetView) redeemRewards(c *ui.Context, r domain.PetRedeemKeyRewards) {
	if r.LP > 0 {
		ui.Text(c, fmt.Sprintf("+%d LP", r.LP)).FontSize(12)
	}
	if r.GrowthValue > 0 {
		ui.Text(c, fmt.Sprintf(v.text("成长 +%d", "Growth +%d"), r.GrowthValue)).FontSize(12)
	}
	for _, item := range r.Items {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Image(c, v.art(item.ItemKey)).Size(28, 28).Fit(ui.Contain)
			ui.Text(c, fmt.Sprintf("%s ×%d", v.itemName(item.ItemKey), item.Quantity)).FontSize(11)
		})
	}
}
