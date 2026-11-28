# 更新日志 / Changelog

仅保留当前版本与未发布改动；旧版本记录见 Git 历史和 [GitHub Releases](https://github.com/whalesky-labs/Liberty/releases)。 / This file retains the current version and unreleased work; older records remain in Git history and GitHub Releases.

## Unreleased

### 中文

- 增加旧端退役前的独立源码验收：在仓库外的临时副本中排除旧 GUI、Cargo 和旧端 workflow，重建共享生成物并检查 Native、历史兼容与三目标编译。修复主展示生成器遗漏动画重建/校验的问题；Native 策略检查不再依赖旧端 workflow，完整发布门禁仍保留现有要求。

### English

- Added isolated source acceptance before retiring the old client: an external temporary copy excludes the old GUI, Cargo and desktop workflow, regenerates shared resources, and verifies Native, historical compatibility and all three build targets. Fixed omitted animation generation/validation in the presentation entry point. Native policy checks no longer depend on the desktop workflow; complete release gates retain their existing requirements.

## 2.0.0-alpha.4 - 2026-11-28

### 中文

- 恢复三平台原生安装包交付：macOS Apple Silicon/Intel 提供 ad-hoc 临时签名 DMG，Windows x64 提供当前用户 MSI；保留便携归档、独立来源清单和 SHA-256，新增有序 MSI 产品版本与安装器解包、内嵌架构及版本核验。不宣称 Developer ID、公证或可信发布者签名。
- 原生会议流程支持最多 32 个文件批量入队、逐项状态与失败项重试；无 Key 基础流程继续复用持久化队列与资源预算，不自动调用 AI。
- 结果工作台新增逐句回听、倍速、正文和讲话人校对，并检查过期编辑冲突；修改后的内容进入后续导出，既有 AI 来源变化时提示复核。
- 人工纪要独立保存、关联原文来源并用于默认 TXT/Markdown/DOCX 导出，后续 AI 完成不覆盖人工稿；共享 schema v13 同步 Go/Rust 迁移。回退 alpha.3 或更早二进制须使用兼容的升级前资料快照。
- 增加 macOS 13+ 系统声音和 Windows WASAPI 回环适配及麦克风混音；实时输入和翻译偏好持久化，模型准备与下载入口集中到设置。系统录音权限、设备切换及长时间实机验收仍待完成。
- 系统设置按用途分组，新增磁盘/麦克风权限指引、本地存储统计、内存分析与性能指示器开关；更新检查使用独立原生窗口，按版本和通道显示 GitHub Release 正文，处理缓存、限流与重试，用户点击后才前往下载。
- 调整双语主题、菜单、前进/后退导航、滚动条及资源页面；编辑、窗口和朗读菜单跟随应用语言并保留原生角色与快捷键。修复 Windows 标题栏外观同步，补齐窗口、任务栏、可执行文件和安装快捷方式图标。
- 调整签到、盲盒和商店布局；成功结算后展示独立盲盒结果，关闭或刷新不会重复发奖，结算后刷新失败仍保留奖励显示。
- 清理停用的 Rust 样本导出器、互操作探针、弃用的圆润 3D 试制工具和过时文档，保留冻结兼容样本、当前像素模型、许可证与现有生产入口。
- 仍为独立资料目录的 Native 预发行候选，未切换稳定生产入口；首次本地使用需下载运行环境与模型。GUI/无障碍、目标设备 ASR、低配置预算、安装升级/回退等验收未全部闭合。当前 Python/Torch 安全例外于 2026-11-30 到期，每次交付须重新扫描，不表示零漏洞。

### English

- Restored native installers for all three targets: ad-hoc-signed DMGs for Apple Silicon/Intel Macs and a Windows x64 per-user MSI. Portable archives, provenance and SHA-256 remain, with ordered MSI product versions and extracted installer architecture/version checks. This does not claim Developer ID signing, notarization or trusted publisher signing.
- Added batches of up to 32 files, per-item admission status and retries for failed items. Basic workflows without API keys retain the durable queue and resource budget, without starting AI automatically.
- Added segment playback, speed control, transcript and speaker corrections with stale-edit conflict checks. Saved changes flow into later exports, and changed AI source material is marked for review.
- Added independent manual minutes with source links and default TXT/Markdown/DOCX exports; later AI completion does not overwrite them. Shared schema v13 migrates in Go and Rust. Rolling back to alpha.3 or earlier binaries requires a compatible pre-upgrade profile snapshot.
- Added macOS 13+ system-audio and Windows WASAPI loopback adapters with microphone mixing. Live input/translation preferences persist, with model preparation and downloads organized in Settings. Recording permissions, device changes and long-session device acceptance remain pending.
- Reorganized Settings and added disk/microphone permission guidance, local storage reports, memory analysis and performance-indicator controls. A dedicated native update window selects versions/channels, displays GitHub release notes and handles caching, rate limits and retries; downloads open only when requested.
- Refined bilingual themes, menus, back/forward navigation, scrollbars and resource pages. Edit, Window and Speech menus follow the application language while retaining native roles and shortcuts. Fixed Windows title-bar appearance and restored icons for windows, taskbars, executables and installed shortcuts.
- Refined check-in, blind-box and store layouts. Committed blind-box rewards appear in a separate result dialog; dismissal or refresh does not award them again, and refresh failure does not hide a settled reward.
- Removed retired Rust fixture exporters, interoperability probes, discarded rounded 3D authoring tools and obsolete documents while retaining frozen compatibility fixtures, current pixel models, licenses and the existing production entry.
- This remains a Native prerelease candidate with an isolated profile, without stable production cutover. First use requires runtime/model downloads. GUI/accessibility, target-device ASR, resource budgets and installation upgrade/rollback acceptance remain incomplete. Current Python/Torch security exceptions expire on November 30, 2026; each delivery must rerun scans and does not imply zero vulnerabilities.
