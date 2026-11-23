package desktop

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

// PetView keeps the original companion, shop and benefits routes in one native
// workspace. Its controller owns I/O; render only reads state and emits actions.
type PetView struct {
	Client            *client.PetController
	Locale, Route     string
	Navigate          func(string)
	OpenItem          func(string)
	VisibleCompanions func() int
	// Platform desktop-pet operations are enabled only after a real adapter is
	// attached. Persisted settings must not imply that a desktop window exists.
	DesktopAvailable               bool
	OpenExtra                      func()
	Environment                    string
	section, category              string
	name                           string
	settings                       domain.PetSettings
	settingsStamp                  string
	settingsDirty                  bool
	dialog, dialogKind, dialogItem string
	dialogOpen, awaiting           bool
	quantity                       string
	actionRevision                 uint64
	key                            string
	calendarOffset                 int
	drawStart                      time.Time
	blindSceneStart                time.Time
	previewFrame                   ui.ImageSource
	previewGroup                   string
}

func PetRouteAvailable(path string) bool {
	switch path {
	case "/pet", "/pet-store", "/daily-check-in", "/daily-blind-box", "/redeem-key":
		return true
	}
	return false
}
func (v *PetView) text(zh, en string) string { return localized(v.Locale, zh, en) }
func (v *PetView) View(c *ui.Context) {
	if v.Client == nil {
		ui.Text(c, v.text("正在读取…", "Loading…"))
		return
	}
	if v.awaiting && v.Client.ActionRevision > v.actionRevision {
		v.awaiting = false
		v.dialogOpen = false
		if v.Client.LastAction == "redeem" {
			v.key = ""
		}
		if v.Client.LastAction == "settings" {
			v.settingsDirty = false
		}
	}
	if s := v.Client.Settings; s != nil && !v.settingsDirty && s.UpdatedAt != v.settingsStamp {
		v.settings = *s
		v.settingsStamp = s.UpdatedAt
	}
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().Padding(12, 8, 28).Gap(14).Children(func() {
			if v.Client.Error != nil {
				ui.Row(c).FillWidth().Padding(10).Gap(12).Radius(7).Background(c.Theme().Danger.Alpha(.07)).Children(func() {
					ui.Text(c, v.errorMessage()).Grow(1).MinWidth(0).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
					if textButton(c, v.text("刷新", "Refresh")).Disabled(v.Client.Busy).Clicked() {
						v.Client.Refresh()
					}
				})
			}
			if v.Client.Store == nil {
				if v.Client.Error == nil || v.Client.Loading {
					ui.Text(c, v.text("正在加载宠物信息…", "Loading companion…")).Padding(24).Role(ui.RoleStatus)
				}
				return
			}
			switch v.Route {
			case "/pet-store":
				v.store(c)
			case "/daily-check-in":
				v.checkIn(c)
			case "/daily-blind-box":
				v.blindBox(c)
			case "/redeem-key":
				v.redeem(c)
			default:
				v.management(c)
			}
		})
	})
	v.confirmation(c)
}
func (v *PetView) errorMessage() string {
	if v.Client.Store == nil {
		return v.text("无法读取宠物信息，请重试。", "Companion information could not be loaded. Please retry.")
	}
	if v.Client.ErrorIsRefresh {
		return v.text("暂时无法刷新全部宠物信息。已成功的操作仍然生效，请重试刷新。", "Some companion information could not be refreshed. Successful actions remain applied. Retry the refresh.")
	}
	return petError(v.Locale, v.Client.Error)
}
func petError(locale string, err error) string {
	switch {
	case errors.Is(err, domain.ErrPetRedemptionUnsupported):
		return localized(locale, "此版本不支持短码。请联系发放方，使用本地换发工具转为签名 Key。", "Short codes are not supported in this version. Ask the issuer to reissue it locally as a signed key.")
	case errors.Is(err, domain.ErrPetRedemptionExpired):
		return localized(locale, "兑换 Key 已过期。", "This redemption key has expired.")
	case errors.Is(err, domain.ErrPetRedemption):
		return localized(locale, "兑换 Key 格式或签名不正确，请检查后重试。", "The redemption key or signature is invalid. Check it and try again.")
	default:
		return localized(locale, "操作未完成，请检查余额、物品数量和今日次数后重试。", "The operation could not be completed. Check your balance, items and daily limits, then retry.")
	}
}
func petPanel(c *ui.Context, fn func()) {
	ui.Column(c).FillWidth().Padding(14).Radius(8).Border(1, palette(c).Separator).Background(palette(c).Panel).Gap(12).Children(fn)
}
func (v *PetView) management(c *ui.Context) {
	p := v.Client.Profile
	if p == nil {
		return
	}
	width, _ := c.Size()
	layout := ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch)
	if width <= 1040 {
		layout.Column()
	}
	layout.Children(func() {
		main := ui.Column(c).MinWidth(0)
		if width <= 1040 {
			main.FillWidth()
		} else {
			main.Grow(1).Basis(0)
		}
		main.Children(func() {
			petPanel(c, func() {
				ui.Text(c, v.text("桌面伙伴", "Desktop Companion")).FontSize(16).Bold().Role(ui.RoleHeading).Level(1)
				separator(c)
				ui.Row(c).FillWidth().MinHeight(290).Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Width(76).Shrink(0).Gap(8).Children(func() {
						for _, a := range []struct{ key, zh, en, icon string }{{"tap", "点击", "Tap", "pet"}, {"pet", "抚摸", "Pet", "pet"}, {"feed", "投喂", "Feed", "gift"}, {"encourage", "鼓励", "Encourage", "growth"}} {
							action := a
							b := ui.ButtonBase(c.Key(a.key)).Width(76).Height(48).Radius(8).Center().Label(v.text(a.zh, a.en)).Disabled(v.Client.Busy).Background(palette(c).Subpanel)
							b.Children(func() { ui.Text(c, v.text(action.zh, action.en)).SingleLine().TextAlign(ui.Center).FontSize(11) })
							if b.Clicked() {
								v.Client.Interaction(action.key)
							}
						}
					})
					ui.Column(c).Grow(1).Basis(0).MinWidth(180).Gap(9).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Size(210, 210).Children(func() {
							if item := v.Client.Store.Equipment.Scene; item != nil {
								ui.Image(c, v.art(item.ItemKey)).Absolute().Left(0).Top(0).Size(210, 210).Fit(ui.Contain)
							}
							group := petVisualAction(v.Environment, p.CurrentMood, v.Client.Events, c.Now())
							scale := float32(domain.PetStageRatio(p.Stage))
							ui.Image(c, v.companionFrame(c, group)).Absolute().Left(18).Top(8).Size(176, 176).Scale(scale, scale).Fit(ui.Contain).Label(p.Name)
							if item := v.Client.Store.Equipment.Accessory; item != nil {
								ui.Image(c, v.art(item.ItemKey)).Absolute().Right(0).Top(0).Size(72, 72).Fit(ui.Contain).Label(v.itemName(item.ItemKey))
							}
							if item := v.Client.Store.Equipment.Badge; item != nil {
								ui.Image(c, v.art(item.ItemKey)).Absolute().Right(4).Bottom(4).Size(48, 48).Fit(ui.Contain).Label(v.itemName(item.ItemKey))
							}
						})
						ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, p.Name).Grow(1).MinWidth(0).SingleLine().Tooltip(p.Name).FontSize(20).Bold()
							if textButton(c, v.text("修改名字", "Edit Name")).Shrink(0).Disabled(v.Client.Busy).Clicked() {
								v.name = p.Name
								v.openDialog("name", "")
							}
						})
						ui.Text(c, v.stage(p.Stage)+" · "+v.mood(p.CurrentMood)).FontSize(12).TextColor(c.Theme().TextMuted)
					})
					ui.Column(c).Width(180).Shrink(0).Gap(12).Children(func() {
						v.stat(c, v.text("等级", "Level"), fmt.Sprint(p.Level))
						v.stat(c, v.text("累计成长", "Total Growth"), fmt.Sprint(p.Experience))
						for _, eq := range []struct {
							zh, en string
							item   *domain.PetInventoryItem
						}{{"配饰", "Accessory", v.Client.Store.Equipment.Accessory}, {"场景", "Scene", v.Client.Store.Equipment.Scene}, {"徽章", "Badge", v.Client.Store.Equipment.Badge}} {
							name := v.text("未装备", "None equipped")
							if eq.item != nil {
								name = v.itemName(eq.item.ItemKey)
							}
							v.stat(c, v.text(eq.zh, eq.en), name)
						}
						progress := fmt.Sprintf("%d/%d", p.LevelSnapshot.CurrentLevelExp, p.LevelSnapshot.NextLevelRequired)
						if p.LevelSnapshot.IsMaxLevel {
							progress = v.text("已满级", "Max Level")
						}
						v.stat(c, v.text("本级成长", "Level Progress"), progress)
						progressBar(c, float32(p.LevelSnapshot.ProgressRatio*100), v.text("等级进度", "Level progress"))
						next := v.text("最高陪伴阶段", "Max companion stage")
						if p.LevelSnapshot.NextStage != nil && p.LevelSnapshot.NextStageLevel != nil {
							next = fmt.Sprintf("%s Lv.%d", v.stage(*p.LevelSnapshot.NextStage), *p.LevelSnapshot.NextStageLevel)
						}
						v.stat(c, v.text("下一阶段", "Next stage"), next)
					})
				})
			})
		})
		sidebar := ui.Column(c).Width(320).Shrink(0)
		if width <= 1040 {
			sidebar.FillWidth()
		}
		sidebar.Children(func() {
			petPanel(c, func() {
				ui.Text(c, v.text("桌面行为", "Desktop Behavior")).FontSize(16).Bold()
				if v.VisibleCompanions != nil {
					v.stat(c, v.text("可见桌宠", "Visible companions"), fmt.Sprint(v.VisibleCompanions()))
				}
				for _, setting := range []struct {
					zh, en string
					value  *bool
				}{{"启用桌宠", "Enable desktop pet", &v.settings.DesktopEnabled}, {"始终置顶", "Always on top", &v.settings.AlwaysOnTop}, {"静音提示", "Mute prompts", &v.settings.Muted}, {"专注模式", "Focus mode", &v.settings.FocusModeEnabled}} {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
						ui.Text(c, v.text(setting.zh, setting.en)).Grow(1).FontSize(12)
						if ui.Switch(c, setting.value).Label(v.text(setting.zh, setting.en)).Disabled(!v.DesktopAvailable || v.Client.Busy).Changed() {
							v.settingsDirty = true
						}
					})
				}
				ui.Text(c, v.text("主动程度", "Proactive level")).FontSize(12)
				selected := fmt.Sprint(v.settings.ProactiveLevel)
				if ui.Select(c, &selected, []string{"0", "1", "2", "3"}).FillWidth().Label(v.text("主动程度", "Proactive level")).Disabled(!v.DesktopAvailable || v.Client.Busy).Changed() {
					fmt.Sscan(selected, &v.settings.ProactiveLevel)
					v.settingsDirty = true
				}
				if ui.Button(c, v.text("多开桌宠", "Open Another Pet")).FillWidth().Disabled(!v.DesktopAvailable || v.OpenExtra == nil || v.Client.Busy).Clicked() {
					v.OpenExtra()
				}
				if ui.PrimaryButton(c, v.text("保存设置", "Save Settings")).FillWidth().Disabled(!v.DesktopAvailable || v.Client.Busy || !v.settingsDirty).Clicked() {
					v.awaitAction()
					v.Client.SettingsSave(v.settings)
				}
				if !v.DesktopAvailable {
					ui.Text(c, v.text("桌宠窗口尚未接入此原生预览，桌面行为操作暂不可用。", "Desktop-pet windows are not yet connected in this native preview; desktop behavior controls are unavailable.")).FillWidth().FontSize(11).TextColor(c.Theme().TextMuted)
				}
			})
		})
	})
	petPanel(c, func() {
		ui.Text(c, v.text("最近事件", "Recent Events")).FontSize(16).Bold()
		if len(v.Client.Events) == 0 {
			ui.Text(c, v.text("还没有宠物事件。", "No pet events yet.")).TextColor(c.Theme().TextMuted)
		}
		for _, event := range v.Client.Events {
			ui.Row(c.Key(event.ID)).FillWidth().Padding(8, 0).Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Text(c, v.eventLabel(event)).FontSize(13).Bold()
					ui.Text(c, v.eventDetail(event)).FontSize(11).TextColor(c.Theme().TextMuted)
				})
				value := v.text("已记录", "Recorded")
				if event.EventValue > 0 {
					value = fmt.Sprintf("+%d XP", event.EventValue)
				}
				ui.Text(c, value).FontSize(12)
				ui.Text(c, createdText(v.Locale, event.EventTime)).FontSize(11).TextColor(c.Theme().TextMuted)
			})
		}
	})
}
func (v *PetView) companionFrame(c *ui.Context, group string) ui.ImageSource {
	if !c.Preferences().ReduceMotion || v.previewFrame == nil || v.previewGroup != group {
		v.previewFrame = presentation.PetFrame(c, group)
		v.previewGroup = group
	}
	return v.previewFrame
}
func (v *PetView) stat(c *ui.Context, label, value string) {
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, label).Grow(1).MinWidth(0).FontSize(11).TextColor(c.Theme().TextMuted)
		ui.Text(c, value).MinWidth(0).FontSize(12).Bold()
	})
}
func (v *PetView) catalog(key string) (domain.PetStoreCatalogItem, bool) {
	if v.Client.Store != nil {
		for _, s := range v.Client.Store.Catalog {
			if s.Item.ItemKey == key {
				return s.Item, true
			}
		}
	}
	return domain.FindPetItem(key)
}
func (v *PetView) itemName(key string) string {
	if item, ok := v.catalog(key); ok {
		return v.text(item.NameZh, item.NameEn)
	}
	return key
}
func (v *PetView) art(key string) ui.ImageSource {
	if item, ok := v.catalog(key); ok {
		return presentation.PetArt(item.AssetKey)
	}
	return presentation.PetArt("gift_box")
}
func (v *PetView) stage(key string) string {
	s := domain.PetStage(key)
	return v.text(s.LabelZh, s.LabelEn)
}
func (v *PetView) mood(key string) string {
	m := map[string][2]string{"cheerful": {"开心", "Cheerful"}, "excited": {"兴奋", "Excited"}, "proud": {"得意", "Proud"}, "needy": {"想互动", "Needs interaction"}, "sleepy": {"犯困", "Sleepy"}, "bored": {"无聊", "Bored"}}
	if s, ok := m[key]; ok {
		return v.text(s[0], s[1])
	}
	return v.text("待机", "Idle")
}
func (v *PetView) openDialog(kind, key string) {
	v.dialogKind, v.dialogItem = kind, key
	v.quantity = "1"
	v.dialogOpen = true
	v.awaiting = false
}
func (v *PetView) awaitAction() { v.actionRevision = v.Client.ActionRevision; v.awaiting = true }

func petEnvironment(jobs []domain.JobSummary) string {
	for _, job := range jobs {
		switch job.OverallStatus {
		case "queued", "transcribing", "speaker_processing", "summarizing":
			return job.OverallStatus
		}
	}
	return ""
}

func petVisualAction(environment, mood string, events []domain.PetEventLedgerEntry, now time.Time) string {
	switch environment {
	case "transcribing", "speaker_processing", "summarizing":
		return "work"
	case "queued":
		return "slack"
	case "completed":
		return "snow"
	case "failed":
		return "pants"
	case "uploaded":
		return "reading"
	}
	byMood := map[string]string{"idle": "slack", "cheerful": "snow", "excited": "run", "proud": "eat", "needy": "toy", "sleepy": "sleep", "bored": "reading"}
	group := byMood[mood]
	if group == "" {
		group = "slack"
	}
	if len(events) > 0 {
		e := events[0]
		stamp, err := time.Parse(time.RFC3339Nano, e.EventTime)
		if err == nil && !stamp.After(now) && now.Sub(stamp) <= 45*time.Second {
			if e.EventType == "interaction" {
				if g := map[string]string{"feed": "eat", "encourage": "rope", "pet": "crush", "tap": "toy"}[e.EventSource]; g != "" {
					return g
				}
			}
			kind := e.EventType
			if kind == "workflow" {
				kind = e.EventSource
			}
			if g := map[string]string{"store_food": "eat", "store_equip": "snow", "blind_box_reward": "run", "blind_box_empty": "toy", "blind_box_duplicate": "snow", "job_created": "drive", "transcription_started": "work", "transcription_completed": "snow", "ai_summary_completed": "snow", "export_completed": "snow", "daily_open": "slack"}[kind]; g != "" {
				return g
			}
			return group
		}
	}
	groups := []string{"slack", "toy", "rope", "drive", "crush", "defecate", "eat", "gaming", "pants", "reading", "run", "sleep", "snow", "studying", "work"}
	n := now.UnixMilli() / 120000 % int64(len(groups))
	if n < 0 {
		n = 0
	}
	return groups[n]
}
func (v *PetView) eventLabel(e domain.PetEventLedgerEntry) string {
	labels := map[string][2]string{"tap": {"点击互动", "Tap interaction"}, "pet": {"抚摸", "Pet"}, "feed": {"投喂", "Feed"}, "encourage": {"鼓励", "Encourage"}, "job_created": {"创建任务", "Job created"}, "transcription_started": {"开始转写", "Transcription started"}, "transcription_completed": {"完成转写", "Transcription completed"}, "ai_summary_completed": {"完成 AI 总结", "AI summary completed"}, "export_completed": {"导出结果", "Result exported"}, "daily_open": {"每日打开", "Daily open"}, "dark_theme_used": {"使用深色主题", "Dark theme used"}, "daily_check_in": {"每日签到", "Daily check-in"}, "daily_check_in_makeup": {"补签", "Make-up check-in"}, "redeem_key": {"兑换奖励", "Redemption reward"}, "purchase": {"商店购买", "Store purchase"}}
	for _, key := range []string{e.EventSource, e.EventType} {
		if s, ok := labels[key]; ok {
			return v.text(s[0], s[1])
		}
	}
	return v.text("宠物事件", "Companion event")
}
func (v *PetView) eventDetail(e domain.PetEventLedgerEntry) string {
	// Raw metadata may contain identifiers. The event type/value/date form the
	// display projection; keep arbitrary JSON and source IDs out of the UI.
	switch {
	case strings.HasPrefix(e.EventType, "blind_box"):
		return v.text("每日盲盒已记录。", "Daily blind-box result recorded.")
	case strings.HasPrefix(e.EventType, "gift_box"):
		return v.text("惊喜礼盒已开启。", "Gift box opened.")
	case strings.HasPrefix(e.EventType, "store_"):
		return v.text("商店与仓库已更新。", "Store and inventory updated.")
	}
	return v.text("这份陪伴已记录。", "This companion moment was recorded.")
}
