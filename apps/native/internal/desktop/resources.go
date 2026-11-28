package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

type ResourcesView struct {
	Files        client.MemberFiles
	Client       *client.ResourceController
	Kind         application.ResourceKind
	Locale       string
	Open         func(application.ResourceKind, string)
	Confirm      func(application.ResourceKind, string) func(context.Context) (bool, error)
	ContentWidth func(*ui.Context) float32
}

func (v *ResourcesView) msg(key string) string {
	return presentation.Message(v.Locale, string(v.Kind)+"."+key)
}
func (v *ResourcesView) View(c *ui.Context) {
	width, _ := c.Size()
	if v.ContentWidth != nil {
		width = v.ContentWidth(c)
	}
	width = min(width, shellContentMaxWidth)
	state := ui.Local(c.Root(), "resource-browser-"+string(v.Kind), func() resourceBrowseState { return resourceBrowseState{} })
	contentScroll(c).Fill().Children(func() {
		ui.Column(c).FillWidth().MaxWidth(shellContentMaxWidth).AlignSelf(ui.Center).Padding(16, 8, 32).Gap(24).Children(func() {
			v.pageHeader(c, width)
			v.overview(c, width)
			if v.Client.Error != nil {
				ui.Text(c, resourceError(v.Locale, v.Client.Error)).FillWidth().TextColor(c.Theme().Danger).Role(ui.RoleAlert)
				if textButton(c, localized(v.Locale, "刷新", "Refresh")).Disabled(v.Client.Busy).Clicked() {
					v.Client.Refresh(v.Kind)
				}
			}
			if v.Client.CleanupPending {
				ui.Text(c, localized(v.Locale, "已保存，旧凭据清理将在下次启动时重试。", "Saved. Retired credentials will be cleaned up on the next start.")).FillWidth().TextColor(c.Theme().Warning).Role(ui.RoleStatus)
			}
			if v.Kind == application.Members {
				if result := v.Client.ImportResult; result != nil {
					ui.Text(c, presentation.Format(v.Locale, "members.importSuccess", map[string]any{"created": result.Created, "updated": result.Updated})).FillWidth().FontSize(13).Role(ui.RoleStatus)
				}
				if v.Client.Exported {
					ui.Text(c, localized(v.Locale, "人员信息已导出。", "Members exported.")).FillWidth().FontSize(13).Role(ui.RoleStatus)
				}
			}
			if v.count() == 0 {
				v.emptyState(c, width)
				return
			}
			ui.Column(c).FillWidth().Gap(16).Children(func() {
				v.listToolbar(c, width, state)
				indices := v.visibleIndices(*state)
				if len(indices) == 0 {
					v.noMatches(c, state)
				} else if v.Kind == application.Members {
					v.members(c, max(0, width-16), indices)
				} else {
					columns := min(v.count(), max(1, int(width/336)))
					ui.Grid(c).FillWidth().Columns(columns).Gap(16).Role(ui.RoleList).Label(v.msg("listTitle")).Busy(v.Client.Busy).Children(func() {
						for _, index := range indices {
							v.card(c, index)
						}
					})
				}
				ui.Text(c, v.layoutCopy().footer).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
			})
		})
	})
}
func (v *ResourcesView) metrics() ([]string, []string) {
	common := func(key string) string { return presentation.Message(v.Locale, "common."+key) }
	switch v.Kind {
	case application.Models:
		enabled := 0
		defaultName := common("notSet")
		for _, m := range v.Client.Models {
			if m.Enabled {
				enabled++
			}
		}
		for _, enabledOnly := range []bool{true, false} {
			found := false
			for _, m := range v.Client.Models {
				if m.IsDefault && (!enabledOnly || m.Enabled) {
					defaultName, found = m.Name, true
					break
				}
			}
			if found {
				break
			}
		}
		return []string{v.msg("total"), v.msg("enabled"), v.msg("disabledTag"), v.msg("defaultLabel")}, []string{fmt.Sprint(len(v.Client.Models)), fmt.Sprint(enabled), fmt.Sprint(len(v.Client.Models) - enabled), defaultName}
	case application.Templates:
		builtin := 0
		latest, stamp := common("noData"), ""
		for _, m := range v.Client.Templates {
			if m.Builtin {
				builtin++
			}
			if m.UpdatedAt > stamp {
				latest, stamp = m.Name, m.UpdatedAt
			}
		}
		return []string{v.msg("total"), v.msg("builtin"), v.msg("custom"), v.msg("latest")}, []string{fmt.Sprint(len(v.Client.Templates)), fmt.Sprint(builtin), fmt.Sprint(len(v.Client.Templates) - builtin), latest}
	default:
		departments := map[string]bool{}
		recorder, latest, stamp := common("notSet"), common("noData"), ""
		recorderFound := false
		for _, m := range v.Client.Members {
			if department := strings.TrimSpace(m.Department); department != "" {
				departments[department] = true
			}
			if m.IsRecorder && !recorderFound {
				recorder = m.Name
				recorderFound = true
			}
			if m.UpdatedAt > stamp {
				latest, stamp = m.Name, m.UpdatedAt
			}
		}
		return []string{v.msg("total"), v.msg("departmentCount"), v.msg("recorder"), v.msg("latest")}, []string{fmt.Sprint(len(v.Client.Members)), fmt.Sprint(len(departments)), recorder, latest}
	}
}
func (v *ResourcesView) card(c *ui.Context, index int) {
	var id, name, detail, updated, tag string
	tagColor := c.Theme().TextMuted
	deletable := true
	defaultModel := false
	if v.Kind == application.Models {
		m := v.Client.Models[index]
		id, name, detail, updated = m.ID, m.Name, m.Model, m.UpdatedAt
		tag = v.msg("disabledTag")
		if m.Enabled {
			tag = v.msg("enabledTag")
			tagColor = c.Theme().Success
		}
		defaultModel = m.IsDefault
	} else {
		m := v.Client.Templates[index]
		id, name, detail, updated = m.ID, m.Name, m.Description, m.UpdatedAt
		if detail == "" {
			detail = v.msg("emptyDescription")
		}
		tag = v.msg("custom")
		if m.Builtin {
			tag = v.msg("builtin")
			deletable = false
			tagColor = palette(c).NavAccent
		}
	}
	card := ui.Column(c.Key(id)).FillWidth().FillHeight().MinWidth(0).MinHeight(226).Radius(12).Border(1, palette(c).Separator).Background(palette(c).Panel).Role(ui.RoleListItem).Label(name).Focusable()
	if card.Hovered() {
		card.Background(palette(c).Subpanel)
	}
	card.Children(func() {
		ui.Column(c).FillWidth().Padding(18, 18, 0).Gap(14).Children(func() {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Size(40, 40).Shrink(0).Radius(8).Background(palette(c).Selected).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Icon(c, presentation.Icon(v.layoutCopy().icon)).Size(20, 20).TextColor(palette(c).NavAccent)
				})
				title := ui.Text(c, name).Grow(1).MinWidth(0).SingleLine().FontSize(17).Tooltip(name)
				if v.Kind == application.Templates {
					title.Role(ui.RoleHeading).Level(3)
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				resourceTag(c, tag, tagColor)
				if defaultModel {
					resourceTag(c, v.msg("defaultTag"), palette(c).NavAccent)
				}
			})
		})
		ui.Column(c).FillWidth().Grow(1).Padding(18).Gap(12).Children(func() {
			if v.Kind == application.Templates {
				ui.Text(c, detail).FillWidth().FontSize(14).TextColor(c.Theme().TextMuted).MaxLines(3).MinHeight(60).Tooltip(detail)
			} else {
				ui.Text(c, v.msg("model")).FontSize(12).TextColor(c.Theme().TextMuted)
				ui.Text(c, detail).FillWidth().FontSize(15).SingleLine().Tooltip(detail)
			}
		})
		separator(c)
		ui.Row(c).FillWidth().Padding(10, 14).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, resourceUpdatedText(v.Locale, updated)).Grow(1).MinWidth(0).SingleLine().FontSize(11).TextColor(c.Theme().TextMuted).Tooltip(v.msg("updatedAt"))
			ui.Row(c).Shrink(0).Gap(4).Children(func() { v.actions(c, id, name, deletable) })
		})
	})
	keyboardOpen := card.Focused() && card.Shortcut(0, ui.KeyEnter) && !c.ShortcutRepeated()
	if v.Open != nil && (card.DoubleClicked() || keyboardOpen) {
		v.Open(v.Kind, id)
	}
}

func resourceTag(c *ui.Context, label string, color ui.Color) {
	ui.Text(c, label).FontSize(12).Padding(3, 8).Radius(12).Background(color.Alpha(.1)).TextColor(color).SingleLine()
}

func (v *ResourcesView) actions(c *ui.Context, id, name string, deletable bool) {
	edit := presentation.Message(v.Locale, "common.edit")
	if textButton(c, edit).Label(edit + ": " + name).Disabled(v.Open == nil).Clicked() {
		v.Open(v.Kind, id)
	}
	if deletable {
		remove := presentation.Message(v.Locale, "common.delete")
		if textButton(c, remove).Label(remove + ": " + name).TextColor(c.Theme().Danger).Disabled(v.Client.Busy || v.Confirm == nil).Clicked() {
			v.Client.Delete(v.Kind, id, v.Confirm(v.Kind, name))
		}
	}
}

func resourceUpdatedText(locale, value string) string {
	stamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	stamp = stamp.In(time.Local)
	if locale == "en-US" {
		return stamp.Format("01/02/06, 03:04 PM")
	}
	return stamp.Format("06/01/02 15:04")
}

var memberColumns = []struct {
	key   string
	width float32
}{
	{"name", 200}, {"department", 160}, {"sortOrder", 80}, {"role", 150}, {"updatedAt", 170},
}

func (v *ResourcesView) members(c *ui.Context, availableWidth float32, indices []int) {
	const minWidth, actionsWidth = 920, 160
	width := max(minWidth, availableWidth)
	ui.ScrollHorizontal(c).FillWidth().Shrink(0).Radius(12).Children(func() {
		ui.Column(c).Width(width).Role(ui.RoleTable).Label(v.msg("listTitle")).Busy(v.Client.Busy).RowCount(len(indices) + 1).ColumnCount(6).Children(func() {
			ui.Row(c).Height(44).Shrink(0).AlignItems(ui.Center).Background(palette(c).Subpanel).Role(ui.RoleRow).RowIndex(1).Children(func() {
				for index, column := range memberColumns {
					columnWidth := column.width
					if index == 0 {
						columnWidth += width - minWidth
					}
					ui.Text(c, v.msg(column.key)).Width(columnWidth).Shrink(0).Padding(0, 12).FontSize(14).TextColor(c.Theme().TextMuted).Role(ui.RoleColumnHeader).RowIndex(1).ColumnIndex(index + 1)
				}
				ui.Row(c).Width(actionsWidth).FillHeight().Shrink(0).Padding(0, 12).Justify(ui.End).AlignItems(ui.Center).Background(palette(c).Subpanel).Sticky().Right(0).Role(ui.RoleColumnHeader).Label(v.msg("actions")).RowIndex(1).ColumnIndex(6).Children(func() {
					ui.Text(c, v.msg("actions")).FontSize(14).TextColor(c.Theme().TextMuted)
				})
			})
			for index, sourceIndex := range indices {
				member := v.Client.Members[sourceIndex]
				rowIndex := index + 2
				row := ui.Row(c.Key(member.ID)).FillWidth().Height(58).Shrink(0).AlignItems(ui.Center).Role(ui.RoleRow).RowIndex(rowIndex).Label(member.Name).Focusable()
				rowColor := palette(c).Panel
				if row.Hovered() {
					rowColor = rowColor.Mix(c.Theme().Accent, .05)
				}
				row.Background(rowColor)
				row.Children(func() {
					for columnIndex, column := range memberColumns {
						columnWidth := column.width
						if columnIndex == 0 {
							columnWidth += width - minWidth
						}
						ui.Row(c.Key(column.key)).Width(columnWidth).FillHeight().Shrink(0).Padding(0, 12).Clip().AlignItems(ui.Center).Role(ui.RoleCell).RowIndex(rowIndex).ColumnIndex(columnIndex + 1).Children(func() {
							v.memberCell(c, member, column.key)
						})
					}
					ui.Row(c).Width(actionsWidth).FillHeight().Shrink(0).Padding(0, 12).Gap(4).Justify(ui.End).AlignItems(ui.Center).Background(rowColor).Sticky().Right(0).Role(ui.RoleCell).RowIndex(rowIndex).ColumnIndex(6).Children(func() {
						v.actions(c, member.ID, member.Name, true)
					})
				})
				keyboardOpen := row.Focused() && row.Shortcut(0, ui.KeyEnter) && !c.ShortcutRepeated()
				if v.Open != nil && (row.DoubleClicked() || keyboardOpen) {
					v.Open(v.Kind, member.ID)
				}
				separator(c)
			}
		})
	})
}

func (v *ResourcesView) memberCell(c *ui.Context, member domain.MeetingMember, key string) {
	switch key {
	case "name":
		initial := "?"
		if name := []rune(strings.TrimSpace(member.Name)); len(name) > 0 {
			initial = strings.ToUpper(string(name[0]))
		}
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Size(36, 36).Shrink(0).Radius(18).Background(palette(c).Selected).Center().Children(func() {
				ui.Text(c, initial).FontSize(14).TextColor(palette(c).NavAccent)
			})
			ui.Text(c, member.Name).Grow(1).MinWidth(0).SingleLine().FontSize(15).Tooltip(member.Name)
		})
	case "department":
		department := member.Department
		if department == "" {
			department = v.msg("emptyDepartment")
		}
		ui.Text(c, department).FillWidth().SingleLine().FontSize(14).Tooltip(department)
	case "sortOrder":
		ui.Text(c, fmt.Sprint(member.SortOrder)).FontSize(14).TextColor(c.Theme().TextMuted)
	case "role":
		key, color := "normalTag", c.Theme().TextMuted
		if member.IsRecorder {
			key, color = "recorderTag", palette(c).NavAccent
		}
		resourceTag(c, v.msg(key), color)
	case "updatedAt":
		ui.Text(c, resourceUpdatedText(v.Locale, member.UpdatedAt)).FontSize(14).TextColor(c.Theme().TextMuted).SingleLine()
	}
}

func resourceError(locale string, err error) string {
	switch err {
	case domain.ErrBuiltinTemplate:
		return localized(locale, "内置模板为只读，可复制后修改。", "Built-in templates are read-only. Duplicate one to edit it.")
	case domain.ErrCredentialRequired:
		return localized(locale, "请填写必填信息和 API Key。", "Complete the required fields and API key.")
	case domain.ErrCredentialStore:
		return localized(locale, "系统凭据存储不可用，请检查系统授权后重试。", "System credential storage is unavailable. Check system access and retry.")
	case domain.ErrResourceInvalid:
		return localized(locale, "请检查必填字段和数字格式。", "Check required fields and number formats.")
	default:
		return localized(locale, "操作未完成，请重试。", "The operation did not complete. Please retry.")
	}
}

type ResourceEditorView struct {
	Client       *client.ResourceEditor
	Locale       string
	Preferences  domain.UIPreferences
	Confirm      func(string, string) func(context.Context) (bool, error)
	focusInvalid bool
}

func (v *ResourceEditorView) msg(key string) string {
	return presentation.Message(v.Locale, string(v.Client.Draft.Kind)+"."+key)
}
func (v *ResourceEditorView) View(c *ui.Context) {
	p := presentation.PreferenceColors(c.Theme().Dark, v.Preferences)
	ui.Themed(c, &p.Theme).Fill().Children(func() {
		ui.Column(c).Fill().Background(p.Window).Role(ui.RoleRegion).Label(resourceEditorTitle(v.Client.Draft.Kind, v.Locale, v.Client.Draft.ID())).Busy(v.Client.Busy).Children(func() {
			contentScroll(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).FillWidth().Padding(16).Gap(16).Children(func() {
					if v.Client.Error != nil {
						message := resourceError(v.Locale, v.Client.Error)
						if v.Client.InvalidField != "" {
							message = v.validationMessage()
						}
						ui.Text(c, message).FillWidth().TextColor(c.Theme().Danger).Role(ui.RoleAlert)
					}
					if v.Client.CleanupPending {
						ui.Text(c, localized(v.Locale, "已保存，旧凭据清理待重试。", "Saved; retired credential cleanup is pending.")).FillWidth().TextColor(c.Theme().Warning).Role(ui.RoleStatus)
					}
					switch v.Client.Draft.Kind {
					case application.Models:
						v.model(c)
					case application.Templates:
						v.template(c)
					case application.Members:
						v.member(c)
					}
				})
			})
			separator(c)
			ui.Row(c).FillWidth().MinHeight(64).Padding(14, 16).Gap(8).AlignItems(ui.Center).Children(func() {
				d := v.Client.Draft
				if d.Kind == application.Templates && d.ID() != "" {
					if textButton(c, v.msg("duplicate")).Disabled(v.Client.Busy).Clicked() {
						v.Client.Duplicate(localized(v.Locale, " - 副本", " - Copy"))
					}
				}
				if d.ID() != "" && !(d.Kind == application.Templates && d.Template.Builtin) {
					if textButton(c, presentation.Message(v.Locale, "common.delete")).Disabled(v.Client.Busy).Clicked() {
						v.Client.Delete(v.Confirm(v.msg("deleteTitle"), presentation.Format(v.Locale, string(d.Kind)+".deleteConfirm", map[string]any{"name": d.Name()})))
					}
				}
				ui.Row(c).Grow(1)
				if textButton(c, v.msg("reset")).Disabled(v.Client.Busy).Clicked() {
					v.Client.Reset(v.Confirm(v.msg("reset"), v.msg("reset")))
				}
				if ui.PrimaryButton(c, v.msg("save")).Disabled(v.Client.Busy || (d.Kind == application.Templates && d.Template.Builtin)).Clicked() {
					v.Client.Save()
					v.focusInvalid = v.Client.InvalidField != ""
				}
			})
		})
	})
}
func (v *ResourceEditorView) field(c *ui.Context, key string, value *string, readonly bool) ui.Element {
	var input ui.Element
	ui.Column(c.Key(key)).FillWidth().Gap(10).Children(func() {
		label := ui.Text(c, v.msg(key)).FontSize(13)
		input = ui.TextInput(c, value).FillWidth().Radius(7).LabelledBy(label).ReadOnly(readonly).Disabled(v.Client.Busy)
		if v.Client.Draft.Kind == application.Models || v.Client.Draft.Kind == application.Members {
			input.Placeholder(v.msg(key + "Placeholder"))
		}
		if readonly {
			input.Background(palette(c).Subpanel).TextColor(c.Theme().TextMuted)
		}
		v.validateField(input, key)
	})
	return input
}

func (v *ResourceEditorView) validationMessage() string {
	key := map[string]string{"name": "validationName", "baseUrl": "validationBaseUrl", "apiKey": "validationApiKey", "model": "validationModel", "prompt": "validationPrompt", "sortOrder": "validationSortOrder"}[v.Client.InvalidField]
	return v.msg(key)
}

func (v *ResourceEditorView) validateField(input ui.Element, key string) {
	if v.Client.InvalidField != key {
		return
	}
	input.Invalid(true).Description(v.validationMessage())
	if v.focusInvalid {
		input.Focus()
		v.focusInvalid = false
	}
	if input.Changed() {
		v.Client.InvalidField = ""
		v.Client.Error = nil
	}
}
func (v *ResourceEditorView) toggle(c *ui.Context, key string, value *bool, readonly bool) {
	ui.Row(c.Key(key)).Gap(10).AlignItems(ui.Center).Children(func() {
		control := ui.Switch(c, value).Disabled(v.Client.Busy || readonly)
		label := ui.Text(c, v.msg(key)).FontSize(13)
		control.LabelledBy(label)
	})
}
func (v *ResourceEditorView) model(c *ui.Context) {
	m := &v.Client.Draft.Model
	v.field(c, "name", &m.Name, false)
	v.field(c, "baseUrl", &m.BaseURL, false)
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		label := ui.Text(c, v.msg("apiKey")).FontSize(13)
		placeholder := "apiKeyPlaceholder"
		if m.ID != "" {
			placeholder = "apiKeyKeepPlaceholder"
		}
		input := ui.TextInput(c, &v.Client.Password).Password().FillWidth().Radius(7).LabelledBy(label).Placeholder(v.msg(placeholder)).Disabled(v.Client.Busy)
		v.validateField(input, "apiKey")
		if input.Changed() {
			v.Client.ClearCredential = false
		}
		if m.ID != "" {
			key := "credentialMissing"
			if m.CredentialPresent {
				key = "credentialStored"
			}
			if v.Client.ClearCredential {
				key = "credentialClearPending"
			}
			status := ui.Text(c, v.msg(key)).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted).Role(ui.RoleStatus)
			input.DescribedBy(status)
			if m.CredentialPresent && strings.TrimSpace(v.Client.Password) == "" {
				key = "clearCredential"
				if v.Client.ClearCredential {
					key = "keepCredential"
				}
				if textButton(c, v.msg(key)).Disabled(v.Client.Busy).Clicked() {
					v.Client.ClearCredential = !v.Client.ClearCredential
				}
			}
		}
	})
	model := v.field(c, "model", &m.Model, false)
	help := ui.Text(c, v.msg("modelHelp")).FillWidth().FontSize(12).TextColor(c.Theme().TextMuted)
	model.DescribedBy(help)
	ui.Row(c).FillWidth().Wrap().Gap(28).Children(func() {
		v.toggle(c, "enabledSwitch", &m.Enabled, false)
		v.toggle(c, "defaultSwitch", &m.IsDefault, false)
	})
}
func (v *ResourceEditorView) template(c *ui.Context) {
	t := &v.Client.Draft.Template
	if t.Builtin {
		ui.Text(c, v.msg("builtinReadonly")).FillWidth().Padding(10, 12).Radius(7).Background(palette(c).Selected).TextColor(c.Theme().TextMuted).FontSize(13).Role(ui.RoleStatus)
	}
	v.field(c, "name", &t.Name, t.Builtin)
	v.field(c, "description", &t.Description, t.Builtin)
	ui.Row(c).FillWidth().Wrap().Gap(28).Children(func() {
		v.toggle(c, "includeSpeakerDefault", &t.IncludeSpeaker, t.Builtin)
		v.toggle(c, "includeTimestampDefault", &t.IncludeTimestamp, t.Builtin)
	})
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		label := ui.Text(c, v.msg("prompt")).FontSize(13)
		input := ui.TextArea(c, &t.Prompt).FillWidth().Height(112).Padding(7, 9).Radius(7).LabelledBy(label).Placeholder(v.msg("promptPlaceholder")).ReadOnly(t.Builtin).Disabled(v.Client.Busy)
		if t.Builtin {
			input.Background(palette(c).Subpanel).TextColor(c.Theme().TextMuted)
		}
		v.validateField(input, "prompt")
	})
}
func (v *ResourceEditorView) member(c *ui.Context) {
	m := &v.Client.Draft.Member
	v.field(c, "name", &m.Name, false)
	v.field(c, "department", &m.Department, false)
	v.field(c, "sortOrder", &v.Client.MemberSortOrder, false)
	v.toggle(c, "recorderSwitch", &m.IsRecorder, false)
}

func nativeResourceConfirm(parent *mygo.Window, locale, title, message string) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		r, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: parent, Type: mygo.MessageWarning, Title: title, Message: message, Buttons: []string{presentation.Message(locale, "common.cancel"), localized(locale, "确认", "Confirm")}, CancelButton: 0, DefaultButton: 0})
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return r.Button == 1, err
	}
}
