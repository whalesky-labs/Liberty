package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/desktop/presentation"
)

// Browsing state belongs to each resource page, separate from asynchronous reads.
type resourceBrowseState struct {
	query  string
	filter string
}

type resourceLayoutCopy struct {
	icon, intro, emptyCopy, guideTitle, footer, search string
	steps                                              [3][2]string
}

func (v *ResourcesView) layoutCopy() resourceLayoutCopy {
	t := func(zh, en string) string { return localized(v.Locale, zh, en) }
	switch v.Kind {
	case application.Models:
		return resourceLayoutCopy{
			icon: "chip", intro: t("管理用于 AI 会议纪要的模型服务。", "Manage model services for AI meeting summaries."),
			emptyCopy:  t("添加你选择的模型服务，在会议转写完成后按需生成纪要。", "Add a model service to generate summaries when your transcript is ready."),
			guideTitle: t("从转写到纪要", "From transcript to summary"),
			steps: [3][2]string{
				{t("连接模型服务", "Connect a service"), t("填写接口地址、模型 ID 与 API Key。", "Enter the endpoint, model ID and API key.")},
				{t("选择常用模型", "Choose your default"), t("启用模型，并将常用配置设为默认。", "Enable a model and set your preferred default.")},
				{t("按需整理纪要", "Summarize when needed"), t("在会议结果中主动发起 AI 总结。", "Start an AI summary from a meeting result.")},
			},
			footer: t("AI 总结为可选功能。不配置模型，也能录音、转写和导出。模型服务可能产生 API 费用。", "AI summaries are optional. Recording, transcription and export work without a model. Provider API fees may apply."),
			search: t("搜索模型名称或 ID", "Search model name or ID"),
		}
	case application.Templates:
		return resourceLayoutCopy{
			icon: "doc", intro: t("为不同会议准备合适的纪要结构和写作要求。", "Define the structure and writing style for each kind of meeting."),
			emptyCopy:  t("创建可复用的总结要求，让每次会议纪要保持一致。", "Create reusable instructions to keep your meeting summaries consistent."),
			guideTitle: t("让纪要符合你的习惯", "Make summaries your own"),
			steps: [3][2]string{
				{t("选择起点", "Choose a starting point"), t("新建模板，或复制内置模板后调整。", "Create a template or duplicate a built-in one.")},
				{t("写清总结要求", "Describe the summary"), t("定义关注重点、内容结构与表达方式。", "Specify the focus, structure and writing style.")},
				{t("在总结时选用", "Use it for a summary"), t("根据会议类型选择对应模板。", "Choose the template that fits your meeting.")},
			},
			footer: t("内置模板可查看和复制；自定义模板可编辑和删除。", "View or duplicate built-in templates. Edit or delete your custom templates."),
			search: t("搜索模板名称或说明", "Search template name or description"),
		}
	default:
		return resourceLayoutCopy{
			icon: "people", intro: t("集中管理参会人员、所属部门和会议记录人。", "Keep meeting participants, departments and recorders in one place."),
			emptyCopy:  t("添加常用参会人员，或通过 Excel 一次导入已有名单。", "Add your regular participants or import an existing list from Excel."),
			guideTitle: t("建立可复用的人员名单", "Build a reusable directory"),
			steps: [3][2]string{
				{t("添加或导入人员", "Add or import people"), t("逐个录入，也可以批量导入 Excel。", "Add people individually or import an Excel list.")},
				{t("完善部门与角色", "Organize the directory"), t("设置所属部门、排序和会议记录人。", "Set departments, display order and the recorder.")},
				{t("维护与复用", "Keep it up to date"), t("随时编辑信息，或导出名单继续整理。", "Edit details anytime or export the directory.")},
			},
			footer: v.msg("importHint"), search: t("搜索姓名或部门", "Search name or department"),
		}
	}
}

func (v *ResourcesView) count() int {
	switch v.Kind {
	case application.Models:
		return len(v.Client.Models)
	case application.Templates:
		return len(v.Client.Templates)
	default:
		return len(v.Client.Members)
	}
}

func (v *ResourcesView) addButton(c *ui.Context) {
	if ui.PrimaryButton(c, v.msg("add")).Disabled(v.Open == nil).Clicked() {
		v.Open(v.Kind, "")
	}
}

func (v *ResourcesView) pageHeader(c *ui.Context, width float32) {
	header := ui.Row(c).FillWidth().Gap(20).AlignItems(ui.Center)
	if width < 700 {
		header.Column().AlignItems(ui.Start)
	}
	header.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
			ui.Text(c, v.msg("title")).FontSize(26).Role(ui.RoleHeading).Level(1)
			ui.Text(c, v.layoutCopy().intro).FillWidth().FontSize(14).TextColor(c.Theme().TextMuted)
		})
		actions := ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center)
		if width < 700 {
			actions.FillWidth().Wrap()
		}
		actions.Children(func() {
			if v.Kind == application.Members && v.Files != nil {
				if ui.Button(c, presentation.Message(v.Locale, "common.import")).Disabled(v.Client.Busy).Clicked() {
					v.Client.Import(v.Files)
				}
				if ui.Button(c, presentation.Message(v.Locale, "common.export")).Disabled(v.Client.Busy).Clicked() {
					v.Client.Export(v.Files)
				}
			}
			if v.count() > 0 || v.Client.Busy || v.Client.Error != nil {
				v.addButton(c)
			}
		})
	})
}

func (v *ResourcesView) overview(c *ui.Context, width float32) {
	labels, values := v.metrics()
	columns := 4
	if width < 620 {
		columns = 2
	}
	p := palette(c)
	ui.Grid(c).FillWidth().Columns(columns).Gap(12).Children(func() {
		for index, label := range labels {
			ui.Column(c.Key(label)).FillWidth().MinWidth(0).Padding(14, 16).Gap(8).Radius(10).Background(p.Panel).Border(1, p.Separator).Children(func() {
				ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine()
				value := values[index]
				if v.count() == 0 && (v.Client.Busy || v.Client.Error != nil) {
					value = "—"
				}
				size := float32(22)
				if index == 3 || v.Kind == application.Members && index == 2 {
					size = 17
				}
				ui.Text(c, value).FillWidth().Height(28).FontSize(size).SingleLine().Tooltip(value)
			})
		}
	})
}

func (v *ResourcesView) emptyState(c *ui.Context, width float32) {
	if v.Client.Error != nil {
		return
	}
	p, copy := palette(c), v.layoutCopy()
	ui.Column(c).FillWidth().Radius(12).Border(1, p.Separator).Background(p.Panel).Role(ui.RoleStatus).Busy(v.Client.Busy).Children(func() {
		if v.Client.Busy {
			ui.Text(c, localized(v.Locale, "正在读取…", "Loading…")).Padding(40, 24).TextColor(c.Theme().TextMuted)
			return
		}
		body := ui.Row(c).FillWidth().Padding(28).Gap(32).AlignItems(ui.Center)
		if width < 760 {
			body.Column().AlignItems(ui.Stretch).Gap(24)
		}
		body.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(16).Children(func() {
				ui.Box(c).Size(52, 52).Radius(14).Background(p.Selected).Center().Children(func() {
					ui.Icon(c, presentation.Icon(copy.icon)).Size(26, 26).TextColor(p.NavAccent)
				})
				ui.Text(c, v.msg("empty")).FillWidth().FontSize(22).Role(ui.RoleHeading).Level(2)
				ui.Text(c, copy.emptyCopy).FillWidth().MaxWidth(440).FontSize(14).TextColor(c.Theme().TextMuted)
				ui.Row(c).Padding(4, 0).Children(func() { v.addButton(c) })
			})
			guide := ui.Column(c).Width(310).Shrink(0).Gap(18)
			if width < 760 {
				guide.FillWidth()
			}
			guide.Children(func() {
				ui.Text(c, copy.guideTitle).FontSize(14).Role(ui.RoleHeading).Level(2)
				for index, step := range copy.steps {
					ui.Row(c.Key(index)).FillWidth().Gap(12).Children(func() {
						ui.Box(c).Size(24, 24).Radius(12).Background(p.Subpanel).Center().Children(func() {
							ui.Text(c, fmt.Sprint(index+1)).FontSize(12).TextColor(c.Theme().TextMuted)
						})
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							ui.Text(c, step[0]).FontSize(13)
							ui.Text(c, step[1]).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
						})
					})
				}
			})
		})
		separator(c)
		ui.Text(c, copy.footer).FillWidth().Padding(14, 24).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}

func (v *ResourcesView) filterLabels() []string {
	labels := []string{localized(v.Locale, "全部", "All")}
	switch v.Kind {
	case application.Models:
		return append(labels, v.msg("enabledTag"), v.msg("disabledTag"))
	case application.Templates:
		return append(labels, v.msg("builtin"), v.msg("custom"))
	default:
		return append(labels, v.msg("recorderTag"), v.msg("normalTag"))
	}
}

func (v *ResourcesView) listToolbar(c *ui.Context, width float32, state *resourceBrowseState) {
	ui.Column(c).FillWidth().Gap(14).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, v.msg("listTitle")).FontSize(17).Role(ui.RoleHeading).Level(2)
			resourceTag(c, fmt.Sprint(v.count()), c.Theme().TextMuted)
		})
		row := ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center)
		if width < 760 {
			row.Column().AlignItems(ui.Stretch)
		}
		row.Children(func() {
			segmentedControl(c, &state.filter, []string{"", "first", "second"}, v.filterLabels()...).Label(localized(v.Locale, "资源分类", "Resource category"))
			if width >= 760 {
				ui.Box(c).Grow(1)
			}
			search := ui.TextInput(c.Key("resource-search"), &state.query).Search().Label(v.layoutCopy().search).Placeholder(v.layoutCopy().search)
			if width >= 760 {
				search.Width(280)
			} else {
				search.FillWidth()
			}
		})
	})
}

func (v *ResourcesView) visibleIndices(state resourceBrowseState) []int {
	query := strings.ToLower(strings.TrimSpace(state.query))
	indices := make([]int, 0, v.count())
	for index := 0; index < v.count(); index++ {
		var text string
		var first bool
		switch v.Kind {
		case application.Models:
			m := v.Client.Models[index]
			text, first = m.Name+"\n"+m.Model, m.Enabled
		case application.Templates:
			m := v.Client.Templates[index]
			text, first = m.Name+"\n"+m.Description, m.Builtin
		default:
			m := v.Client.Members[index]
			text, first = m.Name+"\n"+m.Department, m.IsRecorder
		}
		if state.filter == "first" && !first || state.filter == "second" && first {
			continue
		}
		if query == "" || strings.Contains(strings.ToLower(text), query) {
			indices = append(indices, index)
		}
	}
	return indices
}

func (v *ResourcesView) noMatches(c *ui.Context, state *resourceBrowseState) {
	ui.Column(c).FillWidth().Padding(32).Gap(12).Center().Border(1, palette(c).Separator).Radius(12).Role(ui.RoleStatus).Children(func() {
		ui.Icon(c, presentation.Icon("search")).Size(26, 26).TextColor(c.Theme().TextMuted)
		ui.Text(c, localized(v.Locale, "没有找到匹配的资源", "No matching resources")).FontSize(17)
		ui.Text(c, localized(v.Locale, "试试其他关键词，或清除筛选查看全部。", "Try another search or clear the filters to see everything.")).FillWidth().TextAlign(ui.Center).TextColor(c.Theme().TextMuted)
		if ui.Button(c, localized(v.Locale, "清除筛选", "Clear filters")).Clicked() {
			*state = resourceBrowseState{}
		}
	})
}
