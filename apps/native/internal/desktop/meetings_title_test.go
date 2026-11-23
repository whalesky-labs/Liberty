package desktop

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/domain"
)

func TestQueueLongTitleTooltipMatchesLegacyAndTracksRefresh(t *testing.T) {
	for _, locale := range []string{"zh-CN", "en-US"} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{1100, 1440} {
				t.Run(fmt.Sprintf("%s/dark=%v/width=%d", locale, dark, width), func(t *testing.T) {
					title := strings.Repeat(localized(locale, "合成会议标题 ", "Synthetic meeting title "), 6) + "A-123456"
					job := domain.JobSummary{ID: "synthetic-long-title", Title: title, Source: "local", OverallStatus: "completed"}
					page := domain.QueryJobs([]domain.JobSummary{job}, domain.QueueQuery{Source: "local"})
					controller := &client.Controller{State: client.State{Jobs: page.Items, Page: page}, ResultController: &client.ResultController{}}
					view := NewMeetings(controller, locale)
					tester := ui.NewTesterWith(view.View, width, 760, ui.TesterOptions{Dark: dark})
					label, ok := tester.AccessByRole(ui.RoleText, title)
					if !ok || label.Offscreen || label.Bounds.W <= 0 || label.Bounds.W > queueColumns[0].width {
						t.Fatal("long title lost its full accessible name or overflowed its fixed column", label)
					}
					tester.Move(label.Bounds.X+label.Bounds.W/2, label.Bounds.Y+label.Bounds.H/2)
					tester.Advance(time.Second)
					tooltip, ok := tester.AccessByRole(ui.RoleTooltip, title)
					if !ok || tooltip.Offscreen {
						t.Fatal("hovering the truncated title does not reveal its complete text", tooltip)
					}
					if controller.SelectedID != "" {
						t.Fatal("reading a title tooltip selected the job")
					}
					tester.Key(0, ui.KeyEscape)
					if _, ok := tester.AccessByRole(ui.RoleTooltip, ""); ok {
						t.Fatal("Escape did not dismiss the title tooltip")
					}
					tester.Move(float32(width-10), 740)
					tester.Move(label.Bounds.X+label.Bounds.W/2, label.Bounds.Y+label.Bounds.H/2)
					tester.Advance(time.Second)
					if _, ok := tester.AccessByRole(ui.RoleTooltip, title); !ok {
						t.Fatal("returning to the title did not reopen its tooltip")
					}
					updated := strings.TrimSuffix(title, "A-123456") + "B-654321"
					controller.Jobs[0].Title = updated
					tester.Frame()
					if _, ok := tester.AccessByRole(ui.RoleTooltip, updated); !ok {
						t.Fatal("queue refresh did not update the open title tooltip")
					}
					if tester.HasText(title) {
						t.Fatal("queue refresh retained the old full title")
					}
					controller.Jobs = nil
					tester.Frame()
					if _, ok := tester.AccessByRole(ui.RoleTooltip, ""); ok {
						t.Fatal("removed job left its title tooltip on screen")
					}
				})
			}
		}
	}
}
