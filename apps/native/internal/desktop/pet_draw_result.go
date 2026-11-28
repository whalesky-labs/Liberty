package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func (v *PetView) blindBoxResultDialog(c *ui.Context) {
	if v.Route != "/daily-blind-box" {
		return
	}
	// A committed draw has its own revision. Reads and redraws must not reopen
	// a dismissed result, and a failed follow-up read must not hide the prize.
	if !v.Client.Busy && v.Client.LastAction == "draw" && v.Client.LastDraw != nil && v.Client.ActionRevision > v.drawResultRevision {
		v.drawResultRevision = v.Client.ActionRevision
		v.drawResult = v.Client.LastDraw
		v.drawResultOpen = true
		v.drawResultReturnFocus = true
	}
	if v.drawResult == nil {
		return
	}
	result := v.drawResult
	width, height := c.Size()
	title := v.text("抽奖结果", "Blind-box result")
	name := v.text(result.Prize.NameZh, result.Prize.NameEn)
	amount := fmt.Sprintf("×%d", result.Draw.Quantity)
	message := v.text("已放入库存，可在宠物商店查看。", "Added to your inventory. Find it in the pet store.")
	switch {
	case result.Prize.ItemType == "none":
		amount = v.text("本次未获得物品", "No item this time")
		message = v.text("本次结果已记录，消耗 1 次今日机会。", "This result was recorded and used one daily attempt.")
	case result.Duplicate:
		amount = fmt.Sprintf("+%d LP", result.Draw.DuplicateCompensationLP)
		message = v.text("已拥有这件收藏品，重复奖励已转换为 LP。", "You already own this collectible. The duplicate was converted to LP.")
	}
	ui.Modal(c.Key("blind-box-result"), &v.drawResultOpen, func() {
		ui.Column(c).Width(min(float32(400), max(float32(0), width-80))).Gap(18).Children(func() {
			ui.Text(c, title).FontSize(18).Role(ui.RoleHeading).Level(2)
			contentScroll(c).FillWidth().MaxHeight(max(float32(120), height-200)).Children(func() {
				ui.Column(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
					ui.Box(c).FillWidth().Height(156).Radius(12).Background(palette(c).Subpanel).Center().Children(func() {
						ui.Image(c, presentation.PetArt(result.Prize.AssetKey)).Size(120, 120).Fit(ui.Contain).Label(name)
					})
					ui.Text(c, name).FillWidth().TextAlign(ui.Center).FontSize(24).Role(ui.RoleHeading).Level(3)
					ui.Text(c, amount).FillWidth().TextAlign(ui.Center).FontSize(20).TextColor(palette(c).NavAccent)
					ui.Text(c, v.text(result.Prize.DescriptionZh, result.Prize.DescriptionEn)).FillWidth().TextAlign(ui.Center).FontSize(13).TextColor(c.Theme().TextMuted)
					ui.Text(c, message).FillWidth().TextAlign(ui.Center).FontSize(13).Role(ui.RoleStatus)
					ui.Text(c, fmt.Sprintf(v.text("今日剩余 %d 次", "%d attempts left today"), result.State.RemainingToday)).FillWidth().TextAlign(ui.Center).FontSize(12).TextColor(c.Theme().TextMuted)
				})
			})
			if ui.PrimaryButton(c, v.text("知道了", "Done")).FillWidth().AutoFocus().Clicked() {
				v.drawResultOpen = false
			}
		})
	}).Label(title)
}
