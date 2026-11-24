# CI/CD 操作与检查规则

最后核实：2026-11-24。执行来源为 `.github/workflows/`、`scripts/ci/` 和根 `package.json`。支持 macOS Apple Silicon、macOS Intel、Windows x64；当前 Native 仍为隔离候选，生产切换按[发布要求](release-readiness.md)验收。

## 何时执行

| 事件 | 检查与交付 |
| --- | --- |
| 面向 main 的 PR、推送 main | 计算完整改动范围，执行对应检查，汇总为 `ci-gate`；普通提交不打候选包 |
| 修改 CI、工具链、依赖、共享数据契约、安全策略或发布配置 | 自动升级为完整验证，仍不自动打包 |
| 手动 Quality Gate，mode 为 full | 对所选 ref 在触发时解析出的确切 SHA 执行完整验证 |
| 手动 Quality Gate，mode 为 security | 仅执行真实安全、许可证和 SBOM；结果为 `security-gate`，不能替代 `ci-gate` |
| 手动 Build Native Candidates | 完整门禁通过后，按选择的一个平台或全部三平台构建并核验候选 |
| 从 main 手动 Publish Native Prerelease | 选定已成功的三平台候选运行；核实源提交属于 main 历史，重新执行该确切源码的完整门禁，再由 Actions bot 公开预发行 |
| 稳定 1.x 发行标签或显式旧端生产发布 | 完整门禁后执行现有发布事务，核对版本、提交、Release ID 与资产；2.x/预发布在旧入口拒绝 |

没有 `schedule` 定时任务。工作分支的普通 push 不另跑一次 PR 检查。main 更新不自动发布 Release。

## 改动分类

`scripts/ci/plan.mjs` 输出带 head、base、模式、检查集合、分类原因、路径数量与差异摘要的 JSON，写入 job 输出和运行摘要。多类改动取并集；未知路径、Git diff 失败或找不到可信基准时执行完整验证。

| 范围 | 所选检查 |
| --- | --- |
| 根 README、普通 docs Markdown、模块 README | 文档链接和代码围栏；CHANGELOG 另加版本一致性 |
| Native Go 普通实现 | 生成物漂移、格式、模块校验、vet、普通 Go 测试、必需安全回归 |
| 控制器、应用服务、host、Runner、托管运行时、资料或凭据 | Go 检查并追加 race；当前 race 为整个 Go 模块 |
| repository、旧任务、资料导入、凭据、domain、host | 追加完整冻结兼容回放 |
| macOS / Windows 平台文件 | 追加三目标编译及对应平台的实际 Go 测试和隔离系统凭据验收 |
| 通用凭据与 system 实现 | 三目标编译和三个系统目标执行 |
| Python Runner | Python lint/tests，以及 Native 和 Rust 消费方检查；仅 Python 测试文件改动只跑 Python |
| TS/TSX/CSS、旧生产端 UI | 契约漂移、类型、前端测试、Web 构建 |
| Rust 普通实现 | workspace tests、fmt、Clippy；Windows 实现追加 Windows x64 编译 |
| 共享文案、素材、模板 | Native、前端和 Rust 消费方；宠物 JSON 另加兼容回放 |
| 数据库、共享契约、依赖声明和锁、运行时资源、供应链、脚本、工作流、全局配置 | 完整验证 |

选择粒度以模块为单位：Native 普通检查和 race 覆盖整个 Go 模块，Rust 检查整个 workspace。没有根据文件名猜测单个测试函数。`AGENTS.md`、生成器输入、模板与可执行脚本不进入纯文档通道。

平台范围同时检查文件命名和改动前后的 Go build tags / Rust 条件编译声明；移除平台约束仍会触发目标检查。通用 Go 平台约束扩大到全部受支持目标，Rust 平台条件追加 Windows x64 编译。

PR 从目标分支与 PR head 的 merge-base 比较到 GitHub 实际合并提交，重命名前后路径与删除均纳入。PR 分类器来自目标分支基线；main 来自推送前的提交。引入分类器的首个提交或基线缺少分类器时执行完整验证。

main 的差异基准为 Quality Gate 最近一次成功 push 的祖先提交，排除手动检查结果。若代码提交 A 的检查被后续文档提交 B 取消，B 仍包含 A 尚未验证的代码变化，不能退成文档检查。最多查询最近 500 个成功运行；基准不可用时扩大为完整验证。

`ci-gate` 总会执行汇总。计划失败、必需检查失败、取消或意外跳过均失败。`ci-full.yml` 的 `full-gate` 依赖所有完整检查，候选构建和生产发布必须等待它通过。

## 工作流和命令

| 文件或命令 | 职责 |
| --- | --- |
| `quality.yml` | PR/main 改动分类、按需调用、固定结果汇总 |
| `ci-suite.yml` | 封闭检查单元的工具链、缓存、执行环境 |
| `ci-full.yml` | 文档、Go、Python、前端、Rust、策略、兼容、race、矩阵、三平台执行、Windows 专项及供应链完整集合 |
| `build-native.yml` | 按需完整门禁和候选打包，无签名或公开发布权限 |
| `release-native.yml` | main 授权、确切源提交全量复核、候选 ZIP 与资产校验、备份、bot 草稿事务和公开下载验证 |
| `build-desktop.yml` | 临时生产发布事务，强制调用完整门禁；随旧端退役删除 |
| `pnpm ci:generate` | 从 `scripts/ci/workflows.mjs` 生成五个受控工作流 |
| `pnpm ci:check` | 校验封闭工作流契约、分类器与失败阻断回归 |
| `pnpm ci:docs` | 只检查受 Git 跟踪文档的相对链接与围栏 |
| `pnpm check` | 开发者主动执行的完整本地质量入口，包含共享 Python 一次、Native、临时旧端、发布和 CI 策略 |

`pnpm check` 不包含真实漏洞扫描、完整冻结兼容、全包 race 或所有系统的实际执行；完整 CI 分别执行这些检查。`pnpm native:verify` 仍是无需旧前端或 Rust 的原生本地检查入口。候选交付在旧端退役前调用共同完整门禁，因而也要求临时生产端检查通过。

手动触发示例（GitHub CLI）：

```sh
gh workflow run quality.yml --ref main -f mode=full
gh workflow run quality.yml --ref main -f mode=security
gh workflow run build-native.yml --ref main -f target=windows-x64
```

普通检查和候选构建 checkout 触发时的 `github.sha`。Native 发布的授权与事务代码 checkout main 的触发 SHA，完整复核则通过可复用工作流的 `source_commit` 参数 checkout 已授权的 40 位候选源码 SHA；参数为空时仍使用 `github.sha`，非空且不是确切 SHA 时拒绝。因此发布 run 的 head SHA 表示事务代码，不能代替发行源码 SHA；二者分别写入发布结果。真实模型下载、受控媒体性能、GUI/IME/读屏、外部服务以及证书与安装事务继续按[开发指南](../development.md)和发布要求单独验收。

2.x Native 预发行通过手动候选构建入口准备三平台资产，再从 main 手动调用独立发布入口。发布只接受没有 build metadata 的 2.x 预发行源码版本，所有版本文件必须一致；GitHub Release 强制 `prerelease:true` 和 `make_latest:false`。旧 Tauri 的标签触发器排除预发布和 build metadata，手动发布另以源码版本拒绝非稳定 1.x，避免将 Native 版本名用于旧端安装包。

### Native bot 发布事务

先运行三平台候选构建，取得其成功 run ID，再显式发布：

```sh
gh workflow run build-native.yml --ref main -f target=all
gh workflow run release-native.yml --ref main -f candidate_run_id=<successful-candidate-run-id>
```

发布入口检查候选运行的仓库、原工作流、确切源码、全部 15 项检查、`full-gate` 和三个主机候选任务。源码必须在本地和远端 main 历史中。下载原运行的四个 artifact 时核对 GitHub ZIP 大小及 SHA-256，再检查 ZIP 路径、精确文件集合、tar payload、三目标二进制架构、内嵌与外置 manifest、项目许可证和 Go notices。随后组装 13 份资产。格式 2 的统一清单不内嵌 Release ID 或发布尝试时间，便于断点恢复；发布结果和 Release 正文绑定最终 ID。

授权、完整源码复核和准备阶段均只读。准备阶段将所有资产、来源、摘要、发布计划和必要的原发行备份上传为不可变 artifact 后，发布 job 才获得 `contents: write`，并使用 `${{ github.token }}` 创建草稿。API 创建响应中的 ID用于后续全部操作；已有 bot 草稿只复用同提交、同构建和同清单的资产，拒绝覆盖、未知资产或不完整上传。全部上传后的真实字节通过验证后才公开，随后逐一核验无需登录的下载、标签、bot 作者、ID、预发行状态和 stable latest。

已有人工预发行交接给 bot 时，必须显式传入 `replace_release_id=<exact-manual-release-id>`。这会更换 Release ID。只有该 ID 对应的已公开、非 immutable、2.x 预发行及其 13 份公开下载全部匹配候选源码和构建，且原正文与全部旧字节已备份，才删除这个旧 Release；标签不删除或移动。11 份候选归档、平台 manifest、SBOM 和 notices 保持原字节，统一清单与 `SHA256SUMS` 更新为格式 2。普通发布拒绝替换人工 Release，稳定版不能通过该参数交接。

发行源码以标签解析出的提交、candidate/build manifest 和发布结果为准。[GitHub API](https://docs.github.com/en/rest/releases/releases) 的 `target_commitish` 在标签已存在时不参与标签定位；对历史提交指定此字段还可能触发额外的 workflow 写权限检查。因此已有标签的 Release 创建和公开使用 `target_commitish:main`，并在写入前后独立核对标签仍指向已验证源码。新标签使用确切源码 SHA，绝不以 main 的新提交替换既有标签。

中断后优先对原发布 run 执行 `gh run rerun <publication-run-id> --failed`。publish job 使用已成功 stage 的输出 attempt 获取原 artifact，保留旧备份并只上传缺失资产；即使旧 Release 已删除但创建请求中断，也可从这个原始 bundle 恢复。如果必须修复原 run 的事务代码，旧 run 的 checkout 仍固定在旧 SHA；可从修复后的 main 重新 dispatch，同时传入同一 `candidate_run_id`、原 `replace_release_id` 和 `recovery_run_id=<original-publication-run-id>`。只读 stage 会验证该 run 来自本仓库 main、原源码完整复核和 stage 成功、确切 artifact 的来源/大小/摘要/文件集，以及原 ID、原元数据和全部资产；随后将同一备份随新计划重新保存为不可变 artifact，再进入写入阶段。没有这个原始备份不能恢复已删除的人工发行。

写入和恢复前均验证相关源码复核任务在 24 小时内成功，且源码的安全例外尚未过期；过期不能只复用旧门禁。备份和结果 artifact 保留 14 天，过期后不能声称仍有可用恢复证据。已公开且字节一致的 bot Release 可重复核验，重复运行不覆盖资产。

## 资源、权限和合并

普通 CI 按工作流和 PR/ref 分组，取消被新提交取代的运行。候选构建和正式发布单独分组，不中断正在执行的交付事务。

按需安装对应语言工具链，文档任务只需要 Node。缓存锁定 pnpm store、Go 和 Cargo 依赖/编译缓存、Python 开发工具下载；缓存不提供测试通过证明。共享 Python 不再重复测试，完整供应链任务统一扫描 Node、Go、Python 开发工具和三个生产运行时，并保留 Rust cargo-deny 与两种 SBOM。现有例外的范围和到期时间保持原规则，发布前重新扫描。

Go race 与跨目标编译使用各自的构建缓存，按工具链、系统、架构、锁和提交区分，并从相同模式恢复。普通 Go 检查先写入依赖缓存时，不会让 race 和跨平台编译一直重复冷构建。

默认权限是 `contents: read`。main 分类的基准查询和 Native 发布的跨运行 artifact 读取增加 `actions: read`；Native 发布只有最后的 publish job 增加 `contents: write`。Action 使用固定 SHA；安装使用固定工具链和锁文件；生产凭据与数据库不入缓存。候选归档上传前重新验证文件集合、架构、manifest 和 SHA-256，保留 14 天。正式发布的写权限限定在受控发布事务。

main 的合并门槛应使用 GitHub Actions 的 `ci-gate`，禁止强推和删除，不用 `security-gate` 或条件性单项检查替代。仓库保护是 GitHub 服务端设置，须单独核实；工作流文件本身不证明保护已经生效。当前规则与实际运行证据记录在[实施记录](../ai/designs/2026-11-23-layered-ci-cd.md#实施与验证记录)。
