package desktop

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
	"liberty.local/liberty/internal/domain"
)

func (v *NotesView) editNotes(c *ui.Context) {
	e := v.Editor
	locale := v.Preferences.Locale
	ui.Text(c, localized(locale, "列表每行填写一项。保存后的人工纪要不会被重新生成的 AI 结果覆盖。", "Use one item per line in lists. New AI results will not overwrite saved manual notes.")).FillWidth().FontSize(12)
	for _, field := range []struct {
		key   string
		value *string
	}{{"summary", &e.Overview}, {"topics", &e.Topics}, {"decisions", &e.Decisions}, {"actionItems", &e.ActionItems}, {"risks", &e.Risks}, {"followUps", &e.FollowUps}} {
		label := presentation.Message(locale, "notes."+field.key)
		ui.Text(c, label).FontSize(14)
		ui.TextArea(c, field.value).FillWidth().Height(100).Label(label).Disabled(e.Busy)
		v.editNoteSources(c, field.key)
	}
	if e.Error != nil {
		message := localized(locale, "保存失败，编辑内容已保留，请重试。", "Save failed. Your edits are retained; please retry.")
		if errors.Is(e.Error, domain.ErrMeetingDraftConflict) {
			message = localized(locale, "纪要已在其他位置更新，请复制需要的内容，取消后重新编辑。", "Notes changed elsewhere. Copy any text you need, cancel and reopen the editor.")
		}
		ui.Text(c, message).FillWidth().TextColor(c.Theme().Danger).Role(ui.RoleAlert)
	}
}

func sourceLabel(segment domain.Segment) string {
	text := []rune(strings.ReplaceAll(segment.Text, "\n", " "))
	if len(text) > 55 {
		text = append(text[:55], '…')
	}
	return fmt.Sprintf("%02d:%02d:%02d %s", segment.StartMS/3600000, segment.StartMS/60000%60, segment.StartMS/1000%60, string(text))
}

func (v *NotesView) noteSources(c *ui.Context, section string) {
	r := v.Client.Result
	if r == nil || r.Draft == nil {
		return
	}
	for _, id := range r.Draft.Sources[section] {
		for _, segment := range r.PrimarySegments() {
			if segment.ID == id && ui.Button(c, localized(v.Preferences.Locale, "查看来源：", "View source: ")+sourceLabel(segment)).Disabled(v.OpenSource == nil).Clicked() {
				v.OpenSource(id)
			}
		}
	}
}

func (v *NotesView) editNoteSources(c *ui.Context, section string) {
	r := v.Client.Result
	if r == nil {
		return
	}
	locale := v.Preferences.Locale
	if v.sourceQueries == nil {
		v.sourceQueries = make(map[string]string)
	}
	query := v.sourceQueries[section]
	if ui.TextInput(c, &query).FillWidth().Label(localized(locale, "搜索来源原文", "Search source text")).Placeholder(localized(locale, "按关键词查找来源", "Find sources by keyword")).Disabled(v.Editor.Busy).Changed() {
		v.sourceQueries[section] = query
	}
	labels := []string{localized(locale, "关联原文来源…", "Link transcript source…")}
	segments := []domain.Segment{}
	for _, segment := range r.PrimarySegments() {
		if strings.Contains(strings.ToLower(segment.Text), strings.ToLower(strings.TrimSpace(query))) {
			segments = append(segments, segment)
			labels = append(labels, fmt.Sprintf("%d · %s", len(segments), sourceLabel(segment)))
			if len(segments) == 50 {
				break
			}
		}
	}
	choice := labels[0]
	if selectControl(c, &choice, labels).FillWidth().Label(localized(locale, "本节来源", "Sources for this section")).Disabled(v.Editor.Busy || len(v.Editor.Sources[section]) >= 50).Changed() {
		for i, label := range labels[1:] {
			if label == choice {
				id := segments[i].ID
				found := false
				for _, saved := range v.Editor.Sources[section] {
					found = found || saved == id
				}
				if !found {
					v.Editor.Sources[section] = append(v.Editor.Sources[section], id)
				}
				break
			}
		}
	}
	remove := -1
	for i, id := range v.Editor.Sources[section] {
		for _, segment := range r.PrimarySegments() {
			if segment.ID == id {
				ui.Text(c, sourceLabel(segment)).FillWidth().FontSize(11)
			}
		}
		if ui.Button(c.Key(section+id), localized(locale, "移除来源", "Remove source")).Disabled(v.Editor.Busy).Clicked() {
			remove = i
		}
	}
	if remove >= 0 {
		items := v.Editor.Sources[section]
		v.Editor.Sources[section] = append(items[:remove], items[remove+1:]...)
	}
}
