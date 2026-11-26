# Liberty 全项目工程与代码审查及修复（阶段记录）

文档类型：评审
状态：本批已授权的 6 项修复、3 项局部工程化调整及自动化验证已完成；全项目审查仍为范围化阶段记录，安全与实机限制未关闭
创建日期：2026-11-26
最后核实：2026-11-26（Asia/Shanghai）
适用范围：`main`，基线 `fcf73f84f4d6f005864acac3330d53d7b3f5f7a5` 与本批修复变更；Native、临时生产旧端、共享 Runner/资源、持久化与发布工程
权威边界：本记录描述已检查的调用链、故障复现、修复和验证，不代表逐行证明整个仓库没有缺陷，不替代真实 GUI、跨平台与生产切换验收。不改变现有架构、发布门槛或安全例外。
依据：当前源码、相邻测试、合成数据故障复现、[原生架构](../../architecture/native-desktop-architecture.md)、[CI/CD](../../architecture/ci-cd.md)与本轮实际执行结果。历史验收只作线索，不计为本轮实测。

## 结论与完成度纠正

首轮把“全仓清点、部分关键链路审查、现有检查通过”写成“全项目审查完成”，范围与结论不匹配，已撤回。测试通过不能证明所有代码都经过审查；未发现问题的模块也不能据此认定没有问题。

用户确认后，本批修复下列 6 项已复现问题，并实施人员导入、农场/打工分层、旧逐字稿重复扫描三项有具体依据的调整。目录骨架保留，未引入通用校验框架、额外后台清理器、扩大队列或批量重试。当前仍有安全告警与实机验收边界，不能将本批完成等同于生产准入。

## 已确认问题及最小修复

| 编号 | 级别 | 触发与证据 | 本批处理 |
| --- | --- | --- | --- |
| R1 | P1 | B 正在录音，同时导出历史 A；发布回调阻塞约 3.207 秒时，音频队列积压导致 B 进入 `failed/live_audio_backpressure` | 锁内冻结历史内容，锁外执行文件发布；复用已有 operation 生命周期保证关闭仍等待导出结束 |
| R2 | P2 | 已有默认 A 排在编辑中的 B 前时，明确把 B 设为默认仍被旧排序覆盖；正常操作即可确定性触发 | 两端都优先采用当前提交的、已启用且明确指定的默认模型，再执行原回退规则 |
| R3 | P2 | 旧端 Keep 凭据、元数据写失败，旧凭据引用仍能读到时，保存可能误报成功；合成 CHECK 约束故障复现 | 回读完整目标元数据及凭据引用，只有一致才判定提交已完成；保留暂存凭据回滚与补偿 |
| R4 | P2 | 已转写完成的任务正在生成摘要，此时删除整个任务，数据库行消失但 Provider context 未取消 | 删除持久化 tombstone 建立后，调用现有摘要 worker 的取消能力；与 Start 共用既有锁 |
| R5 | P2 | Word 发布期间持有整个窗口集合的锁，其他窗口授权/打开被阻塞；服务层已复现 | 复用单 grant mutation 锁，缩短集合锁范围；关闭和替换仍等待该 grant 已接受的发布 |
| R6 | P2 | 成长配饰卸下后读取商店，旧解锁记录重新将其装备；真实临时库持久化复现，无需并发 | 两端导入旧解锁记录时，以已存在的库存装备状态为准；首次导入保留旧初始选择 |

上述可能性依据触发条件区分：R2、R6 在指定正常操作顺序下稳定发生；R1、R5 依赖慢发布与操作重叠；R3 依赖可回读旧记录的写失败；R4 依赖摘要请求与删除重叠。没有遥测或生产样本，不能给出可信百分比。R5 的服务锁阻塞是事实，真实窗口停顿由同步 UI 调用链推断，本轮未实机复现。

### 修复路径与行为回归

- R1：[live_export.go](../../../apps/native/internal/application/live_export.go) 只调整快照与发布的锁边界。`TestHistoryExportPublicationKeepsNewRecordingResponsive` 在历史发布仍被阻塞时输入超过队列容量的 35 帧，验证识别全部接收、新录音可停止且正常完成；原有字幕内容测试继续保留。
- R2：[Native 资源服务](../../../apps/native/internal/application/resources.go) 与[旧端 store](../../../apps/desktop/src/features/ai/stores/useAiStore.ts)同步优先级。`TestExplicitDefaultModelWinsRegardlessOfExistingOrder` 与新增 store 测试覆盖两种排序、保存结果和唯一默认值，Native 还覆盖禁用回退。
- R3：[保存用例](../../../apps/desktop/src-tauri/src/application/save_ai_model.rs) 使用[仓储回读](../../../apps/desktop/src-tauri/src/infrastructure/repositories/ai_models.rs)。新增测试确认 Keep 写失败必须上报，并逐字段验证错误元数据不能被“相同引用”掩盖；不把故障注入发生率当作实际用户发生率。
- R4：[删除编排](../../../apps/native/internal/application/deletion.go)、[摘要取消](../../../apps/native/internal/application/summary_ports.go)和 host 装配连接一个窄取消端口。`TestWholeJobDeletionCancelsAcceptedSummaryBeforeServiceClose` 在全局关闭前断言 context 已取消；`TestJobDeletionFencesSummaryAcceptanceAlreadyInProgress` 覆盖 Start 已进入、tombstone 先落盘的交错，确认不调用 Provider，也不能重新发布已删除结果。
- R5：[word_export.go](../../../apps/native/internal/application/word_export.go) 的 `TestWordPublicationLeavesOtherWindowGrantsResponsive` 验证慢发布时子窗口仍可读/开/关，原 grant 关闭必须等待，撤销后不再调用 publisher。首次整套检查发现伪造 grant 会在授权前等待的修复遗漏，已补回锁前授权检查，并保留锁后的撤销检查；完整权限矩阵与 application race 已通过。
- R6：[Go 库存导入](../../../apps/native/internal/infrastructure/repositories/jobdb/pet_repository.go)与[Rust 库存导入](../../../apps/desktop/src-tauri/src/infrastructure/repositories/pet_store.rs)使用已有库存的 `equipped` 值，不改通用库存 upsert、数量、奖励和成长规则。独立回归覆盖真实成长解锁、卸下、替换、刷新、关闭重开及装备唯一性。

R6 与旧冻结黄金样本有明确业务差异。归档和校验清单不变；兼容测试从 `funded/use-over-99` 的商店读取开始，仅转换 `clover-bow` 的装备标志、商品状态与配饰槽投影，其余字段仍严格核对。依据、规则和边界已写入[样本说明](../../../packages/desktop-resources/fixtures/native-v10/README.md)及[宠物系统](../../pet-system.md)，没有通过刷新金样掩盖行为变化。

## 工程化与复杂度

### 人员导入：一次业务校验

原 Excel → application → repository 三处重复批次校验，保存每条实体时还会再检查。现在 Excel 模块仅负责文件/XML/单元格格式，应用入口赋予内部身份与时间后，通过 `domain.NewMemberImport` 一次校验姓名、重复和记录人数。内部以私有存储的 `domain.MemberImport` 流转，仓储不重新校验同一批业务条件；读取批次返回副本，避免 adapter 修改已经校验的数据。

单条 SaveMember 是独立输入入口，保留实体校验；授权、取消、数据库约束与会变化的状态仍保留。文件适配将业务拒绝映射为原工作簿错误，保持原 UI 提示契约。持久化回归继续验证无效导入、事务中途故障、重试及反复重开后原数据不变。详见[人员文件边界](../../../apps/native/internal/infrastructure/memberxlsx/README.md)。

### 农场/打工：仅移动纯规则

原 `jobdb/work.go` 内不依赖 SQL 的阶段时间、进度、奖励、目录查询与汇总移至 [domain/work_rules.go](../../../apps/native/internal/domain/work_rules.go)。事务、SQL 和账本留在 repository。用户可区分的错误使用明确类型，View 不再按完整中文句子识别错误再翻译。

删除只转发参数的 `cropRewards`、`jobRewards`、`errorsText`，调用处就近表达。没有改奖励算法、周期、抽取、幂等或新增 service/manager/strategy。原周期、回滚与冻结兼容回放继续约束行为。

### 旧逐字稿：按已发布快照复用判定

`Result.DisplaySegments` 和 `CanRenameSpeaker` 对 `legacy_unverified` 数据有逐段一致性扫描；此前在每次重绘、每个可见行重复执行。当前 [ResultView](../../../apps/native/internal/desktop/result.go) 将显示来源与编辑资格绑定到结果快照，沿用原过滤缓存，不增加全局缓存，也不改变旧数据权威规则。

已核实 `ResultController` 发布新快照而不修改已发布逐字稿切片。两处旧 UI 测试改为按实际控制器语义替换快照，保留原“不匹配必须回退”“降级不可编辑”断言；新增回归覆盖相同任务 ID 下的匹配、失配和恢复，避免缓存陈旧结果。

## 性能测量

本轮使用固定工具链、Apple M1、MyGo UI 测试器与合成 10000 段逐字稿；不含实际窗口/GPU 合成、真实媒体或用户资料。原结果组件通过仓库外 overlay 作基线，随后串行运行当前实现，各子项三次、500 ms。背景进程仍造成普通场景波动，不将开发机结果当作 p95 或产品验收。

| 场景 | 基线均值范围 | 当前均值范围 | 每次分配 |
| --- | --- | --- | --- |
| 普通工作台一帧 | 0.220–0.509 ms | 0.253–0.391 ms | 约 13.2 KB，两侧相同 |
| 普通搜索稳态一帧 | 0.211–0.273 ms | 0.207–0.278 ms | 约 13.2 KB，两侧相同 |
| 旧说话人回退搜索稳态一帧 | 0.690–0.741 ms | 0.222–0.255 ms | 约 12.2 KB，两侧近似相同 |

这些数据支持消除已定位的旧数据重复扫描，不支持宣称全应用性能提升相同比例。另一次当前实现完整基准中，仅替换结果包装的 10000 段刷新为 0.190–0.199 ms；包装变化会重新核对旧投影，未放宽一致性判断。

前轮数据库合成基准观察到分页和 revision 探测减少读取/分配，本批没有改这些路径，也没有把前轮结果当作修复后重新测量。冷启动、整应用空闲 CPU/RSS、真实长时 ASR、GPU/输入延迟、低内存与跨设备性能仍未在本轮验证。

## 安全与资源治理

### 生产 Python 例外仍是明确限制

本轮以固定 OSV Scanner 2.5.0 执行 `pnpm native:security:check`，扫描 root pnpm 锁、Go 模块、Python 开发锁及三个生产运行时的固定锁、构建输入与上游身份。扫描覆盖验证完整：三个目标在 current-policy 模式均退出 0，unfiltered 模式均退出 1；未过滤输出均涉及 Torch 2.2.2。扫描记录明确 `productionClearance=false`。

[现有配置](../../../python/funasr-runner/osv-scanner.toml)的 20 条例外标识均于 **2026-11-30** 到期。标识可能互为别名，既不能说有 20 个独立漏洞，也不能把策略通过写成没有漏洞。本批没有延长期限或降低门槛。固定运行时替代涉及 Intel、Windows、许可证及模型质量等条件；此前的候选研究不能替代本轮完整准入。

`pnpm native:licenses:check` 通过，收集 Go 工具链及 17 个固定模块的 52 项 notices；这不替代 Python 替代运行时的完整许可证裁定。

### Rust 补充锁扫描需按实际依赖图解释

无过滤的 `osv-scanner scan -L Cargo.lock --format=json` 输出在 16 个锁内包下包含告警，混合漏洞、维护状态以及不同平台/可选依赖；条目与别名不等于独立且可达的生产漏洞数。

已核对 macOS ARM64 实际依赖图：`quinn-proto`、`chrono` 未进入当前启用图；`tokio` 通过 reqwest/Tauri 进入；`sevenz-rust2` 是声明依赖，但当前运行时入口拒绝 `.7z` 且未调用其解压 API。仅凭这些不能证明其他调用不可达，也不能覆盖 Intel/Windows。

固定 cargo-deny 0.20.2 与完整 `pnpm security:check` 已通过当前目标图、advisories/bans/sources 及后续扫描。与额外 OSV 输出的差异已有部分明确解释：目标图过滤掉非目标/未启用依赖；该版本工具的 `unsound` 默认范围为 workspace 直接依赖，传递依赖 Tokio 的相关条目不在默认范围；sevenz 的 GitHub Advisory 条目未出现在本轮 RustSec 数据库对应目录。RustSec 库已核对为当日更新，不将差异简单归因为旧缓存。现有策略通过不能消除补充告警，是否需要扩大扫描范围、清理未使用依赖或定向升级仍需按可达 API 与目标平台分诊；本批未机械升级依赖或自动增加豁免。

### 资产大小不直接代表应用占用

前轮已跟踪工作树约 409.4 MiB，其中宠物模型目录约 232.02 MiB，主要为制作候选与编辑源。它们不是 Git 传输量、发行包大小或 RSS。当前可选预览和生产资产边界有明确来源；没有测量 CI checkout/缓存成本就迁 LFS、删除候选或重写历史的依据。本批保留资产。

## 审查覆盖与剩余未知

| 维度 | 已核对的代码/证据 | 边界 |
| --- | --- | --- |
| 目录与依赖 | 非生成 Go 内部依赖、domain/application 分层、desktop/client 外部副作用引用；本批纯规则与导入端口 | 未把文件清单计为逐行审查，不按大小批量拆文件 |
| 任务与生命周期 | submission、execution、scheduler、deletion、recovery、summary、windows、host 装配；lease/tombstone/取消/关闭回归 | 合成 provider/runner，不是实际服务故障或断电验收 |
| 数据与恢复 | schema 顺序迁移、backup/restore、profile transfer、凭据 journal、真实临时库与冻结跨语言回放 | 本轮未执行真实系统 Keychain、Windows Credential Manager 或用户资料迁移 |
| 运行时与边界 | managed 下载/哈希/归档预检，Runner JSONL/进程树/有界输出，AI/remote 目标解析、固定解析地址与有界 HTTP | 现有安全回归通过不构成渗透测试；未增加无证据的保护层 |
| 原生 UI/client | 会议/实时/资源/设置/摘要/纪要、pet/controller、桌宠指针/窗口生命周期和结果缓存；测试器的焦点/键盘/虚拟列表 | 真实窗口、读屏、IME、触屏/GPU 和所有菜单交互未全量人工验收 |
| 宠物与玩法 | 成长、兑换签名、商店/签到/盲盒/礼盒事务，农场/岗位结算、装配练习 | R6 为确认缺陷；不声称已证明全部奖励状态组合 |
| 旧端与权限 | 模型保存及 UI、摘要/结果/Excel/导出、window_scope/webview_policy；90 个 command 注册与 build 清单一致，8 个 capability 的自定义权限均有对应命令 | 静态完整性不等于每个 command 语义安全；Tauri 权限不用于 Go 窗口 |
| 发布工程 | 版本、锁文件、生成工作流、Release ID/来源/资产绑定、正文来自目标 CHANGELOG、失败阻断自测 | 本轮没有创建 tag、修改线上 Release、上传资产或验证线上下载；没有关闭 quality/security gate |

## 本批验证记录

所有故障与业务回归均使用合成数据、临时库、测试凭据或受控 adapter，没有读取用户会议/凭据，没有启动旧客户端。

| 检查 | 本批结果 |
| --- | --- |
| `pnpm check` | 最终通过。含 Native 生成源/格式/vet/测试，Python lint 与 135 项测试，TS 类型及 16 文件/49 项测试，Rust fmt/292 通过/18 原标记忽略，web 构建、Clippy `-D warnings`、发布策略及 76 项 CI 自测 |
| application/client 全量 `go test -race ... -count=1 -timeout=5m` | 通过；覆盖本批应用服务变更与完整窗口权限矩阵；Rust 与库存另由相邻测试及兼容回放验证 |
| 删除、成长与相关 jobdb 定向 race | 通过；实际取消/拒绝与持久化断言成立 |
| `pnpm native:compat` | 通过；含 schema、jobdb、资料锁、子进程/host、Excel、Word 的现有兼容与 race 路径；jobdb 约 1233 秒。未把父进程跳过的专用子进程入口声称为独立实机验收 |
| `pnpm native:build:matrix` | 最终三目标通过。主程序与兼容测试包均编译，并核验 ARM64/x64 Mach-O 与 x64 PE 格式；不是目标平台执行 |
| `pnpm native:security:check` | 通过现有例外策略；三个生产目标 unfiltered 仍有发现，详见上文 |
| `pnpm native:licenses:check` | 通过当前原生许可清单检查 |
| UI 相邻测试与基准 | 最终完整 UI 测试与快照匹配/失配回归通过；两处旧夹具按控制器快照语义修正，原断言保留；当前与基线对照基准通过 |
| `pnpm security:check` / Rust 补充 OSV 锁扫描 | 完整供应链脚本按当前策略通过；额外无过滤 Cargo 锁扫描有告警，不能与按目标图和工具默认范围过滤的 cargo-deny 等同 |
| 文档/差分 | 正式规则、样本说明和中英文 Unreleased 已同步；`pnpm ci:docs`、新增报告相对链接/围栏及 `git diff --check` 通过 |

首次整套检查曾因 Word 伪造 grant 等待与旧 UI 原地修改夹具失败；已定位并修正，没有跳过原有行为断言，也没有将失败轮次计为通过。Rust 构建出现外置卷增量缓存硬链接回退复制的环境警告，未通过降低 Clippy 门槛处理。

## 本批交付边界

上述验证针对本批修复；最终整套检查通过后复核的 52 个源码指纹没有变化。随后获授权将修复与保留的设置、运行时、侧栏和界面改动分批提交，纳入 `2.0.0-alpha.3` 发布准备，并同步版本和双语变更记录。其余界面改动不计入上述 6 项修复。本记录不充当线上交付凭证；提交、标签、Release ID、公开状态和资产以 [alpha.3 Release](https://github.com/whalesky-labs/Liberty/releases/tag/v2.0.0-alpha.3) 及随包发行清单为准。需要继续验证的高影响事项是生产运行时例外到期处置、补充 Rust 告警的 API 可达性和政策覆盖，以及真实三平台 GUI/ASR/凭据与安装升级回退。不能从本批代码修复和本地检查推导出 Native 已满足生产入口切换条件。
