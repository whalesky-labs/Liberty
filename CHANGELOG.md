# 更新日志 / Changelog

仅保留当前版本与未发布改动；旧版本记录见 Git 历史和 [GitHub Releases](https://github.com/whalesky-labs/Liberty/releases)。 / This file retains the current version and unreleased work; older records remain in Git history and GitHub Releases.

## Unreleased

### 中文

- 清理已停用的 Rust 迁移样本导出器与互操作探针、弃用的圆润 3D 试制工具链和一次性制作脚本；保留冻结兼容样本、当前像素模型及现有生产入口。
- 原生会议流程支持最多 32 个文件批量入队、失败项重试、逐句回听与倍速、正文和讲话人校对；人工纪要独立保存、关联原文并用于默认导出，schema v13 同步迁移，AI 历史保留。
- 增加 macOS 13+ 系统声音和 Windows WASAPI 回环适配及麦克风混音，保留本地无 Key 流程；系统录音权限、设备切换及目标平台实机验收仍未闭合。
- 系统设置新增「访问权限」，说明磁盘与麦克风的使用用途，提供 macOS/Windows 系统隐私设置入口及打开失败时的手动指引，不自动申请权限或假报授权状态。
- 修复 Windows 原生标题栏与应用深浅主题不同步的问题；为窗口、任务栏、exe、开始菜单快捷方式及卸载列表补齐共享应用图标。
- 恢复原生安装包交付：macOS Apple Silicon/Intel 提供临时签名 DMG，Windows x64 提供当前用户 MSI；保留便携归档及独立安装器来源、签名状态和 SHA-256 清单，不宣称已公证或具备可信发布者签名。
- 设置加入双语 GitHub 下载更新入口；从 Releases 选择匹配平台的安装包，正常退出后更新并保留资料。此入口不在后台下载、执行或自动替换应用。
- 为 alpha/beta/rc 到稳定版提供有序 MSI 产品版本映射，并将安装包解包、内嵌字节、架构与版本验证接入现有完整发布门禁。

### English

- Removed retired Rust migration-fixture exporters and interoperability probes, the discarded rounded 3D study toolchain, and one-off authoring scripts; retained frozen compatibility fixtures, current pixel models and the existing production entry.
- Native meeting workflows now support batches of up to 32 files, failed-admission retries, segment playback and speed control, text/speaker corrections, and independent manual notes with source links and default exports. Shared schema v13 preserves AI history.
- Added macOS 13+ system audio and Windows WASAPI loopback adapters with microphone mixing, retaining local workflows without API keys. Recording permissions, device switching and target-device acceptance remain incomplete.
- Added Access Permissions under System settings, with disk and microphone purposes, macOS/Windows privacy-settings links and manual recovery instructions, without requesting access automatically or implying that access has been granted.
- Fixed Windows native title bars diverging from the app's light/dark appearance, and restored the shared application icon for windows, taskbar buttons, executables, Start menu shortcuts and the installed-apps list.
- Restored native installer delivery: ad-hoc-signed DMGs for Apple Silicon/Intel Macs and a per-user MSI for Windows x64, retaining portable archives and separate installer provenance, signature status and SHA-256 records without claiming notarization or a trusted publisher signature.
- Added a bilingual GitHub download-updates entry in Settings. Choose a matching installer from Releases and quit before upgrading; data is retained. This entry does not download, execute or replace the app in the background.
- Added ordered MSI product versions from alpha/beta/rc through stable, and required extracted installer bytes, architecture and version verification under the existing complete release gate.

## 2.0.0-alpha.3 - 2026-11-26

### 中文

- 新增可折叠的模块菜单，收起后释放内容空间，并保留当前页面、草稿、键盘焦点与窗口内导航状态；同步中英文标签和窄窗口设置布局。
- 调整设置数字输入、运行时状态与进度展示，并对齐宠物商店详情按钮及打工地图统计区域。
- 修复历史字幕慢速导出阻塞另一场实时录音，以及 Word 导出阻塞其他任务窗口的问题，保留关闭、撤销与文件发布的生命周期约束。
- 修复两端明确设置默认 AI 模型受旧排序影响、旧端模型元数据写入失败误报成功，以及原生整任务删除未取消在途 AI 摘要请求的问题。
- 修复成长配饰卸下或替换后被商店刷新重新装备的问题，两端均保留库存中的当前装备选择。
- 收敛原生人员导入的重复业务校验，将农场和打工的纯规则移入领域层，并按结果快照复用旧逐字稿显示与编辑资格判定。

### English

- Added collapsible module navigation that reclaims content space while preserving the current page, drafts, keyboard focus and window navigation state, with bilingual labels and compact Settings layouts.
- Refined Settings number inputs, runtime status and progress displays, and aligned Pet Store detail actions and work-map statistics.
- Fixed slow historical subtitle exports blocking another live recording and Word exports blocking other job windows, preserving close, revocation and publication ordering.
- Fixed explicit default AI model selections being overridden by list order in both clients, failed model metadata writes reporting success in the retained client, and native whole-job deletion leaving an accepted AI summary request active.
- Fixed shop refresh restoring unequipped or replaced growth accessories; both clients now preserve the current inventory equipment choice.
- Consolidated native member-import business validation, moved pure farm/work rules into the domain layer, and reused legacy transcript display and edit eligibility decisions per result snapshot.
