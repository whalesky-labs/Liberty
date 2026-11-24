# CI/CD 操作与检查规则

最后核实：2026-11-23。执行来源为 `.github/workflows/`、`scripts/ci/` 和根 `package.json`。支持 macOS Apple Silicon、macOS Intel、Windows x64；当前 Native 仍为隔离候选，生产切换按[发布要求](release-readiness.md)验收。

## 何时执行

| 事件 | 检查与交付 |
| --- | --- |
| 面向 main 的 PR、推送 main | 计算完整改动范围，执行对应检查，汇总为 `ci-gate`；普通提交不打候选包 |
| 修改 CI、工具链、依赖、共享数据契约、安全策略或发布配置 | 自动升级为完整验证，仍不自动打包 |
| 手动 Quality Gate，mode 为 full | 对所选 ref 在触发时解析出的确切 SHA 执行完整验证 |
| 手动 Quality Gate，mode 为 security | 仅执行真实安全、许可证和 SBOM；结果为 `security-gate`，不能替代 `ci-gate` |
| 手动 Build Native Candidates | 完整门禁通过后，按选择的一个平台或全部三平台构建并核验候选 |
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
| `build-desktop.yml` | 临时生产发布事务，强制调用完整门禁；随旧端退役删除 |
| `pnpm ci:generate` | 从 `scripts/ci/workflows.mjs` 生成四个受控工作流 |
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

运行均 checkout 触发时的 `github.sha`。执行后核对 run 的 head SHA；不能把另一个提交的成功结论移给当前源码。真实模型下载、受控媒体性能、GUI/IME/读屏、外部服务以及证书与安装事务继续按[开发指南](../development.md)和发布要求单独验收。

2.x Native alpha 通过手动候选构建入口准备三平台资产，后续 GitHub Release 必须明确设为 prerelease 且不替换 stable latest。旧 Tauri 的标签触发器排除预发布和 build metadata，手动发布另以源码版本拒绝非稳定 1.x，避免将 Native 版本名用于旧端安装包。

## 资源、权限和合并

普通 CI 按工作流和 PR/ref 分组，取消被新提交取代的运行。候选构建和正式发布单独分组，不中断正在执行的交付事务。

按需安装对应语言工具链，文档任务只需要 Node。缓存锁定 pnpm store、Go 和 Cargo 依赖/编译缓存、Python 开发工具下载；缓存不提供测试通过证明。共享 Python 不再重复测试，完整供应链任务统一扫描 Node、Go、Python 开发工具和三个生产运行时，并保留 Rust cargo-deny 与两种 SBOM。现有例外的范围和到期时间保持原规则，发布前重新扫描。

Go race 与跨目标编译使用各自的构建缓存，按工具链、系统、架构、锁和提交区分，并从相同模式恢复。普通 Go 检查先写入依赖缓存时，不会让 race 和跨平台编译一直重复冷构建。

默认权限是 `contents: read`，只有 main 分类的基准查询增加 `actions: read`。Action 使用固定 SHA；安装使用固定工具链和锁文件；生产凭据与数据库不入缓存。候选归档上传前重新验证文件集合、架构、manifest 和 SHA-256，保留 14 天。正式发布的写权限只用于既有发布事务。

main 的合并门槛应使用 GitHub Actions 的 `ci-gate`，禁止强推和删除，不用 `security-gate` 或条件性单项检查替代。仓库保护是 GitHub 服务端设置，须单独核实；工作流文件本身不证明保护已经生效。当前规则与实际运行证据记录在[实施记录](../ai/designs/2026-11-23-layered-ci-cd.md#实施与验证记录)。
