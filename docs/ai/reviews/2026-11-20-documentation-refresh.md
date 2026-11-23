# 原生重构后的项目文档整理

- 文档类型：评审（文档同步与分类）
- 状态：已完成（文档整理；不代表产品验收或生产切换）
- 创建日期：2026-11-20
- 最后核实：2026-11-20
- 适用范围：Liberty 1.1.24 当前工作区的项目入口、正式文档、模块说明、AI 过程记录及美术输入索引
- 权威边界：证明本次文档分类、源码对照和静态检查结果；不重跑历史媒体、运行时、原生窗口、平台、安全扫描或发布验收，也不把历史完成状态改为当前完成状态。
- 依据：根 `package.json`、`apps/native/go.mod`、原生启动/用例/界面/生成脚本、Rust schema、运行时 manifest、CI 配置，以及本轮链接、围栏、命令和版本/契约/迁移基线检查。

## 后续清理（2026-11-20）

按用户后续要求，已删除旧 React/Tauri 专属架构说明及该阶段的 14 份设计、8 份计划、2 份评审，共 25 份文档；同步移除旧端教程、模块/命令列表及索引链接。保留原生重构记录、共同数据/协议和运行时事实来源。以下整理数量与验证结果属于删除前那一轮，不能当作删除后的文档清单；覆盖表已去掉删除项。

## 主要修正

- 根中英文 README 从单一 React/Tauri 说明改为原生候选与保留生产客户端的明确分工，补齐目录、平台和验证边界。
- 新增文档总览、开发指南和原生正式架构，共同约束集中到原生架构与发布要求；旧架构文件在后续清理中删除。
- 核实 `processingMode` 为本地/远端选择依据；远端 URL 非空不自动切换。协议枚举中的创建/总结能力不等于现有客户端已开放这些入口。
- 将原生 README 的操作入口与累积验收细节分开：原内容保存到独立历史快照，现 README 聚焦候选启动、资料锁、接管、恢复、运行时和模块导航。
- 同步 AGENTS 的 Go 分层、单一主题来源、固定工具链、六处版本一致性与原生检查；不改变授权、保留用户改动或发布边界。
- 宠物说明增加 Go 模块映射、临时装配练习与同源资源流程；后续清理移除了旧 Tauri command 与打包教程。
- 原生实施记录按批次证据保留，现场 PID 与包路径明确为记录时点。原生记录保留正文与原验证日期；旧客户端计划与评审在后续清理中删除。
- 素材制作说明按当前八作物、土壤命名和原生生成器更新，移除机器专属 Python 路径和过时的直接复制指令；21 份美术文本输入逐字保留。
- CHANGELOG 仅补充 Unreleased，已发布版本正文保持不变。

## 文档覆盖

整理前共 53 份 Markdown 文档和 21 份美术 `.txt` 输入；依赖声明和平台 lock 虽为 `.txt`，属于可执行依赖输入，不作为文档改写。现新增 6 份 Markdown：文档总览、开发指南、原生架构、素材索引、本记录和原生模块历史快照。

下表仅列出整理后仍保留的原有 Markdown；数量与本节首段的删除前统计不同。历史条目“分类与导航”不表示其技术正文重新验收。

| 文档 | 处理 |
| --- | --- |
| [AGENTS.md](../../../AGENTS.md) | 更新当前实现、命令、分工或素材引用 |
| [CHANGELOG.md](../../../CHANGELOG.md) | 补充双语 Unreleased，已发布历史不变 |
| [README.md](../../../README.md) | 更新当前实现、命令、分工或素材引用 |
| [README.zh-CN.md](../../../README.zh-CN.md) | 更新当前实现、命令、分工或素材引用 |
| [apps/native/README.md](../../../apps/native/README.md) | 重整操作说明，原详细内容完整归档 |
| [apps/native/internal/infrastructure/ai/README.md](../../../apps/native/internal/infrastructure/ai/README.md) | 核对现有契约和测试路径，补上级导航 |
| [apps/native/internal/infrastructure/diagnostics/README.md](../../../apps/native/internal/infrastructure/diagnostics/README.md) | 核对现有契约和测试路径，补上级导航 |
| [apps/native/internal/infrastructure/legacyjobs/README.md](../../../apps/native/internal/infrastructure/legacyjobs/README.md) | 核对现有契约和测试路径，补上级导航 |
| [apps/native/internal/infrastructure/memberxlsx/README.md](../../../apps/native/internal/infrastructure/memberxlsx/README.md) | 核对现有契约和测试路径，补上级导航 |
| [apps/native/internal/infrastructure/remote/README.md](../../../apps/native/internal/infrastructure/remote/README.md) | 核对现有契约和测试路径，补上级导航 |
| [apps/native/internal/infrastructure/runner/README.md](../../../apps/native/internal/infrastructure/runner/README.md) | 核对现有契约和测试路径，补上级导航 |
| [benchmarks/asr/README.md](../../../benchmarks/asr/README.md) | 更新当前实现、命令、分工或素材引用 |
| [docs/ai/README.md](../README.md) | 补全所有过程记录索引与治理入口 |
| [docs/ai/designs/2026-11-16-native-desktop-refactor.md](../designs/2026-11-16-native-desktop-refactor.md) | 分类、状态边界与现行入口；保留原证据 |
| [docs/ai/designs/2026-11-19-native-gameplay-experience-design.md](../designs/2026-11-19-native-gameplay-experience-design.md) | 分类、状态边界与现行入口；保留原证据 |
| [docs/ai/plans/2026-11-17-native-desktop-implementation.md](../plans/2026-11-17-native-desktop-implementation.md) | 分类、状态边界与现行入口；保留原证据 |
| [docs/ai/research/2026-11-19-native-docx-visual-acceptance.md](../research/2026-11-19-native-docx-visual-acceptance.md) | 分类、状态边界与现行入口；保留原证据 |
| [docs/ai/research/2026-11-19-native-read-allocation.md](../research/2026-11-19-native-read-allocation.md) | 分类、状态边界与现行入口；保留原证据 |
| [docs/ai/research/2026-11-19-native-runtime-security-candidate.md](../research/2026-11-19-native-runtime-security-candidate.md) | 分类、状态边界与现行入口；保留原证据 |
| [docs/ai/research/2026-11-20-native-member-import-persistence.md](../research/2026-11-20-native-member-import-persistence.md) | 分类、状态边界与现行入口；保留原证据 |
| [docs/architecture/release-readiness.md](../../architecture/release-readiness.md) | 更新当前实现、命令、分工或素材引用 |
| [docs/images/farm-map-gpt-image-2-asset-plan.md](../../images/farm-map-gpt-image-2-asset-plan.md) | 更新当前实现、命令、分工或素材引用 |
| [docs/images/farm-map-imagegen-runbook.md](../../images/farm-map-imagegen-runbook.md) | 更新当前实现、命令、分工或素材引用 |
| [docs/images/farm-map-layer-prompts.md](../../images/farm-map-layer-prompts.md) | 更新当前实现、命令、分工或素材引用 |
| [docs/images/work-games-gpt-image-2-assets.md](../../images/work-games-gpt-image-2-assets.md) | 更新当前实现、命令、分工或素材引用 |
| [docs/pet-system.md](../../pet-system.md) | 更新当前实现、命令、分工或素材引用 |
| [packages/shared-types/README.md](../../../packages/shared-types/README.md) | 更新当前实现、命令、分工或素材引用 |
| [python/funasr-runner/vendor/README.md](../../../python/funasr-runner/vendor/README.md) | 核对候选边界，补文档入口 |

美术文本输入为 [完整农场输入](../../images/farm-map-full-prompt.txt)与 [prompts 目录](../../images/prompts/)中的 20 份 `.txt`；它们是素材配方，不作为修改代码或调用外部服务的指令。

## 验证

- 全部 Markdown 相对文件链接、代码围栏、现行 pnpm 命令和完整仓库路径核对。
- AI 过程文档七项元数据检查、索引覆盖检查。
- 已发布 CHANGELOG 正文、历史设计/计划/评审的技术章节与 21 份美术文本输入保留检查。
- `pnpm release:check-versions`：通过，六处版本一致。
- `pnpm contracts:check`：通过，五份生成输出无漂移。
- `pnpm native:contracts:check`：通过，Go 嵌入 schema 与共享源一致。
- `pnpm native:inventory`：通过，基线为 25 路由、90 commands、8 类窗口 capability、schema v10。这是迁移清单基线，不代表所有原生 UI 已验收。

本轮只修改 Markdown，没有修改程序、数据库、锁文件或发布配置；未重跑 `pnpm check`、真实扫描、完整兼容矩阵或原生客户端。旧文档中的历史测试数量保留原时点，不作为本轮结果。

## 协作与限制

整理期间另一工作会话更新了原生 README 和实施记录的启动措辞。本次读取最新内容后合并其“默认生产入口”与“仅 macOS ARM 运行时已有实跑”澄清，并将最新整理前 README 保存为[历史快照](2026-11-20-native-module-reference-snapshot.md)，没有回退其修订。

外部 URL 的在线可达性、历史临时证据文件及所有第三方服务没有在本轮重新验证；这些内容保持原日期和范围。未来发布应按[发布要求](../../architecture/release-readiness.md)重新取得对应证据。
