package main

import (
	"errors"
	"fmt"

	"github.com/egoist/mygo"
	"liberty.local/liberty/internal/domain"
	"liberty.local/liberty/internal/host"
	"liberty.local/liberty/internal/infrastructure/managed"
	"liberty.local/liberty/internal/infrastructure/profile"
	"liberty.local/liberty/internal/infrastructure/repositories/schema"
)

var eventLoopStarted bool
var errNativeGUIUnsupported = errors.New("native_gui_platform_unsupported")

func nativeGUIAvailable(operatingSystem, architecture string) bool {
	return operatingSystem == "darwin" && (architecture == "amd64" || architecture == "arm64") || operatingSystem == "windows" && architecture == "amd64"
}

func startupFailureMessage(err error) string {
	switch {
	case errors.Is(err, errNativeGUIUnsupported):
		return "此系统架构不受支持。请使用 macOS Apple Silicon、macOS Intel 或 Windows x64 对应的候选程序。启动在打开资料库前停止，无需删除或重置资料。\n\nThis system architecture is unsupported. Use the candidate for macOS Apple Silicon, macOS Intel or Windows x64. Startup stopped before opening a profile. Keep the existing data."
	case errors.Is(err, profile.ErrProfileOverlap):
		return "此目录与现有生产资料重叠。请选择独立目录；使用 --import-profile <源目录> --profile-directory <新目录> 导入一致性快照，保留旧资料。\n\nThis directory overlaps the existing production profile. Choose a separate directory and use --import-profile <source> --profile-directory <new-directory> to import a consistent snapshot while preserving the original profile."
	case errors.Is(err, profile.ErrProductionMutation):
		return "旧版客户端可能不遵守资料锁，不能原地写入或恢复生产资料。请使用 --import-profile <源目录> --profile-directory <新目录> 导入独立 Native 资料，再对新目录执行写入或恢复；不要删除或重置旧库。--default-profile 仍可只读查看。\n\nOlder clients may ignore the profile lock. Import a separate Native profile with --import-profile <source> --profile-directory <new-directory>, then write or restore only that new directory. Keep the original database. --default-profile remains read only."
	case errors.Is(err, schema.ErrProfileTransferBusy):
		return "源资料仍有排队、执行、删除或安装任务。请在旧客户端完成或取消这些操作后重试；不会从新目录终止旧版进程，也不会重置旧库。\n\nThe source snapshot still has queued, running, deletion or installation work. Finish or cancel those operations in the old client, then retry. The import will not terminate legacy processes or reset the original database."
	case errors.Is(err, schema.ErrProfileTransfer):
		return "导入尚未确认完成。请保留旧资料、新目录和其中的导入记录，恢复磁盘及系统凭据访问后，用同一命令重试；不要删除资料库或手工清理凭据。\n\nThe import could not be confirmed complete. Keep the original profile, new directory and import records. Restore disk and system credential access, then retry the same command. Do not delete databases or manually remove credentials."
	case errors.Is(err, profile.ErrInUse):
		return "资料目录正在由另一个 Liberty 客户端使用。请先退出该客户端，再重新打开。\n\nAnother Liberty client is using this profile. Quit that client, then reopen this app."
	case errors.Is(err, schema.ErrBackup):
		return "无法保存切换前备份。请检查磁盘空间和资料目录写入权限，再重试。\n\nThe recovery backup could not be saved. Check free disk space and profile write permissions, then retry."
	case errors.Is(err, schema.ErrRestore):
		return "无法确认回退快照已安全恢复。请保留现有资料库、恢复前安全副本和所有 .bak 文件，检查磁盘后再决定是否重试。\n\nThe recovery snapshot could not be confirmed safe. Keep the current database, the pre-restore safety copy and all .bak files before deciding whether to retry."
	case errors.Is(err, schema.ErrVersion):
		return "所选资料库版本不受此启动方式支持。请使用匹配版本，或对隔离副本显式执行升级。\n\nThis launch mode does not support the selected database version. Use a matching client, or explicitly migrate an isolated copy."
	case errors.Is(err, domain.ErrCredentialStore), errors.Is(err, domain.ErrCredentialCleanup):
		return "系统凭据访问或清理未完成。请恢复系统凭据库访问权限，再重试。\n\nSystem credential access or cleanup could not finish. Restore access to the system credential store, then retry."
	case errors.Is(err, managed.ErrManifest), errors.Is(err, managed.ErrResources), errors.Is(err, managed.ErrChecksum):
		return "运行时输入未通过完整性检查。请重新获取完整的候选应用包，保留资料目录后再重试。\n\nRuntime inputs failed integrity checks. Obtain the complete candidate app package again, keep the profile directory, then retry."
	default:
		return "无法打开所选资料目录。请检查启动参数、目录权限和资料库版本，再重试；不要删除原资料库。\n\nThe selected profile could not be opened. Check launch arguments, directory permissions and database version, then retry. Keep the original database."
	}
}

func showStartupFailure(err error) {
	mygo.App.SetName("Liberty Native Candidate")
	mygo.App.SetVersion(host.Version)
	mygo.App.WhenReady(func() {
		go func() {
			_, _ = mygo.Dialog.Message(mygo.MessageOptions{
				Type: mygo.MessageError, Title: "无法启动 Liberty / Cannot start Liberty",
				Message: startupFailureMessage(err), Buttons: []string{"退出 / Quit"}, DefaultButton: 0, CancelButton: 0,
			})
			mygo.App.Quit()
		}()
	})
	_ = mygo.App.Run()
}

func showRestoreComplete(version int, safetyBackup string) {
	mygo.App.SetName("Liberty Native Candidate")
	mygo.App.SetVersion(host.Version)
	mygo.App.WhenReady(func() {
		go func() {
			_, _ = mygo.Dialog.Message(mygo.MessageOptions{
				Type: mygo.MessageInfo, Title: "资料库恢复完成 / Profile restored",
				Message: "已从已校验快照恢复资料库（schema " + fmt.Sprint(version) + "）。恢复前资料库的安全副本为 " + safetyBackup + "。\n\nThe verified snapshot was restored (schema " + fmt.Sprint(version) + "). The pre-restore safety copy is " + safetyBackup + ".",
				Buttons: []string{"退出 / Quit"}, DefaultButton: 0, CancelButton: 0,
			})
			mygo.App.Quit()
		}()
	})
	_ = mygo.App.Run()
}
