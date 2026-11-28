<p align="center">
  <img src="https://avatars.githubusercontent.com/u/277389313?s=200&v=4" width="128" height="128" alt="Liberty">
</p>

<h1 align="center">Liberty</h1>

<p align="center">本地优先的会议音视频桌面工作台</p>

[English](./README.md) | [简体中文](./README.zh-CN.md)

Liberty 将本地转写、说话人分离、逐字稿校对、会议纪要和结果导出连接成完整桌面流程，并提供可选的本地宠物与打工玩法。

产品免费，基础会议流程无需 API Key 或付费 API。AI 整理纪要由用户主动发起，所选模型服务可能产生费用。应用负责管理运行环境和模型；默认面向 CPU 与普通配置电脑，最低配置和性能范围仍需目标设备实测。

## 当前状态

工作区正在准备 **2.0.0-alpha.4**；本版范围见[第四个预发行说明](docs/releases/2.0.0-alpha.4.md)，后续改动记录在 [Unreleased](CHANGELOG.md#unreleased)。版本号和源码不代表已公开发行，下载以 [GitHub Releases](https://github.com/whalesky-labs/Liberty/releases) 的实际资产为准。

| 实现 | 当前职责 |
| --- | --- |
| `apps/native` | Go + MyGo + SQLite 原生候选；日常开发、启动与候选打包入口 |
| `apps/desktop` | 冻结的 React/Tauri 实现；临时生产发布入口与迁移对照 |
| 共享资源与 Python Runner | 运行时、模型、数据契约、模板、文案、素材和转写协议 |

Native 生产切换尚未完成。[切换门槛](docs/architecture/release-readiness.md#cutover-and-retirement)全部通过后，旧端及其发布链在同一变更中删除。候选版使用独立资料目录，已有资料须显式导入。

## 会议与陪伴能力

- **导入与处理**：单文件或最多 32 个文件批量入队，持久化队列、进度、日志、重试和删除；批量创建失败项可独立重试。
- **回听与校对**：逐句回听、倍速、正文修改、讲话人改归属，以及已验证讲话人的筛选和改名。说话人不可用时保留原始逐字稿，不生成虚假标签。
- **整理纪要**：独立人工纪要、原文来源关联、可选的 OpenAI 兼容 AI 总结、模板与历史结果；AI 完成不会覆盖人工稿。
- **导出与资源**：TXT、Markdown、正式 DOCX，人员 Excel 导入导出、部门与记录人管理，以及模型和模板管理。
- **实时转写与翻译**：麦克风中英文识别、自动离线互译、历史编辑、补译及双语 SRT/VTT/TXT 导出。系统声音与混合输入已有适配，权限及目标设备验收仍待完成。
- **系统设置**：中英文、外观、运行环境与模型安装、处理默认值、存储、权限、诊断及主动检查更新。
- **本地陪伴**：255 级成长、LP、商店与仓库、签到/补签、每日免费盲盒、礼盒、签名兑换、农场和打工地图；工厂装配练习可选接入既有照看动作，不改变奖励。

使用与规则见[会议工作流](docs/meeting-workflow.md)、[实时翻译](docs/live-translation.md)和[宠物系统](docs/pet-system.md)。陪伴功能失败不得阻塞会议主流程；LP 与奖励不引入真钱、交易或排行榜。3D 桌宠仍为开发预览。

## 安装、启动与更新

从 [GitHub Releases](https://github.com/whalesky-labs/Liberty/releases)选择实际提供的对应平台资产。当前交付链准备 macOS ad-hoc 临时签名 DMG、Windows 当前用户 MSI，并保留便携候选归档；社区安装包不提供可信发布者签名或 Apple 公证。旧发行保持其原有资产。

源码开发使用固定的 `pnpm@10.30.3`、Go `1.27.4` 和 Rust `1.94.0`：

```sh
pnpm install --frozen-lockfile
pnpm native:check
pnpm native:package
pnpm start
```

`pnpm start` 启动已校验的原生候选包，不编译源码。启动器选择当前平台最高版本候选，并激活同构建的已有实例；其他可执行路径的原生实例会阻止启动。加载新构建前先正常退出。平台行为、资料目录、备份与恢复见[原生客户端说明](apps/native/README.md)。

首次本地处理需要联网下载运行时和模型。更新检查仅由用户主动触发，下载与安装仍由用户操作。同一可写资料目录不要同时运行两个客户端。

| 平台 | Go 目标 | 打包参数 |
| --- | --- | --- |
| macOS Apple Silicon | `darwin/arm64` | `darwin-arm64` |
| macOS Intel | `darwin/amd64` | `darwin-x64` |
| Windows x64 | `windows/amd64` | `windows-x64` |

目标编译不代表原生交互、ASR 质量、最低系统或安装器验收通过。剩余要求统一见[发布就绪检查](docs/architecture/release-readiness.md)。

## 数据与运行时

本地模式使用 SQLite，以及应用管理的 Python、ffmpeg 和模型。[运行时清单](packages/desktop-resources/runtime/runtime-manifest.json)固定版本和下载资产；默认仍为 Python 3.11.18、FunASR `1.3.9+liberty.local2`。`security-candidate` 和 `funasr-upgrade` 是显式隔离配置。当前 schema v13 保存实时字幕和独立人工纪要；回退旧二进制须使用兼容的升级前快照。

`processingMode` 决定本地或远端处理，填写 `backendUrl` 不会自动切换模式。远端 `liberty-meeting` v1 按协商能力开放既有任务查询、重试、删除和讲话人改名，当前不开放上传创建或 AI 总结写入。API Key 与远端 Token 保存于系统凭据库，SQLite 仅保存引用。

## 开发与文档

| 目录 | 职责 |
| --- | --- |
| `apps/native/` | 原生界面、控制器、用例、领域、基础设施和宿主 |
| `packages/desktop-resources/` | 共享资源和冻结的合成兼容样本 |
| `packages/shared-types/` | 版本化 JSON Schema 与生成契约 |
| `python/funasr-runner/` | Runner、验证器和平台依赖锁 |
| `scripts/`、`config/` | 开发、生成、验证及交付策略 |
| `benchmarks/asr/` | 受控媒体和目标设备证据契约 |
| `docs/` | 持续维护的指南与进行中的工作 |

从[文档总览](docs/README.md)和[开发指南](docs/development.md)开始。实现边界见[原生架构](docs/architecture/native-desktop-architecture.md)，自动门禁见 [CI/CD 规则](docs/architecture/ci-cd.md)。

`pnpm native:verify` 检查 Native 与共享 Python；`pnpm native:compat` 重放冻结兼容样本；`pnpm native:build:matrix` 编译三个目标。跨层改动执行 `pnpm check`，同时检查临时生产端。真实漏洞扫描、实机交互和发行核验分别执行；纯文档检查使用 `pnpm ci:docs`。

## 组织

Liberty 托管于 [whalesky-labs](https://github.com/whalesky-labs)。[项目仓库](https://github.com/whalesky-labs/Liberty) · [问题反馈](https://github.com/whalesky-labs/Liberty/issues) · [版本下载](https://github.com/whalesky-labs/Liberty/releases)。

## 许可证

项目采用 [MIT License](LICENSE)。依赖和运行时资产各自的许可证、NOTICE 与分发要求仍需遵守。
