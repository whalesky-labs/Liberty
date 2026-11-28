package desktop

import (
	"errors"
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func (v *ResultView) segmentCorrection(c *ui.Context, result *domain.Result, segment domain.Segment) {
	if v.Playback != nil && result.Job.Source == "local" && segment.EndMS > segment.StartMS && ui.Button(c, v.text("回听此句", "Listen to segment")).AlignSelf(ui.Start).Clicked() {
		v.Playback.Play(segment.ID)
	}
	busy := v.Actions == nil || v.Actions.Busy
	if v.Actions != nil && v.Actions.Correction != nil && v.Actions.Correction.ID == segment.ID {
		ui.TextArea(c, &v.Actions.CorrectionText).FillWidth().Height(100).Label(v.text("校对原文", "Correct transcript")).Disabled(busy).AutoFocus()
		ui.TextInput(c, &v.Actions.CorrectionSpeaker).FillWidth().Label(v.text("本句讲话人", "Speaker for this segment")).Placeholder(v.text("未指定", "Unassigned")).Disabled(busy)
		if v.Actions != nil && errors.Is(v.Actions.Error, domain.ErrTranscriptConflict) {
			ui.Text(c, v.text("此句已被修改。请保留需要的文字，取消后重新打开校对。", "This segment changed elsewhere. Keep any text you need, cancel and reopen the correction.")).FillWidth().TextColor(c.Theme().Danger).Role(ui.RoleAlert)
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, presentation.Message(v.Locale, "common.save")).Disabled(busy || strings.TrimSpace(v.Actions.CorrectionText) == "").Clicked() {
				speaker := strings.TrimSpace(v.Actions.CorrectionSpeaker)
				v.Actions.EditSegment(domain.SegmentEdit{Original: *v.Actions.Correction, Text: v.Actions.CorrectionText, Speaker: &speaker}, func() {
					v.Actions.CancelCorrection()
					v.Client.Refresh()
				})
			}
			if ui.Button(c, v.text("还原本次修改", "Reset changes")).Disabled(busy).Clicked() {
				v.Actions.ResetCorrection()
			}
			if ui.Button(c, presentation.Message(v.Locale, "common.cancel")).Disabled(busy).Clicked() {
				v.Actions.CancelCorrection()
			}
		})
		return
	}
	ui.Text(c, segment.Text).PreserveSpaces().Selectable()
	if v.EditingEnabled && result.CanEditTranscript() && ui.Button(c, v.text("校对此句", "Correct segment")).AlignSelf(ui.Start).Disabled(busy || v.Actions != nil && v.Actions.Correction != nil).Clicked() {
		v.Actions.BeginCorrection(segment)
	}
}
