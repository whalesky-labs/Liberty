package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/desktop/presentation"
)

func (v *NewJobView) batchFiles(c *ui.Context) {
	remove := -1
	created := v.Client.SelectionCount() - v.Client.PendingCount()
	ui.Text(c, fmt.Sprintf(localized(v.Locale, "已加入队列 %d / %d；转写进度请查看任务队列。", "Queued %d / %d. See the task queue for transcription progress."), created, len(v.Client.Batch))).FillWidth().Role(ui.RoleStatus).FontSize(12)
	contentScroll(c).FillWidth().Height(300).Label(v.msg("inputFiles")).Children(func() {
		ui.Column(c).FillWidth().Gap(12).Children(func() {
			for i := range v.Client.Batch {
				item := &v.Client.Batch[i]
				ui.Column(c.Key(item.Media.Token)).FillWidth().Padding(10).Gap(6).Background(palette(c).Subpanel).Children(func() {
					ui.Text(c, item.Media.Name).FillWidth().SingleLine().Tooltip(item.Media.Name).FontSize(12)
					ui.TextInput(c, &item.Title).FillWidth().Label(v.msg("jobTitle") + " " + item.Media.Name).Disabled(v.Client.Busy || item.JobID != "")
					status := localized(v.Locale, "等待创建", "Pending")
					if item.JobID != "" {
						status = localized(v.Locale, "已加入队列", "Queued")
					} else if item.Error != nil {
						status = localized(v.Locale, "创建失败；重试只处理未成功的文件。", "Creation failed. Retry only processes files that were not queued.")
					}
					ui.Text(c, status).FillWidth().FontSize(11).Role(ui.RoleStatus)
					if ui.Button(c, presentation.Message(v.Locale, "common.delete")).Label(presentation.Format(v.Locale, "newJob.removeFile", map[string]any{"name": item.Media.Name})).Disabled(v.Client.Busy || v.Client.Picking).Clicked() {
						remove = i
					}
				})
			}
		})
	})
	if remove >= 0 {
		v.Client.RemoveBatchItem(remove)
	}
}
