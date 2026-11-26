package desktop

import (
	"context"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"liberty.local/liberty/internal/application"
	"liberty.local/liberty/internal/client"
	"liberty.local/liberty/internal/infrastructure/fileout"
	"strconv"
)

func (v *SettingsView) diagnostics(c *ui.Context) {
	d := v.Diagnostics
	v.diagnosticRow(c, localized(v.Locale, "系统健康检查", "System Health"), localized(v.Locale, "平台矩阵、数据库版本、运行时与安全基线", "Platform matrix, database, runtime and security baseline"), func() {
		state := localized(v.Locale, "待刷新", "Not checked")
		if d.Report != nil {
			state = localized(v.Locale, "正常", "Ready")
		}
		if d.Error != nil {
			state = localized(v.Locale, "异常", "Unavailable")
		}
		if d.Busy {
			state = localized(v.Locale, "刷新中", "Checking")
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, state).FontSize(12).Role(ui.RoleStatus).Busy(d.Busy)
			if textButton(c, localized(v.Locale, "刷新", "Refresh")).Disabled(d.Busy).Clicked() {
				d.Refresh()
			}
		})
	})
	if r := d.Report; r != nil {
		platform := localized(v.Locale, "不支持", "Unsupported")
		if r.CurrentPlatform != nil {
			platform = r.CurrentPlatform.Label + " (" + r.CurrentPlatform.ID + ")"
		}
		log := localized(v.Locale, "未生成", "Not generated")
		if r.DesktopPetLogAvailable {
			log = localized(v.Locale, "可导出", "Available to export")
		}
		for _, row := range [][3]string{
			{"应用版本", "App Version", r.AppVersion}, {"当前平台", "Current Platform", platform}, {"数据库版本", "Database Version", strconv.FormatInt(r.SchemaVersion, 10)},
			{"运行时状态", "Runtime Status", r.RuntimeStatus}, {"桌宠诊断日志", "Desktop Pet Diagnostic Log", log}, {"CSP", "CSP", localized(v.Locale, "原生界面（不适用）", "Native UI (not applicable)")},
			{"权限范围", "Capability Scope", localized(v.Locale, "已收敛", "Scoped")}, {"凭据存储", "Credential Store", localized(v.Locale, "需要系统钥匙串", "System credential store required")},
		} {
			v.diagnosticRow(c, localized(v.Locale, row[0], row[1]), "", func() { ui.Text(c, row[2]).FontSize(12).Bold() })
		}
		v.diagnosticRow(c, localized(v.Locale, "发布平台矩阵", "Release Platform Matrix"), "", func() {
			ui.Row(c).FillWidth().Gap(5).Justify(ui.End).Children(func() {
				for _, p := range r.SupportedPlatforms {
					ui.Text(c, p.ID).SingleLine().FontSize(11).Padding(3, 8).Radius(10).Background(palette(c).Subpanel)
				}
			})
		})
		v.diagnosticRow(c, localized(v.Locale, "桌宠拖拽诊断日志", "Desktop Pet Drag Diagnostic Log"), "", func() {
			if textButton(c, localized(v.Locale, "导出诊断日志", "Export Diagnostic Log")).Disabled(!r.DesktopPetLogAvailable || v.DiagnosticFiles == nil || d.Busy).Clicked() {
				d.Export(v.DiagnosticFiles)
			}
		})
	}
	if d.Error != nil {
		ui.Text(c, localized(v.Locale, "诊断操作失败，请重试。", "Diagnostic operation failed. Please retry.")).TextColor(c.Theme().Danger).Role(ui.RoleAlert).Selectable()
	}
	if d.Exported {
		ui.Text(c, localized(v.Locale, "诊断日志已导出。", "Diagnostic log exported.")).FontSize(12).Role(ui.RoleStatus)
	}
}
func (v *SettingsView) diagnosticRow(c *ui.Context, label, hint string, control func()) {
	settingsRow(c, label, hint, control)
}

type NativeDiagnosticFiles struct {
	Parent  *mygo.Window
	Service *application.Diagnostics
}

func (f *NativeDiagnosticFiles) Export(ctx context.Context) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: f.Parent, DefaultPath: "liberty-desktop-pet-diagnostics.log", Filters: []mygo.FileFilter{{Name: "Log", Extensions: []string{"log"}}}})
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return false, fileout.ErrOutput
	}
	if path == "" {
		return false, nil
	}
	err = f.Service.ExportPetLog(ctx, func(data []byte) error { return fileout.Write(ctx, path, data) })
	return true, err
}

// AttachRemoteDiagnostics is called after AttachSettings. The controllers are
// main-window-only; CloseAuxiliary must run before the workspace releases them.
func (w *Workspace) AttachRemoteDiagnostics(remote *application.RemoteMeetings, diagnostics *application.Diagnostics) {
	if w.settings == nil {
		return
	}
	changed := func() {
		if !w.closed {
			w.main.Invalidate()
		}
	}
	if remote != nil {
		w.settings.Remote = client.NewRemote(remote, mygo.RunOnMain, changed)
	}
	if diagnostics != nil {
		w.settings.Diagnostics = client.NewDiagnostics(diagnostics, mygo.RunOnMain, changed)
		w.settings.DiagnosticFiles = &NativeDiagnosticFiles{Parent: w.main, Service: diagnostics}
	}
	w.settings.ConfirmTokenClear = func() {
		if w.settings.Remote != nil {
			w.settings.Remote.Reset()
		}
		w.settings.Client.ConfirmClearToken(nativeResourceConfirm(w.main, w.preferences.Locale, w.settings.msg("apiToken"), w.settings.msg("apiTokenClearConfirm")))
	}
}
func (v *SettingsView) CloseAuxiliary() {
	if v.Remote != nil {
		v.Remote.Close()
	}
	if v.Diagnostics != nil {
		v.Diagnostics.Close()
	}
}
