# Liberty 原生候选客户端

最后核实：2026-11-22。`apps/native` 使用 Go + MyGo，是日常开发、验证和启动入口；**正式生产发布入口暂时仍为 `apps/desktop`**。旧 React/Tauri 仅作为 Native 验收期间冻结的临时生产入口与迁移对照；Native 完整验收与生产切换完成后，旧端及其发布链必须在同一退役变更中删除，不作长期兼容维护。候选包使用独立资料身份。新端开发见[开发指南](../../docs/development.md)，模块职责和数据契约见[原生架构](../../docs/architecture/native-desktop-architecture.md)。

## 启动与验证入口

在仓库根目录运行：

```sh
pnpm native:check
pnpm native:launch
```

完整本地质量入口为 `pnpm native:verify`，包括 Go/生成物、Python 和原生发布策略，不调用 React/Tauri 或 Rust。独立的 `build-native.yml` 工作流执行四目标候选测试、打包及归档核验，并保留真实供应链阻断；它不发布正式 Release。命令与验证边界见[开发指南](../../docs/development.md)。

`native:launch` 校验候选包清单、文件大小与 SHA-256 后，选择当前平台 SemVer 最高的候选；同版本按包目录修改时间选择。它只启动已打包文件，不编译当前源码，也不会通过通用应用名解析旧 Tauri 客户端。检测到原生进程的可执行文件路径与所选候选不同时，启动器拒绝启动并要求先从界面正常退出；不会强杀或主动结束任何运行进程。尚无候选包时先运行：

```sh
pnpm native:package
```

`native:package` 输出本机候选包的确切路径。macOS 启动器通过 `open <确切 .app 路径>` 启动或激活同构建实例；Windows 核实已有可执行文件、进程身份与唯一主窗口后，恢复窗口并核对前台状态。激活失败会返回错误，不重复启动。Windows 分支已通过启动器协议测试，真实窗口激活仍需 Windows 验收。构建后先正常退出当前候选，再启动新包才能加载修改；已有窗口不会自动更新。

候选打包支持 `--target darwin-arm64`、`darwin-x64`、`windows-x64`、`windows-x86`；macOS 输出未签名/未公证应用，Windows 输出便携包。`native:package` 只核对根 `package.json` 与 `apps/native/internal/host/profile.go` 的 Native 版本，以及候选/生产资料身份；生产 Release 的版本一致性仍由发布检查单独核对旧生产来源。候选打包核验所选目标与归档；四目标编译及测试包交叉编译由 `native:build:matrix` 覆盖。编译、归档检查均不等于目标平台执行或安装器验收。

合成数据预览：

```sh
pnpm native:preview --theme light
pnpm native:preview --writable --theme light
```

预览脚本先校验共享的冻结 Rust 黄金样本并执行 Go 兼容回归，全部通过后才构建并打开预览窗口；不再构建或运行旧客户端，也不等于全量 UI 或生产验收。默认把临时合成库作为 `--database <临时目录>/liberty.sqlite3` 只读打开；`--writable` 则使用 `--profile-directory <临时目录> --writable`。退出后临时根目录会清理，两者都不能当作日常资料保存位置。

直接运行 preview 二进制时，除 `--version` 或帮助输出外，必须显式选择 `--database`、`--profile-directory` 或 `--default-profile` 之一。隔离资料写入使用 `--profile-directory` 配合 `--writable`，创建缺失目录/新库或升级旧库另加 `--prepare-database`；`--default-profile` 明确选择现有生产资料。preview 构建不会自动使用候选默认目录；candidate 无参数启动才会选择独立、持久的候选资料目录。

## 资料选择

| 参数 / 通道 | 行为 |
| --- | --- |
| candidate 无参数启动 | 独立、持久的候选资料目录，默认可写并允许准备新库 |
| `--database <绝对路径>` | 只读 SQLite 快照；不与 `--writable` 或 `--prepare-database` 组合 |
| `--profile-directory <绝对目录>` | 显式资料目录；写入需 `--writable`，初始化/升级另需 `--prepare-database` |
| `--default-profile` | 仅只读查看现有生产资料；拒绝可写、升级或恢复操作 |
| `--import-profile <绝对源目录>` | 与 `--profile-directory <不存在的新目录>` 配合，执行离线 SQLite 资料导入后退出；不覆盖源库或已有目标 |
| `--restore-backup <文件名>` | 离线 CLI 恢复所选资料；本身会写入，不能与 `--database`、`--writable` 或 `--prepare-database` 组合。candidate 未指定资料时选择候选目录，preview 仍需显式选择资料；不是已验收的 GUI 恢复工作流 |
| `--locale zh-CN\|en-US`、`--theme auto\|light\|dark` | 本次会话显示覆盖；只在用户主动选择对应设置时解除并保存 |
| `--version` | 输出版本与通道，不访问资料库 |

显式可写目录和恢复入口拒绝与生产目录重叠的路径、别名或同一数据库身份。生产资料接管必须通过 `--import-profile` 导入独立目录，不能用 `--default-profile` 原地写入。没有 `--prepare-database` 不自动升级旧 schema。

解包后的 macOS 候选可执行文件示例（应用包内文件名含空格，必须整体引用）：

```sh
binary='./Liberty Native Candidate.app/Contents/MacOS/liberty-native'
"$binary" --version
"$binary" --profile-directory '/absolute/path/isolated' --writable --prepare-database
```

Windows 便携目录中的文件名为 `Liberty Native Candidate.exe`；不要把示例中的 macOS 路径当作 Windows 启动命令。

## 生产资料接管与离线恢复

此处说明的是候选 CLI 的资料操作，不代表生产切换或完整 GUI 恢复已验收。生产目录始终只读；旧发行客户端不具有本分支的锁与恢复协议，因此不允许 Native 原地接管。先让旧端完成或取消在途操作并正常退出，再导入一个尚不存在的独立目录：

```sh
binary='./Liberty Native Candidate.app/Contents/MacOS/liberty-native'
"$binary" --import-profile '/absolute/path/old-profile' --profile-directory '/absolute/path/new-native-profile'
"$binary" --profile-directory '/absolute/path/new-native-profile' --writable
```

导入在同一只读事务中取得含 WAL 的 SQLite 快照，只接受静止的任务/删除/总结/安装状态，避免恢复旧进程身份。目标迁移并重新绑定现用凭据的随机引用，源凭据不删除；补偿与重试保留持久意图。目标的 `.native-profile-import.json` 只记录源目录摘要和操作身份，不保存原路径或秘密。同源重复导入只验证已发布标记并退出，不覆盖 Native 后续数据。源/目标重叠、软链接和任意既有目标均拒绝。导入不复制安装运行时文件或媒体目录。

回滚到旧客户端时使用保留的旧目录；Native 后续产生的数据不会反向同步。独立 Native 目录需要恢复时，使用 `--profile-directory <Native目录> --restore-backup <同目录实际备份文件名>`。`--restore-backup` 只接受文件名，不接受任意路径。命令成功时当前内容回到快照时点，恢复前的当前内容保留在 `pre-restore` 安全副本中；进入写入阶段后返回错误不保证当前库未变。

可写启动先持有 `.liberty-profile.lock`，为非空资料建立或校验首次接管恢复点，才导入或启动服务。文件名为 `liberty.sqlite3.pre-native-v<接管前schema版本>-<随机后缀>.bak`，版本取原库而非一律 v10；接管升级复用该恢复点，不保证另建迁移副本。真正空的新库记录无需备份，已有未版本化表则仍备份为 v0。后续启动复用已验证恢复点；不要删除该文件、锁文件或数据库规避启动失败。

恢复先预检当前库、源快照、schema、快照引用的系统凭据及已有恢复记录；预检失败不替换当前业务数据。通过后创建包含已提交 WAL 的经验证 `pre-restore` 安全副本，再使用 SQLite backup API 恢复，并按恢复路径处理 WAL checkpoint。进入恢复写入后，凭据记录导入、checkpoint、读回验证、文件同步或恢复记录清理仍可能失败，此时数据可能已经改变；错误不保证当前库字节不变。失败后不要删除仍存在的安全副本或恢复记录；恢复记录清理阶段可能已部分完成，不能假设所有记录都仍存在。恢复对话框的完整原生 GUI 流程仍未单独验收。复制在线 SQLite 主文件也不构成 WAL 一致性备份。

凭据始终留在系统存储。现用记录和可恢复快照仍引用的条目不能清理；轮换保留持久退休意图。恢复前发布的 `*.restore-credentials-<32hex>.json` 仅含引用而非秘密，跨越数据库覆盖保存回收依据；恢复路径完成数据校验、WAL checkpoint 和文件同步后才移除。不要手工删除已生成的恢复记录或凭据以跳过恢复。物理断电、安装器回退和已发行旧客户端回退仍需独立验收。

## 当前业务范围

- 工作台、任务创建/队列/详情、持久调度、重试和删除；资料恢复目前限离线 CLI 操作，恢复 GUI 未完整验收。
- 独立结果窗口、讲话人改名、TXT/Markdown/DOCX、AI 总结运行及只读纪要。
- 模型/模板/人员编辑、Excel 导入导出、设置、运行时与诊断。
- 远端能力协商与已开放操作；连接快照绑定窗口，当前没有远端上传或总结写入。
- 桌宠、多开、商店、签到/补签、盲盒、兑换、农场及打工；工厂“需照看”可选进入装配练习，原一键照看保持可用；规则见[宠物系统](../../docs/pet-system.md)。

只读入口不提供可写服务。可写候选关闭前排空设置保存，检查资源编辑器的未保存内容和在途操作，再停止服务和释放锁。OS 强制终止不能等同于未保存表单恢复。

原生设置与主菜单复用同一主题和导航样式；组件测试、截图和平台窗口验收分别记录。历史详细验收从此 README 归档至[模块说明快照](../../docs/ai/reviews/2026-11-20-native-module-reference-snapshot.md)，不再在快速操作入口累积逐次测试日志。

## 运行时与安全候选

唯一清单是共享的 [runtime-manifest.json](../../packages/desktop-resources/runtime/runtime-manifest.json)，四个固定模型角色及其 pinned revision 和缓存发布脚本也从同一资源目录嵌入。默认生产配置已固定 Python 3.11.18、三平台依赖锁、builder、recipe 和五件 vendor 产物摘要。安装器下载固定原包，以托管 Python 的标准库模式构建 FunASR local2 修正版；逐件校验后，pip 从私有输入快照安装哈希锁依赖。普通安装无需调用者提供 sidecar，仍需联网下载其余依赖和模型。`security-candidate` 保持独立显式配置。托管安装先检查解释器完整版本，再校验依赖和模型；正常启动会对托管解释器做内容摘要缓存校验，文件变化时重新执行完整版本探针；显式 system Python 仍按系统解释器策略处理。

当前支持范围限四个固定 FunASR 模型；Transformers 5.x 的 Torch 后端在保留 Torch 2.2.2 时停用。三平台生产扫描已通过现有策略，未扩大例外；无豁免扫描仍有 Torch 已知漏洞。安全门禁及实际安装证据见[发布要求](../../docs/architecture/release-readiness.md)。

原生候选输入的显式交付流程：

```sh
pnpm native:runtime:prepare \
  --locks-directory "$PWD/packages/desktop-resources/runtime/candidates/security-candidate" \
  --vendor-directory /absolute/vendor-output \
  --output-directory /absolute/new-runtime-inputs
pnpm native:runtime:security --runtime-input-directory /absolute/new-runtime-inputs
pnpm native:runtime:candidate:acceptance --runtime-input-directory /absolute/new-runtime-inputs
pnpm native:package --target darwin-arm64 --runtime-input-directory /absolute/new-runtime-inputs
```

从仓库根目录运行。三份候选锁已在共享资源中，按清单固定文件集、大小与 SHA-256。先按固定配方构建 vendor 输出，再把 prepare 的输出指向尚不存在的目录。构建步骤见[固定 wheel 配方](../../python/funasr-runner/vendor/README.md)。

随包带输入仍默认选择生产运行时。只有显式隔离可写目录配合 `--runtime-profile security-candidate` 才启用候选；输入从确切可执行文件相邻资源中读取，在开库前校验。该组合拒绝生产资料、只读快照、恢复和版本输出。Windows x86 没有此候选运行时。候选安装仍需联网获取其余依赖和模型，不是全离线包，也不代表漏洞已经消除。

## 专项模块与检查

| 专项 | 正式说明 |
| --- | --- |
| AI 响应与持久化边界 | [ai](internal/infrastructure/ai/README.md) |
| 远端协议与字段匹配 | [remote](internal/infrastructure/remote/README.md) |
| Runner V1/V2 与子进程 | [runner](internal/infrastructure/runner/README.md) |
| 旧 JSON 导入 | [legacyjobs](internal/infrastructure/legacyjobs/README.md) |
| XLSX 文件与人员事务 | [memberxlsx](internal/infrastructure/memberxlsx/README.md) |
| 日志与诊断脱敏 | [diagnostics](internal/infrastructure/diagnostics/README.md) |

`pnpm native:compat` 校验并重放[冻结的 Rust 黄金样本](../../packages/desktop-resources/fixtures/native-v10/README.md)，覆盖 Go 查询/事务/重开、迁移、业务差分及 XLSX/DOCX。恢复 journal 与跨进程锁由 Native 自身回归验证；当前不再执行 Go/Rust 双向互操作。`pnpm native:build:matrix` 仅编译四目标。截至 2026-11-22，默认 Python 3.11.18 生产配置已在 macOS ARM 完成真实安装、四模型离线验证、Runner／host 两种说话人模式及重开持久化验收；82 项已安装依赖通过现有安全策略，未过滤扫描仍有 Torch 已知漏洞。Windows x64 和 Intel Mac 仍缺真实运行时结果。真实系统凭据、运行时、会议 GUI、远端服务和读取性能使用独立验收命令，参数与副作用统一见[开发指南](../../docs/development.md)。正式发布条件见[发布就绪检查](../../docs/architecture/release-readiness.md)。
