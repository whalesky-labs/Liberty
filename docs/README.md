# Liberty 文档总览

整理日期：2026-11-28。本目录维护当前源码的使用、开发、架构和交付规则。Native 仍为候选，生产切换未完成；源码能力、测试要求和已发行资产分别判断。

## 使用与开发入口

| 目的 | 文档 |
| --- | --- |
| 产品、平台、组织与下载 | [中文 README](../README.zh-CN.md) / [English README](../README.md) |
| 安装、启动、资料导入、备份与恢复 | [原生客户端说明](../apps/native/README.md) |
| 批量导入、回听校对和独立人工纪要 | [会议工作流](meeting-workflow.md) |
| 实时采集、离线中英互译和模型准备 | [实时翻译](live-translation.md) |
| 宠物、LP、福利、农场与打工 | [宠物系统](pet-system.md) |
| 工具链、测试、生成和验收命令 | [开发指南](development.md) |
| 分层、数据、窗口授权和共享来源 | [原生架构](architecture/native-desktop-architecture.md) |
| 导航、主题、设置、多窗口与更新交互 | [原生界面](architecture/native-ui.md) |
| 按改动范围运行 CI、构建及发布事务 | [CI/CD 规则](architecture/ci-cd.md) |
| 交付格式、验收门槛与旧端退役 | [发布就绪检查](architecture/release-readiness.md) |
| 未发布改动及当前版本记录 | [CHANGELOG](../CHANGELOG.md) / [alpha.4 发行说明](releases/2.0.0-alpha.4.md) |
| 修改范围与验证约束 | [AGENTS](../AGENTS.md) |

日常启动用 `pnpm start`，没有候选包或需要加载源码修改时先运行 `pnpm native:package`。`native:preview` 使用退出后清理的合成资料，不用于日常数据保存。

## 模块与资源

| 内容 | 文档 |
| --- | --- |
| 跨语言 JSON Schema | [共享契约](../packages/shared-types/README.md) |
| 数据库、运行时、文案、模板与素材来源 | [桌面共享资源](../packages/desktop-resources/README.md) |
| 冻结数据库、JSON、XLSX、DOCX 回归输入 | [兼容样本](../packages/desktop-resources/fixtures/native-v10/README.md) |
| Runner 协议与子进程 | [Runner](../apps/native/internal/infrastructure/runner/README.md) |
| AI 响应与发布边界 | [AI](../apps/native/internal/infrastructure/ai/README.md) |
| 远端能力与 JSON 适配 | [Remote](../apps/native/internal/infrastructure/remote/README.md) |
| 旧格式任务导入 | [Legacy jobs](../apps/native/internal/infrastructure/legacyjobs/README.md) |
| Excel 文件与人员事务 | [Member XLSX](../apps/native/internal/infrastructure/memberxlsx/README.md) |
| 日志脱敏 | [Diagnostics](../apps/native/internal/infrastructure/diagnostics/README.md) |
| FunASR 固定 wheel 与来源证明 | [Vendor recipe](../python/funasr-runner/vendor/README.md) |
| 受控媒体、质量与目标设备证据 | [ASR 验证资产](../benchmarks/asr/README.md) |
| 图片规范与同步 | [素材说明](images/README.md) |
| 实时页动画来源与许可证 | [bloub](../packages/desktop-resources/presentation/bloub/README.md) |
| 3D 伙伴模型制作与原生预览 | [模型资源](../packages/desktop-resources/pet/models/README.md) / [原生渲染模块](../apps/native/internal/desktop/presentation/companion3d/README.md) |

## 进行中的工作

[过程文档入口](ai/README.md)只保留仍需执行的计划、设计及其直接依据。已被当前文档覆盖的迁移方案、验收快照、旧版本发布说明和旧界面截图不再留档。历史发行可查 Git 历史和 [GitHub Releases](https://github.com/whalesky-labs/Liberty/releases)。

当前仍需跟进会议工作流的实机与质量验收，以及 3D 桌宠的正式接入、互动和平台验证；具体范围见对应计划。本文不把旧测试日志作为当前工作区通过验证的证据。

## 维护规则

1. 根中英文 README 同步产品范围和组织信息；详细行为写在专题文档，命令写在开发指南，交付门槛写在发布要求。
2. 当前源码、共享 schema、运行时 manifest 和脚本是核对依据。未发布源码与发行包分别说明，不用旧日期的测试结论推断当前通过。
3. 模块 README 就近维护契约；过程文档不作为业务、数据、安全或发布规则的唯一来源。完成后提炼有效结论并删除重复过程记录。
4. 删除或移动文件时同步检查链接、标题锚点、脚本引用和图片用途。兼容样本、生成器输入、许可证与 NOTICE 不按“历史文档”删除。
5. 运行 `pnpm ci:docs` 检查已跟踪 Markdown 的链接与围栏；新文件还需单独核对，命令须与 `package.json` 一致。文档整理不等于构建、实机或发布验收。
