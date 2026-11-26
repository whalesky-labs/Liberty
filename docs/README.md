# Liberty 文档总览

整理日期：2026-11-21。新版开发和日常启动围绕 `apps/native` 的 Go + MyGo 实现进行；React/Tauri 在 Native 验收完成前冻结为临时对照、生产入口和回退保护。切换门槛通过后，旧端及其发布链在同一退役变更中删除。文档描述源码能力、操作要求或特定日期证据时，应明确各自范围，不能把某次候选运行或构建结果等同于生产切换。

## 从这里开始

| 要了解的内容 | 文档 |
| --- | --- |
| 产品能力与原生候选状态 | [中文 README](../README.zh-CN.md) / [English README](../README.md) |
| 工具链、启动、测试、生成与验收命令 | [开发指南](development.md) |
| 原生分层、数据、窗口授权和共享来源 | [Go + MyGo 原生架构](architecture/native-desktop-architecture.md) |
| 候选包、资料目录、显式接管与恢复 | [原生模块说明](../apps/native/README.md) |
| 平台、安全、安装器与正式发布门槛 | [发布就绪检查](architecture/release-readiness.md) |
| 按改动范围执行 CI、手动全量与候选构建 | [CI/CD 操作规则](architecture/ci-cd.md) |
| v2.0.0-alpha.3 范围、资产与已知限制 | [当前原生 alpha 发布说明](releases/2.0.0-alpha.3.md) |
| 第二个原生 alpha 的历史范围 | [v2.0.0-alpha.2](releases/2.0.0-alpha.2.md) |
| 首个原生 alpha 的历史范围 | [v2.0.0-alpha.1](releases/2.0.0-alpha.1.md) |
| 宠物、LP、商店、福利、农场和打工 | [宠物系统](pet-system.md) |
| 麦克风实时转写、翻译与升级候选 | [实时翻译](live-translation.md) |
| 图片来源、生成与原生同步 | [素材说明](images/README.md) |
| 变更记录 | [CHANGELOG](../CHANGELOG.md)，未发布工作只写 Unreleased |
| Agent 修改与验证约束 | [AGENTS](../AGENTS.md) |

日常启动使用 `pnpm start`（等价于 `pnpm native:launch`），没有候选包时先运行 `pnpm native:package`。启动器选择已打包候选，不编译当前源码，也不启动旧 React/Tauri；运行中构建的复用、平台差异及显式资料选择见[原生模块说明](../apps/native/README.md#启动与验证入口)。`native:preview` 使用退出后清理的临时合成资料，不适合保存日常数据。

## 模块契约

| 模块 | 文档 |
| --- | --- |
| 跨语言 JSON Schema | [Shared contracts](../packages/shared-types/README.md) |
| 两端共用的运行时、文案、模板和地图素材 | [桌面共享资源](../packages/desktop-resources/README.md) |
| Runner V1/V2、进程与结果 | [Runner](../apps/native/internal/infrastructure/runner/README.md) |
| AI 完成响应与发布边界 | [AI](../apps/native/internal/infrastructure/ai/README.md) |
| 远端能力与 JSON 适配 | [Remote](../apps/native/internal/infrastructure/remote/README.md) |
| 旧 JSON 任务读取 | [Legacy jobs](../apps/native/internal/infrastructure/legacyjobs/README.md) |
| Excel 与人员导入事务 | [Member XLSX](../apps/native/internal/infrastructure/memberxlsx/README.md) |
| 日志脱敏 | [Diagnostics](../apps/native/internal/infrastructure/diagnostics/README.md) |
| FunASR 候选 wheel 与来源证明 | [Vendor recipe](../python/funasr-runner/vendor/README.md) |
| ASR 真实媒体与平台证据 | [ASR 验证资产](../benchmarks/asr/README.md) |

## 计划与历史证据

[过程文档索引](ai/README.md) 只保留原生重构相关设计、实施计划、调研和评审。旧 React/Tauri 专属文档与历史方案已删除；当前说明统一围绕 Go/MyGo。保留的原生验收记录仍受各自日期、输入和平台范围限制。

当前重点入口：

- [原生重构设计](ai/designs/2026-11-16-native-desktop-refactor.md)：迁移目标与退出条件。
- [原生实施记录](ai/plans/2026-11-17-native-desktop-implementation.md)：带日期的增量证据。
- [小游戏体验设计](ai/designs/2026-11-19-native-gameplay-experience-design.md)：独立 P5-G 工作线。
- [本次文档整理记录](ai/reviews/2026-11-20-documentation-refresh.md)：范围、修正与检查结果。

## 维护原则

1. 长期操作、架构和业务规则留在本目录及模块 README；过程文档不成为唯一事实来源。
2. 根中英文 README 同步能力与边界，细节链接到单一正式说明，避免重复堆积验收日志。
3. 当前源码、共享 schema、运行时 manifest 和脚本优先；发现文档与代码冲突先核实，不按旧描述修改实现。
4. 历史记录保留原日期、输入与限制；归档日期不是重新验收日期。平台运行、签名、公证、扫描和发布分别记录。
5. 更新文档时检查相对链接、代码路径与命令；生成物从来源生成，依赖锁不当作普通文本说明改写。媒体、数据库、凭据、临时日志与打包产物不放入文档仓库。
