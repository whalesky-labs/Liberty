package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/infrastructure/fileout"
)

type LiveView struct {
	Client        *client.LiveController
	Locale        string
	OpenSettings  func()
	ConfirmDelete func()
	list          ui.ListState
	listSession   string
	editing       *domain.LiveSegment
	mode          string
	settingsOpen  bool
	avatar        liveAvatar
}

func (v *LiveView) text(zh, en string) string { return localized(v.Locale, zh, en) }
func liveStatus(locale, state string) string {
	values := map[string][2]string{"preparing": {"正在加载模型", "Preparing model"}, "listening": {"正在收听", "Listening"}, "paused": {"已暂停", "Paused"}, "stopping": {"正在结束与保存", "Finishing and saving"}, "completed": {"已结束", "Completed"}, "failed": {"会话未完成", "Session failed"}, "interrupted": {"会话已中断", "Interrupted"}}
	if value, ok := values[state]; ok {
		return localized(locale, value[0], value[1])
	}
	return localized(locale, "准备就绪", "Ready")
}
func liveError(locale string, err error, code string) string {
	if errors.Is(err, domain.ErrLiveBusy) {
		return localized(locale, "请先结束当前实时会话，或等待文件转写完成。", "Finish the current live session or wait for file transcription to complete.")
	}
	if errors.Is(err, domain.ErrConflict) {
		return localized(locale, "字幕已被更新，请重新打开片段再编辑。", "This caption changed. Reopen it before editing.")
	}
	if errors.Is(err, domain.ErrLiveModel) || code == "live_model_not_ready" {
		return localized(locale, "请先下载本地识别和翻译模型。", "Download the local recognition and translation models first.")
	}
	if errors.Is(err, domain.ErrRuntime) {
		return localized(locale, "请先在系统设置中安装并验证本地运行环境。", "Install and verify the local runtime in Settings first.")
	}
	if errors.Is(err, domain.ErrLiveBackpressure) || code == "live_audio_backpressure" {
		return localized(locale, "音频处理已落后，已停止收听并保留定稿。请关闭其他转写任务后重试。", "Audio processing fell behind. Listening stopped and finalized text was kept. Close other transcription tasks and retry.")
	}
	if errors.Is(err, domain.ErrLiveCapture) || code == "live_audio_capture_failed" {
		return localized(locale, "无法使用所选麦克风，请检查系统权限并刷新设备。", "Cannot use the selected microphone. Check system permissions and refresh devices.")
	}
	if errors.Is(err, domain.ErrLiveInput) {
		return localized(locale, "输入无效，请检查当前填写的内容或选择项。", "Invalid input. Check the current text or selection.")
	}
	if code == "live_session_interrupted" {
		return localized(locale, "上次会话被中断，已保存的文字仍可查看与导出。", "The previous session was interrupted. Saved text is available for review and export.")
	}
	if code == "live_translation_incomplete" {
		return localized(locale, "部分译文尚未完成，原文已保留；可在历史会话中补译。", "Some translations are incomplete. Original text is retained; retry from session history.")
	}
	if errors.Is(err, context.Canceled) {
		return localized(locale, "操作已取消。", "Operation cancelled.")
	}
	return localized(locale, "操作未完成，已保存的文字会保留。请检查设备、运行环境或本地翻译模型后重试。", "The operation could not complete. Saved text is retained. Check the device, runtime or local translation model and retry.")
}
func liveStamp(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d", ms/3600000, (ms/60000)%60, (ms/1000)%60)
}

func (v *LiveView) View(c *ui.Context) {
	state := v.Client
	width, height := c.Size()
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(1120).AlignSelf(ui.Center).Padding(28, 36).Gap(20).Children(func() {
			ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Text(c, v.text("实时翻译", "Live Translation")).FontSize(23).Bold().Grow(1).Role(ui.RoleHeading).Level(1)
				if v.mode == "" {
					if textButton(c, v.text("历史记录", "History")).MinHeight(40).Clicked() {
						v.mode = "history"
						v.settingsOpen = false
					}
				} else {
					if v.mode == "review" {
						if textButton(c, v.text("历史记录", "History")).MinHeight(40).Disabled(v.editing != nil).Clicked() {
							v.mode = "history"
						}
					}
					if textButton(c, v.text("返回收听", "Back to listening")).MinHeight(40).Disabled(v.editing != nil).Clicked() {
						v.mode = ""
						state.ShowCurrent()
					}
				}
			})
			if state.Error != nil {
				ui.Text(c, liveError(v.Locale, state.Error, "")).FillWidth().Padding(12).Radius(8).Background(c.Theme().Danger.Alpha(.08)).TextColor(c.Theme().Danger).Role(ui.RoleStatus)
			}
			if code := state.Snapshot.Session.ErrorCode; code != "" {
				ui.Text(c, liveError(v.Locale, nil, code)).FillWidth().FontSize(13).TextColor(c.Theme().Warning).Role(ui.RoleStatus)
			}
			if v.mode != "" && domain.LiveActive(state.Snapshot.Session.State) {
				ui.Row(c).FillWidth().Gap(10).Children(func() {
					ui.Text(c, liveStatus(v.Locale, state.Snapshot.Session.State)).Grow(1).FontSize(13).TextColor(palette(c).NavAccent).Role(ui.RoleStatus)
					label, command := v.text("暂停", "Pause"), "pause"
					if state.Snapshot.Session.State == "paused" {
						label, command = v.text("继续收听", "Resume listening"), "resume"
					}
					if ui.Button(c, label).MinHeight(40).Disabled(state.Busy || (state.Snapshot.Session.State != "listening" && state.Snapshot.Session.State != "paused")).Clicked() {
						state.Control(command)
					}
					if ui.Button(c, v.text("结束", "Finish")).MinHeight(40).Disabled(state.Busy || state.Snapshot.Session.State == "stopping").Clicked() {
						state.Control("stop")
					}
				})
			}
			switch v.mode {
			case "history":
				v.history(c)
			case "review":
				v.captions(c, max(280, height-230))
			default:
				v.listening(c, width, height)
			}
		})
	})
}

func (v *LiveView) listening(c *ui.Context, width, height float32) {
	s := v.Client
	phase := s.Snapshot.Session.State
	active := domain.LiveActive(phase)
	busy := s.Busy || s.Preparing
	ui.Column(c).FillWidth().MaxWidth(760).AlignSelf(ui.Center).AlignItems(ui.Center).Gap(20).Padding(max(4, min(36, (height-650)/4)), 0).Children(func() {
		ui.Row(c).AlignSelf(ui.Center).Gap(14).Children(func() {
			label := v.text("自动识别 · 中英互译", "Auto-detect · Chinese ↔ English")
			if !s.Request.Translate {
				label = v.text("自动识别 · 中英转写", "Auto-detect · Chinese & English")
			}
			ui.Text(c, label).FontSize(13).Padding(7, 16).Radius(18).Background(palette(c).Subpanel)
		})
		v.pickup(c, min(float32(240), max(180, height*.27)))
		ui.Column(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			title := v.text("让语言，跟上你的声音", "Let your words flow")
			subtitle := v.text("开始收听，原文与译文随声呈现。", "Start listening. See your words and their translation.")
			if active || phase == "completed" || phase == "failed" || phase == "interrupted" {
				title = liveStatus(v.Locale, phase)
				switch phase {
				case "listening":
					subtitle = v.text("像平常一样说话就好。", "Just speak naturally.")
				case "paused":
					subtitle = v.text("麦克风已暂停，准备好后继续。", "Microphone paused. Resume whenever you’re ready.")
				case "preparing":
					subtitle = v.text("正在准备本地模型，请稍候。", "Getting the local models ready.")
				case "stopping":
					subtitle = v.text("正在保存最后一段字幕。", "Saving the final captions.")
				default:
					subtitle = v.text("字幕已保留，可查看、编辑与导出。", "Your captions are saved for review and export.")
				}
			}
			if s.Preparing {
				title = v.text("正在准备模型", "Preparing your models")
				subtitle = v.text("首次下载完成后，即可离线使用。", "Once downloaded, everything works offline.")
			}
			ui.Text(c, title).FillWidth().TextAlign(ui.Center).FontSize(26).Bold().Role(ui.RoleStatus)
			ui.Text(c, subtitle).FillWidth().TextAlign(ui.Center).FontSize(14).TextColor(c.Theme().TextMuted)
		})
		ui.Row(c).AlignSelf(ui.Center).Gap(10).Children(func() {
			if !active {
				label := v.text("开始收听", "Start listening")
				needsModels := !s.Options.ModelReady || (s.Request.Translate && !s.Options.TranslationReady)
				if needsModels {
					label = v.text("下载本地模型", "Download local models")
				}
				if s.Preparing {
					label = v.text("正在下载…", "Downloading…")
				}
				if ui.PrimaryButton(c, label).MinWidth(174).Height(46).Radius(23).Disabled(busy || v.editing != nil).Clicked() {
					if needsModels {
						s.Prepare()
					} else if s.Request.DeviceID == "" {
						v.settingsOpen = true
					} else {
						s.Begin()
					}
				}
				if s.Preparing && textButton(c, v.text("取消下载", "Cancel download")).MinHeight(44).Clicked() {
					s.CancelPrepare()
				}
			} else {
				label, command := v.text("暂停", "Pause"), "pause"
				if phase == "paused" {
					label, command = v.text("继续收听", "Resume listening"), "resume"
				}
				if ui.PrimaryButton(c, label).MinWidth(142).Height(46).Radius(23).Disabled(busy || (phase != "listening" && phase != "paused")).Clicked() {
					s.Control(command)
				}
				if ui.Button(c, v.text("结束", "Finish")).MinWidth(88).Height(46).Radius(23).Disabled(busy || phase == "stopping").Clicked() {
					s.Control("stop")
				}
			}
			anchor := ui.ButtonBase(c).Size(46, 46).Radius(23).Background(palette(c).Subpanel).Label(v.text("收听设置", "Listening settings")).Expanded(v.settingsOpen).Disabled(active || busy).Children(func() {
				ui.Icon(c, presentation.Icon("accent")).Size(18, 18).TextColor(c.Theme().TextMuted).AlignSelf(ui.Center)
			}).Justify(ui.Center)
			if anchor.Clicked() {
				v.settingsOpen = !v.settingsOpen
			}
			v.settings(c, anchor, min(float32(340), width-64), active || busy)
		})
		device := v.text("选择麦克风", "Choose a microphone")
		for _, d := range s.Options.Devices {
			if d.ID == s.Request.DeviceID {
				device = d.Name
				break
			}
		}
		if active {
			ui.Text(c, device).MaxWidth(480).MinHeight(28).TextAlign(ui.Center).FontSize(12).TextColor(c.Theme().TextMuted)
		} else if textButton(c, device).MaxWidth(480).MinHeight(28).Disabled(busy).Clicked() {
			v.settingsOpen = true
		}
		v.focusedCaptions(c)
	})
}

func livePickupLevel(phase string, level float64) float32 {
	if phase != "listening" || math.IsNaN(level) || math.IsInf(level, 0) {
		return 0
	}
	return float32(math.Sqrt(max(0, min(1, level))))
}

func (v *LiveView) pickup(c *ui.Context, size float32) {
	phase := v.Client.Snapshot.Session.State
	if v.Client.Preparing {
		phase = "preparing"
	}
	level := livePickupLevel(phase, v.Client.Snapshot.Level)
	stage := ui.Box(c.Key("live-pickup")).Size(size, size).AlignSelf(ui.Center).Role(ui.RoleProgress).
		Label(v.text("麦克风输入电平", "Microphone input level")).Range(0, 100, float64(level*level*100))
	motion := !c.WindowHidden() && c.WindowActive() && !c.Preferences().ReduceMotion
	amplitude := level
	if phase == "listening" && motion {
		amplitude = stage.Animate("input", level, 180*time.Millisecond)
	}
	v.avatar.observe(phase, c.Now())
	colors := liveAvatarColors(c)
	stage.Draw(func(p *ui.Painter, r ui.Rect) {
		v.avatar.draw(p, r, amplitude, motion, colors)
	})
}

func (v *LiveView) settings(c *ui.Context, anchor ui.Element, width float32, disabled bool) {
	s := v.Client
	ui.Popover(c, anchor, &v.settingsOpen, func() {
		ui.Column(c).Width(width).Padding(12).Gap(14).Children(func() {
			ui.Row(c).FillWidth().Children(func() {
				ui.Text(c, v.text("收听设置", "Listening settings")).Grow(1).FontSize(16).Bold()
				if textButton(c, v.text("完成", "Done")).Clicked() {
					v.settingsOpen = false
				}
			})
			ids, labels := []string{""}, []string{v.text("请选择麦克风", "Select a microphone")}
			for index, d := range s.Options.Devices {
				ids = append(ids, d.ID)
				labels = append(labels, fmt.Sprintf("%s (%d)", d.Name, index+1))
			}
			v.selectField(c, v.text("输入设备", "Input device"), &s.Request.DeviceID, ids, labels, disabled)
			if textButton(c, v.text("刷新设备", "Refresh devices")).Disabled(disabled).Clicked() {
				s.RefreshOptions()
			}
			separator(c)
			ui.Checkbox(c, &s.Request.Translate, v.text("自动中英互译", "Automatically translate Chinese ↔ English")).Disabled(disabled)
			if !s.Options.ModelReady || !s.Options.TranslationReady {
				ui.Text(c, v.text("首次使用需下载约 990 MiB 本地模型。", "First use downloads about 990 MiB of local models.")).FontSize(12).TextColor(c.Theme().TextMuted)
			}
			if textButton(c, v.text("运行环境设置", "Runtime settings")).Disabled(domain.LiveActive(s.Snapshot.Session.State)).Clicked() && v.OpenSettings != nil {
				v.settingsOpen = false
				v.OpenSettings()
			}
		})
	}).Label(v.text("收听设置面板", "Listening settings panel"))
}

func (v *LiveView) focusedCaptions(c *ui.Context) {
	s := v.Client
	var latest *domain.LiveSegment
	if n := len(s.Snapshot.Segments); n > 0 {
		latest = &s.Snapshot.Segments[n-1]
	}
	if latest == nil && s.Snapshot.Partial == nil {
		return
	}
	ui.Column(c).FillWidth().MaxWidth(720).Padding(16, 12).Gap(14).Label(v.text("当前字幕", "Current caption")).Children(func() {
		if latest != nil {
			ui.Text(c, latest.Text).FillWidth().FontSize(18).TextAlign(ui.Center).Selectable()
			if latest.Translation != "" {
				ui.Text(c, latest.Translation).FillWidth().FontSize(21).TextColor(palette(c).NavAccent).TextAlign(ui.Center).Selectable()
			} else if s.Snapshot.Session.Translate {
				label := v.text("正在翻译…", "Translating…")
				if latest.TranslationState == "failed" {
					label = v.text("译文待补充，可在完整字幕中重试。", "Translation pending. Retry from full captions.")
				}
				ui.Text(c, label).FillWidth().FontSize(14).TextColor(c.Theme().TextMuted).TextAlign(ui.Center)
			}
		}
		if partial := s.Snapshot.Partial; partial != nil {
			ui.Text(c, partial.Text).FillWidth().FontSize(16).TextColor(c.Theme().TextMuted).TextAlign(ui.Center).Selectable()
			ui.Text(c, v.text("识别中…", "Recognizing…")).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted).TextAlign(ui.Center)
		}
		if textButton(c, v.text("查看完整字幕", "View full captions")).AlignSelf(ui.Center).MinHeight(36).Disabled(s.Busy).Clicked() {
			v.mode = "review"
			if domain.LiveActive(s.Snapshot.Session.State) {
				s.ShowCurrent()
			} else {
				s.Select(s.Snapshot.Session, 0)
			}
		}
	})
}

func (v *LiveView) selectField(c *ui.Context, label string, id *string, ids, labels []string, disabled bool) {
	selected := labels[0]
	for index, value := range ids {
		if value == *id {
			selected = labels[index]
		}
	}
	ui.Text(c, label).FontSize(12)
	if ui.Select(c, &selected, labels).FillWidth().Label(label).Disabled(disabled).Changed() {
		for index, value := range labels {
			if value == selected {
				*id = ids[index]
				break
			}
		}
	}
}
func (v *LiveView) history(c *ui.Context) {
	s := v.Client
	ui.Column(c).FillWidth().MaxWidth(800).AlignSelf(ui.Center).Padding(12).Gap(12).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, v.text("历史会话", "Session history")).Grow(1).FontSize(14).Bold()
			if textButton(c, v.text("刷新", "Refresh")).Disabled(s.Busy || s.Preparing).Clicked() {
				s.RefreshHistory(s.HistoryOffset)
			}
		})
		for _, session := range s.History {
			if ui.ButtonBase(c.Key(session.ID)).FillWidth().Padding(8).Radius(6).Label(createdText(v.Locale, session.CreatedAt)).Disabled(s.Busy || v.editing != nil).Children(func() {
				ui.Column(c).FillWidth().Gap(3).Children(func() {
					ui.Text(c, createdText(v.Locale, session.CreatedAt)).FontSize(12)
					ui.Text(c, fmt.Sprintf("%s · %d", liveStatus(v.Locale, session.State), session.SegmentCount)).FontSize(11).TextColor(c.Theme().TextMuted)
				})
			}).Clicked() {
				v.mode = "review"
				s.Select(session, 0)
			}
			separator(c)
		}
		if len(s.History) == 0 {
			ui.Text(c, v.text("暂无历史会话", "No sessions yet")).FontSize(12).TextColor(c.Theme().TextMuted)
		}
		ui.Row(c).Gap(8).Children(func() {
			if textButton(c, v.text("上一页", "Previous")).Disabled(s.Busy || s.HistoryOffset == 0).Clicked() {
				s.RefreshHistory(max(0, s.HistoryOffset-20))
			}
			if textButton(c, v.text("下一页", "Next")).Disabled(s.Busy || len(s.History) < 20).Clicked() {
				s.RefreshHistory(s.HistoryOffset + 20)
			}
		})
	})
}
func (v *LiveView) captions(c *ui.Context, height float32) {
	s := v.Client
	session := s.Snapshot.Session
	segments := s.Snapshot.Segments
	partial := s.Snapshot.Partial
	if s.Selected != nil {
		session = *s.Selected
		segments = s.Segments
		partial = nil
	}
	identity := session.ID + fmt.Sprint(s.Selected != nil, s.SegmentAfter)
	if identity != v.listSession {
		v.list = ui.ListState{FollowEnd: s.Selected == nil}
		v.listSession = identity
		v.editing = nil
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, v.text("双语字幕", "Bilingual captions")).Grow(1).FontSize(17).Bold()
		for _, format := range []string{"srt", "vtt", "txt"} {
			if textButton(c, strings.ToUpper(format)).Disabled(s.Busy || v.editing != nil || session.ID == "" || domain.LiveActive(session.State)).Clicked() {
				s.Export(format)
			}
		}
	})
	if s.Exported {
		ui.Text(c, v.text("已导出。", "Exported.")).TextColor(c.Theme().Success).FontSize(12).Role(ui.RoleStatus)
	}
	if v.editing != nil {
		ui.Column(c).FillWidth().Padding(16).Gap(8).Radius(8).Background(palette(c).Subpanel).Children(func() {
			ui.Text(c, v.text("编辑定稿", "Edit finalized caption")).FontSize(14).Bold()
			ui.TextArea(c, &v.editing.Text).FillWidth().Height(70).Label(v.text("原文", "Original text")).Disabled(s.Busy)
			if session.Translate {
				ui.TextArea(c, &v.editing.Translation).FillWidth().Height(70).Label(v.text("译文", "Translation")).Disabled(s.Busy)
				ui.Text(c, v.text("修改原文后，请同步修改译文，或清空译文后补译。", "After editing the original, update the translation or clear it and retry translation.")).FontSize(11).TextColor(c.Theme().TextMuted)
			}
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, v.text("保存修改", "Save changes")).Disabled(s.Busy || strings.TrimSpace(v.editing.Text) == "").Clicked() {
					s.Edit(*v.editing, func() { v.editing = nil })
				}
				if textButton(c, v.text("取消", "Cancel")).Disabled(s.Busy).Clicked() {
					v.editing = nil
				}
			})
		})
	}
	if s.Selected == nil && s.Snapshot.TranslationPending > 0 {
		ui.Text(c, fmt.Sprintf(v.text("%d 段译文正在处理或等待补译。", "%d translations are processing or awaiting retry."), s.Snapshot.TranslationPending)).TextColor(c.Theme().TextMuted).FontSize(12).Role(ui.RoleStatus)
	}
	count := len(segments)
	if partial != nil {
		count++
	}
	if count == 0 {
		ui.Column(c).FillWidth().Height(height).Padding(28).Radius(10).Background(palette(c).Panel).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			ui.Text(c, v.text("原文与译文会显示在这里", "Original text and translations appear here")).FontSize(17)
			ui.Text(c, v.text("选择麦克风后开始收听。暂停会释放麦克风。", "Select a microphone and start listening. Pausing releases the microphone.")).FontSize(12).TextColor(c.Theme().TextMuted)
		})
		if s.Selected != nil && textButton(c, v.text("删除空会话", "Delete empty session")).Disabled(s.Busy || domain.LiveActive(session.State)).Clicked() && v.ConfirmDelete != nil {
			v.ConfirmDelete()
		}
		return
	}
	v.list.Key = func(index int) any {
		if index < len(segments) {
			return segments[index].ID
		}
		return partial.ID
	}
	ui.List(c, &v.list, count, func(index int) {
		pending := index >= len(segments)
		var segment domain.LiveSegment
		if pending {
			segment = *partial
		} else {
			segment = segments[index]
		}
		ui.Column(c).FillWidth().Padding(16, 8).Gap(10).Children(func() {
			label := liveStamp(segment.StartMS)
			if pending {
				label += " · " + v.text("识别中", "Recognizing")
			}
			ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
			if s.Selected != nil && !domain.LiveActive(session.State) && textButton(c, v.text("编辑", "Edit")).Disabled(s.Busy || v.editing != nil).Clicked() {
				copy := segment
				v.editing = &copy
			}
			ui.Text(c, segment.Text).FillWidth().FontSize(16).Selectable()
			if segment.Translation != "" {
				ui.Text(c, segment.Translation).FillWidth().FontSize(15).TextColor(c.Theme().Accent).Selectable()
				if segment.TranslationState == "completed" && segment.TranslationMS > 0 {
					ui.Text(c, fmt.Sprintf(v.text("翻译耗时 %.1f 秒", "Translated in %.1f s"), float64(segment.TranslationMS)/1000)).FontSize(11).TextColor(c.Theme().TextMuted)
				}
			} else if session.Translate && !pending {
				label := v.text("等待翻译…", "Waiting for translation…")
				if segment.TranslationState == "failed" {
					label = v.text("翻译未完成，可在会话结束后补译。", "Translation failed. Retry after the session ends.")
				}
				ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
			}
			separator(c)
		})
	}).FillWidth().Height(height).Gap(10).Label(v.text("字幕列表", "Captions"))
	if s.Selected != nil {
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if textButton(c, v.text("回到开头", "First page")).Disabled(s.Busy || v.editing != nil || s.SegmentAfter == 0).Clicked() {
				s.Select(session, 0)
			}
			if textButton(c, v.text("后续字幕", "More captions")).Disabled(s.Busy || v.editing != nil || len(segments) < domain.LiveVisibleSegments).Clicked() {
				s.Select(session, segments[len(segments)-1].ID)
			}
			if textButton(c, v.text("补译失败片段", "Retry translations")).Disabled(s.Busy || v.editing != nil || !session.Translate || domain.LiveActive(session.State)).Clicked() {
				s.Retry()
			}
			if textButton(c, v.text("删除会话", "Delete session")).TextColor(c.Theme().Danger).Disabled(s.Busy || v.editing != nil || domain.LiveActive(session.State)).Clicked() && v.ConfirmDelete != nil {
				v.ConfirmDelete()
			}
		})
	}
}

type NativeLiveFiles struct {
	Parent  *mygo.Window
	Service *application.LiveService
}

func (f *NativeLiveFiles) Export(ctx context.Context, id, format string) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if format != "srt" && format != "vtt" && format != "txt" {
		return false, domain.ErrLiveInput
	}
	filename, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: f.Parent, DefaultPath: "Liberty-live." + format, Filters: []mygo.FileFilter{{Name: strings.ToUpper(format), Extensions: []string{format}}}})
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return false, fileout.ErrOutput
	}
	if filename == "" {
		return false, nil
	}
	err = f.Service.Export(ctx, id, format, func(data []byte) error { return fileout.Write(ctx, filename, data) })
	return err == nil, err
}
func (w *Workspace) AttachLive(service *application.LiveService) {
	controller := client.NewLive(service, &NativeLiveFiles{Parent: w.main, Service: service}, mygo.RunOnMain, func() {
		if !w.closed {
			w.main.Invalidate()
		}
	})
	w.live = &LiveView{Client: controller, Locale: w.preferences.Locale, OpenSettings: func() { w.navigate("/settings") }}
	w.closing.register(&closeGuard{check: func() closeCheck {
		draft := w.live.editing
		var value domain.LiveSegment
		if draft != nil {
			value = *draft
		}
		return closeCheck{busy: controller.Busy && draft != nil, dirty: draft != nil, unchanged: func() bool { return !controller.Busy && w.live.editing == draft && (draft == nil || *draft == value) }}
	}, focus: func() { w.navigate("/live"); w.main.Focus() }})
	w.live.ConfirmDelete = func() {
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: w.main, Type: mygo.MessageWarning, Title: localized(w.view.Locale, "删除会话", "Delete session"), Message: localized(w.view.Locale, "删除此会话及其原文、译文？", "Delete this session and its original text and translations?"), Buttons: []string{localized(w.view.Locale, "取消", "Cancel"), localized(w.view.Locale, "删除", "Delete")}, DefaultButton: 0, CancelButton: 0})
		if err == nil && answer.Button == 1 {
			controller.DeleteSelected()
		}
	}
	w.main.OnHide(func() { controller.SetVisible(false) })
	w.main.OnMinimize(func() { controller.SetVisible(false) })
	w.main.OnShow(func() { controller.SetVisible(w.tab == "/live") })
	w.main.OnRestore(func() { controller.SetVisible(w.tab == "/live") })
	controller.StartPolling()
	controller.SetVisible(w.tab == "/live")
}
