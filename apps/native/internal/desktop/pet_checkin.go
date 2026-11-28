package desktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/domain"
)

func (v *PetView) checkIn(c *ui.Context) {
	s := v.Client.CheckIn
	if s == nil {
		ui.Text(c, v.text("正在加载签到状态…", "Loading check-in…"))
		return
	}
	title, copy := v.text("今日福利待领取", "Daily reward is waiting"), fmt.Sprintf(v.text("领取第 %d 天奖励，获得 LP、成长值和里程碑物品。", "Claim Day %d to collect LP, growth and milestone items."), s.NextCycleDay)
	if s.CheckedInToday {
		title = v.text("今日福利已入袋", "Today's reward is secured")
		copy = fmt.Sprintf(v.text("明天回来领取第 %d 天奖励，连续签到正在保持中。", "Come back tomorrow for Day %d. Your streak is still alive."), s.CurrentStreak+1)
	}
	if s.MakeupAvailable {
		title = v.text("断签可以补回", "Missed check-in can be repaired")
		copy = v.text("先消耗 1 张补签票券补回缺失日期，再继续保持连续签到。", "Use one make-up ticket to repair the missed day before restarting from today.")
	}
	if !v.Client.Busy && (v.Client.Error == nil || v.Client.ErrorIsRefresh) {
		message := ""
		if r := v.Client.LastClaim; r != nil && v.Client.LastAction == "check-in" {
			message = fmt.Sprintf(v.text("签到完成，LP +%d，成长值 +%d。", "Checked in. LP +%d, growth +%d."), r.Entry.RewardLP, r.Entry.GrowthValue)
			if r.Duplicate {
				message = v.text("今日已经签到过了。", "Already checked in today.")
			}
		}
		if r := v.Client.LastMakeup; r != nil && v.Client.LastAction == "makeup" {
			message = fmt.Sprintf(v.text("已补签 %s，消耗 1 张补签票券。", "Repaired %s. One make-up ticket used."), r.Entry.CheckInDate)
		}
		if message != "" {
			ui.Text(c, message).FillWidth().FontSize(12).TextColor(c.Theme().Success).Role(ui.RoleStatus)
		}
	}
	v.benefitsHeader(c, v.text("每日签到", "Daily Check-in"), v.text("每天为伙伴积累一点成长，记录每一次陪伴。", "A little growth each day. Keep a record of your time together."), v.text("连续签到", "Day streak"), fmt.Sprint(s.CurrentStreak))
	width := v.benefitsWidth(c)
	benefitsPanel(c, func() {
		spotlight := ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Center)
		if width < 680 {
			spotlight.Column().AlignItems(ui.Stretch)
		}
		spotlight.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(12).Children(func() {
				ui.Text(c, s.CheckInDate).FontSize(12).TextColor(c.Theme().TextMuted)
				ui.Text(c, title).FillWidth().FontSize(22).Role(ui.RoleHeading).Level(2)
				ui.Text(c, copy).FillWidth().FontSize(13).TextColor(c.Theme().TextMuted)
				ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
					if s.MakeupAvailable {
						label := v.text("补签", "Repair")
						if s.MakeupDate != nil {
							label += " " + *s.MakeupDate
						}
						if s.MakeupTicketQuantity <= 0 {
							label = v.text("需要补签票券", "Need Ticket")
						}
						if v.Client.PendingAction == "makeup" {
							label = v.text("补签中…", "Repairing…")
						}
						if ui.PrimaryButton(c, label).Disabled(v.Client.Busy || s.MakeupTicketQuantity == 0).Clicked() {
							v.Client.Makeup()
						}
					}
					label := v.text("签到领取", "Check In")
					if s.CheckedInToday {
						label = v.text("今日已签到", "Checked In Today")
					} else if s.MakeupAvailable {
						label = v.text("从今日重新开始", "Restart Today")
					}
					if v.Client.PendingAction == "check-in" {
						label = v.text("领取中…", "Claiming…")
					}
					var button ui.Element
					if s.MakeupAvailable {
						button = ui.Button(c, label)
					} else {
						button = ui.PrimaryButton(c, label)
					}
					if button.Disabled(v.Client.Busy || s.CheckedInToday).Clicked() {
						if s.MakeupAvailable {
							v.openDialog("restart", "")
						} else {
							v.Client.CheckInNow()
						}
					}
				})
			})
			reward := ui.Column(c).Width(230).Shrink(0).Padding(16).Gap(10).Radius(10).Background(palette(c).Subpanel)
			if width < 680 {
				reward.FillWidth()
			}
			reward.Children(func() {
				ui.Text(c, v.text("今日福利包", "Today Package")).FontSize(14)
				v.rewardPreview(c, s.TodayReward)
			})
		})
	})
	benefitsColumns(c, width, func() {
		benefitsPanel(c, func() {
			benefitsHeading(c, v.text("奖励日历", "Reward Calendar"))
			ui.Column(c).FillWidth().Gap(6).Children(func() { v.calendar(c, *s) })
			ui.Text(c, v.text("前 14 天包含里程碑物品，之后继续领取每日基础奖励。", "Milestone items are awarded during the first 14 days; daily base rewards continue afterwards.")).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
		})
	}, func() {
		benefitsPanel(c, func() {
			benefitsHeading(c, v.text("奖励概览", "Reward Overview"))
			for _, item := range []struct {
				zh, en string
				value  int64
			}{{"连续签到", "Streak", s.CurrentStreak}, {"LP 余额", "LP Balance", s.StoreState.Wallet.Balance}, {"仓库物品", "Inventory", int64(len(s.StoreState.Inventory))}, {"补签票券", "Make-up Tickets", s.MakeupTicketQuantity}, {"断签天数", "Missed Days", s.MissedDays}} {
				v.stat(c, v.text(item.zh, item.en), fmt.Sprint(item.value))
			}
			target := s.CycleLength
			if reward := nextPetCheckInMilestone(*s); reward != nil {
				target = reward.CycleDay
			}
			v.stat(c, v.text("特别奖励", "Special Reward"), fmt.Sprintf("%d/%d", min(s.CurrentStreak, target), target))
			if target > 0 {
				progressBar(c, float32(min(s.CurrentStreak, target)*100/target), v.text("里程碑进度", "Milestone progress"))
			}
		})
		if reward := nextPetCheckInMilestone(*s); reward != nil {
			benefitsPanel(c, func() {
				benefitsHeading(c, v.text("下一个里程碑", "Next Milestone"))
				ui.Text(c, fmt.Sprintf(v.text("第 %d 天奖励", "Day %d reward"), reward.CycleDay)).FontSize(12)
				v.rewardPreview(c, *reward)
			})
		}
		benefitsPanel(c, func() {
			benefitsHeading(c, v.text("最近记录", "Recent Records"))
			if len(s.History) == 0 {
				benefitsEmpty(c, "check", v.text("暂无签到记录。", "No check-ins yet."), v.text("领取后会在这里留下记录。", "Your claimed rewards will appear here."))
			}
			for _, entry := range s.History[:min(5, len(s.History))] {
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					ui.Text(c, entry.CheckInDate).Grow(1).MinWidth(0).FontSize(12)
					ui.Text(c, fmt.Sprintf(v.text("连续 %d 天", "Streak %d"), entry.StreakCount)).FontSize(11)
				})
				rewards := fmt.Sprintf("+%d LP · +%d XP", entry.RewardLP, entry.GrowthValue)
				if len(entry.RewardItems) > 0 {
					names := make([]string, 0, len(entry.RewardItems))
					for _, item := range entry.RewardItems {
						names = append(names, v.itemName(item.ItemKey))
					}
					rewards += " · " + strings.Join(names, "、")
				}
				ui.Text(c, rewards).FillWidth().FontSize(11)
				separator(c)
			}
		})
	})
}
func (v *PetView) rewardPreview(c *ui.Context, r domain.PetDailyCheckInRewardPreview) {
	ui.Text(c, fmt.Sprintf("+%d LP · +%d XP", r.RewardLP, r.GrowthValue)).FontSize(13)
	for _, item := range r.Items {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Image(c, v.art(item.ItemKey)).Size(24, 24).Fit(ui.Contain)
			ui.Text(c, fmt.Sprintf("%s ×%d", v.itemName(item.ItemKey), item.Quantity)).FontSize(11)
		})
	}
}
func (v *PetView) calendar(c *ui.Context, s domain.PetDailyCheckInState) {
	today, err := time.Parse("2006-01-02", s.CheckInDate)
	if err != nil {
		return
	}
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, v.calendarOffset, 0)
	label := month.Format("2006-01")
	if v.Locale == "en-US" {
		label = month.Format("January 2006")
	}
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Grow(1)
		if textButton(c, v.text("上月", "Previous")).Clicked() {
			v.calendarOffset--
		}
		if textButton(c, v.text("本月", "This Month")).Disabled(v.calendarOffset == 0).Clicked() {
			v.calendarOffset = 0
		}
		if textButton(c, v.text("下月", "Next")).Clicked() {
			v.calendarOffset++
		}
	})
	days := []string{"一", "二", "三", "四", "五", "六", "日"}
	if v.Locale == "en-US" {
		days = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	}
	ui.Row(c).FillWidth().Gap(5).Children(func() {
		for _, day := range days {
			ui.Text(c, day).Grow(1).Basis(0).TextAlign(ui.Center).FontSize(12).TextColor(c.Theme().TextMuted)
		}
	})
	cells := petCalendarCells(s, month)
	for start := 0; start < len(cells); start += 7 {
		ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Stretch).Children(func() {
			for index, cell := range cells[start:min(start+7, len(cells))] {
				day := ui.Column(c.Key(fmt.Sprint(start + index))).Grow(1).Basis(0).MinWidth(0).MinHeight(72).Padding(8).Gap(3).Radius(8).Background(palette(c).Subpanel)
				if cell.Day > 0 {
					day.Role(ui.RoleGroup).Label(cell.Date)
					if cell.Date == s.CheckInDate {
						day.Current(ui.CurrentDate).Border(1, c.Theme().Accent).Background(palette(c).Selected)
					}
				}
				day.Children(func() {
					if cell.Day == 0 {
						return
					}
					color := c.Theme().TextMuted
					if cell.Status == "signed" {
						color = c.Theme().Success
					} else if cell.Status == "today" || cell.Status == "makeup" {
						color = c.Theme().Accent
					} else if cell.Status == "missed" {
						color = c.Theme().Danger
					}
					ui.Text(c, fmt.Sprint(cell.Day)).FontSize(12)
					labels := map[string][2]string{"signed": {"已签", "Done"}, "today": {"今日", "Today"}, "makeup": {"补签", "Repair"}, "missed": {"断签", "Missed"}, "future": {"待到", "Soon"}}
					if l, ok := labels[cell.Status]; ok {
						ui.Text(c, v.text(l[0], l[1])).TextColor(color).FontSize(10)
					}
					if cell.Reward != nil {
						ui.Text(c, fmt.Sprintf(v.text("第 %d 天", "Day %d"), cell.Reward.CycleDay)).FontSize(10)
						ui.Text(c, fmt.Sprintf("+%d LP", cell.Reward.RewardLP)).FontSize(10)
						for _, item := range cell.Reward.Items[:min(2, len(cell.Reward.Items))] {
							label := fmt.Sprintf("%s ×%d", v.itemName(item.ItemKey), item.Quantity)
							ui.Image(c, v.art(item.ItemKey)).Size(22, 22).Fit(ui.Contain).Label(label).Tooltip(label)
						}
					}
				})
			}
		})
	}
}

func nextPetCheckInMilestone(s domain.PetDailyCheckInState) *domain.PetDailyCheckInRewardPreview {
	var next *domain.PetDailyCheckInRewardPreview
	day := s.NextCycleDay
	for _, reward := range s.Rewards {
		if len(reward.Items) > 0 && reward.CycleDay >= day && (next == nil || reward.CycleDay < next.CycleDay) {
			r := reward
			next = &r
		}
	}
	return next
}

type petCalendarCell struct {
	Date   string
	Day    int
	Status string
	Reward *domain.PetDailyCheckInRewardPreview
}

func petCalendarCells(s domain.PetDailyCheckInState, month time.Time) []petCalendarCell {
	first := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	days := first.AddDate(0, 1, -1).Day()
	leading := (int(first.Weekday()) + 6) % 7
	cells := make([]petCalendarCell, leading)
	entries := map[string]domain.PetDailyCheckInEntry{}
	for _, h := range s.History {
		entries[h.CheckInDate] = h
	}
	anchor := s.CheckInDate
	if s.MakeupAvailable && s.MakeupDate != nil {
		anchor = *s.MakeupDate
	}
	anchorDate, _ := time.Parse("2006-01-02", anchor)
	for day := 1; day <= days; day++ {
		date := first.AddDate(0, 0, day-1).Format("2006-01-02")
		cell := petCalendarCell{Date: date, Day: day, Status: "idle"}
		if e, ok := entries[date]; ok {
			cell.Status = "signed"
			cell.Reward = &domain.PetDailyCheckInRewardPreview{CycleDay: e.CycleDay, RewardLP: e.RewardLP, GrowthValue: e.GrowthValue, Items: e.RewardItems}
		} else {
			switch {
			case s.MakeupDate != nil && date == *s.MakeupDate:
				cell.Status = "makeup"
			case s.MissedDays > 0 && !s.CheckedInToday && len(s.History) > 0 && date > s.History[0].CheckInDate && date < s.CheckInDate:
				cell.Status = "missed"
			case date == s.CheckInDate:
				cell.Status = "today"
			case date > s.CheckInDate:
				cell.Status = "future"
			}
			if date == s.CheckInDate || s.MakeupDate != nil && date == *s.MakeupDate {
				reward := s.TodayReward
				cell.Reward = &reward
			} else {
				d, _ := time.Parse("2006-01-02", date)
				offset := int64(d.Sub(anchorDate).Hours() / 24)
				if offset >= 0 {
					cycle := s.NextCycleDay + offset
					reward := domain.PetDailyCheckInRewardPreview{CycleDay: cycle, RewardLP: 20, GrowthValue: 5}
					for _, r := range s.Rewards {
						if r.CycleDay == cycle {
							reward = r
							break
						}
					}
					cell.Reward = &reward
				}
			}
		}
		cells = append(cells, cell)
	}
	for len(cells)%7 != 0 {
		cells = append(cells, petCalendarCell{})
	}
	return cells
}
