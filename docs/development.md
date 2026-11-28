# 开发、验证与候选启动

文档核对：2026-11-28。命令依据根 `package.json`、`scripts/run-native.mjs`、`scripts/package-native.mjs` 和当前质量/候选工作流。本页是操作说明，不表示以下所有检查已在某台设备执行。

## 工具链

| 工具 | 仓库约束 |
| --- | --- |
| pnpm | `10.30.3`，安装使用既有 lockfile |
| Go | `apps/native/go.mod` 声明最低 `1.27.3`，项目脚本和 CI 固定 `go1.27.4` |
| MyGo | `go.mod/go.sum` 锁定提交 `eadf1fce6af3`，不直接跟随最新版本 |
| Rust | `1.94.0`，沿用根 workspace 和 `Cargo.lock` |
| Node | CI 使用 `24` |
| Python 开发环境 | 通过 `pnpm python:bootstrap` 准备；与产品托管解释器分开 |

在仓库根目录安装 JavaScript 依赖：

```sh
pnpm install --frozen-lockfile
```

原生 Go 检查使用只读模块模式，不自动调整依赖。正常开发从相关包开始：

```sh
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go -C apps/native test ./internal/domain
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go -C apps/native test -race ./internal/application
```

上述环境变量写法适用于 POSIX shell；PowerShell 中先设置 `$env:GOTOOLCHAIN = "go1.27.4"` 和 `$env:GOFLAGS = "-mod=readonly"`，再运行 `go`。`native:check`、`native:compat`、`native:preview`、`native:build:matrix` 等启动/验证脚本，以及候选打包和许可证脚本会自动固定 Go 工具链；单独运行 Go 命令时仍需显式设置。

## 选择启动方式

| 用途 | 命令 / 入口 | 资料与副作用 |
| --- | --- | --- |
| 构建新版候选 | `pnpm native:package`，随后运行 `pnpm start` | 打包命令输出候选包目录；启动器校验并激活该平台候选，默认使用独立资料 |
| 日常启动新版 | `pnpm start`（等价于 `pnpm native:launch`） | 只启动已打包的原生候选，不解析或启动旧 React/Tauri |
| 合成只读预览 | `pnpm native:preview --theme light` | 先执行完整兼容测试，再打开临时合成库，退出清理 |
| 合成可写预览 | `pnpm native:preview --writable --theme light` | 显式临时目录；只适合验收，不保存日常资料 |

启动前先检查同资料目录是否已有 Liberty 实例，优先激活已有窗口。不要通过通用 `Liberty` 应用名或陈旧的 Tauri bundle 解析“新版”。构建包不会自动替换当前运行窗口。preview 和 candidate 的身份、参数及资料恢复方法见 [apps/native/README.md](../apps/native/README.md)。

`apps/desktop` 在迁移期间冻结为临时生产入口和对照端；它不是长期兼容产品。根目录已移除旧 UI 的启动别名。仅在明确维护旧端时，使用 `pnpm --dir apps/desktop dev`、`dev:web` 或 `tauri`；这些命令会启动旧端，不是新版入口。旧端测试、类型检查、构建和回退 workflow 只为迁移门禁服务；共享事实源迁出、原生发布门禁和回退演练完成后，须在同一退役变更中删除旧端及这些检查。

只读快照调试示例：

```sh
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go -C apps/native run ./cmd/liberty-native --database /absolute/path/liberty.sqlite3 --locale zh-CN --theme light
```

不要将生产数据库复制为在线快照，也不要删除库以解决锁或迁移问题。接管现有生产资料和恢复备份属于显式操作，遵守原生模块说明中的锁、恢复点和凭据边界。

## 按修改范围验证

| 范围 | 检查 |
| --- | --- |
| 原生 Go 局部变更 | 相邻测试、`gofmt`；并发/生命周期变化加相关 `-race` |
| 原生整体 | `pnpm native:check`：共享生成源漂移、打包/运行时策略自测、格式、模块校验、vet、全包普通测试；不读取旧端 inventory |
| 原生完整本地质量入口 | `pnpm native:verify`：原生整体、Python lint/tests、Native 版本/平台/安全边界/锁/候选工作流检查与阻断自测；不调用旧前端或 Rust |
| 旧端退役前的源码独立性 | `pnpm native:source:check`：复制当前受 Git 跟踪的 Native/共享输入到隔离目录，排除旧端、Cargo 和旧端 workflow；全新安装开发依赖，删除并重建九组生成产物，再执行 `native:verify`、`native:compat` 和三目标编译 |
| 迁移数据/协议兼容 | `pnpm native:compat`：校验共享的冻结 Rust 黄金样本，重放 Go 迁移、查询、业务事务、重开及 XLSX/DOCX；不构建或运行旧客户端 |
| 原生目标编译 | `pnpm native:build:matrix`：三目标主程序和测试包，只编译，不执行其他平台二进制 |
| 旧端 TypeScript/界面 | `pnpm desktop:typecheck`；涉及 CSS、打包入口或资源解析时补 `pnpm desktop:build:web` |
| Rust | 相邻测试、`pnpm rust:fmt:check`；公共路径加 workspace tests 与 `pnpm rust:clippy` |
| 跨层、数据库、权限、发布 | `pnpm check` 加原生及专项检查 |
| 真实依赖安全与许可证 | `pnpm security:check`、`pnpm licenses:check`；必须有固定扫描器 |
| 原生独立供应链 | `pnpm native:security:check`、`pnpm native:licenses:check`、`pnpm native:sbom:generate`；不读取 Cargo，不调用 cargo-deny；真实漏洞扫描仍会阻断未获豁免的发现 |
| 仅文档 | `pnpm ci:docs`；另核对未跟踪新文档、标题锚点、源码路径和命令，不能把旧测试日志写成本轮通过 |

黄金样本和校验边界见[样本说明](../packages/desktop-resources/fixtures/native-v10/README.md)。跨进程锁、恢复 journal 和文件写入由 Native 回归验证；当前兼容入口不执行 Go/Rust 动态互操作，完整界面和系统交互仍需实机验收。

`native:source:check` 在 `target/native/source-isolation-*` 保存输入摘要、基准提交、工作区修改标记和逐项结果；源码副本放在仓库外的系统临时目录，避免向上查找时借用原工作区的 `node_modules`。成功后清理临时源码与依赖，失败时保留并输出隔离目录。它不复制工作区缓存、用户资料或 `.git`，不改当前源码；合成 ASR 阻断测试使用临时副本自己的空 Git 提交。新增业务输入须先纳入 Git 跟踪才能参加验证。工具会复用 Go 模块/编译缓存，不能称为断网或全空缓存构建；修改中的输入按摘要绑定，不能冒充已提交发行证据。此命令不打包、不运行 GUI，也不替代实机 ASR、安装回退、漏洞扫描或生产切换。

`pnpm check` 先执行 `native:verify`，再通过 `legacy:check:fast:without-python`、Web 构建、Clippy、发布与 CI 策略检查完成临时生产端验证；共享 Python 只执行一次。单独 `legacy:check` 仍包含 Python，随旧端退役一起删除。完整兼容/race/matrix、真实漏洞扫描和发布验收分别执行。

PR 和 main 的 `quality.yml` 按改动范围选择检查，未知或高风险改动执行 `ci-full.yml`；没有定时任务，也不自动构建候选。`ci-gate` 汇总所有所需结果，取消的未验证改动会保留在后续 main 提交的检查范围中。操作和分类规则见 [CI/CD 说明](architecture/ci-cd.md)。

`.github/workflows/build-native.yml` 只供手动或显式调用，选择单个目标或全部三目标。它先执行共同完整门禁，包含 Native、临时生产端、冻结兼容、race、三目标编译、实际系统测试与隔离凭据验收、真实漏洞扫描、许可证/SBOM。全部通过后才打包，执行解包后的版本检查，并独立核对候选文件集合、架构、manifest、归档与 SHA-256，再制作并核验社区 DMG/MSI。此工作流只有读取仓库的权限，不发布 Release 或写标签；macOS 使用无需证书的 ad-hoc 临时签名，不提供 Developer ID 签名或公证。配置存在不等于某次 GitHub 执行已通过。

只改文档时核对源码、相对链接、Markdown 围栏、命令存在性和历史状态即可；不把旧日志冒充本轮构建结果。

## 需要显式环境的验收

| 命令 | 行为与要求 |
| --- | --- |
| `pnpm native:credentials:acceptance` | 使用隔离测试 service 写入、回读并清理真实系统凭据；不访问生产条目 |
| `pnpm native:runtime:acceptance` | 默认生产配置的全新哈希锁安装、vendor 重建、四模型验证、真实 Runner/host 两种说话人模式、持久化重开、安装库存与真实扫描；使用合成音频和忽略目录，需固定 OSV 扫描器 |
| `pnpm native:runtime:candidate:acceptance --runtime-input-directory <绝对目录>` | 验证固定候选输入、安装库存及扫描；不采用生产资料 |
| `pnpm native:meeting:acceptance` | macOS 原生交互验收；需先完成运行时验收，使用合成资料、回环 AI 和临时凭据 |
| `pnpm native:portable:acceptance --previous-archive <绝对旧包路径> --candidate-archive <绝对新包路径>` | 在匹配目标主机校验两份不同版本的候选归档，在全新临时安装/资料目录交互验证包替换、失败回退、SQLite 快照恢复及旧版本重开；不操作现有应用或用户资料，不证明 MSI 或其他主机通过 |
| `pnpm native:remote:acceptance` | 外部测试服务的只读验收；环境中指定 URL、凭据引用、合成任务 ID |
| `pnpm native:bench:reads` | 在冻结合成库上测量 Go 读取与分配；不运行 Rust 对照，不代替整应用性能验收 |

生产配置的运行时验收和会议 GUI 验收在 `target/native/runtime-acceptance/` 下共用缓存，按目标平台、运行时版本、模型集版本和模型 profile 区分。切换运行时或模型集后先跑 `pnpm native:runtime:acceptance`，再开始会议 GUI 验收。安全候选在 `target/native/runtime-candidate-acceptance/` 下单独缓存，目录键还包括已验证的随包输入摘要。两者均保留旧缓存。

远端验收变量为 `LIBERTY_REMOTE_ACCEPTANCE_URL`、`LIBERTY_REMOTE_ACCEPTANCE_CREDENTIAL_REF` 和 `LIBERTY_REMOTE_ACCEPTANCE_JOB_ID`。凭据引用指向系统凭据库中的测试条目，不在命令或文档中填写 Token。真实媒体 ASR 基准的输入和证据要求见 [benchmarks/asr](../benchmarks/asr/README.md)。

便携包验收需人工或桌面自动化操作四个阶段：旧原生候选创建自己的数据库，在人员页面新增且只保存一名 `Portable baseline` 后正常退出；新候选将该人员改名为 `Portable upgraded` 并保存退出；确认恢复完成提示；回退到旧原生候选，复核 `Portable baseline` 后退出。脚本在升级前持有资料锁，通过 SQLite 保存包含 WAL 的旧 schema 快照，随后核对升级后的当前 schema、恢复后的旧 schema、失败替换前后的数据库字节、恢复前安全副本及每次退出释放资料锁。成功时清理本轮临时目录，失败时保留现场；不要将旧 Tauri 包作为 `--previous-archive`。归档内清单、逐文件摘要、确切文件集、目标架构及实际版本输出均须通过，档案摘要随日志输出；验收结果仅覆盖所选两个包及实际 schema 路径，不自动证明发行源码归因、其他旧 schema 或未来版本兼容。

## 生成源与发布

双语文案、SQLite 迁移 SQL、内置总结模板、展示和宠物素材、运行时清单与模型版本均在共享目录维护。改共享来源后运行对应 `*:generate` 并检查生成差异。契约变更分别用 `pnpm contracts:generate` 和 `pnpm native:contracts:generate` 生成两端类型。退役前必须让 Native 和共享目录独立提供所有仍需的生成输入与脱敏夹具，再删除旧端读取路径。完整映射见[桌面共享资源](../packages/desktop-resources/README.md)和[原生架构](architecture/native-desktop-architecture.md)。

### 候选归档

候选打包支持 `darwin-arm64`、`darwin-x64`、`windows-x64`。manifest 的 macOS ARM 平台 ID 是 `darwin-aarch64`，Go 架构是 `arm64`，不能把三个命名位置混用。Native 包版本只由根 `package.json` 与 Native host 版本决定；默认 `release:check-versions` 仍核对当前生产 Release 的全部来源。`native:package` 的 Windows 输出为便携包。

Windows 打包从共享 `packages/desktop-resources/presentation/icon.ico` 生成确定性的 x64 COFF 图标资源，在隔离的 `.mygo` 临时 main 包中链接进 exe，完成或失败后清理，不把 `.syso` 留在源码目录。原生窗口使用同源生成的 PNG；修改图标后运行 `pnpm native:presentation:generate`。打包和独立资产检查均拒绝缺失或篡改的 PE 图标，MSI 开始菜单和卸载列表也引用该共享图标。macOS 继续使用原有 ICNS。

### 社区安装包

社区安装交付使用 `pnpm native:installers:check` 和 `pnpm native:installers:build --target <target> --commit <40位SHA> --input artifacts/native/<target>`。后者要求干净且提交匹配的源码，并消费前一步 `check-native-release-policy.mjs verify-artifacts` 生成的四份已验证文件；在匹配主机制作 macOS ad-hoc 临时签名 DMG 或未做可信发布者签名的 Windows per-user MSI。每份安装器均重新挂载或解包，比对内嵌文件并执行其中程序的 `--version`；输出安装器、`installer-manifest.json` 并扩展原 `SHA256SUMS`。清单绑定源码、输入归档、安装器字节、真实签名状态和固定 GitHub Releases 更新地址。此命令不发布 Release、不授予 Git 写权限，不宣称公证、无安全提示、安装升级事务或数据库回退已验收。`build-native.yml` 在完整门禁后调用它；历史候选归档和可选可信证书安装器的检查不削弱。

Windows 社区及证书安装器共用有序产品版本编码：`major.minor.(patch*1000+rank)`，alpha 为序号 1–299，beta 加 300，rc 加 600，稳定版使用 999。major/minor 最大 255、patch 最大 64，超界或不能映射的 SemVer 明确拒绝；原始版本保留在文件名、应用输出及清单中。该规则避免 alpha 升级互相覆盖或稳定版被识别为降级，真实 MSI 升级/卸载仍需 Windows 验收。

### 可选证书签名安装包

原生分发入口为 `pnpm native:distribution:check`、`native:distribution:prepare`、`native:distribution:build`、`native:distribution:verify` 和 `native:distribution:accept-install`。后三类产物操作使用确切 `--target`、`--commit <40位SHA>`、`--output <新目录>`；`accept-install` 另需 `--previous <较旧已签名MSI>`。prepare 只生成不可分发的安装输入。build 在匹配目标平台强制执行质量、兼容、race、平台、供应链和凭据检查，再制作 macOS Developer ID 签名/公证 DMG 或 Windows Authenticode 签名的 per-user MSI。verify 重新校验签名、信任、摘要、架构、版本和签名覆盖的源码归因。Windows 事务验收覆盖失败升级回滚、成功升级、阻止降级、卸载和旧安装器回退；不证明数据库回滚。证书缺失或检查失败会阻断，不能跳过。凭据变量和生产切换条件见[发布就绪检查](architecture/release-readiness.md)。

### 资产核验与版本

独立复核已生成的候选资产（输出目录须为空）使用 `node scripts/check-native-release-policy.mjs verify-artifacts --target <manifest平台ID> --commit <40位Git提交> --output <输出目录>`。该命令不重新编译、不启动 GUI；候选文件清单与归档任一不一致均失败。

提交、推送、标签和发布不是构建命令的后续默认动作。版本一致性包括两个 `package.json`、Tauri 配置、`Cargo.toml`、`Cargo.lock` 与 Go `host.Version`；发布变更同时维护双语 CHANGELOG。

## 本地实时翻译验收

用户流程、离线模型与候选构建见[实时翻译](live-translation.md)。真实链路回放使用已准备的隔离 `funasr-upgrade` 目录及固定公开音频，不能指向生产资料。隔离目录必须带有 `.native-funasr-upgrade-acceptance` 标记，内容为 `isolated-funasr-upgrade-v1` 加换行；该标记只用于专门准备的测试目录。基础运行时与流式模型先由 `managed/upgrade_acceptance_test.go` 验证并安装。

```sh
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly \
LIBERTY_NATIVE_UPGRADE_ROOT=/absolute/isolated-upgrade \
LIBERTY_NATIVE_UPGRADE_MODEL_SEED=/absolute/verified-stream-model \
go -C apps/native test -tags=runtime_acceptance ./internal/application \
  -run '^TestRealLiveASROfflineTranslationPersistenceAndSubtitleExport$' -count=1 -v
```

另设 `LIBERTY_NATIVE_LIVE_SOAK_SECONDS=7200` 并添加 `-timeout=130m` 可执行两小时按墙钟回放；在 macOS 可用 `caffeinate -i` 包裹测试进程防止空闲休眠。该测试生成的临时音频来自固定公开样例，不涉及用户录音；结果写入隔离缓存的 `live-soak-result.json`，包含测试进程及子进程的 CPU、物理内存/working set 和字幕延迟，不是完整 GUI 或通用质量基准。退出码、持续时间、定稿持久化与停止释放均需通过，不能只检查结果文件存在。
