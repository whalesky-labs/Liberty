package desktop

import (
	"context"
	"errors"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type MediaGrants interface {
	Authorize(string) (application.SelectedMedia, error)
	Release(string)
}
type NativeMediaPicker struct {
	Parent *mygo.Window
	Grants MediaGrants
}

func (p *NativeMediaPicker) Pick(ctx context.Context) (application.SelectedMedia, error) {
	if err := ctx.Err(); err != nil {
		return application.SelectedMedia{}, err
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: p.Parent, Multiple: false, Filters: []mygo.FileFilter{{Name: "Meeting Media", Extensions: []string{"m4a", "mp3", "wav", "aac", "flac", "mp4", "mov", "mkv"}}}})
	if ctx.Err() != nil {
		return application.SelectedMedia{}, ctx.Err()
	}
	if err != nil {
		return application.SelectedMedia{}, application.ErrJobInput
	}
	if len(paths) == 0 {
		return application.SelectedMedia{}, nil
	}
	if len(paths) != 1 {
		return application.SelectedMedia{}, application.ErrJobInput
	}
	return p.Grants.Authorize(paths[0])
}
func (p *NativeMediaPicker) Release(token string) { p.Grants.Release(token) }

func (p *NativeMediaPicker) PickMany(ctx context.Context) ([]application.SelectedMedia, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: p.Parent, Multiple: true, Filters: []mygo.FileFilter{{Name: "Meeting Media", Extensions: []string{"m4a", "mp3", "wav", "aac", "flac", "mp4", "mov", "mkv"}}}})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(paths) > 32 {
		return nil, application.ErrJobInput
	}
	selected := make([]application.SelectedMedia, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		media, err := p.Grants.Authorize(path)
		if err != nil {
			for _, prior := range selected {
				p.Grants.Release(prior.Token)
			}
			return nil, application.ErrJobInput
		}
		selected = append(selected, media)
	}
	return selected, nil
}

type NewJobView struct {
	Client      *client.SubmissionController
	Runtime     *client.RuntimeController
	Locale      string
	Remote      bool
	advanced    bool
	WarnRuntime func()
}

func (v *NewJobView) msg(key string) string {
	if key == "localFileRule" {
		return localized(v.Locale, "本地模式按文件分别创建任务", "Local mode creates a separate task for each file")
	}
	return presentation.Message(v.Locale, "newJob."+key)
}
func localJobRuntimeReady(runtime *client.RuntimeController) bool {
	return runtime != nil && runtime.Error == nil && runtime.Panel != nil && runtime.Panel.Overview.ShellReady
}
func (v *NewJobView) ready() bool {
	return !v.Remote && localJobRuntimeReady(v.Runtime)
}
func (v *NewJobView) submit() {
	if v.Remote || !v.Client.CanSubmit() {
		return
	}
	if !v.ready() {
		if v.WarnRuntime != nil {
			v.WarnRuntime()
		}
		return
	}
	v.Client.Form.SizeLabel = presentation.Message(v.Locale, "common.localPath")
	v.Client.SubmitWithDefaults(v.msg("defaultSummaryTemplateName"))
}
func (v *NewJobView) View(c *ui.Context) {
	width, _ := c.Size()
	unavailable := v.Remote || v.Client.Unavailable
	narrow := width <= 680
	contentScroll(c).Fill().Children(func() {
		page := ui.Column(c).FillWidth().MaxWidth(1180).AlignSelf(ui.Center).Padding(12, 8, 36).Gap(24)
		if narrow {
			page.Padding(8, 2, 28).Gap(18)
		}
		page.Children(func() {
			header := ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Center)
			if narrow {
				header.Column().AlignItems(ui.Start)
			}
			header.Children(func() {
				copy := ui.Column(c).MinWidth(0).Gap(6)
				if !narrow {
					copy.Grow(1)
				}
				copy.Children(func() {
					title := ui.Text(c, v.msg("heroTitle")).FontSize(28)
					if narrow {
						title.FontSize(24)
					}
					ui.Text(c, v.msg("heroCopy")).FillWidth().FontSize(14).TextColor(c.Theme().TextMuted)
				})
				mode := "localPython"
				if unavailable {
					mode = "remoteService"
				}
				ui.Text(c, v.msg(mode)).FontSize(12).Padding(6, 10).Radius(16).Background(palette(c).Subpanel).Label(v.msg("currentStatus") + ": " + v.msg(mode))
			})
			var layout ui.Element
			if width <= 980 {
				layout = ui.Column(c).FillWidth().Gap(24)
			} else {
				layout = ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Start)
			}
			layout.Children(func() {
				source := ui.Column(c).MinWidth(0).MinHeight(520).Padding(24).Gap(20).Radius(14).Background(palette(c).Panel)
				if width <= 980 {
					source.FillWidth()
				} else {
					source.Grow(1)
				}
				if narrow {
					source.MinHeight(0).Padding(18)
				}
				source.Children(func() {
					v.heading(c, "01", "inputFiles", "localFileRule")
					if v.Client.SelectionCount() == 0 {
						b := ui.ButtonBase(c).FillWidth().MinHeight(342).Padding(32).Radius(12).Border(1, c.Theme().Accent.Alpha(0.38)).Background(palette(c).Selected.Alpha(0.3)).Label(v.msg("addFiles")).Disabled(unavailable || v.Client.Picking || v.Client.Busy)
						if narrow {
							b.MinHeight(280).Padding(24, 18)
						}
						b.Children(func() {
							ui.Column(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
								ui.Icon(c, presentation.Icon("tray")).Size(42, 42).TextColor(palette(c).NavAccent)
								ui.Text(c, v.msg("addFiles")).FontSize(17)
								ui.Text(c, v.msg("desktopFilePicker")).FillWidth().TextAlign(ui.Center).FontSize(13).TextColor(c.Theme().TextMuted)
								ui.Text(c, v.msg("supportedFormats")).FillWidth().TextAlign(ui.Center).FontSize(11).TextColor(c.Theme().TextMuted)
							})
						})
						if b.Clicked() {
							v.Client.Pick()
						}
					} else {
						selection := ui.Column(c).FillWidth().MinHeight(342).Gap(14)
						if narrow {
							selection.MinHeight(280)
						}
						selection.Children(func() {
							selectedHeader := ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center)
							if narrow {
								selectedHeader.Column().AlignItems(ui.Start)
							}
							selectedHeader.Children(func() {
								count := ui.Text(c, presentation.Format(v.Locale, "newJob.selectedFiles", map[string]any{"count": v.Client.SelectionCount()})).FontSize(13)
								if !narrow {
									count.Grow(1)
								}
								ui.Row(c).Gap(8).Children(func() {
									if textButton(c, v.msg("reselect")).Disabled(unavailable || v.Client.Busy || v.Client.Picking).Clicked() {
										v.Client.Pick()
									}
									if textButton(c, v.msg("clearList")).Disabled(v.Client.Busy).Clicked() {
										v.Client.Clear()
									}
								})
							})
							if len(v.Client.Batch) > 0 {
								v.batchFiles(c)
							} else if media := v.Client.Media; media != nil {
								ui.Row(c).FillWidth().Padding(12).Radius(10).Background(palette(c).Subpanel).Gap(12).Children(func() {
									ui.Icon(c, presentation.Icon("doc")).Size(32, 32)
									ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
										ui.Text(c, media.Name).FillWidth().FontSize(13).SingleLine().Tooltip(media.Name)
										ui.Text(c, presentation.Message(v.Locale, "common."+media.Kind)+" · "+presentation.Message(v.Locale, "common.localPath")).FontSize(11).TextColor(c.Theme().TextMuted)
									})
									label := presentation.Format(v.Locale, "newJob.removeFile", map[string]any{"name": media.Name})
									if ui.Button(c, presentation.Message(v.Locale, "common.delete")).MinHeight(44).Shrink(0).Label(label).Tooltip(label).Disabled(v.Client.Busy).Clicked() {
										v.Client.Clear()
									}
								})
							}
						})
					}
					ui.Text(c, localized(v.Locale, "一次最多选择 32 个文件；每个文件单独进入任务队列，失败项可单独重试。", "Select up to 32 files. Each file enters the task queue separately; failed admissions can be retried.")).FillWidth().FontSize(11).TextColor(c.Theme().TextMuted)
					ui.Text(c, v.msg("mediaSupported")).FontSize(11).TextColor(c.Theme().TextMuted)
				})
				config := ui.Column(c).Width(340).Shrink(0).Radius(14).Background(palette(c).Panel)
				if width <= 980 {
					config.FillWidth()
				}
				config.Children(func() {
					ui.Column(c).FillWidth().Padding(22, 20, 20).Gap(20).Children(func() {
						v.heading(c, "02", "basicInfo", "")
						ui.Column(c).FillWidth().Gap(8).Children(func() {
							ui.Text(c, v.msg("jobTitle")+" *").FontSize(12)
							if len(v.Client.Batch) > 0 {
								ui.Text(c, localized(v.Locale, "在左侧逐个修改任务名称；以下设置应用于待创建的全部文件。", "Edit each title in the file list. The settings below apply to all pending files.")).FillWidth().FontSize(12)
							} else if ui.TextInput(c, &v.Client.Form.Title).FillWidth().Label(v.msg("jobTitle")).Placeholder(v.msg("titlePlaceholder")).Disabled(v.Client.Busy).AutoFocus().Submitted() {
								v.submit()
							}
						})
						separator(c)
						ui.Text(c, v.msg("advancedSettings")).FontSize(13)
						language := ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center)
						if narrow {
							language.Column().AlignItems(ui.Stretch)
						}
						language.Children(func() {
							copy := ui.Column(c).MinWidth(0).Gap(3)
							if !narrow {
								copy.Grow(1)
							}
							copy.Children(func() {
								ui.Text(c, v.msg("language")).FontSize(12)
								ui.Text(c, v.msg("languageHint")).FillWidth().FontSize(10).TextColor(c.Theme().TextMuted)
							})
							labels := []string{v.msg("langZh"), v.msg("langEn"), v.msg("langJa")}
							ids := []string{"zh-CN", "en-US", "ja-JP"}
							selected := labels[0]
							for j, id := range ids {
								if id == v.Client.Form.Language {
									selected = labels[j]
								}
							}
							selectLanguage := selectControl(c, &selected, labels).Width(108).Label(v.msg("language")).Disabled(v.Client.Busy)
							if narrow {
								selectLanguage.FillWidth()
							}
							if selectLanguage.Changed() {
								for j, label := range labels {
									if label == selected {
										v.Client.Form.Language = ids[j]
									}
								}
							}
						})
						separator(c)
						speaker := ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center)
						if narrow {
							speaker.Column().AlignItems(ui.Start)
						}
						speaker.Children(func() {
							copy := ui.Column(c).MinWidth(0).Gap(3)
							if !narrow {
								copy.Grow(1)
							}
							copy.Children(func() {
								ui.Text(c, v.msg("speaker")).FontSize(12)
								ui.Text(c, v.msg("speakerHint")).FillWidth().FontSize(10).TextColor(c.Theme().TextMuted)
							})
							ui.Switch(c, &v.Client.Form.Speaker).Label(v.msg("speaker")).Disabled(v.Client.Busy)
						})
						separator(c)
						ui.Text(c, presentation.Message(v.Locale, "settings.defaultSummaryTemplate")).FontSize(12)
						template := strings.TrimSpace(v.Client.Form.SummaryTemplate)
						if template == "" {
							template = v.msg("defaultSummaryTemplateName")
						}
						ui.Text(c, template).FillWidth().SingleLine().Tooltip(template).FontSize(10).TextColor(c.Theme().TextMuted)
						separator(c)
						if textButton(c, v.msg("hotwords")).Role(ui.RoleButton).Expanded(v.advanced).Clicked() {
							v.advanced = !v.advanced
						}
						if v.advanced {
							if ui.TextArea(c, &v.Client.Form.Hotwords).FillWidth().Height(90).Label(v.msg("hotwords")).Placeholder(v.msg("hotwordsPlaceholder")).Disabled(v.Client.Busy).Changed() {
								v.Client.SetHotwords(v.Client.Form.Hotwords)
							}
							ui.Text(c, v.msg("hotwordsHint")).FillWidth().FontSize(10).TextColor(c.Theme().TextMuted)
						}
					})
					separator(c)
					ui.Column(c).FillWidth().Padding(16, 20, 20).Gap(12).Background(palette(c).Subpanel).Children(func() {
						if unavailable {
							ui.Text(c, v.msg("remoteUploadUnavailable")).FillWidth().FontSize(11).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
						} else if v.Client.Error != nil {
							message := v.msg("createFailed")
							var defaultsError *client.JobDefaultsError
							var mediaPickError *client.MediaPickError
							switch {
							case errors.As(v.Client.Error, &defaultsError):
								message = localized(v.Locale, "无法读取任务默认设置，请重试。", "Could not load job defaults. Please retry.")
							case errors.As(v.Client.Error, &mediaPickError):
								message = localized(v.Locale, "无法读取所选媒体，请重新选择文件。", "Could not read the selected media. Reselect the file.")
							case errors.Is(v.Client.Error, domain.ErrRuntime):
								message = v.msg("pendingEnvHint")
							case errors.Is(v.Client.Error, application.ErrJobInput):
								message = localized(v.Locale, "无法读取所选媒体，请重新选择文件并检查任务配置。", "Could not read the selected media. Reselect the file and check the job setup.")
							}
							ui.Text(c, message).FillWidth().FontSize(11).TextColor(c.Theme().Danger).Role(ui.RoleAlert)
						}
						hint := "readyToCreate"
						color := c.Theme().Success
						switch {
						case unavailable:
							hint = "remoteUploadUnavailable"
							color = c.Theme().Danger
						case v.Client.Media == nil:
							hint = "selectMediaFirst"
							color = c.Theme().TextMuted
						case strings.TrimSpace(v.Client.Form.Title) == "":
							hint = "enterTitle"
							color = c.Theme().TextMuted
						case !v.ready():
							hint = "pendingEnvHint"
							color = c.Theme().Warning
						}
						ui.Text(c, v.msg(hint)).FillWidth().FontSize(11).TextColor(color).Role(ui.RoleStatus)
						label := "createJob"
						if v.Client.Busy {
							label = "creating"
						}
						caption := v.msg(label)
						if len(v.Client.Batch) > 0 && !v.Client.Busy {
							caption = localized(v.Locale, "创建待处理任务", "Create pending jobs")
						}
						if ui.PrimaryButton(c, caption).FillWidth().MinHeight(46).Disabled(unavailable || !v.Client.CanSubmit()).Clicked() {
							v.submit()
						}
					})
				})
			})
		})
	})
}
func (v *NewJobView) heading(c *ui.Context, step, title, hint string) {
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.Row(c).Size(30, 30).Shrink(0).Radius(15).Background(palette(c).Selected).AlignItems(ui.Center).Justify(ui.Center).Children(func() { ui.Text(c, step).SingleLine().FontSize(11).TextColor(palette(c).NavAccent) })
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, v.msg(title)).FontSize(17)
			if hint != "" {
				ui.Text(c, v.msg(hint)).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
			}
		})
	})
}
