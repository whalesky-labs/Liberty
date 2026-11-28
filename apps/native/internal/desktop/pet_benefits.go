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
	v.benefitsHeader(c, v.text("每日盲盒", "Daily Blind Box"), fmt.Sprintf(v.text("每天 %d 次免费开启，给伙伴带来一点小惊喜。", "%d free opens each day, with a little surprise for your companion."), s.DailyLimit), v.text("今日剩余", "Remaining Today"), fmt.Sprint(s.RemainingToday))
	width := v.benefitsWidth(c)
	mainWidth := width
	if width >= 1000 {
		mainWidth -= 320
	}
	benefitsColumns(c, width, func() {
		benefitsPanel(c, func() {
			ui.Column(c).FillWidth().Gap(16).Children(func() {
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
						accent := c.Theme().Accent
						button := ui.Button(c, label).Absolute().LeftPercent(50).TranslatePercent(-50, 0).Bottom(30).MinWidth(128).MinHeight(38)
						button.Background(accent).TextColor(c.Theme().AccentText)
						if button.Disabled(v.Client.Busy || s.RemainingToday <= 0).Clicked() {
							v.drawStart = c.Now()
							v.Client.Draw()
						}
						if v.drawResultReturnFocus && !v.drawResultOpen && !v.Client.Busy {
							if s.RemainingToday > 0 {
								button.Focus()
							}
							v.drawResultReturnFocus = false
						}
					})
				})
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, v.text("今日抽取结果", "Today Results")).Grow(1).FontSize(17).Role(ui.RoleHeading).Level(2)
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
					benefitsEmpty(c, "gift", v.text("今日还没有抽取记录。", "No opens today."), v.text("开启后，获得的物品或 LP 补偿会显示在这里。", "Items and any duplicate LP compensation will appear here."))
				}
			})
		})
		benefitsPanel(c, func() {
			benefitsHeading(c, v.text("完整奖池", "Full Prize Pool"))
			cols := max(1, int((mainWidth-40+12)/180))
			ui.Grid(c).FillWidth().Columns(cols).Gap(12).Children(func() {
				for _, prize := range s.Pool {
					item := prize.Item
					if catalogItem, ok := v.blindBoxHistoryItem(s, prize.Item.ItemKey); ok {
						item = catalogItem
					}
					ui.Column(c.Key(prize.Item.ItemKey)).FillWidth().FillHeight().MinWidth(0).Padding(14).Gap(8).Radius(10).Background(palette(c).Subpanel).Children(func() {
						ui.Image(c, presentation.PetArt(item.AssetKey)).FillWidth().Height(80).Fit(ui.Contain)
						ui.Text(c, v.text(item.NameZh, item.NameEn)).FillWidth().FontSize(14).MaxLines(2).Tooltip(v.text(item.NameZh, item.NameEn))
						ui.Text(c, v.typeName(item.ItemType)+" · "+v.stage(item.Rarity)).FontSize(12).TextColor(c.Theme().TextMuted)
						ui.Text(c, v.text(item.DescriptionZh, item.DescriptionEn)).FillWidth().FontSize(12).LineHeight(1.4).TextColor(c.Theme().TextMuted)
						if prize.Owned {
							ui.Text(c, v.text("已拥有", "Owned")).FontSize(12)
						}
					})
				}
			})
		})
	}, func() {
		benefitsPanel(c, func() {
			benefitsHeading(c, v.text("规则", "Rules"))
			v.stat(c, v.text("每日次数", "Daily Attempts"), fmt.Sprint(s.DailyLimit))
			v.stat(c, v.text("奖池数量", "Pool Items"), fmt.Sprint(len(s.Pool)))
			v.stat(c, v.text("不包含", "Excluded"), v.text("宠物", "Pets"))
			separator(c)
			ui.Text(c, v.text("免费开启，每天重置；奖池包含空奖。", "Free to open, resets daily; the pool includes empty results.")).FillWidth().FontSize(13)
			ui.Text(c, v.text("重复规则", "Duplicate Rule")).FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Text(c, v.text("消耗品叠加，已拥有收藏品转 LP。", "Consumables stack, owned collectibles convert to LP.")).FillWidth().FontSize(12).LineHeight(1.45)
		})
		benefitsPanel(c, func() {
			benefitsHeading(c, v.text("最近开启", "Recent Opens"))
			history := slices.Clone(s.History)
			slices.SortStableFunc(history, func(left, right domain.PetBlindBoxDrawEntry) int {
				return petDrawTimeCompare(right.CreatedAt, left.CreatedAt)
			})
			if len(history) == 0 {
				benefitsEmpty(c, "gift", v.text("暂无开启记录。", "No history yet."), v.text("最近的盲盒结果会保留在这里。", "Your recent box results will be kept here."))
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
						ui.Text(c, name).FillWidth().FontSize(12).SingleLine().Tooltip(name)
						ui.Text(c, meta).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(meta)
						ui.Text(c, createdText(v.Locale, draw.CreatedAt)).FontSize(12).TextColor(c.Theme().TextMuted)
					})
					amount := v.text("空", "Empty")
					if draw.DuplicateCompensationLP > 0 {
						amount = fmt.Sprintf("+%d LP", draw.DuplicateCompensationLP)
					} else if draw.ItemType != "none" {
						amount = fmt.Sprintf("×%d", draw.Quantity)
					}
					ui.Text(c, amount).FontSize(12).TextColor(c.Theme().TextMuted)
				})
			}
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
	v.benefitsHeader(c, v.text("兑换中心", "Redeem Center"), v.text("使用兑换 Key，为伙伴领取 LP、成长值和道具。", "Use a redemption key to collect LP, growth and items for your companion."), v.text("LP 余额", "LP Balance"), fmt.Sprint(v.Client.Store.Wallet.Balance))
	benefitsColumns(c, v.benefitsWidth(c), func() {
		benefitsPanel(c, func() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(44, 44).Radius(12).Background(palette(c).Selected).Center().Children(func() {
					ui.Icon(c, presentation.Icon("key")).Size(22, 22).TextColor(palette(c).NavAccent)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					benefitsHeading(c, v.text("兑换 Key", "Redeem Key"))
					ui.Text(c, v.text("粘贴完整 Key，确认后领取奖励。", "Paste the complete key, then redeem your rewards.")).FillWidth().FontSize(13).TextColor(c.Theme().TextMuted)
				})
			})
			ui.TextArea(c, &v.key).FillWidth().Height(140).Label(v.text("兑换 Key", "Redemption key")).Placeholder("LIB1… / LIB2…").Disabled(v.Client.Busy)
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
		benefitsPanel(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
				benefitsHeading(c, v.text("历史记录", "History"))
				ui.Text(c, fmt.Sprint(len(v.Client.Redemptions))).FontSize(12).TextColor(c.Theme().TextMuted)
			})
			if len(v.Client.Redemptions) == 0 {
				benefitsEmpty(c, "key", v.text("暂无兑换记录。", "No records yet."), v.text("兑换成功后，可在这里查看领取时间和奖励。", "After redeeming, check the time and rewards here."))
			}
			for _, redemption := range v.Client.Redemptions {
				ui.Column(c.Key(redemption.ID)).FillWidth().Gap(8).Padding(8, 0).Children(func() {
					ui.Text(c, redemption.CampaignID).FillWidth().FontSize(14).SingleLine().Tooltip(redemption.CampaignID)
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
	}, func() {
		benefitsPanel(c, func() {
			benefitsHeading(c, v.text("兑换说明", "How redemption works"))
			for _, item := range [][2]string{
				{v.text("完整粘贴", "Paste the full key"), v.text("保留 Key 的全部字符，系统会处理多余空白。", "Keep every character. Extra whitespace is handled automatically.")},
				{v.text("一次领取", "Claim once"), v.text("同一 Key 在本机仅发放一次奖励。", "Each key grants rewards only once on this device.")},
				{v.text("奖励入库", "Rewards are saved"), v.text("LP 与成长值自动计入，道具可在宠物商店查看。", "LP and growth are added automatically. Find items in the pet store.")},
			} {
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					ui.Text(c, item[0]).FontSize(14)
					ui.Text(c, item[1]).FillWidth().FontSize(13).TextColor(c.Theme().TextMuted)
				})
			}
			separator(c)
			ui.Text(c, v.text("短码请由发行方使用本地工具换发为签名 Key。", "Ask the issuer to locally reissue short codes as signed keys.")).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
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
