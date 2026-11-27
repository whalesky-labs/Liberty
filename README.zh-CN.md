<p align="center">
  <img src="https://avatars.githubusercontent.com/u/277389313?s=200&v=4" width="128" height="128" alt="Liberty">
</p>

<h1 align="center">Liberty</h1>

<p align="center">本地优先的会议音视频桌面工作台</p>

[English](./README.md) | [简体中文](./README.zh-CN.md)

Liberty 将本地转写、说话人分离、AI 总结、会议纪要和结果导出组织成完整桌面工作流，并提供独立的本地宠物与打工玩法。

## 当前重构状态

当前源码版本为 **v2.0.0-alpha.3**，面向 macOS Apple Silicon、macOS Intel 和 Windows x64 的原生候选归档；变化、资料升级与已知限制见 [alpha.3 说明](docs/releases/2.0.0-alpha.3.md)，公开发行状态以 [GitHub Release](https://github.com/whalesky-labs/Liberty/releases/tag/v2.0.0-alpha.3) 为准。

截至 2026-11-25，**新版实现位于 `apps/native`，采用 Go + MyGo，是日常开发、验证和启动入口；正式生产发布入口暂时仍为 `apps/desktop`。** `pnpm start` 启动已打包的原生候选，不编译源码。原生端仍是候选，完整 UI 验收与生产切换均未完成。旧 React/Tauri 只在迁移期间冻结为生产入口和对照实现；Native 门禁全部通过后，在一次可审查的切换变更中删除旧端及其发布链。启动、资料和恢复边界见[原生模块说明](apps/native/README.md)。

| 实现 | 作用 |
| --- | --- |
| `apps/native` | Go/MyGo 原生候选；日常开发、验证、启动与候选打包入口 |
| `apps/desktop` | 迁移期间冻结的 React/Tauri 对照端和生产入口；Native 门禁通过后随发布链一并删除 |
| Python Runner / SQLite / 共享 schema | 两端沿用的转写协议、数据和资源事实来源 |

全 UI、输入法/读屏、真实远端、Intel/Windows 实机、供应链及升级回退仍需独立验收。待发行的安装交付恢复为 macOS 临时签名 DMG 与 Windows 当前用户 MSI，便携归档保留为技术资产；系统可信签名与 Apple 公证尚未提供，不宣称安装时没有安全提示。设置中的“从 GitHub 下载更新”打开 Releases，由用户下载匹配安装包并在正常退出后更新。既有 alpha.3 的已公开资产不会因此自动改变；候选身份及其他验收要求见 [发布就绪检查](docs/architecture/release-readiness.md)。

## 主要能力

- **会议流程**：工作台指标与趋势、新建单文件本地任务、队列搜索筛选、详情进度与日志、重试和删除。
- **结果工作台**：独立窗口查看逐字稿、筛选/重命名已验证讲话人，打开 AI 总结和只读会议纪要。说话人不可用或失败时保留原始逐字稿，不生成虚假标签。
- **AI 总结**：OpenAI 兼容接口、模型和模板选择、持久化运行与分块、失败重试、历史结果选择。总结由用户主动发起，不在转写结束后自动调用。
- **导出与人员**：逐字稿 TXT、纪要 Markdown、完整 Markdown、正式 DOCX；人员 Excel 导入/导出、部门、排序和记录人管理。
- **系统设置**：外观、中英文、主题、运行概览、处理默认值、Python/ffmpeg/模型组件、远端兼容和诊断。原生设置菜单与主菜单共用配色规则。
- **资源管理**：模型、模板、人员卡片与独立编辑窗口；密钥和 Token 保存在系统凭据库。
- **本地陪伴**：桌宠多窗口、255 级成长、LP、商店/仓库、签到/补签、每日免费盲盒、礼盒、签名兑换、农场及打工地图。工厂需照看岗位可选用装配练习完成照看，原有一键操作保留，成绩不影响奖励。

宠物系统为尽力提供的陪伴能力，失败不阻塞会议主流程；LP、盲盒与兑换均不引入真钱、充值、交易或排行榜。规则见 [宠物系统](docs/pet-system.md)。

原生源码支持麦克风中英文实时转写、默认自动切换方向的离线中英互译（无需第三方服务或 API Key）、历史编辑、失败补译和双语字幕导出；默认不保存音频。使用方式与当前限制见[实时翻译](docs/live-translation.md)。FunASR 1.4.29 通过独立 `funasr-upgrade` 候选评估，尚未替换默认运行时。

## 运行方式与边界

设置中的 `processingMode` 决定本地或远端模式。`backendUrl` 是远端连接配置，填写地址不会自动切换模式。

本地模式使用 SQLite、托管或显式配置的 Python、ffmpeg 和模型。默认运行时固定 Python 3.11.18 和经过哈希校验的 FunASR 依赖修正版，具体版本、平台和下载资产以共享的 [runtime-manifest.json](packages/desktop-resources/runtime/runtime-manifest.json) 为准。`security-candidate` 仍是独立的显式试验配置；默认依赖升级不代表原生生产切换已完成。四个固定 FunASR 模型的支持范围和 Torch 剩余安全例外见[发布要求](docs/architecture/release-readiness.md)。并发设置是资源预算上限，8 GiB 设备默认单任务。

远端通过 `liberty-meeting` v1 协商能力，分别启用列表、详情、结果、重试、删除和讲话人改名。当前客户端未开放远端上传创建和远端 AI 总结写入；不能因为协议枚举包含某项就宣称界面支持。

AI 模型可连接 OpenAI 兼容服务；网络请求受目标地址、DNS、重定向和响应大小规则约束。API Key 与远端 Token 使用 macOS Keychain / Windows Credential Manager，SQLite 保存引用。

## 技术与目录

| 层 | 当前原生实现 |
| --- | --- |
| UI 与窗口 | MyGo，自绘原生窗口 |
| 用例与业务 | Go application/domain/client |
| SQLite | `modernc.org/sqlite`，typed repositories |
| 平台能力 | Go profile/credentials/system 适配 |
| 共同来源 | JSON Schema、Python Runner、运行时 manifest、DOCX 模板、消息与素材 |

```text
apps/native/                  Go + MyGo 原生候选
  cmd/liberty-native/         启动参数与通道
  internal/desktop/           窗口、布局与系统对话框
  internal/client/            页面控制器与异步状态
  internal/application/       用例、端口、调度与授权
  internal/domain/            类型与业务策略
  internal/infrastructure/    SQLite、运行时、网络、文件与平台
  internal/host/              装配、启动恢复和退出
apps/desktop/                 临时生产客户端与迁移对照端
packages/desktop-resources/   共享来源、素材与冻结合成回归样本
packages/shared-types/        版本化 JSON Schema 与生成类型
python/funasr-runner/          转写 Runner、验证器与平台依赖锁
scripts/                      开发、生成、验收、打包与发布检查
benchmarks/asr/                ASR 样本和平台证据契约
config/                       平台、安全和许可证策略
docs/                         原生文档与过程记录
```

数据库当前 schema v12，支持实时会话、字幕和自动翻译方向。Native 从共享来源生成迁移定义，在 `packages/desktop-resources/fixtures/native-v10` 中重放历史 Rust 实现导出的合成库和期望输出，已不再构建或运行 Rust oracle。切换前必须从干净检出独立生成并通过检查，不能再读取 `apps/desktop` 作为事实源；原生分层与源文件映射见 [原生架构](docs/architecture/native-desktop-architecture.md)。

## 开发与验证

使用固定 `pnpm@10.30.3`、Go `1.27.4`、Rust `1.94.0`，完整环境说明见 [开发指南](docs/development.md)。

```sh
pnpm install --frozen-lockfile
pnpm native:check
pnpm native:package
pnpm start
```

启动器的候选选择、单实例和平台行为，以及迁移期间旧端的显式诊断启动命令，详见[原生模块说明](apps/native/README.md)。macOS 候选未签名/未公证，Windows 为便携包；加载新构建前需正常退出当前窗口。

合成预览和兼容验证：

```sh
pnpm native:preview --writable --theme light
pnpm native:compat
pnpm native:build:matrix
```

`native:preview` 会先校验共享的冻结黄金样本并运行 Go 回归，再打开临时合成资料，退出后清理，不再执行旧客户端；这不等于全量 UI 或生产验收。`native:build:matrix` 只证明三目标编译，不代表其他平台实机通过。

跨层和共享来源的整仓检查：

```sh
pnpm check
```

`pnpm check` 执行原生与临时生产端检查，共享 Python 只执行一次。PR 和 main CI 按改动范围选择检查，高风险变更、手动全量及发布执行完整门禁；没有定时任务，普通提交不自动打候选包。手动 `build-native.yml` 在完整门禁通过后才构建和核验所选目标。兼容、race、安全、实机及正式发布验收见 [CI/CD 规则](docs/architecture/ci-cd.md)和[开发指南](docs/development.md)；旧端检查随退役变更一并删除。

## 平台

| 平台 | Go 目标 | 候选打包参数 | 运行时边界 |
| --- | --- | --- | --- |
| macOS Apple Silicon | `darwin/arm64` | `darwin-arm64` | FunASR；本机证据限具体测试范围 |
| macOS Intel | `darwin/amd64` | `darwin-x64` | 需独立运行时和 UI 实机验收 |
| Windows x64 | `windows/amd64` | `windows-x64` | 需独立运行时和 UI 实机验收 |

## 文档

- [文档总览](docs/README.md)：当前文档、模块说明和历史资料分类。
- [开发指南](docs/development.md)：启动、检查、生成和验收入口。
- [原生架构](docs/architecture/native-desktop-architecture.md)。
- [原生候选操作说明](apps/native/README.md) / [发布就绪检查](docs/architecture/release-readiness.md)。
- [宠物系统](docs/pet-system.md) / [过程文档索引](docs/ai/README.md)。

## 许可证

项目采用 [MIT License](LICENSE)。依赖和运行时资产各自的许可证、NOTICE 与发布责任仍需遵守供应链检查。
