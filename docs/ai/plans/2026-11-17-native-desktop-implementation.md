# Liberty 原生重构实施与验收记录

文档类型：计划（含阶段验收快照）<br>
状态：已确认，执行中；完整重构未完成<br>
创建日期：2026-11-17<br>
最后核实：2026-11-22（默认生产运行时安全整改与真实安装验收、独立资料导入与中断补偿、原生分发实现；完整会议 GUI 与生产切换尚未完成。其他阶段证据日期见各节）<br>
适用范围：Liberty 1.1.24，`codex/native-architecture`，Go/MyGo 原生迁移<br>
权威边界：记录已实现能力及验证边界，不代表全部业务等价、全部 UI 相同或具备发布条件。当前行为分别以两端源码、共享契约、运行时清单和正式架构为准；有意修正的旧端缺陷不作为新端兼容要求。<br>
依据：[已确认方案](../designs/2026-11-16-native-desktop-refactor.md)、当前工作区代码、Rust 生成的合成库、Go 测试与本机运行记录。

> 退役决策说明（2026-11-21）：下文较早批次中“保留旧端”的表述记录的是当时切换门槛尚未关闭的状态，不代表长期兼容承诺。当前终态见[旧 Tauri 退役决策收口](#2026-11-21-旧-tauri-退役决策收口)：切换条件通过后，在同一退役变更中删除旧 React/Tauri 客户端和发布链。

> 文档整理说明（2026-11-20）：本文件按批次保留证据，PID、候选包路径、测试数量和“当前”均限各条记录的核验时点，不代表持续运行状态。日常操作请从[开发指南](../../development.md)与[原生模块说明](../../../apps/native/README.md)进入；长期约束见[原生架构](../../architecture/native-desktop-architecture.md)，剩余发布门槛见[发布要求](../../architecture/release-readiness.md)。

## 2026-11-22 收尾执行与验收边界

本节覆盖下方较早批次的当前状态；旧扫描、测试数量及未提交说明仅代表其原核实日期。工程检查与真实交付验收分别记录，不能把本机测试、候选归档或既有漏洞例外解释为生产切换完成。

- 默认生产运行时已切至 `2026.11.22-python311-security.1`、Python 3.11.18 和可复现 FunASR `1.3.9+liberty.local2`。共享清单绑定 builder、recipe、三份平台锁和五件 vendor 产物；Go 原生及临时 Rust 生产端都校验固定上游并从私有快照按哈希安装，不采用未验证回退。metadata 修正版去除无条件 OSS2 依赖，保留上游身份与摘要。
- 三份真实生产锁均通过现有安全策略，覆盖完整；无例外扫描每平台仍有 Torch 2.2.2 的 20 组／24 条 OSV 记录，计数重叠。既有例外未扩大，到期日仍为 2026-11-30。Transformers 5.x 的 Torch 后端停用，支持范围只包含四个固定 FunASR 模型。最新 Native SBOM 实际生成为 135 个组件：17 Go、23 npm、95 PyPI，保留修正版与上游身份及依赖关系。
- 默认生产运行时真实验收通过：新默认配置实际安装、四模型离线验证、Runner 与 host 的两种说话人模式转写、关闭重开后的结果和奖励持久化。82 项安装库存和三平台生产锁的当前策略扫描均通过，无例外扫描仍有前述 Torch 发现。host 验收资料与运行时处于同一隔离作用域；曾因夹具跨目录借用运行时而被拒绝，现修正夹具，未放宽生产绑定。证据为忽略目录中的 `production-installer-acceptance-final-20261122.log` 和 `runtime-security/scan-2FLl1S/`。这不代表 GUI、识别准确率或其他平台实跑已验收。
- 生产资料入口已收口为只读；离线导入复制包含 WAL 的同一 SQLite 读事务快照，并为 Native 分配独立凭据引用。拒绝活跃任务、删除、总结和运行时操作，不接管源进程或源清理意图；`idle`、`failed` 可导入，未知运行时状态拒绝。已发布目标重试不覆盖 Native 后续修改。独立审查发现的 ready 未发布时补偿状态不可重读、失败终态误判活跃两项缺陷均先复现再修复。最终 profiletransfer/schema 全包 race 通过，分别耗时 9.536／27.968 秒。
- 分发脚本已具备 macOS DMG 的签名、公证、Gatekeeper 与嵌入应用核验，以及 Windows 每用户 MSI 的升级、失败回滚、降级拒绝、卸载保留资料与旧安装器恢复流程。66 项分发自测通过；固定 WiX 下载已核验摘要。实际签名、公证、MSI 编译及真实安装仍缺证书或目标系统，不能据此宣称安装器已交付。候选身份和 `productionReady:false` 保持，正式切换仍要求同一变更删除旧端与专属发布链。

最终整仓检查、正式源码追踪、真实提交干净检出和 GitHub CI 正在收尾；完成证据须绑定实际提交。当前仍缺完整原生 GUI／IME／读屏及桌宠试玩、专用外部远端服务、Intel／Windows 目标运行、8 GiB 设备与人工标注受控 ASR、证书签名安装及生产观察。没有凭这些检查跳过切换门槛，也没有宣称旧客户端已经退役。

## 2026-11-21 工程收尾：独立 Native 质量与候选交付

- `pnpm native:verify` 已成为不读取旧 UI 或 Rust 的完整本地质量入口，串联 Native 生成物/Go 检查、Python lint/测试和 `native:release:check`。根 `pnpm check` 先执行它，再执行显式的 `legacy:check`；临时生产端的既有质量与安全检查没有被削弱。Native 的长期事实源位于共享资源和原生目录，兼容入口重放 175 个经过 SHA-256 校验的历史合成黄金文件。
- 新增独立 `build-native.yml`，供手动调用和根质量工作流复用。固定工具链与 Action SHA、按确切源码提交检出、仅有仓库读取权限；配置完整兼容、race、四目标编译、许可证/SBOM、四平台目标测试/系统凭据验收和候选包核验。独立真实安全扫描失败仍使工作流失败；候选产物可保留供检查，不获得生产安全放行。工作流未在本轮实际提交至 GitHub 执行，也不提供 Release、标签、签名或公证入口。
- 候选验证器检查精确文件集合、manifest、二进制架构、归档内容及所有摘要，拒绝脏 Native 构建输入和软链接输出祖先。默认 CLI 核对实际 HEAD，合成单测注入值不能作为真实提交归因。策略/平台/安全脚本的 86 项 Node 测试通过；另有 25 项具名 Go 安全回归逐项通过，缺失或跳过不能充当通过。
- `native:security:check`、`native:licenses:check` 和 `native:sbom:generate` 已完全独立于 Cargo。真实许可证归集通过，收集 Go 工具链及 17 个锁定模块的 50 份通知；Native SBOM 为 17 Go、23 Node、83 Python，共 123 项锁文件库存，不包含旧端 importer/Rust、解释器、FFmpeg、模型或安全候选运行时输入，也不冒充已安装库存。
- 本轮根 `pnpm check` 完整退出 0，包含 Native 验证、47 项前端测试、105 项 Python 测试、280 项 Rust 测试及 18 项约定忽略、Web 构建、fmt、Clippy 和发布/阻断夹具检查。普通 Go 检查部分使用缓存。独立发布策略与资产自测也通过；外置盘硬链接回退和既有 Web chunk 提示仍存在。
- 重新安装依赖的隔离验证暴露 macOS SIGKILL 测试的就绪竞态：fixture 的结果文件创建后尚未写完，子进程即可发布可崩溃标记。现要求完整结果通过真实 Runner 协议解析，父测试在 SIGKILL 前再核对旧结果字节；保留原有孤儿进程终止、过期 lease 拒绝、精确事件数、旧文件保留及重开持久化断言。修正后连续 20 次普通执行和 5 次 race 均通过；Darwin ARM/Intel 的 host 测试包补编译及 Mach-O 架构核对通过，不代表 Intel 实机执行。未修改生产恢复逻辑。
- 最终隔离源快照含 1,174 个允许输入，排除 `apps/desktop`、Cargo、项目 `.git`、原 Node 安装和原 Python 虚拟环境；重新进行固定 pnpm 冻结安装与 Python 哈希锁安装，完整 `native:verify`、真实 Native 许可证归集及 123 项 SBOM 生成全部退出 0，逐文件确认输入和锁未变化。Node 解析范围限制在独立安装中，Go 使用已缓存的固定工具链与模块。前一次隔离脚本遗漏共享 ASR 配置，已补入；ASR 拒绝夹具需要 Git 元数据，故只在临时目录创建未跟踪源码的空合成提交，不把它当成项目 HEAD 或干净检出证据。临时源目录已清理，原始失败与最终结果保留在忽略的 `target/native/closeout-isolation-*/` 和 `closeout-20261122/`。
- 真实 `native:security:check` 仍退出 1：本次 Node 468 项与 Go 18 项扫描没有命中，首个生产 Python 锁涉及 `filelock`、`pillow`、`transformers` 三个包的 54 组未豁免漏洞，其中 6 组 Critical。入口在首个失败锁停止，本轮不声称重新扫描了全部生产锁；另外两平台的独立失败证据保留在前述批次。未扩大例外或替换生产运行时。

完整会议 GUI、全页面 IME/读屏与 P5-G1 实机体验、真实远端和受控 ASR/8 GiB 设备、Intel/Windows 实机、安装升级/回退及签名公证、生产观察和项目干净检出重建仍未验收。当前工作树中的 Native/共享资源尚未纳入正式版本控制；本轮没有暂存、提交、推送、发布、接管生产资料、切换生产发布入口或删除旧客户端。旧端的临时生产地位由这些切换条件限制，不作为长期兼容承诺。

## 2026-11-21 原生重试、资料恢复与候选验收

- 本地任务在队列和详情页重试前共用运行时预检。运行时未就绪时提示用户，远端任务按行绑定的来源和能力提交。从设置返回队列会恢复处理中、已完成或失败筛选。定向 race 通过，覆盖中英文告警、同 ID 的来源隔离、窗口能力和连接变更后的授权撤销。迁移基线为 25 条路由、90 个 command、8 项旧端窗口 capability、schema v10。
- CLI 恢复测试确认有进程持锁时拒绝恢复。释放锁后恢复接管前快照，接管后新增的合成人员可从恢复前安全副本找回。定向 race 通过。独立系统凭据验收通过写入回读、轮换、恢复和延后回收。远端验收脚本的回环测试确认只访问读路由，真实服务所需的三个测试参数未配置。
- 固定时钟跨日测试经原生组件、控制器、用例和 SQLite 比较直接照看、稳妥单与一次错配的急单。超时或退出暂停局保持岗位和钱包状态。成功只推进一个阶段，领取后重开仍只有一条奖励和一条 LP 流水，重复领取被拒绝。相关竞态检查通过。
- Apple M1 上读取基准使用同一份 Rust 合成库。队列 Rust P50/P95 为 8.534/19.287 ms，Go 为 4.908/8.032 ms。逐字稿 Rust 为 11.479/21.112 ms，Go 为 10.366/18.602 ms。装配领域三轮为 14.15–45.43 ns/op、零分配，无窗口活动帧为 4.35–6.52 ms/op、每帧 2–3 次分配。测量期间还有验证任务并行，不同批次的读取比较也曾反转。整机性能与实际输入到画面反馈的时延仍需独立测量。
- 共享资源收纳三平台安全候选哈希锁，并对这三份锁固定 LF。`native:check` 检查名称、大小、SHA-256 和锁格式。按固定上游 wheel 重建的五件 vendor 产物和三份锁全部匹配清单。三平台候选按现有例外过滤后扫描通过，未过滤扫描仍有发现。生产扫描继续阻断，每份 Python 锁有 54 组未豁免发现、共计 75 条 OSV 记录，平台计数重叠。Python 例外到期日为 2026-11-30。详见[发布要求](../../architecture/release-readiness.md)。
- 首次会议验收在 FFmpeg 步骤拒绝了旧版就绪标记。生产运行时与会议验收现在按目标、运行时版本、模型集版本与模型 profile 共享新缓存。缓存键回归通过。操作流程见[开发指南](../../development.md)。

### 同日最终复核：冻结样本、检查入口与产物

- 当前 `native:compat`、preview 和读取 benchmark 已改用共享的[冻结黄金样本](../../../packages/desktop-resources/fixtures/native-v10/README.md)，不再启动 Rust oracle。175 个合成文件由压缩包和逐文件 SHA-256 校验。旧 inventory 脚本、基线和专用测试已移除；上文 25 路由、90 command 和 8 capability 是历史迁移基线，不能视为当前检查持续验证的计数。原有 Go/Rust 双向 journal、锁互斥和 Rust 回读 Go XLSX 的通过记录也仅限原流程。
- 本轮检查先暴露两处新测试入口问题：ES 模块中的八进制 NUL 转义无法解析；无 tag 的恢复测试引用了只在 `compatibility` tag 下定义的快照辅助函数。分别改为等值合法转义、将辅助函数原样移入无 tag 的共享测试文件；没有改变生产 Runner 或迁移逻辑。最终 `GOMAXPROCS=2 node scripts/run-native.mjs check` 通过 61 项 Node 测试、生成物漂移、格式、模块校验、vet 和 26 个 Go 包测试，部分 Go 结果使用缓存。
- 独立审查继续发现编辑器几何测试仍读取旧 `windows.ts`。核实三类历史尺寸后将其固定在现有测试中，保留双语、新建/编辑、四项几何及标题断言，移除对旧源文件的读取；相邻普通/race 和四目标 desktop 测试包编译通过。随后 `GOMAXPROCS=2 pnpm native:check` 再次完整通过。
- 无旧端的隔离源快照含 1,139 个 allowlist 文件，未包含 `apps/desktop`、Cargo、`.git` 或 `node_modules`。Node 从祖先目录解析现有固定依赖，Go 使用已缓存的固定工具链与模块。完成九生成器的检查、删除输出后重建及再检查，283 个输出 SHA-256 一致；61 项 Node 测试、几何回归、契约/展示包以及本机 ARM CLI 构建和版本输出通过。该结果证明所测源快照的生成与最小构建独立性，不替代 Git 干净检出、完整测试或平台实机。
- 恢复固定 `pnpm 10.30.3` 的可用入口后，`GOMAXPROCS=2 pnpm check` 完整退出 0：TypeScript 47 项、Python 105 项、Rust 280 项通过及 18 项约定忽略，Web 构建、fmt、Clippy 和静态发布/阻断夹具检查通过。独立发布策略及资产自测也通过。外置盘增量缓存的硬链接回退和既有 Web 大 chunk 提示仍存在；没有删除或重装 `node_modules`，也不把静态安全基线当成真实漏洞扫描通过。
- 新样本入口的最小重放通过 28 个顶层测试，涵盖文本/纪要/AI、旧库读取、v0–v10 迁移、Native 恢复 journal 与跨进程锁、XLSX 和 18 个 DOCX 案例。schema、profile、XLSX 和 DOCX 定向启用 race。该最小重放未覆盖完整经济差分；较早动态兼容与当前冻结样本重放应分别理解。
- 随后 `GOMAXPROCS=2 pnpm native:compat` 完整退出 0，冻结样本下的宠物核心、商店、盲盒、签到补签、兑换、农场与打工全动作差分及失败回滚/重开通过，jobdb 包耗时 774.480 秒；执行服务、host、XLSX 和 DOCX 也通过。日志发现 profile 筛选仍指向已删除的 Rust 用例，故将筛选替换为现有 `TestLock`，再按同一参数定向 race 补跑资料锁通过。子进程 helper 顶层按设计跳过，父测试实际启动并核验互斥；筛选前后比对确认仅增加三项 Native profile 函数，其他包所选测试未变，没有重复执行整套经济差分。修改后的入口相关 43 项 Node 测试和语法检查通过。
- 四目标完整编译于 22:48–22:54（UTC+8）通过，独立核对 4 个主程序和 104 个测试可执行文件的 Mach-O/PE 架构、Go 1.27.4、目标设置及生成时间。快照辅助函数随后迁移，再定向补编译四目标 schema 测试包通过。此处均为编译与产物检查，不是其他平台实跑。
- 最新含安全候选输入的 ARM 包为 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-security-runtime-yn9pR9/`，归档 SHA-256 为 `baad2f1b0d0ea21dc4f563395facf9163f62b396bf849f7cad0b959e4e834de7`。14 项文件及 8 件输入摘要独立复核一致，包含最新装配练习反馈；215 项产品 Go/依赖输入与打包时一致。包为 1.1.24、候选身份、默认 `production`、可选 `.2` 安全运行时、`productionReady:false`，只有可执行文件 ad hoc 签名，没有分发签名或公证。
- 同日生产运行时与 `.2` 安全候选的真实 Runner/host 两种说话人模式均通过，结果和奖励在重开后保持；候选的 82 项安装库存通过精确锁和当前策略扫描。最新包再次完成三平台当前策略及未过滤扫描，覆盖完整；当前策略通过、未过滤均有发现，生产安全门禁未解除。这些不构成多说话人准确率或 Intel/Windows 实机证据。
- 会议 GUI 验收未通过。一次会话中断后原进程已结束，重新建立的隔离窗口因原生界面控制工具不再可用而受控结束，尚未创建任务，测试以未满足完成任务断言退出 1。该结果既不是 GUI 通过证据，也不足以认定会议功能故障。验收临时凭据已清理，未读取生产资料；完整 GUI/IME/读屏、P5-G1、真实远端、受控 ASR/8 GiB 设备、生产依赖整改、Intel/Windows 实机、安装升级回退及签名公证仍须完成。
- 本地时点证据保存在忽略的 `target/native/`：`current-plan-native-check-independent.log`、`current-plan-workspace-check-resumed.log`、`current-plan-native-compat-frozen-final.log`、`current-plan-native-compat-lock-final.log`、`current-plan-native-compat-selection.json`、`current-plan-native-golden-resumed.log`、`current-plan-build-matrix-resumed.log`、`schema-snapshot-compile.WCsorPYd/summary.json`、`native-source-isolation-utzfmn2i/`、`candidate-package-assembly.RnMiBmDm/summary.json`、`runtime-security/scan-OeOlW5/summary.json` 与 `current-plan-meeting-resumed.log`。这些本地日志不是可从干净检出获得的长期夹具。Native/共享资源仍需纳入正式版本控制并完成干净检出重建；本轮未提交、推送、发布、切换生产入口或删除旧客户端。

## 2026-11-21 接续整改：排队保存的生效模式与真实安全复扫

- 复核上一会话已落盘的 Dashboard 来源绑定和设置失败重试实现；进一步复现“第一笔模式切换已成功保存，第二笔排队保存未完成或失败时，主窗口仍保留旧模式”的边界。修复前新增回归的 8 个中英文、本地↔远端、后续线程修改/反向模式修改场景均失败。
- `SettingsController.OnSaved` 现在逐笔通知成功持久化的快照，工作区立即更新生效模式及读取代次；`SaveThen`/`Flush` 仍等待整批草稿保存成功。后续失败保留草稿供重试，不撤回已提交模式，也不在手动重试时重放此前失败动作的回调。长期约束同步至[原生架构](../../architecture/native-desktop-architecture.md)。
- 修复后定向测试、设置与模式相关 `go test -race ./internal/client ./internal/desktop -run 'Settings|ProcessingMode|SavedSettings|DashboardActivity' -count=1 -timeout=900s`、`pnpm native:check` 和 `pnpm check` 均通过。Go 检查使用 `GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly`。这是控制器及 headless UI 验证，本批未重建、重启或目视验收原生候选。
- 初次 `pnpm security:check` 因缺少扫描器退出 2；随后在仓库外临时目录取得并校验固定版本工具，重跑后 Rust advisories/bans/sources、pnpm 和 Go 依赖扫描通过，门禁在首个生产 Python 锁退出 1。独立复扫另两份生产锁也退出 1，三平台各有 54 组未被现有例外过滤的漏洞，涉及 `filelock`、`pillow`、`transformers`；平台计数不能相加。未修改生产依赖锁或例外，最新结论同步至[发布要求](../../architecture/release-readiness.md)。本批未执行兼容全量、四目标编译、目标机安装、签名公证、提交或发布。

## 2026-11-21 新版入口收口与切换回归补充

- 根 `pnpm start` 和 `pnpm native:launch` 继续只选择已校验的 Go/MyGo 候选；本轮实际执行 `pnpm native:launch` 后激活候选包 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-j2CEtw/`，复核到唯一 `liberty-native` 进程 PID `15402`。本次未启动旧 React/Tauri、未解析通用 `Liberty` 应用名、未访问生产资料库。
- 移除根级易误用的旧端 GUI 别名 `desktop:dev`、`desktop:dev:web`、`desktop:tauri`；旧端需要显式启动时使用 `desktop:compat:*` 命名。旧端测试、类型检查、Web 构建、Rust workspace、正式 Tauri 发布、迁移/兼容夹具和安全门禁仍保留，避免把“日常启动入口切换”误记为“旧端已退役”。
- 新增 `apps/native/internal/client/switchability_audit_test.go` 与 `apps/native/internal/desktop/switchability_audit_test.go`，覆盖远端检查的检查中/不可用/重试/可用、Dashboard 刷新失败保留上次快照、工作区本地↔远端状态投影、设置返回应用及模式提交后的可见状态。新增测试只补充回归证据，不改变生产行为。
- `node --test scripts/test-native-launch.mjs` 通过 27 项；`go test -race ./internal/client ./internal/desktop -count=1` 通过。当前 Native/共享资源仍未进入 Git 索引，且 Python 生产锁安全门禁、Intel/Windows 实机、全量 UI/IME/读屏、真实远端、安装升级回退、签名公证仍未关闭，因此没有删除 `apps/desktop`、切换正式发布入口或发布候选。

## 2026-11-21 共享资源迁出旧端

- 三条并行任务迁出双语文案与宠物来源标签、Word 模板以及农场/打工的 96 个地图素材；主线程迁出运行时清单、四个模型版本和缓存发布脚本。两端读取同一套[共享源](../../../packages/desktop-resources/README.md)，不保留旧端的并行源副本。模型版本、运行时清单摘要、Go 嵌入生成物和 Word 模板字节均未变化。
- 运行时依赖门禁改为解析共享模型清单，检查四个顺序固定的角色、合法模型 ID 和四个不同的 40 位提交哈希。分支名、重复版本和遗漏角色有拒绝回归。运行时平台、锁定依赖和安全策略未调整。兼容基线复核 97 个搬迁资源指纹一致，除 4 个消费方路径变更与新增共享指纹外，25 路由、90 个 command、8 个窗口 capability 和 schema v10 均未变化。
- macOS Apple Silicon 上 `pnpm check`、`pnpm native:check`、`pnpm release:check`、Rust 运行时 21 项定向测试通过；以 Rust 实际导出夹具复验 18 个 Word 案例，Go 文档 XML 语义一致。没有重跑全量 `native:compat`、真实漏洞扫描或原生 GUI 验收，也没有启动、重启、签名或发布客户端。
- 此批仅减少旧端退役的源码依赖，未替换生产入口。旧端仍保留 schema/迁移、Rust 兼容夹具、部分展示及宠物资源和发布链；完整业务与 UI 差分、真实远端、生产供应链门禁、目标机及安装升级回退仍须独立验收。

## 2026-11-21 小游戏 benchmark 稳定性与新版启动复核

- 修复 `BenchmarkAssemblyPracticeFrame` 的 headless 测试时序：练习到达超时边界后显式渲染终态，再查找“Play Again”控件；增加超时终态断言，避免旧帧导致控件查询失败。此前失败是 benchmark 未刷新终态界面，不是练习状态机失败。
- `go test ./internal/desktop -run '^TestAssembly' -count=1` 与 `go test ./internal/domain -run '^TestAssembly' -count=1` 通过；5 秒 benchmark 通过，报告 `423716 ns/op`、`223 B/op`、`3 allocs/op`。这是 headless UI 帧基准，不代表真实 GPU 绘制、物理键鼠延迟或整机资源验收。
- `git diff --check` 通过。定向 `go test -race ./internal/domain ./internal/desktop -count=1` 通过；覆盖领域状态机和桌面交互的竞态检查，不替代跨平台实机验收。
- 执行 `pnpm native:launch` 激活当前 macOS ARM 原生候选；PID `88302` 的可执行文件和 `lsof` 文本映像均指向 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-vHxK1o/` 内的 `liberty-native`，并打开候选资料目录数据库。进程复核未发现 Liberty 的 React/Tauri 进程；其他无关项目的 Vite 进程不属于 Liberty 客户端。本次没有重新构建候选，因为唯一源码改动是测试文件。
- 本批未新增 GUI 截图或 UI 逐项验收证据。旧端源码、默认生产发布入口和共享生成事实源仍保留；这次启动只证明候选应用可复用，不代表生产入口已切换、旧端已退役或全量重构已完成。

## 2026-11-21 最新候选重建、启动与状态切换

- 先通过新版候选窗口的原生关闭按钮正常退出上一构建，没有使用信号强杀；随后运行 `pnpm native:check`，启动器、候选打包策略、运行时输入安全测试、生成物漂移检查、Go vet 和 `go test ./...` 全部通过。
- 运行 `pnpm native:package` 生成并校验最新 macOS ARM 候选 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-UBeWZk/`。包清单、版本与候选身份、逐文件 SHA-256、归档回读均通过；产物仍明确为 `productionReady:false`、未签名、未公证。
- 运行 `pnpm native:launch` 后，当前窗口来自上述确切候选包，进程 PID 为 `8081`，`lsof` 指向该包内的 `liberty-native`。本次进程检查未发现 `target/debug/liberty`、旧 `Liberty.app` 或 React/Tauri 客户端进程；这是本机当前运行状态，不代表仓库旧端或外部已发行版本已退役。
- 在实际新版窗口的“运行概览”中完成“本地 FunASR → 远端服务 → 本地 FunASR”切换。远端未配置时显示“远端服务 · 待检查”，切回后等待异步刷新，设置页与主窗口状态均恢复为本地；未发起真实远端请求，也未访问生产资料库。
- 生产切换结论不变：`apps/desktop` 仍保留共享 schema/迁移/运行时清单/消息/素材来源和默认发布入口，旧端发布工作流仍存在。未执行旧端源码删除、默认入口切换、签名、公证、发布或部署。

## 2026-11-21 详情返回与模式读取错误整改

- 修复原生工作区从任务详情进入设置后返回的真实生命周期回归：进入设置前保存详情绑定的来源和任务 ID，返回时重新创建来源隔离的详情控制器，而不是退回到可能过期或属于当前队列模式的列表投影。新增回归覆盖详情关闭、设置返回、来源保持和控制器重建。
- 模式轮询读取失败不再静默保留旧状态。工作区保留最后一个可用模式，同时在状态栏显示双语、可恢复的错误状态；下一次成功读取合法的 `local`/`remote` 模式后清除错误。非法模式也按读取失败处理，不回填未经验证的状态。
- 本轮验证：定向工作区测试通过；`go test -race ./internal/desktop -count=1` 通过（327.377 秒）；`pnpm native:presentation:check` 与 `pnpm desktop:typecheck` 通过；`git diff --check` 通过。此前运行中的候选窗口没有被强制终止，尚未包含本轮修复，需要正常退出后重建候选才能进行新版 GUI 复验。
- 本轮没有改变默认生产入口、生产资料目录或旧端退役结论。`apps/desktop` 仍是共享消息/资源生成来源和生产发布入口；候选包仍不是签名、公证或 `productionReady` 产物。

## 2026-11-21 新版候选真实状态切换复核

- 当前运行实例为 Go/MyGo 原生候选 `liberty-native`，可执行文件来自 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-0PkBTo/`；复核时仅有一个该原生进程，未发现 `target/debug/liberty`、旧 `Liberty.app` 或 React/Tauri 客户端进程。本轮没有启动旧端，也没有访问生产资料库。
- 在该新版窗口的“运行概览”中实际从“本地 FunASR”切换到“远端服务”：设置页状态变为“远端服务 · 待检查”，主窗口顶栏变为“远端服务”，工作台因候选未配置远端地址显示可恢复的“无法加载工作台数据”。随后再次切回“本地 FunASR”，设置页恢复“本地 FunASR · 等待本地配置”；当前窗口停留在本地模式，避免继续发起远端请求。
- 本次窗口确认本地→远端→本地的模式选择、概览状态和主窗口顶栏联动；只验证当前窗口，不代表重启后持久性或真实远端可用。`TestSettingsRuntimeModeChoicePersistsBothDirections` 另验证两种设置均经控制器保存并清除脏状态，`TestSettingsRuntimeOverviewFollowsSelectedProcessingModeAndRemoteState` 覆盖中英文和远端状态优先级。
- 旧 React/Tauri 尚未全部下线、删除或清理：`apps/desktop` 仍是默认生产发布入口、共享 schema/迁移/运行时清单/消息/素材来源，旧端发布工作流仍存在。生产供应链门禁、完整 UI/IME/读屏差分、Intel/Windows 实机、安装升级/回退、真实远端、签名与公证仍未关闭，因此没有执行默认入口切换。

## 2026-11-20 原生启动、UI 对照与旧端退役审查

- 执行 `pnpm native:launch` 后，启动器校验并选择确切候选包 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-mcBG2i/`，识别到同路径实例后复用；复查进程为 PID `18480`，可执行文件位于该候选 `.app` 内，并使用独立 `target/native/launch-profile.uPdFbb/`。本次未启动 React/Tauri；进程检查未发现 `target/debug/liberty` 或旧 `Liberty.app`。这是本机当时的运行状态，不证明其他机器或默认发布入口已经切换。
- 只读对照确认原端 25 条路由在原生端均有映射；这只证明路由入口覆盖，不等于逐页布局、视觉、交互状态和系统行为等价。历史本机证据覆盖部分 macOS ARM 窗口与流程，完整同数据截图差分、中英文/主题矩阵、系统读屏、全部 IME、文件对话框和多窗口焦点验收仍未完成。
- 原生远端读取能力按协商开放，但远端新建任务在原生端明确不可用。旧 React 页面虽保留创建入口，其提交链也明确因安全分块上传协议未实现而失败；因此这是两端共有的未实现能力，不应描述为原生已兼容，也不能在缺少服务端协议的情况下臆造上传实现。切换前需由产品明确远端创建是否属于本次发布范围。
- 当前 checkout 中 `apps/desktop`、Tauri/Rust 源码、旧端质量检查与 `build-desktop.yml` 仍保留；原生生成器仍从旧端读取 schema/迁移、运行时清单、消息、图标及素材。候选包仍为 `productionReady:false`、未签名且未公证。不能删除整个旧端或停用旧端发布链来声称已切换；应先迁出事实源、替代兼容 oracle、完成原生发布链和观察期，再按批准计划分阶段退役。
- 退役审查确认当前没有可直接删除的完整旧端源码范围。生产 Python 依赖扫描、Intel/Windows 目标实机、安装升级/回退、签名/公证、生产资料接管及全量 UI 验收仍是独立门槛；本机候选运行和四目标编译不能替代这些证据。

## 2026-11-20 新版窗口验收与单实例收口

- 从确切候选包路径检查新版 `Liberty Native Candidate.app`，确认主窗口为新版原生应用，而不是通用应用名解析到旧 Tauri。桌面截图与无障碍树验证工作台及“新建任务”两页在 1440×960 下可见、控件有可访问名称、无文件时创建按钮禁用；未选择文件或提交任务。两页截图不构成全路由视觉等价、主题覆盖或系统读屏验收。
- 通过该候选自身的正常退出快捷键关闭较早原生候选 PID `22352`，未用信号强杀。复核只剩最新 PID `18480`，其可执行文件来自 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-mcBG2i/`，独占 `target/native/launch-profile.uPdFbb/` 下的资料库和锁；未发现旧 React/Tauri 客户端进程。该观察不代表仓库旧端已下线或外部已发行旧版已撤回。
- 完整 `GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go test -race ./... -count=1 -timeout=900s` 通过，覆盖命令行、host、业务与桌面全部 Go 包；`git diff --check` 通过。未在此批次重跑 OSV、目标平台实机或整仓旧端 `pnpm check`。
- UI 仍不能宣称与旧客户端全部一致；本次只目视/无障碍树验收了新版工作台和新建任务。React/Tauri 源码、Tauri release workflow 及原生生成/校验的多个事实来源仍在，因此旧端未下线、未删除、未切换默认发布入口。生产依赖扫描、完整页面/IME/读屏差分、Intel/Windows 实机、真实远端、安装升级回退、签名公证仍阻止生产切换。

## 2026-11-20 接续整改：会议选择恢复语义与运行时解释器完整性

- 新建会议表单继续对照旧 React 行为：取消重新选择时保留已有错误、媒体授权和表单内容；读取失败保留当前选择并显示中英文可恢复提示；只有成功选择新媒体后才替换授权并清除错误。新增客户端和桌面组件回归覆盖取消、失败、成功重选及远端创建禁用边界。
- 托管 Python 启动 reconciliation 不再只信任 `ready.json`、路径和声明版本；显式 managed Python 会执行 `runtime_validate.py --interpreter-only`，错误解释器降为 `interpreter_invalid`，不会继续显示 ready。成功探针缓存现在同时绑定文件身份、大小、修改时间和 SHA-256 内容摘要，并在探针前后确认文件未变化；显式 system Python 保持原有未固定版本策略。新增同 inode、同大小、恢复修改时间后的原位替换回归，避免旧缓存绕过版本校验。
- 定向 `go test -race`（managed、client、desktop）通过；`pnpm native:check` 首次运行时，一个 macOS SIGKILL 恢复用例失败，单测单独通过，随后整条命令串行重跑退出 0。首次失败与同时运行的四目标构建存在时间重叠，但因果未证实，故记录为未复现失败，不改弱测试。`pnpm native:build:matrix` 重新确认 macOS ARM/Intel 与 Windows x64/x86 的 Mach-O/PE 编译格式。`pnpm native:package` 重新生成 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-mcBG2i/`，包内清单和归档校验通过，仍标记未签名、未公证、`productionReady:false`。
- 为响应“启动新版”，从该确切包路径启动 PID `18480`，使用 `target/native/launch-profile.uPdFbb/` 独立临时资料目录并确认 SQLite/WAL 已打开；未发现旧 React/Tauri 进程。较早候选 PID `22352` 因可能存在未保存窗口状态未强制结束，因此当前存在两个资料隔离的原生候选进程；这不代表生产入口已切换，正常退出旧窗口后应只保留一个候选实例。
- 旧 React/Tauri 仍为默认生产入口和部分共享事实来源；Python 生产锁 OSV 门禁、完整 UI/IME/读屏、真实远端、Intel/Windows 实机、安装升级回退、签名公证及全量业务差分仍未关闭。上述代码和候选验证不构成删除旧端或生产切换授权。

## 2026-11-20 文档准确性核对

- 本批仅核对和修改文档。当前 `parseOptions` 允许 preview 显式选择 `--database`、`--profile-directory` 或 `--default-profile`；`--version`/帮助不需选择资料。`native:preview` 先跑完整兼容链，通过后才打开临时合成资料，退出清理；直接 preview 的显式目录与 candidate 无资料参数的持久默认目录是不同入口。
- `native:launch` 选择已打包候选，按 SemVer 和同版本包目录修改时间排序，不编译源码；发现不同可执行路径的原生进程会拒绝启动，不强杀进程。macOS 按确切 `.app` 路径启动或激活同构建；Windows 直接启动 `.exe`，尚无已有窗口激活逻辑，已有实例应直接切回窗口。此为源码核对，未新增真实窗口验收。
- 版本检查当前覆盖两个 `package.json`、Tauri 配置、桌面 `Cargo.toml`、`Cargo.lock` 中的 `liberty` 版本及 Go host `Version` 共六个来源。接管恢复点按原 schema 版本与随机后缀命名；旧库接管升级复用首次恢复点，空新库无需快照。恢复预检失败不替换当前业务数据，进入写入后再报错则可能已经改变数据，安全副本与重试记录须保留。
- 复核既有 `target/native/continuation-native-compat.log`：该次 `native:compat` 退出 1，jobdb 包在 30 分钟时限触发超时（1800.210 秒），当时正在运行 `TestWriteRustPetCoreActionsAndReopen`。其他包通过不能将整条命令改记为通过；该日志尚不能确定是负载、测试总量还是实际卡点。本批未复跑，也不覆盖下文早期完整通过记录的原批次结论。
- 根中英文 README 与文档索引同步日常启动入口和临时预览边界；P5-A/P5-G 内容保持原样。验证限于源码/日志核对、`rg`、链接/命令/围栏核对及文档差异空白检查，不新增构建、扫描、客户端操作或发布证据。

## 2026-11-20 兼容超时整改、队列 UI 与候选重建

- 重新分析先前 `native:compat` 日志：jobdb 在目标宠物核心场景开始前，包内其他长差分已累计运行约 1,583 秒；未发现死锁或数据库泄漏。将六组相互独立、各使用隔离资料库的差分并行执行，场景内动作顺序与完整比较保持不变，并去掉规范化快照上重复 JSON 编解码，新增整数精度和快照隔离测试；没有删减差分或提高 30 分钟 timeout。jobdb 六组写入差分全套 race 通过，统一 `pnpm native:compat` 现已完整退出 0，jobdb 包耗时 945.730 秒，profile、执行、host、XLSX、DOCX 及双向凭据互读也通过。
- 对照旧版队列交互，长任务标题在保持单行固定列宽时新增悬停完整标题 tooltip；覆盖中英文、明暗主题、1100/1440 窗口宽度、Escape 关闭、数据刷新和行删除。相邻桌面完整测试及此用例 race 通过；这是组件交互测试，不代表原生系统窗口的逐页视觉验收。
- `pnpm check` 通过：前端 47 项、Python 105 项、Rust 278 项通过、18 项按约定忽略，Web 构建、Rust fmt/Clippy 和静态发布检查均通过。另有 `pnpm native:check`、修复后的 `pnpm native:compat`、四目标 `pnpm native:build:matrix`、启动器 11 项与打包 4 项测试以及独立 `pnpm licenses:check` 通过；`git diff --check` 通过。许可证检查有既存的未命中 BSD-2-Clause allowance 提示；外置盘增量编译硬链接回退提示仍存在。
- 固定 `cargo-deny 0.20.2` 和 OSV Scanner `2.5.0` 重跑安全门禁：Rust advisories/bans/sources 通过；三份 Python 生产平台锁逐份均报告 3 个包、54 组漏洞（6 Critical、29 High、16 Medium、2 Low、1 Unknown），总 `pnpm security:check` 仍以退出 1 阻断。未改生产锁、扩大例外或降低门禁；`pnpm check` 的静态发布安全基线不是实际漏洞扫描。
- 以本批源码重新打包 `darwin-aarch64`、`darwin-x64`、`windows-x64`、`windows-x86` 四个候选，候选清单逐文件/归档校验成功，均标记 `productionReady:false`、未签名、未公证。ARM 最新包 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-BA9NcO/` 离线 `--version` 为 `Liberty Native 1.1.24 (candidate)`，主程序 SHA-256 为 `5ec10d6ca01fb38fe13333401c0b820421b3e1e9c77f8d4ccee65272945a28e8`。四目标打包/编译不替代目标机运行、安装器及 UI/IME 验收。
- 当前唯一 Liberty 客户端进程仍是 Go/MyGo `liberty-native`，PID `22352`，从前一 ARM 候选包 `...-Bve1Bb/` 运行；本轮未发现旧 React/Tauri 进程。新打包源包含队列 tooltip，而正在运行二进制 SHA 为 `7892eb...`，与最新包不同；未因无法确认窗口未保存状态而强制重启，也未启动第二个争用候选资料库的实例。需先在当前窗口正常退出，再运行 `pnpm native:launch` 才会加载本轮最新 UI。未通过当前 GUI 画面验收。

旧 React/Tauri 仍是默认生产发布入口与共享事实来源，不能称为已下线；生产漏洞门禁、完整 UI/IME/读屏与业务差分、真实远端、Intel/Windows 实机、安装升级回退及签名公证仍未全部通过，故默认切换和删除旧端继续禁止。

## 2026-11-20 继续整改：可选照看、结果状态与候选选择

- 工厂岗位处于 `needsCare` 时新增可选装配练习入口；原有一键“照看岗位”仍可直接执行。小游戏只在成功终态消费该次临时授权并调用既有 `WorkController.Care("factory", taskID)`，分数不参与经济计算；重开不会重复调用，暂停与超时不改变任务。覆盖成功一次、重开、超时、暂停、旧照看按钮保留及 controller 后续刷新。该 UI/调用接线已落地，不等于 P5-G1 试玩校准、application/旧 Rust 经济黄金样本、强杀/断电或真实窗口验收完成。
- 结果工作台新增当前 AI 总结选择状态：只在活动 ID 对应实际总结记录时提示可导出；缺失或失配显示无总结状态。双语与无效 ID 有组件回归。
- `native:launch` 修正候选版本排序对 SemVer build metadata 使用数字转换造成 `NaN` 的问题；忽略 `+build` 比较 precedence，并对超出安全整数的 numeric identifier 使用字符串长度/字典序比较。启动器和打包自测共 14 项通过。
- 本批定向 `go test ./internal/desktop -run 'TestAssembly|TestWorkCarePractice|TestWorkbenchShowsWhetherAnAISummaryResultIsSelected' -count=1` 通过，覆盖新增 care 流程及并行的结果状态差距。固定 OSV Scanner 2.5.0 的实际 `pnpm security:check` 退出 1；另分别扫描三份 Python 生产锁，均退出 1，各报 54 组（6 Critical、29 High、16 Medium、2 Low、1 Unknown）。主安全门禁中的 pnpm/Go 锁扫描以及独立 `cargo-deny 0.20.2 check advisories bans sources` 通过；未扩大例外或升级产品锁。
- 候选启动器 SemVer 自测、Node 语法检查、差异空白检查、四目标 `native:build:matrix` 以及定向桌面 race 通过。已将本批更改打包为 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-Bve1Bb/`；包内 `--version` 为 `Liberty Native 1.1.24 (candidate)`，清单有 6 个文件，声明 `productionReady:false`、未签名/未公证，主程序 SHA-256 为 `7892eb2083609c3633c421d1379ee1da64644f025cdc480b1c14efc0179c5943`。实测 `pnpm native:launch` 先发现旧候选仍运行时曾因脚本漏定义平台 target 报错，随后修复并以 11 项测试及真实启动复核；最终 PID `22352` 的进程和 `lsof` 均指向新包 `...-Bve1Bb/`，`--version` 和摘要与包一致，持有独立候选 SQLite/WAL。当前仅一个 `liberty-native` 进程，没有启动旧 React/Tauri、生产资料或第二个写实例。当前 GUI 的逐项画面未通过自动化检查，不以进程/摘要替代 UI 目视验收。
- 旧 React/Tauri 仍保留并承担默认发布入口、兼容/生成来源及回退职责；没有删除、切换、提交、发布或部署。安全门禁、P5-G1、完整页面/UI/IME/读屏差分、其他平台实机、真实远端、安装升级/回退及签名公证仍阻断生产切换。

## 2026-11-20 候选与检查状态快照（非实时状态）

- 当前运行的是 Go/MyGo 原生候选 `liberty-native`，PID `22352`，可执行文件来自 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-Bve1Bb/liberty-native-candidate-1.1.24-darwin-aarch64/Liberty Native Candidate.app/`；包内二进制 SHA-256 为 `7892eb2083609c3633c421d1379ee1da64644f025cdc480b1c14efc0179c5943`。进程使用独立 `com.westng.liberty.native-candidate` 资料身份，当前仅有一个 `liberty-native` 进程，未发现旧 React/Tauri 客户端。已确认进程与包路径相同且打开候选 SQLite/WAL；未通过 GUI 自动化检查窗口当前画面。
- `pnpm native:launch` 已逐文件校验清单、SHA-256 和符号链接边界，并按确切 `.app` 路径启动最新候选；另增加运行中可执行文件路径检查，不会把不同构建静默报告为新版。候选清单仍声明 `productionReady:false`、未签名、未公证。
- 固定版 `cargo-deny 0.20.2` 的 advisories/bans/sources 检查通过；OSV Scanner `2.5.0` 对三个当前 Python 生产锁分别退出 `1`，每份均为 54 组漏洞（6 Critical、29 High、16 Medium、2 Low、1 Unknown）。未扩大例外或升级不兼容依赖，`pnpm security:check` 因此仍阻止生产切换。
- 本轮没有强制结束带未保存内容的窗口，也没有并行启动第二个 Liberty 实例；启动器复用现有候选资料目录和窗口。
- 本轮 `native:check`、四目标 `native:build:matrix`、care/结果状态定向 race、11 项启动器和 4 项打包 Node 测试、`git diff --check` 均通过。旧 Tauri 窗口配置差分不能替代逐页新旧 GUI 目视验收；本次未声称完整 UI/IME/读屏等价。
- 真实供应链结果仍不能视为通过：OSV Scanner `2.5.0` 对三个当前 Python 生产锁各报告 54 组漏洞（含 6 Critical），未扩大例外；候选运行时的覆盖扫描通过不等于生产安全门禁解除。
- 旧 React/Tauri 源码仍在仓库，仍是默认生产发布入口，`.github/workflows/build-desktop.yml` 仍构建 Tauri；原生生成器和兼容检查仍从旧端读取 schema/迁移、运行时清单、双语消息与素材。没有删除、下线或切换默认入口。
- 已完成的主窗口、Shell 与部分页面对照不等于完整 UI 等价；跨平台实机、完整交互/IME/读屏、真实远端、生产依赖安全门禁、安装升级回退、签名与公证仍是独立切换条件。

## 2026-11-20 现场状态快照（非实时进程状态）

- 记录时新版 `apps/native` 候选运行于 macOS ARM，PID `19029`，来自 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-WKXXFD/`；进程持有独立 `com.westng.liberty.native-candidate` 资料库。当前检查未发现旧 React/Tauri 客户端进程。
- 旧 React/Tauri 源码、默认生产入口、资料格式事实来源和 Tauri 发布工作流仍保留；这不是生产切换，也未完成旧端下线。
- 本机标题栏与主窗口尺寸有配置/组件测试；完整新旧页面截图差分、所有交互、其他平台实机、远端服务、安装升级回退、生产供应链安全、签名和公证仍未全部通过。
- 本摘要记录手工运行的候选，不证明窗口当前画面或未保存状态；本次没有重启该进程。

## 2026-11-20 继续整改：原生 UI 状态反馈与恢复路径

- 主窗口任务完成反馈已接入 `OpenWorkspace` 创建的原生队列控制器轮询回调：仅在已加载且成功的队列中观察到非完成态到 `completed` 的变化时显示双语状态栏提示七秒，并通过 MyGo 系统通知尝试提示；初始历史数据、重复轮询、失败刷新、来源相同 ID 和关闭窗口时的通知/计时器均有回归覆盖。提交/删除控制器只触发界面刷新，不会冒充队列完成事件。宠物转写奖励仍由执行服务负责，UI 不重复发放。
- 主窗口几何和标题栏对照已固定为旧 Tauri 的 `1440×960`、最小 `1100×760`；macOS traffic-light 坐标、Windows 标题栏高度以及 MyGo 动态左右系统控件留白有测试，标题栏指标、拖动区和项目链接不覆盖原生控件。
- 设置与运行时页面区分首次读取失败、后台刷新失败和操作失败；读取失败清除陈旧面板/操作控件，重试只重新读取，保存失败保留草稿，错误通过 alert 语义呈现。新增客户端和桌面组件恢复测试，未把一次轮询成功当成安装或跨平台运行时验收。
- 总结窗口补齐旧端显示的源文件名、已有保存结果的完成状态和任务不存在文案；纪要重试期间保留已保存内容、显示加载语义并隐藏重复重试按钮，失败后恢复可重试状态。农场/打工接受新操作时清除旧成功反馈，忙碌或忽略操作不清除；schema 接管迁移失败重试继续复用首次恢复点。
- 本批验证（随后状态更新见下一条）：完成提示/标题栏定向 `go test -race`、队列轮询完成/失败集成测试、设置/运行时/总结/纪要/工作定向测试、schema 包 `go test -race`、`gofmt`、`git diff --check` 和修复后的 `pnpm native:check` 已通过；本机 ARM 新候选包已重建并通过包内/归档校验。当批检查时桌面仍运行重建前的隔离候选，未强制退出或尝试同资料第二实例；该现场结论已被下条记录更新。
- 随后已从该次重建的精确包路径启动新版原生候选；核验时进程 `19029` 的可执行文件来自 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-WKXXFD/`，SHA-256 为 `7f5d9404dee49588667c023409578a4ae0f9eaab2ae514b030d09593916165e`，`--version` 输出 `Liberty Native 1.1.24 (candidate)`。进程持有独立 `com.westng.liberty.native-candidate` 资料库及 WAL。核验未发现旧 React/Tauri 主程序；未启动旧版、未接管生产资料。
- 旧 React/Tauri 仍是默认生产入口，且仍被原生生成器、共享 schema/消息/运行时清单和发布工作流引用；本轮没有删除或下线。完整 UI 截图/交互、输入法和读屏、Windows x64/x86 与 Intel Mac 实机、真实远端服务、安装升级/回退、供应链安全门禁、签名/公证仍未完成，因此不能宣称已达到生产切换条件。

## 2026-11-20 接续整改：隔离入口、运行时交付与桌宠反馈

- 三方独立只读审查及主线程复核确认：默认运行的是 `apps/native` 隔离候选，而非 React/Tauri；旧端仍是生产入口、发布流程仍构建旧端，没有默认切换或删除旧回退。全量 command 授权、逐页新旧 GUI、平台和发布验收均未结束。
- 修复显式目录接管旁路：可写启动或离线恢复通过普通 `--profile-directory` 选择生产目录/祖先/后代、路径别名或共享数据库硬链接时，此前可到达资料库；现拒绝含 `..` 的路径分量，将规范路径用于重叠判定、目录创建与开库，重叠错误给出专门的双语恢复指引。覆盖卷根、缺失后代、大小写、父链接、硬链接、可写、restore、默认候选和显式生产选择的测试改为四目标平台 CI 实跑启动选项与 profile 包。
- 启动选择的当前边界：preview 必须显式给出 `--database`、`--profile-directory` 或 `--default-profile`（版本/帮助输出除外）；`native:preview` 默认传合成只读快照，`--writable` 改用临时显式资料目录。候选无参数启动只选独立候选资料目录；`--database` 一直只读，现有 Tauri 生产资料需显式 `--default-profile`，可写或恢复仍受目录隔离与共享锁约束。此机制提供隔离验收入口，不等于切换默认生产客户端。
- 修复旧端运行时清单中的五处失效归档路径：四个平台指向南大 Python 归档的镜像地址及 Windows x64/x86 旧 FFmpeg 地址在 2026-11-20 的 HTTPS Range 实测无效。保留南大 PyPI 包索引，Python 归档回退正式官方固定摘要；Windows 现固定 FFmpeg-Builds 8.1.3 两档及完整 SHA-256，同步清单版本、Go 生成资源和 Rust/Go 固定资产测试。独立复核两个本地 ZIP 完整性、完整摘要、FFmpeg 源码提交和 x86/x64 PE 架构，未执行 Windows 二进制。
- 桌宠迁入旧版食物成长浮字：仅对运行窗口基线之后新增的持久正 `store_food` 账本显示 +N 三秒；同刻重复、排序和刷新不重播、不延长，提示可见/消失时拖拽仍被捕获，减少动态效果会静止显示。主/额外窗口各自维护基线并在关闭后重建。MyGo 组件测试覆盖时间边界、键鼠捕获、独立窗口和历史事件，尚无 Windows/Darwin 原生跨平台动画或读屏验收。
- 显式恢复仅接受同目录认可的非链接 `.bak` 文件，要求当前库存在且有效；源/目标 schema、完整性与快照凭据在恢复前预检，并先留经验证的 `pre-restore` 副本。预检失败测试确认当前库不变；进入 SQLite backup 写入后的 checkpoint、读回验证或同步失败可能发生在数据已改变之后，故不能承诺所有恢复错误均保持原库字节不变，安全副本必须保留。安装器级升级/回退、物理断电和所有恢复中断时点仍待验证。
- 截至该批次，`native:package` 的既有记录覆盖四个目标的隔离候选归档，并校验六个版本来源（根/桌面 `package.json`、Tauri 配置、`Cargo.toml`、`Cargo.lock`、Go host）、候选身份、Mach-O/PE 架构、NOTICE、逐文件 SHA-256 和归档解包内容；Windows 产物是便携目录，不是安装器。候选未签名、未公证且标记 `productionReady:false`，不代表跨平台运行或安装回退通过。该批的“未重新打包”状态已由后续批次覆盖，当前活动进程是否包含最新工作区改动以文首核验为准。
- 运行时档案 HEAD/Range 轻量探测不核验完整新下载：清单原官方固定资产与 macOS FFmpeg 地址返回成功，南大镜像归档与旧 Windows FFmpeg 返回 404；新 Windows FFmpeg ZIP 的本地完整 SHA 与上游校验信息匹配。新上游是分仓 GPL 构建、发布非 immutable，最低系统、完整可再分发源合规、真实 x64/x86 安装/解码及回退仍未验收；不能将可达和摘要匹配当作平台验收。
- 截至该批次，桌宠成长浮字的自动化记录为定向 `go test -race ./internal/desktop -run '^TestCompanion' -count=5`、`go vet ./internal/desktop` 和格式检查通过；覆盖历史事件不重放、同刻重复不延长、过期边界、刷新/排序、窗口基线和提示期间拖拽捕获。当时四目标重打包与窗口观察仍待执行；后续批次已记录候选重建和部分窗口验收。Windows/Intel Mac 动画、系统读屏与全页面 UI 差分仍未完成。
- 路径隔离与清单修复不触及用户库/凭据。本次文档整理未启动或操作客户端、未接管生产资料、提交或发布；旧 React/Tauri 仍是默认生产入口，未删除或切换。

## 2026-11-20 本轮收尾核实

- 复核确认：仓库内 React/Tauri 尚未下线，仍是默认生产入口；`build-desktop.yml` 仍为 Tauri 构建安装包。`apps/native` 是隔离候选，Go 生成器仍读取旧端 schema、迁移、消息/图标、运行时清单等事实来源；不能直接删除旧端目录。
- 该批根中英文 README 的桌面开发指引优先显示新版合成预览与候选打包方式，并将旧端开发命令标记为仅维护旧端使用。后续文档整理已改为日常使用 `pnpm native:launch`，临时 `native:preview` 另列；此举不切换正式生产入口或发布工作流。
- 旧 Rust 资料库仍服务当前生产客户端。复审发现宠物经验值在工作流成长、签到、食物使用和兑换奖励中的 i64 加法可溢出；现统一使用 `checked_add`，溢出失败整笔事务回滚，并新增精确上限和签到/兑换回滚测试。
- 修复后的整仓 `pnpm check`、`pnpm native:check`、完整 `pnpm native:compat`、全包 `go test -race ./... -count=1 -timeout=900s`、`pnpm native:build:matrix` 均通过。兼容差分在 Rust 溢出修复后完整重跑，jobdb 整包 1215.992 秒；全部宠物、签到、商店、恢复凭据互操作、host 生命周期、资料锁、DOCX 和双向 XLSX 测试通过。四目标候选已重建并分别通过包内逐文件 SHA 与归档解包验证；本机 ARM 新包离线 `--version` 返回 `Liberty Native 1.1.24 (candidate)`，没有打开资料库。四包均未签名、未公证，Windows 产物不是安装器。
- 当前桌面激活的是 `com.westng.liberty.native-candidate`，同一进程未增开实例，进程清单中未发现旧 Tauri 客户端。该进程的 Mach-O 摘要不同于 12:23 刚重建的 ARM 包；因无法确认窗口无未保存输入，本轮没有强制退出或重启。故当前可确认“新版原生候选正在运行”，但不能声称 12:23 新包里的最新工作区改动已显示。
- 未完成的切换门槛仍包括生产依赖安全扫描告警、Windows/Intel 实机、完整 UI/IME/读屏差分、真实远端服务、安装升级回退、签名公证。未提交、推送、发布或删除旧端。运行时/自动化门禁通过不等于生产切换完成。

## 2026-11-20 前一阶段：新版候选启动与协作边界

- 本次接续由主代理单独完成，没有派发、调用、等待或整合子代理。文档中更早批次关于并行工作的历史记录保持原样，不代表本次继续工作的协作方式。
- 当前运行进程来自 `apps/native` 的隔离候选包 `liberty-native`；进程核验未发现旧 React/Tauri 的 `com.westng.liberty` 实例。没有启动旧客户端、接管生产资料、提交、发布或切换默认入口。
- 旧 React/Tauri 源码和生产入口仍保留是有意的切换保护：原生候选仍未完成安全门禁、其他平台实机、完整 UI/无障碍/输入法、真实远端、安装升级回退及签名公证等退出条件；在这些条件关闭前删除旧入口会失去回退路径。

## 2026-11-20 固定扫描器下重跑候选运行时验收

- 将仓库固定版 `osv-scanner 2.5.0` 加入本次命令的临时 `PATH` 后，`native:runtime:candidate:acceptance` 完整退出 0。新版候选随包输入实际安装、校验 Python/FFmpeg 和四个固定模型；Runner 两种说话人模式均生成非空转写，状态分别为 `disabled` / `completed`。82 个已安装发行项通过锁版本和 `pip check` 核对；真实 host 两个任务均完成，关闭并重开后逐字稿、说话人和每任务三类奖励均一致。
- 当前策略下三平台锁、82 项安装集和 FunASR 来源 SBOM 均完成覆盖扫描并通过。无例外扫描仍退出 1，保留其漏洞报告；未扩大例外，也不把候选扫描通过说成生产安全门禁已通过。验收只覆盖本机 macOS ARM；其他平台仍需实机验证。
- 复核时运行中的应用来自 `target/native/packages/.../Liberty Native Candidate.app`，不是旧 Tauri bundle；本轮没有派发子代理、启动旧版、删除旧版代码或切换默认生产入口。候选仍未签名/公证，旧 React/Tauri 生产入口仍保留。

## 2026-11-20 固定模型音频与跨平台验收接线

- 运行时与 host 的真实验收改为使用已下载并由 pinned model 文件清单逐项校验的 `speech_seaco_paraformer.../example/asr_example.wav`，不依赖 macOS `say`，不使用用户媒体。Runner 的关闭/开启说话人两种路径及完整 host 媒体授权、任务调度、转写、退出重开与工作流奖励核对均可由同一固定音频复现。验收命令在 Darwin 和 Windows 执行 host 闭环；Windows x86 按当前产品边界明确跳过本地 ASR，不把该目标伪称为可用。
- Apple Silicon 本机生产运行时实测：安装/复用固定 FFmpeg、Python 与模型，离线校验后两种 Runner 模式通过；随后 host 两个任务均完成，结果分别为关闭说话人 1 段/0 speaker、开启说话人 1 段/1 speaker。关闭并重开资料库后逐字稿、说话人结果和三类工作流奖励均核对通过。前一 managed 真实验收 154.45 秒，host 96.27 秒。Windows x64 `runtime_acceptance` 的 managed 与 host 测试包交叉编译通过，但没有 Windows 实机执行证据；Intel Mac 与 Windows GUI/IME、安装器流程也未因此验收。
- 本批仅调整运行时验收夹具、Darwin/Windows host 验收接线及相邻说明；测试缓存位于忽略的 `target/native/`。没有接触旧 React/Tauri 进程、生产资料或用户媒体，没有派发子代理、提交、发布或切换默认入口。

## 2026-11-20 小游戏帧更新与状态机测量

- `AssemblyRound.Advance` 返回值表示当前阶段或界面显示的剩余整秒是否变化；活动循环据此避免每固定步创建并复制状态快照。回归覆盖不足一秒、整秒边界、错误扣时、截止超时、暂停及完成状态，同时核对推进不改变订单、命中数和准确率。
- Apple M1 三轮 `go test ./internal/domain ./internal/desktop -run '^$' -bench '^BenchmarkAssembly' -benchmem -count=3`：纯状态推进为 2.431–2.553 ns/op、0 B/op、0 allocs/op；真实 `ui.NewTester` 驱动的活动界面推进为 421.528–435.822 μs/op、168–172 B/op、2 allocs/op。后者含 headless MyGo 的组件布局/绘制，不含原生窗口、GPU 合成或物理输入时延；不得据此声称系统级帧率、输入延迟或跨平台性能达标。
- `go test -race ./internal/domain ./internal/desktop -count=1` 通过（domain 15.703 秒，desktop 226.930 秒）。活动帧 benchmark 以真实开始按钮进入并在一局结束后通过真实“Play Again”按钮重开；基准循环不额外轮询克隆快照。

P5-G1 仍未完成：玩法评价、真实键盘/辅助技术全流程、物理输入延迟以及闲置/活动和会议处理并行时的资源预算仍需实机验收。

## 2026-11-20 原生启动器修复与候选激活

- `native:launch` 原先只在随机外层包目录读取 `candidate-manifest.json`，而实际 `native:package` 将清单放在内层版本目录，导致刚构建出的真实候选无法被启动器发现。现同时识别直接内容目录与打包器生成的单层嵌套目录，并继续逐文件拒绝符号链接、核对大小/SHA-256、按 SemVer 选目标平台最高版本；歧义的多内容目录拒绝启动。八项启动器测试通过，另以当前真实 ARM 包目录实测解析到内层 `.app`。
- `pnpm native:launch` 已按校验后的确切 `Liberty Native Candidate.app` 路径执行。已有 `liberty-native` PID `83341` 保持唯一，打开的 SQLite/WAL 位于独立 `com.westng.liberty.native-candidate` 资料目录；本轮进程清单未发现旧 Tauri 主程序。未终止或并行启动第二实例，未访问生产资料。
- 原生窗口几何/标题栏定向测试通过：默认 `1440×960`、最小 `1100×760`，macOS inset 与 traffic-light 对齐旧配置；Windows 控件避让有模拟测试，但其系统按钮外观并非旧端自绘按钮的像素级一致，且没有 Windows 实机验收。
- 本轮通过启动器 Node 测试（8 项）、`go test ./internal/desktop -run '^(TestShellLeavesRoomForNativeTitlebarControls|TestMainWindowMatchesLegacyGeometryAndPlatformChrome)$' -count=1`、`gofmt -d` 与 `git diff --check`。这些检查及启动新版都不表示完整 UI 等价或默认生产入口已切换。

React/Tauri 源码及默认发布入口仍保留；全 UI 新旧截图/交互差分、IME/读屏、Windows/Intel 实机、远端服务、安全门禁、安装升级回退、签名/公证仍需完成。候选客户端可启动，但尚不能安全宣布旧端全部下线或删除。

## 2026-11-20 逐字稿工作台稳态重绘

- `ResultView` 将讲话人计数/本地化筛选项与可见行筛选分开缓存，按结果快照、语言、显示切片底层地址和长度失效。查询文本归一化索引延迟到首次搜索；无查询/讲话人筛选时直接复用原始显示切片，避免长期持有额外的全量 Segment 副本。控制器刷新以新 Result 快照替换旧对象；相同任务 ID 的新对象和同对象内替换切片均有回归覆盖。缓存不适用于同一个快照、同一底层数组被外部原地改写的调用方式；现有控制器不通过该方式发布刷新。
- 回归验证筛选计数在查询变化时复用、结果 ID 相同但快照变化时刷新、来源切片替换时刷新，以及 `legacy_unverified` 投影切换后回落到原始逐字稿；原有双语搜索、讲话人切换、空态、键盘滚动、虚拟列表和编辑交互一并覆盖。
- Apple M1 上的稳态组件基准以首次 Frame 预热后测重复帧。相同的 10,000 段无筛选场景由约 0.86–1.09 ms、2.67 MB、113 allocs 降至 0.19–0.20 ms、13.2 KB、93 allocs；全文搜索由约 1.94–2.12 ms、3.63 MB、20,114 allocs 降至 0.189–0.192 ms、13.2 KB、94 allocs。400 段全文搜索由约 0.264–0.294 ms、117 KB、902 allocs 降至 0.190–0.191 ms、13.2 KB、92 allocs。修改前后均为本机 darwin/arm64；轮数不同，结果是稳态组件基准而非冷启动、整窗性能或其他平台结论。
- `go test -race ./internal/desktop -count=1` 通过（222.453 秒）；修改后工作台基准 `go test ./internal/desktop -run '^$' -bench '^BenchmarkResultWorkbenchFrame$' -benchmem -count=3` 通过。完整原生检查与四目标编译将在本轮收尾时再运行。已有窗口尺寸、标题栏 overlay、traffic-light 坐标及动态按钮留白实现/测试已核对，本批没有重复改动或启动旧版客户端。**本轮未派发子代理**，未启动新客户端、重建候选包、访问生产资料或凭据，未提交/发布/切换默认入口。

此优化不关闭其余 UI 差分、其他平台实机、真实远端、生产依赖安全、安装回退和发布门槛；Go/MyGo 仍为隔离候选，旧 React/Tauri 入口仍保留。

## 2026-11-20 任务生命周期、远端工作台、任务 UI 与真实兑换

- 四个既有子代理先按不重叠范围只读复现，再分别负责任务执行与导入、任务 UI、真实兑换差分和远端工作台授权；主线程处理结果文件发布集成、Rust 导入保护、重复导出正文修复、正式文档及统一 gate。修复后交叉复审，新增缺陷重新转移写集再实施，没有并发修改同一生产文件。用户既有改动保留，没有提交、推送、发布或默认入口切换。
- 真实 SQLite 红测确认首次旧 JSON 导入会覆盖同 ID 的已完成 SQLite 结果，并使 `jobs` 和 `job_runs` 状态不一致。这是两端继承缺陷，现 Go 与本分支 Rust 同事务保留已有 job 和持久删除意图，仅导入缺失 ID；同批重复 ID 保留首个，摘要全局 ID 碰撞整批回滚且 marker 不提交。Go `Imported` 计新增，`Skipped` 合并解析拒绝与碰撞跳过；不是静默覆盖。永久测试覆盖关闭重开三次、原摘要归属、执行 lease、事件和 marker 回滚后重试。
- 执行器结果提交失败后会留下无法显式重试的 running 任务；现沿用原 lease，用新的五秒有界清理上下文记录失败，随后可显式重试。新 lease、删除意图、已提交结果和终止未核实路径不能被降级。租约取得前的结构化可恢复错误由调度器从 100ms 至最多 2s 退避，等待时释放并发名额，预留、取消与关闭撤销等待；普通执行错误不自动重跑。真实 SQLite 争用保留原五秒超时，锁释放后 Runner 只执行一次，显式重试及重开结果通过。提交和失败记录均无法写入时仍返回错误并保留恢复依据，不宣称终态已经持久化。
- 远端工作台原先在地址切换后会用旧授权操作新服务的同 ID 任务，且慢网络读取持有所有窗口共享锁。现 grant 绑定原 URL、凭据引用和实际 Token 快照，失效连接拒绝读取/改名/导出；主窗口仅切换处理模式不重绑连接。前置读取与提交使用同一连接；成功改名不会被事后取消改报失败。网络 I/O 移出共享注册锁，同 grant 的已接受写入、撤销和替换保持顺序，独立本地纪要可关闭。永久回环/SQLite 和取消/并发 race 通过，独立复审未发现新增 P1/P2；旧 Tauri 远端取当前设置的继承行为未同步。真实外部服务以及“服务端已提交而应答丢失”未验收。
- 队列失败不再显示成功空列表，loading/status、失败/alert 和空态互斥，键盘重试只发起一次读取；失败原因提示沿用旧交互并统一脱敏，支持悬停与键盘。详情日志用索引只读文本缓冲支持跨段全选复制，保留倒序、分级颜色、换行与空白，视口保持 420 DIP。双语、明暗、1100/1440 宽度组件回归通过；4096 段日志复制为中文 483,328 字节、英文 663,552 字节，AX 树均 68 节点，PageDown/Home/End 实际改变可见文本。主线程检查了中文提示和英文深色日志代表图；组件图不代表系统剪贴板、IME、原生读屏或其他平台验收。
- 兑换使用真实 Rust `redeem_key_tx`、实际 Ed25519 签名及 Go repository，最终 190 步、81 次拒绝（其中九次真实 SQLite 故障）、十一表与 570 次逐步重开。先红修复 Go 对重复已知字段、大小写和 null 的 Serde 接受域差异，以及身份去重前检查过期导致同身份换发误拒绝。独立复审再用三项真实签名发现未配对 surrogate 被替换成 U+FFFD 后仍发 LP，扩充真实 oracle 与合法配对、字面替换字符、转义反斜杠和未知字段控制后，完整 race 通过，为 132.208 秒；Rust 相邻六项及 fmt/Clippy 通过。新短码拒绝、既有 hash 重复、同身份签名换发不重复发奖分别验证，测试 MAC 不进入 Go，公钥 seam 不进入无 tag 发布构建。十一项比较器变异负控保留严格业务值与行序。累计 i64 溢出两端等价未验证；有界 Go 探针的两种边界均事务回滚，Rust 探针未执行，不能当作双端通过。
- 原生结果导出新增仅保存对话框 seam，真实编码及文件发布回归覆盖四格式的取消、对话框错误、窗口关闭/授权替换、读取失败和链接目标保护；双语使用真实 SQLite 的讲话人回读、纪要 backfill、实际 DOCX XML 与多次关闭重开，文本/Word 反复导出仍只奖励一次。界面已验证通用失败 alert，没有声称具体错误原因全部展示。
- 真实持久化链发现非结构化摘要首次 Word 导出回填后再次导出会失去唯一正文，Go/Rust 同步仅在缺失 payload 的重建时把有效正文持久到 `GlobalSummary`。已有 payload 保持权威，不恢复用户已清空的旧 AI 文本。复审又修正 AI 时间/地点等元数据在清理 overview 时丢失的问题，保留 user 空值及 schema v1 契约，统一过滤 ASCII/中文冒号与冒号前空格。最后补上仅元数据和全占位内容的真实 Word XML 红绿回归，首次导出不再误把它们放入正文附录；回填并重开前后输出一致。最终定向普通/race、45 项 Rust 导出测试与 18 组纪要数据及 Word XML 的严格差分均退出 0。
- 最终 `pnpm check`、`pnpm native:check` 和 `pnpm native:build:matrix` 均退出 0。整仓含 47 项前端、105 项 Python、272 项 Rust（另有 18 个默认忽略的专用 helper）、Web 构建、fmt、Clippy 及发布策略。末次冻结重新核对 921 个输入，较上一冻结点仅变更三处纪要修订文件和原生 README，证据为 `target/native/task-ui-remote-redemption-final-inputs.json`。前一冻结点主链五包 race 均通过，desktop 为 275.158 秒；最后纪要改动的独立定向 race 通过，未重跑最后源码的全包 race。外置盘硬链接回退和 Web 大 chunk 提示仍存在。
- 停写后的完整 `pnpm native:compat` 退出 0。190 步真实签名兑换差分用时 84.55 秒，216 步宠物核心 106.78 秒，834 步全作物和岗位 277.18 秒，礼盒盲盒 332.24 秒，jobdb 整包 893.731 秒；签到、商店、旧库迁移、恢复凭据记录互操作、DOCX 与双向 XLSX 也通过。耗时不作为性能基准。本次统一检查证据为 `target/native/task-ui-remote-redemption-{workspace-check,native-check,native-matrix,native-compat}.log`，不以编辑期间的日志替代。
- 四目标 `pnpm native:package` 均退出 0，且已独立复核完整目录、归档解包、逐文件摘要、`SHA256SUMS`、版本、隔离身份和二进制架构；产物索引为 `target/native/task-ui-remote-redemption-package-index.json`。macOS ARM/Intel 与 Windows x64 包含显式的候选运行时输入，默认仍选生产运行时；Windows x86 仅普通便携候选。三次打包均使用固定 OSV Scanner 2.5.0，三锁现有策略均退出 0（覆盖完整），空例外均退出 1。这不是生产依赖安全通过。四包未签名、未公证且标记 `productionReady:false`；未运行非本机架构或安装器。
- 本机 ARM `--version` 返回 `Liberty Native 1.1.24 (candidate)`；从该包的确切应用路径启动，观察到唯一候选 `liberty-native` 进程与前台 `Liberty Native Candidate` 原生主窗（1440 × 960）。SQLite 打开文件均属显式隔离测试目录；未启动旧 Tauri 或接管生产资料。这里只核验最新候选启动和主窗，不把它计作全部 UI、真实远端或运行时重验。

本批自动化与候选包验收已完成，不代表生产切换。生产漏洞、全命令与全 UI 实机、真实远端、其他平台、性能、安装升级回退、签名公证及 P5-G 后续门槛仍未关闭；旧 React/Tauri 入口没有删除或切换。

## 2026-11-20 宠物核心、强杀恢复与候选运行时

- 用户明确授权四个子代理后，分别处理宠物核心真实差分、宠物中心/签到/兑换 UI、进程崩溃恢复及运行时交付。主线程负责 host/CLI、准备/打包/扫描工具、真实运行时验收、正式文档和统一整合。写集互不重叠，后续独立复审发现的问题先转移所有权再修复；没有提交、推送、发布、签名、公证或改变生产默认入口。
- 宠物核心使用真实 Rust repository 和固定时钟生成六组 216 步，比较完整 API、十张持久表及每步三次重开，包含 20 次拒绝，其中 10 次为真实 SQLite 故障；随机 ID 和 interaction key 仅做严格实体双射，另有 13 项业务值/身份变异负控。初次完整 race 仍有七步失败，保留红证据后修复空名错误，并明确处理坐标契约：Go 已有偏好不覆盖拖动坐标保护，旧 Rust 则会用过期表单覆盖新位置。Rust 生产链现分离偏好和位置 UPDATE，command、桌宠启用和拖动均接相应方法，oracle 调用这些真实生产方法。这是消除旧 stale-write 的契约修正，不声称保持旧缺陷。最终 216/216 race 通过，为 123.198 秒；三项 Rust 双向覆盖/故障回归、Go 六项回归和独立只读复审通过。
- 宠物中心在最小窗口内保留合法长英文/中文名字的完整可访问值和可点击改名按钮；签到补齐仓库数、特别奖励进度、历史连签与物品。刷新不重放兑换/签到、不清除未解决操作错误；已接受操作的回读失败仍只读恢复。独立复审又发现原生两处取模会向第 17 天预告已过的第 3 天礼盒，且旧测试错误地要求此行为；先红后修为绝对连续天数，第 14 天后基础奖励、无后续一次性里程碑。新回归对照真实 SQLite 连续 31 天及跨月，双语、键盘和历史物品名称仍保留。六张组件代表图已目视检查，未当作真实 GUI、输入法或读屏验收。
- 两项新增真实 SIGKILL 回归通过普通测试、两轮 race 和 vet：宿主持锁进程被杀后，核验内核锁释放、旧 Runner 仍存活，新 host 先按身份终止旧进程、拒绝旧 lease 发布、仅执行一次新 attempt并重开核对结果；另一项在恢复 journal 已完成耐久发布而 SQLite 尚未还原时强杀，重试逐值核对 32 表、安全副本中的 WAL 数据及退休意图元数据。仅使用合成目录和经身份核验的自建进程，没有物理断电、还原实际写入中途强杀或 Windows 实机证据。
- 候选运行时不再只停留在临时 venv：唯一生产 manifest 增加显式 `candidateProfiles.security-candidate`，固定 Python 3.11.18 三归档和八个输入的大小/SHA。三锁仅规范化移除机器路径所在注释及空行，所有版本和哈希保持原样，FunASR wheel、补丁、recipe、provenance 和上游来源 SBOM 随包交付。准备工具、显式打包参数、CLI 与 host 已接线；默认 production 及旧 Tauri 生产锁不变。候选仅接受显式隔离可写目录，开库前拒绝生产目录、祖先/后代、大小写/符号链接别名及同一数据库 inode；输入只从确切可执行文件布局推导，失败不 fallback。
- 独立复审先红复现内部 generation 链接会写到生产缓存、验证后锁路径可变以及生产接纳候选模型缓存。最终实现逐级拒绝 runtime 目录链接，双向核验 managed artifact 归属；ready 标记绑定 profile/input digest。候选命令从已验字节建立随机私有输入快照并做前后校验，完成或失败均清理。该保护不承诺抵御同权限恶意进程的竞态。candidate pip 隔离配置、明确 index 和已验本地 wheel，保持完整哈希锁；其余依赖在线下载，不能称完整离线环境。managed 全包 race、vet 和三项红绿复现均通过。
- 安全工具为三平台完整依赖分别加入上游 FunASR 1.3.9 来源 SBOM，验证扫描返回确实包含所有包；保留现有例外及空例外两组结果。新增实际安装清单以 input digest 绑定，要求全部锁定版本一致、只允许额外 bootstrap pip，完成 pip check 后再扫描。已验证的例外字节复制成当前扫描私有快照，避免扫描源文件被替换后报告仍记旧摘要。永久脚本回归覆盖六次/八次调用、漏上游、installed 工具错误、漏洞阻断及策略快照；与输入/打包测试合计 17 项通过。
- 本机 ARM 首轮通过真正新版安装器的新建 Python 3.11 哈希安装、四固定模型下载/离线预热及原 Runner 两模式，关闭说话人为 1 段/disabled，开启为 6 段/completed。该轮 host 夹具把另一个目录的 artifact 导入临时 profile，按新归属保护被拒绝；保留失败日志后改为同一受管根内的独立合成 DB，没有放宽生产保护。最新源码重新编译复验通过：Runner 为 234.23 秒，82 个实际安装发行项收集及 pip check 为 17.21 秒，host 为 115.35 秒；真实创建、媒体授权、调度、转写、退出重开及每任务三奖励均核对。三锁和实际安装集分别扫描现有策略均退出 0，空例外均退出 1；Transformer 的 Torch 后端仍因要求 >=2.6 而禁用，成功证据仅适用于 FunASR 四固定模型，不推广其他模型。
- 整仓 `pnpm check` 退出 0：47 个前端、105 个 Python、259 个 Rust 测试，17 个默认忽略的专用 helper，另包含 Web、fmt、Clippy 与发布策略。首次 `native:check` 被基线指纹拦下，核对仅有已审查的 manifest 和 local_db 修改后由标准生成命令更新；25 路由、90 commands、8 capabilities 和 schema v10 不变。重新执行 native 检查、全包 race、停写后四目标 matrix 均退出 0。四个候选包已重建并经打包自身及主线程独立文件集合/大小/SHA、完整归档解包、SHA256SUMS、身份版本和架构复核；本机 ARM 实际 `--version` 为 candidate。三个受支持目标携带显式运行时输入，Windows x86 仅普通便携候选，不伪造 ASR 支持。产物继续未签名、未公证、`productionReady:false`，未执行 foreign 二进制或安装器。
- 完整统一 `native:compat` 最终退出 0，覆盖新增 216 步宠物核心（105.98 秒）、834 步全作物/岗位（368.96 秒）、礼盒/盲盒（327.90 秒）、商店、签到、原农场/打工、schema、Go→Rust→Go 恢复、DOCX 和原始/转义 Excel 双向互读；jobdb 整包为 937.369 秒。未删步骤、降低比较强度或以单端往返替代旧生产解析器。这些并发检查耗时不是性能基准。
- 本批证据位于忽略目录 `target/native/core-runtime-crash-{workspace-check,native-check-final,native-race,native-matrix-final,native-compat,candidate-acceptance-final}.log`、`core-runtime-crash-package-index.json` 及 `runtime-security/scan-VijrYG/summary.json`。原基线失败与首轮 host 夹具失败分别保存在 `core-runtime-crash-native-check.log`、`core-runtime-crash-candidate-acceptance.log`。940 个冻结输入复核仅有原生 README 和已审核的基线 JSON 两项后续变更。外置盘硬链接回退和 Web 大 chunk 提示仍在，本批未重新扫描未变的生产依赖锁。

本批关闭了宠物核心及已复现的 UI、进程恢复和候选运行时产品接线缺口。生产依赖锁未提升，既有漏洞例外仍会到期；最低系统、Intel/Windows 实跑、全命令/UI 原生验收、远端测试服务、完整性能、安装器升级回退与 P5-G 后续仍未关闭。旧 Tauri 生产入口保留，完整重构与默认切换尚未完成。

## 2026-11-20 窗口授权、人员 Excel 与桌宠诊断

- 继续按互不重叠写集协作，资源 UI、农场/打工真实差分、人员 Excel 与窗口权限分别整改；主线程处理资源授权、运行时交互、诊断页、正式文档及最终整合。后续桌宠窗口 seam 和日志异步化先转移写集所有权再修改；统一验证前各生产写集已停写。25 路由/90 commands 的方法映射只证明存在去向，不作为全部业务/权限验收通过依据。
- 窗口权限矩阵覆盖角色、来源、替换/关闭/伪造/空授权、绑定任务读写、文本/Word、子窗口和 AI 操作。真实临时 SQLite 核对改名持久化、导出、跨任务拒绝、已接受总结在窗口关闭后继续完成及删除后的 worker 发布围栏；保留父工作台换 job 不撤销独立子窗口的旧语义。读取前与读取期间取消不再委派后续读写或返回部分 SummaryState。定向 race 为 20 个顶层测试及 240 个权限矩阵子例，通过。
- 主线程继续用真实 SQLite 和凭据调用计数验证三类资源作用域：跨 ID/类型、旧新建草稿重放、删除后复用和关闭后操作无持久/凭据副作用。先红复现取消的新建、关闭后的新建、读取期间取消仍签发授权及关闭后仍能 Reset；签发末端在服务锁内复核生命周期和 context，Reset 同步检查服务关闭。资源 UI 的模板复制只从已保存来源读取，失败保留草稿；补齐模型帮助、列表 busy、准确刷新文案与长正文完整复制。双语键盘回归和代表图检查通过，未启动原生窗口。
- 人员 Excel 的发布链沿用同目录暂存、同步和目标身份复核，取消/损坏输入不进入导入，已提交导入的回读失败不重复写入。独立复审复现四个 namespace 红测，扩充后十二个失败子场景全部修复；从 workbook/relationship 到 row/cell/富文本按完整 XML 名称解析，保留普通、Strict 和无 namespace。真实双向 Rust 实测又证明 `_x0041_` 的字面值会变成 `A`，现读取单次解码、导出保护字面转义，并验证回车、代理对、孤立代理及重叠下划线。原始和转义工作簿均接入统一 `native:compat`，Rust 仅增加 test helper，不修改其生产解析。
- 农场/打工新增真实 Rust oracle 的 834 步，覆盖八种作物、三张地图全部九个岗位、394 次拒绝和 42 次 SQLite 写入故障，比较完整 API、十四表与每步三次重开；原 72 步及严格数字/ID 双射反例保留。先红修复 Go 领取会补写无关设置并规范化已有档案、收获列表擅自夹到 1–100 的差异。LP 两端及额外奖励分支均通过，定向联合 race 为 292.477 秒，不作为性能基准。正式宠物文档补齐当前八作物，原四作物表不再冒充当前目录。
- 90 commands 只读审计发现桌宠诊断导出有方法却未接日志源，现 host 默认提供严格枚举的有界源。先红修复提前取消仍读取、超限仍标记可导出；独立复审再推动日志从 UI 同步写盘改为单 worker 合并持久化，并移除全局 current-profile 路由。慢盘注入验证 Record/快照不阻塞于写盘，Close 排空并等待 worker，跨 profile 的迟到事件留在原所属源；不声称文件 Sync 可强制取消。host/真实文件与定向 race 通过；诊断页另验证取消、失败、成功、busy 和刷新不重发导出。
- 桌宠管理器增加最小原生窗口工厂 seam，真实调用管理方法验证启用失败无窗口、多开一次一例、重复 sync 不增殖、隐藏归零、重开不复活旧额外实例以及 close 排空最后位置；十项顶层回归连续五轮 race 通过。运行时页验证组件/source 参数、批量安装、活动/不支持/缺源禁用及失败后只读刷新，补齐可区分按钮名称和 alert/status/busy。代表图仅来自双语组件渲染，不替代原生窗口、IME、读屏或其他平台实机。
- 停写后的整仓 `pnpm check` 退出 0：47 个前端、105 个 Python、255 个 Rust 测试，16 个默认忽略的专用 helper；含 Web、fmt、Clippy 和发布策略。首轮 native 检查和全 race 发现旧设置布局测试仍按未区分的“下载”及旧 text role 查找，保留失败日志后将断言改为三个组件各自完整可访问名称及 status role，不删除边界断言。定向设置/运行时 race、重新执行的 `native:check`、全包 `go test -race ./... -count=1 -timeout=600s` 和四目标 `native:build:matrix` 均退出 0；最终 desktop 为 288.502 秒、host 为 89.600 秒，不作为性能基准。
- 统一 `native:compat` 退出 0，实际覆盖新增 834 步（354.95 秒）、礼盒/盲盒 362 步（363.31 秒）、商店、签到、原农场/打工、schema 和 Go→Rust→Go 恢复、DOCX 与两组人员工作簿。jobdb 整包为 807.571 秒，保留既有 30 分钟兼容时限；未用单端往返代替真实旧解析器读取。
- 四目标候选重新构建，打包自身逐文件/解包校验和主线程独立文件集合、大小、SHA、完整归档字节、SHA256SUMS、身份/版本及架构复核均通过。本机 ARM `--version` 实际返回 candidate，不访问资料目录。产物仍未签名、未公证、`productionReady:false`，Windows 仍为便携目录，未执行 Intel/Windows 二进制或安装器。584 个冻结输入只有正式 README 和上述设置测试的一次明确修订，末次校验与最终包对应的摘要保持一致。
- 本批整合证据位于忽略目录 `target/native/window-excel-gameplay-{workspace-check,native-check,native-race,native-compat,native-matrix}.log`、`window-excel-gameplay-package-index.json` 与 `window-excel-gameplay-inputs-verified.json`；首次旧标签失败保存在 `window-excel-gameplay-native-{check,race}-old-label.log`。专项证据包括 `resource-editor-{authority-read-before,reset-before}.log`、`window-resource-runtime-diagnostics-race.log`、`member-excel-final-race.log`、`member-escape-{go-red,rust-red,interop-green}.log`、`companion-lifecycle-final.log` 和 `current-pet-diagnostics-async-20261120-6PCADR/summary.json`。外置盘硬链接回退和 Web 大 chunk 提示仍存在；生产锁未变，本批未重新执行真实安全扫描。

本批关闭了已复现的授权、人员字符串互读和诊断接线缺口，并扩大了完整作物/岗位差分。生产安全、vendor 不可变分发/安装链、全 UI 与命令逐项验收、真实远端、其他平台实机、完整性能、安装升级回退和 P5-G 后续门槛仍未关闭，默认生产入口尚未切换。诊断已进入 worker 的文件 Sync 仍可能延长退出等待，不能据单元回归承诺磁盘永久阻塞时可及时退出。

## 2026-11-19 解释器、总结窗口与礼盒盲盒

- 三条互不重叠的任务分别处理 Python 验证器、总结窗口和礼盒/盲盒真实 Rust 差分；主线程负责 Go/Rust 安装器、生成资源、故障审查、正式文档与统一验证。三条写集均已冻结；差分暴露的新排序问题由主线程协调两端修复，随后重生成 oracle。未启动旧客户端、接触生产资料或切换入口。
- 新合成回归先复现安装器在三个平台适配中接受错误解释器、复用错误缓存并准备执行 pip。受管安装流程（含安装时复用该代缓存）现先用 `-I` 执行纯标准库 preflight，要求正式版 CPython 的完整 `X.Y.Z` 与原清单精确一致；依赖修复和最终验证仍绑定平台哈希锁。错误缓存从已验 SHA 的归档重新提取，新提取仍错则不执行 pip、不发布 ready。取消探测立即返回且不替换既有文件；显式系统 Python 保持原能力策略。本分支 Rust 使用相同顺序与验证器，未升级生产 Python、锁、模型或最低系统。只读复审另确认：正常启动 reconciliation 仍按路径及声明版本判断，不重跑此 probe；不能据安装验收声称能发现已 ready 的解释器被原地替换。Rust HEAD 可证实该策略早已存在，Go 尚未跟踪，无法仅据 Git 确认首次引入批次。
- 验证器定向 31 项 pytest、Ruff、真实隔离解释器 probe、managed race/vet、八项 Rust runtime 测试及 host/application race 通过。重新执行真实 `native:runtime:acceptance`：原固定运行时的四模型离线预热通过，Runner 与 host 各以合成中文语音分别运行关闭/开启说话人，两模式均为 1/6 段并为 disabled/completed；host 退出重开后结果和各任务三项奖励仍在。Runner 228.88 秒、host 234.14 秒，属验收耗时而非性能基准。该结果使用 macOS ARM 与系统合成语音，不是 vendor local2 的产品安装链，也不涵盖其他平台。
- 总结窗口修正旧版 980 DIP 单栏断点、长标题/配置名折行与完整复制、主题内加载/错误/重试、历史选中与列表语义。预览跨章节选择及屏外复制保留空白，Tab、选择器 Enter/Escape、生成、选中/最新失败记录分别重试及删除确认沿用原控制器和快照。普通与 race 定向回归通过；主线程查看双语明暗主题的六张 960/1120 DIP 组件图。保留 1120×860 默认、960×720 最小尺寸，未把组件截图当成系统窗口、输入法或真实读屏验收。
- 只读复审又复现三项既有总结缺口，已追加红绿修复：后缀避开原名/占位名，重名的第三项能按正确 ID 提交；顶部重试区分原参数操作失败与已接受操作的回读失败，后者只读、不重复生成，删除重试重新确认；历史无障碍名称纳入模型、状态、当前/最新标记及本地化错误，并用非敏感序号区分同分钟记录。轮询保留操作错误，忙/关闭拒绝重试，关闭清空闭包。追加写集仅 desktop/client 的四个 Summary 源码与测试文件，定向普通/race 回归通过；未改总结业务服务或授权边界。
- 礼盒/盲盒新增真实 Rust repository 生成的五组 362 步，覆盖 296 个加权区间端点、35 次拒绝和 16 次写入故障。每步严格比较完整 API、十一张持久表及三次重开，生成 ID 和礼盒补偿键的随机实体部分只做双射；金额、物品、时间、业务前缀、JSON 和双语事件保持严格比较。修复礼盒秒取模和奖池过滤、缺失/零库存错误、补偿来源和事件，以及盲盒时间盐值、边界、双语台词、事件与事务顺序。覆盖空奖、重复补偿、库存叠加、每日耗尽、跨日、相同毫秒连续开启和 Unix 纪元之前的时刻；不人为替换奖池或弱化抽取规则。
- 首轮 362 步 race 通过，联合复跑真实暴露同毫秒两笔补偿在 API 中顺序互换，持久业务值相同仍判失败。保留红证据后，两端经济流水与盲盒历史查询均在原时间排序后明确 `rowid DESC`，定义并列时后插入优先，不在比较器中排序或丢弃差异。新数据库回归用 27 条记录、25 条完全相同时间、逆序 ID、20 条截断及两种 SQLite 遍历方向复现并验证；Go 额外三次关闭重开、五轮 race 通过，Rust 在真实红回归后随整仓检查通过。扩充后的 race 包包含先前约 543 秒的礼盒/盲盒及原约 140 秒的整包工作，将统一兼容驱动的显式时限设为 30 分钟，不删步骤或断言；首次过小预算的运行主动中止并留存，再完整重启。最终统一 `native:compat` 退出 0，新的 362 步为 354.80 秒，jobdb 整包为 447.009 秒；新旧商店、签到、Go→Rust→Go 恢复和工作簿互读均通过。
- 本批跨层 `pnpm check` 退出 0，含 47 个前端、105 个 Python、255 个 Rust 测试及 13 个默认忽略的专用 helper，另包含 Web、fmt、Clippy 和发布策略。Summary 追加修复停写后，重新执行 `native:check`、全包 `go test -race ./... -count=1 -timeout=600s` 和四目标 `native:build:matrix`，均退出 0；最终 desktop 为 241.209 秒、host 为 63.813 秒，这些并发验证耗时不作为性能基准。兼容链已验证的新礼盒/商店、schema 与业务代码未随 Summary 追加改动变化，不重复同一长链。
- Summary 最后修复后再次重建四目标候选，打包自身解包校验及主线程独立文件集合、逐文件大小/SHA、归档完整字节、归档 SHA、身份/版本和二进制架构复核均通过；本机 ARM `--version` 实际返回 candidate，不访问资料目录。四包继续为未签名、未公证、`productionReady:false`，Windows 为便携目录；未执行 Intel/Windows 程序、安装器或新原生窗口验收。记录的 826 个可执行/测试输入仅出现两次明确修订：兼容时限及四个 Summary 文件，最终再次核对摘要未变。外置盘硬链接回退和 Web 大 chunk 警告仍存在，生产依赖锁未改，未用质量检查代替真实安全复扫。
- 最终整合证据在忽略目录 `target/native/interpreter-summary-box-workspace-check-final.log`、`interpreter-summary-box-native-compat-final.log`、`interpreter-summary-box-native-{check,race,matrix}-after-summary.log`、`interpreter-summary-box-package-index-after-summary.json` 和 `interpreter-summary-box-inputs-{final,verified}.json`。定向证据为 `managed-python-version-{before,final-race}.log`、`python-version-{rust-tests,host-application-race,managed-runtime-acceptance}.log`、`pet-box-differential-{iteration-1,final}.log`、`pet-history-order-{go,rust}-before.log`、`pet-history-order-go-after.log`、`current-cutover-python-version-integration-20261119T132346Z/summary.json` 与 `summary-parity/`。首次因扩充后的总时限而主动中止的统一链另保留为 `interpreter-summary-box-native-compat-initial-timeout-budget.log`，不计为最终通过。

生产安全整改和 vendor 不可变分发/产品安装链、全路由与命令/UI 逐项验收、真实远端、其他平台实机、完整性能、安装升级回退、签名公证及 P5-G 后续门槛仍未关闭。新候选保持隔离，完整重构与默认入口切换尚未完成。

## 2026-11-19 商店、结果页与受管候选构建

- 用户明确要求多个子代理协作后，按互不重叠写集分配结果工作台、商店真实业务差分、FunASR vendor 构建器三条任务；主线程核查凭据发布、审查产物与代表图、维护正式文档并整合。三条任务均已停写，再启动统一检查；未启动客户端或切换生产入口。
- 结果页修复最小窗口下长讲话人筛选溢出，保留搜索、导出、编辑和虚拟逐字稿区域。Escape/取消清空草稿并返回原编辑按钮，Enter 继续绑定原讲话人确认流程。补齐标题、busy/status/alert、选中筛选及虚拟条目位置语义，覆盖键盘搜索、Tab、Home/End 和长文本完整复制。定向普通/race 回归通过，主线程目视核对中文亮色与英文暗色 960 DIP 代表图；未据此声称原生 IME、真实读屏或系统保存对话框已验收。
- 商店用真实 Rust repository 和线程局部时钟生成 83 步，含 40 次拒绝、10 次事务写入故障、一次完全相同购买重放及跨日领取，每步对照完整商店 API、十张持久表并重开三次。先红后绿修复通用错误与原契约不同、免费领取数量处理、随机购买流水键、额外购买事件、缺失装备/食物双语事件、额外徽章授予、LP API 混入内部 `event_key` 以及同槽装备选择顺序。ID 只按实体双射规范化，新增反例拒绝身份漂移、交换、碰撞及事件值/元数据差异被掩盖。商店正常/race、Rust 三项 repository 测试、fmt 和 Clippy 通过，现有 Rust 总夹具入口已调用新模块；随机礼盒成功开启与盲盒抽取明确排除，没有人为同步 seed。
- 主线程的真实 SQLite 故障回归先复现凭据端口 `Set` 返回成功后，无回读就发布新引用。Go 资源服务现对模型与远端 Token 的暂存值执行 `Get` 和精确比较；缺失、变更、回读失败或取消时，不发布元数据、引用、设置 revision 或订阅通知，使用独立上下文清理暂存值。16 个模型/设置与立即/延期清理组合验证旧凭据保留、持久 rollback intent 和重开后的幂等回收。application/client/credentials race 通过，随机独立 Keychain 服务的真实保存、轮换、恢复和回收再次通过；未把注入故障解释为已发现 Windows 系统存储丢值。
- FunASR 元数据补丁从临时试验提炼为标准库构建器、固定 recipe 和 63 项自动回归。真实官方 PyPI 元数据的 URL、SHA-256、大小与配方一致；两次隔离构建复现原 local2 摘要 `38919720a30ab86428e2aded9a9e12cd9800cc36ebebc17f5b6b33182364f78e`，主线程独立核验全部五个输出、424 行 RECORD、417 个 Python 源文件和许可证不变。来源 SBOM 保留 `pkg:pypi/funasr@1.3.9`；发行版本与 RECORD 的记账变化明确记录，不用本地版本逃避扫描。实际构建限 macOS ARM/Python 3.12.11，其他压缩器输出不符固定摘要会拒绝；生产锁、清单、例外和门禁均未因此变更。持续使用说明在 `python/funasr-runner/vendor/README.md`。
- 停写后的整仓 `pnpm check`、`native:check`、统一 `native:compat`、全包 `go test -race ./... -count=1 -timeout=600s` 和 `native:build:matrix` 均退出 0。整仓为 47 个前端、75 个 Python、252 个 Rust 测试及 12 个默认忽略的专用 helper，包含 Web、fmt、Clippy 和发布策略；统一兼容日志实际覆盖新商店 83 步、原签到 83 步和 Go→Rust→Go 恢复互操作。全 Go race 的 desktop 为 405.605 秒、host 为 181.511 秒；这些并发验证耗时不作为性能基准。四目标候选已重新构建，打包自身解包校验通过，主线程另独立核对身份/版本、确切文件集合、逐文件摘要、归档摘要和全部归档字节；继续标为未签名、未公证及 `productionReady:false`，未执行其他目标平台程序或安装器。
- 本批最终整合证据为忽略目录 `target/native/parallel-refactor-{workspace-check,native-check,native-compat,native-race,native-matrix}.log` 与 `parallel-refactor-package-index.json`，不使用此前批次的绿色日志替代。定向证据为 `credential-publication-{before,after,resource-race,system-acceptance}.log`、`pet-store-differential-{red,red-ledger,final}.log`、`result-parity/` 及 `current-cutover-security-vendor-20261119T124252Z-22893/summary.json`。外置盘硬链接回退和 Web 大 chunk 提示仍存在；本批没有重扫未变的生产依赖锁，不把 `pnpm check` 或来源 SBOM 当成真实安全扫描。

本批没有建立 vendor 的不可变分发和产品安装链，也没有重新宣布生产安全通过。此前真实扫描的 Python 阻断、2026-11-30 例外期限、全路由/命令与 UI 验收、Intel/Windows 实机、真实远端、完整性能、安装升级回退和 P5-G 后续门槛仍保留；完整重构与默认生产入口切换尚未完成。

## 2026-11-19 恢复互操作、纪要与最终整合

- 用户授权后，三条互不重叠的并行任务分别完成 Rust 恢复记录导入、只读纪要可访问性和真实安全/许可证复核；主代理处理 Go 迁移重试、互操作夹具、代码审查和最终整合。各工作线停写后才执行最终跨层检查，没有启动客户端、删除兼容实现或切换生产入口。
- 新回归先复现 Go v3 重试会覆盖快照仍需保留的既有 journal。修复在同一事务读出并合并旧条目，再持久提交本次暂存意图；发布与版本升级保持原事务边界。回归验证升级、关闭重开及移除合成快照后的回收，不重新暂存现用凭据；既有 `entries:null` 格式继续兼容。本分支 Rust 迁移器采用相同 journal 和合并原则。
- 恢复覆盖数据库前，先保存经验证的 `pre-restore` 安全副本，再同步、原子发布只含数据库文件名和凭据引用的版本化外部记录。Go 可写入口和本分支 Rust 迁移器均在 v10 导入非现用引用，保留既有清理意图元数据；旧 schema 的记录保留到明确升级后。FULL 提交、WAL checkpoint 和主库同步全部成功后才移除记录；即使导入未新增行也要执行 checkpoint，不能用 FULL 空提交冒充旧 NORMAL WAL 已耐久。坏记录、链接、错库、checkpoint 受阻及同步/发布失败保留重试依据。恢复执行后才发生的错误仍可能已改变数据库，不承诺所有错误都保持原字节。
- host 恢复回归对照 32 表，覆盖模型/Token 轮换、恢复与重开、多个安全副本、最后相关副本移除后的幂等回收，以及仅存在于已提交 WAL 的数据。`native:credentials:acceptance` 在 macOS ARM 的随机独立系统服务实际通过保存、接管、轮换、SQLite 恢复、重开、快照保留与最终回收，不访问用户的 `LibertyDesktop` 条目；这次证据不再只有内存适配。额外编译了带验收 tag 的 Intel Mac、Windows x64/x86 凭据测试程序并确认文件架构，未执行它们。
- 统一 `native:compat` 新增 Go 实际 schema/侧记录写入器 → Rust 启动迁移器 → Go 回读的真实链路，核对现用引用不退休、既有意图完整元数据不变、新引用导入为退休意图和重复恢复幂等。独立夹具与统一脚本内新建的夹具均通过，未拿手写 JSON 的单端测试代替互操作。
- DOCX 修复保留 Rust 属性限定名、有效 namespace 绑定及 Word 文本纯空白；Go 比较器按 URI/名称/值严格比较。18 个同输入 DTO 差分通过，三个代表输入用打包 LibreOffice 复验，两端分别为 2、2、1 页，五对整页差异像素均为 0。主代理复核源码摘要及代表页面，Go 生产导出代码和模板未因该修复改变。结论不涵盖其余输入的视觉、Word/WPS、Windows 宋体或客户端保存对话框，详见[视觉记录](../research/2026-11-19-native-docx-visual-acceptance.md)。
- 只读纪要补齐跨章节选择和屏外内容复制、摘要空白、Tab/Home/End 阅读路径、标题/列表层级及 busy/status/alert 语义；修复首次读取失败后的重试短暂显示“未找到”。双语、明暗主题、原最小窗口及更窄的组件压力回归通过，主代理查看中文暗色和英文亮色代表图。正文原本已真实折行，窗口尺寸与旧版一致，没有据此重做排版或声称完整原生读屏已验收。
- 停写后的 `pnpm check`、`native:check`、`native:compat`、全包 `go test -race ./... -count=1` 和 `native:build:matrix` 均退出 0；整仓含 47 个前端、12 个 Python、252 个 Rust 测试及 11 个按标记忽略的专用用例，包含 Web、fmt、Clippy 和发布策略。全 Go race 的 desktop 为 277.779 秒、host 为 40.614 秒。四目标候选重新构建并通过归档解包和逐文件 SHA-256，主代理另独立复核实际输出的文件集合、归档哈希及 manifest 身份/版本/未签名/未公证/`productionReady:false`。证据为忽略目录 `target/native/current-cutover-{workspace-check,native-check,native-race,native-compat,native-matrix}.log`、`current-go-rust-restore-fixture-{generate,verify}.log`、`current-recovery-system-credentials-final.log` 和 `current-cutover-package-index.json`。外置盘硬链接回退及 Web 大 chunk 警告仍存在。
- 固定扫描器真实复扫：`pnpm security:check` 退出 1，主脚本在首份 Python 锁停止；后两份独立直扫同样退出 1。三平台包数为 69/69/70，每平台当前例外下为 54 组漏洞/75 条 OSV 记录，不带例外为 85 组/113 条记录；跨平台重复问题不相加。31 项 Python 例外均于 2026-11-30 到期。Rust、pnpm 和 Go 的既定锁扫描通过，Rust/Go 许可证门禁通过，17 个锁定 Go 模块归集 50 份 notices。额外 Go 源码分析因固定扫描器不兼容 Go 1.27 退出 127，不能记为分析通过。24 项关键生产输入哈希前后一致，没有升级、扩大例外或削弱门禁；证据位于 `target/native/current-cutover-security-20261119T120924Z-8248/summary.json` 及同目录 `report.md`。

本批关闭了已复现的迁移 journal 覆盖、恢复覆盖清理意图和 Go/Rust 外部记录互操作缺口，但完整重构仍未完成。安全修复、真实远端服务、Intel/Windows 实机、完整 UI/IME/读屏、CPU/内存/延迟测量、安装升级回退、物理断电与全部崩溃时点、签名/公证以及 P5-G 后续工作仍未据此验收。候选包未发布，默认生产入口保持原状态。

## 2026-11-19 读取、恢复与导出专项（此前批次）

- 按用户授权拆分互不重叠的 DOCX 视觉验收、host 恢复回归及 Rust 凭据清理，主代理负责 Go 生产修复、读取优化和整合。未启动旧客户端，未删除兼容实现或切换生产入口。
- 读取第二批保持严格 JSON 和数值转换语义，空数组及默认导出格式减少无效分配；逐字稿在同一事务计数，初始容量上限 65,536，不限制返回行数。65,537 段、nullable、独立投影及真实 SQLite 转换回归通过，严格 JSON fuzz 10 秒执行 97,013 次。四对固定 200 次基准相对首批版本的分配字节中位数再下降：队列 8.5%、逐字稿 55.9%；这是局部累计分配，不代表整应用内存、全部场景延迟或跨平台性能通过，详见[分配复核](../research/2026-11-19-native-read-allocation.md)。
- 公共 host 无凭据资料与注入内存凭据适配的恢复演练分别记录：任务、结果、总结与纪要、人员、模板、设置、宠物及玩法流水共 32 表对照；含提交仅在 WAL 中的新数据仍保存在 `pre-restore` 安全副本。使用内存适配的凭据成功演练不是 Keychain 或 Credential Manager 实机集成。
- 演练复现正常模型/Token 轮换会删除旧恢复点所需凭据。Go 资源清理现检查当前与同目录可恢复快照的引用；快照保留时不删系统凭据、不删除退休意图、不增加失败次数，移除最后相关快照后才能回收。损坏、链接、未知 schema 或读取失败会阻止删除；v1/v2 已有引用也受保护，恢复预检不再仅看版本是否至少 v3。外部丢失或撤销凭据仍拒绝恢复并保留当前业务库。
- Go v3 迁移日志同样保留快照需要的旧引用。正常备份延期不把已提交保存报告为失败。首次接管快照另受 SHA 标记约束，删除它仍阻止再次 `OpenHandover`；回收测试显式区分仅清理生命周期的 `Open`，没有据此绕过接管门禁。
- 该批 Go 检查、最终 Reader 对真实 Rust 合成库的差分及凭据边界定向回归通过。当时仍待整合的 Rust 与 DOCX 修改现已由上节最终整合验证覆盖，早期并发日志不作为最新完成依据。

该批发现的“恢复旧库覆盖当前清理意图”及后续退役问题已由上节外部记录协议和生命周期回归整改；这不代表安装器、物理断电、强杀/崩溃全部时点或跨平台恢复已经验收。安全和切换门禁继续有效。

## 2026-11-19 业务差分与真实宿主执行

- 用户授权并行后按互不重叠文件范围整改地图交互、任务新建/详情、签到差分与运行时安全候选；主代理负责生产签到/播种根因、模型路径接线、跨来源回调及整合验证。没有启动旧 React/Tauri、删除兼容代码或切换生产入口。
- 签到按真实 Rust repository 对照发现并修复：返回的宠物成长值滞后一笔、事件把 LP 当成长值、连续第 15 天后重复前 14 天里程碑、补签预览/拒绝原因和多余自动解锁副作用。普通回归覆盖 21 天及补签；真实差分共 83 步，含 25 次重复、6 次拒绝、2 次触发器失败，每步三次重开并比较 API 与十张表。Rust 时钟只在测试中以线程局部 RAII 生效，UTC 与本地日期来自同一时间戳，覆盖嵌套、unwind 和线程隔离；Go 不改全局时区。新 ID 按实体类别维护跨步骤双射，JSON 数值保留 int64 精度，注入错误仅在双方确认命中对应触发器时归类，未放宽正常业务差分。
- 播种不再额外初始化宠物及商店，避免只消费种子却刷新默认宠物时间戳；回归先在原实现复现，再验证只修改目标种子和地块。农场/打工的真实 72 步旧端差分已在本批统一 `native:compat` 中通过，签到 83 步和重开对照也一并通过。
- 新建成功后固定打开本地详情，详情轮询绑定原始来源，不受队列随后切换远端影响。异步重试/删除的完成回调携带发起来源，同 ID 的另一来源详情不会被关闭或刷新；缺少远端端口时拒绝操作，不能回退本地。热词的显式清空与迟到默认读取、运行时失败后的旧 ready 快照、详情读取失败及本地/远端重试条件有定向 race 回归。
- 远端专项继续检查来源、同 ID 碰撞、能力撤回、详情/结果端点和提交阻断；定向 application/client/remote race 通过，未发现需要扩大修改的可复现缺陷，不等于真实远端已经验收。本机专用验收所需的三个环境变量均未配置。
- 候选启动专项复现非法 `--restore-backup` 文件名过晚拒绝的问题，现于参数解析时拒绝跨目录、驱动器/ADS、异库前缀、错误后缀及 NUL，避免进入资料解析和加锁链。36 个案例先复现失败、修后通过；CLI/profile/legacydb 定向 race 通过，preview/candidate、显式生产选择、快照只读及 writable/prepare/restore 互斥保持原有边界。
- 四个模型目录从当前选中 artifact 与同源固定 revision 清单解析，预热和真实转写共用。子进程前检查绝对路径、选中根目录所属、目录存在和越界符号链接，显式覆盖继承的模型别名。原生产 Python 3.10 固定运行时离线预热与 Runner 两模式实际通过（272.103 秒）；新增 opt-in 宿主验收通过真实媒体授权、host 装配、调度和 Runner 得到 1/6 段逐字稿，关闭后重开资料库，结果/说话人段与每任务三项奖励保持一致（186.666 秒）。两者均使用合成中文录音，不是 GUI、生产资料接管或安全候选 local2 的产品交付验收。
- 四地图补齐侧栏偏移下热区滚轮/捏合坐标、HUD 手势阻隔、焦点避让、键盘重复和已提交后读取失败状态。窄屏 HUD 按原 CSS 区分农场 760 DIP、打工 860 DIP 断点及宽度/留白，保留原缩放拖动断言；双语四地图组件手势回归与完整 desktop/client race 通过（236.131/1.072 秒）。主代理核对了四地图代表组件图；这不等于原生触控板或完整旧端像素对照。
- 队列与逐字稿读取仅复用查询内扫描存储，逐行重置 nullable 字段并保留独立切片；同设备三对交替基准的中位分配字节减少 32.2%/13.7%。延迟受并行负载影响且逐字稿中位回退，不能声称延迟、整进程内存或 ASR 优于 Rust。详见[分配复核](../research/2026-11-19-native-read-allocation.md)。
- 完整 `native:bench:reads` 已再次执行通过。该批队列 P50/P95 为 Rust 2.610/2.867 ms、Go 4.614/5.825 ms；逐字稿为 Rust 3.708/3.872 ms、Go 7.777/8.328 ms。两项 Go 仍较慢，分配改善不能替代完整延迟、CPU 和内存验收。已确认设计要求同场景测量与可接受的资源表现，没有另加“所有 Go 查询必须快于 Rust”的门槛。
- 安全线已补全 ARM 全新 Python 3.11.18 环境、最终 local2 锁哈希安装、原四模型预热与两种转写证据，主代理重算 19 项证据摘要。但 Torch 的 20 组漏洞/24 个公告和 2026-11-30 到期例外仍未解决；候选未写入生产清单或锁。产品安装/vendor 交付、最低系统、Intel/Windows 与回退仍未验收，详见[隔离调研](../research/2026-11-19-native-runtime-security-candidate.md)。

整合首次检查发现宿主合成夹具只有空模型根目录，新路径契约据此拒绝执行；已按同源四模型 pins 补齐目录，并在真实合成子进程中断言五个模型环境值覆盖了父进程别名。该宿主定向 race 及重跑的统一 `native:compat` 通过。另修正 Rust gameplay 夹具的索引迭代 Clippy 错误，未增加忽略规则；最终 `pnpm check` 通过，包含 47 个前端、12 个 Python、214 个 Rust 测试与 10 个默认忽略专用用例，以及 Web 构建、fmt、Clippy 和发布策略。外置盘硬链接回退和 Web 大 chunk 警告仍存在。`pnpm check` 不是真实漏洞扫描。

停写整合后的 `native:check`、`go test -race ./... -count=1` 与 `native:build:matrix` 全部通过，全量 race 的 desktop 包为 268.338 秒。重新生成四目标候选包，逐文件和解包校验通过；主代理又按本批实际输出路径核对四份 manifest 的候选身份、版本、`productionReady:false`、未签名/未公证标记和归档 SHA-256。四目标仍只是编译与归档证据，不是 Intel Mac 或 Windows x64/x86 实机验收。没有启动旧客户端、提交、推送、发布或切换默认生产入口。

完整重构仍未完成，真实远端服务、四平台实机、全量 UI/IME/读屏、完整性能、安装升级回退及安全门槛继续独立关闭。

## 2026-11-19 并行整改与设置关闭集成

- 用户明确授权后，按互不重叠范围并行处理结果/总结/纪要、宠物与福利页面、运行时安全升级隔离验证、设置启动覆盖及保存协调。主代理复核并集成代码；安全试验限定忽略的 `target/`，不改生产依赖锁、清单、例外或门禁。没有提交、推送、发布或启动旧 React/Tauri。
- 结果工作台补齐首次加载、读取失败和缺失时的关闭入口；任务或来源变化会清理上一任务的讲话人草稿、筛选和列表状态。讲话人输入自动获焦，导出使用真实进行中状态并阻止重复操作，确认期间授权替换或关闭后的回调不能继续写入。总结区分操作与读取失败，瞬时读取错误恢复后清除；已接受生成但回读超时后可刷新恢复选中，不重新生成。失败记录按原运行 ID 重试，无新结果时仍预览已保存纪要；运行中的记录可按原确认语义删除。纪要加载或失败不再伪装为没有总结。
- 宠物商店按实际内容区计算商品列宽，窄窗主区使用自然高度，避免概览覆盖商品详情按钮；原点名的详情按钮测试保留并通过。数量输入检查余额、库存和每日次数，补齐购买总价、成长预览与键盘确认。签到、补签、里程碑、空奖名称及今日盲盒记录实际时间正序与旧端对应。操作已经提交而后续刷新失败时保留成功结果并显示读取错误；新兑换前清理旧结果。减少动态效果采用静帧；本批没有修改宠物经济、数据库或桌宠平台调度。
- 启动 `--locale` / `--theme` 通过同一个显式 `StartupPreferences` 接入窗口和 SettingsController，不再被首次异步设置读取覆盖。显示投影和持久化草稿分离，无关保存、刷新和 revision 冲突不写入会话覆盖；用户主动选择对应字段才解除该字段覆盖，保存途中重选仍保留最新意图。整合回归发现点击当前已保存选项会新增一次多余写入，已修复干净且没有覆盖时的 no-op，保留原键盘测试和覆盖值/原值显式选择测试。
- 正常关闭先等待设置 `Flush`，包含最后仍有焦点而未触发失焦保存的输入，以及保存途中新增的编辑；成功后才进入资源编辑器统一预检。失败、加载失败、冲突耗尽和取消都会释放退出 pending，保留草稿；失败同时撤销本次全局资源关闭批准/待决确认，防止单独编辑器随后永远无法关闭。取消全局尝试不影响无关单窗确认，晚回调不能恢复已撤销授权。OS 会话终止仍遵循不可取消边界，不把普通退出保存保证泛化到强杀或注销。
- macOS ARM 独立合成资料实机：新建默认 `zh-CN` / `auto` 资料，带 `--locale en-US --theme dark` 启动，异步读取完成后外观仍显示 English / Dark。默认总结模板输入合成文本后保持焦点，直接 Cmd+Q 正常退出；离线 `integrity_check` 为 `ok`，最后输入已保存，库中语言/主题仍是 `zh-CN` / `auto`，没有偷偷持久化覆盖。原生截图核对了英文深色外观设置。用户正在使用的候选实例未重启，旧生产实例保持未运行。

- 安全线取得明确本地模型路径下的 ARM 补丁候选实跑证据：原 Runner 关闭/开启说话人得到 1/6 段，协议、关键词与完成进度断言通过。但三候选锁仅在现有例外下扫描通过；无例外仍有 Torch 20 组漏洞/24 个公告，例外于 2026-11-30 到期。候选还需 vendor/上游交付方案、全新安装、默认模型路径、最低系统与 Intel/Windows 实跑，未写入生产清单或锁。九个生产输入与入场 SHA 一致，由主代理重算复核。详见[隔离调研](../research/2026-11-19-native-runtime-security-candidate.md)。

最终整合后的 `native:check`、`pnpm check`、`native:compat`、`native:build:matrix` 全部通过；整仓为 47 个前端测试、12 个 Python 测试、213 个 Rust 测试及 9 个默认忽略的专用用例，包含 Web、fmt、Clippy 与发布策略。`go test -race ./cmd/liberty-native ./internal/client ./internal/desktop ./internal/host -count=1` 通过，desktop 为 151.079 秒。四目标候选包重新生成并通过架构、归档解包及逐文件哈希验证，均保持未签名/未公证与 `productionReady:false`。双语宽窄组件截图重新生成，主代理检查了窄窗商店、签到和深色设置代表图。既有外置盘硬链接回退与 Vite 大 chunk 警告保留。

本批不代表全量重构或生产切换已经通过。组件截图不等于完整原生差分；用户运行的候选实例未重启，最新产物已经生成但未替换其进程。Windows/Intel 实机、完整读屏/IME、真实远端、安装升级回退、签名/公证及当前安全阻断仍未关闭。

## 2026-11-19 多窗口关闭预检与退出收尾

- 审查发现主窗口原先在 `OnClosed` 中先取消控制器，再依次请求关闭资源编辑器；子编辑器此时才弹未保存确认，取消后主窗口和服务已经退出。现由主窗口 `OnClose` 与应用 `OnBeforeQuit` 共用关闭协调器，在销毁任何窗口前统一检查全部资源编辑器。保存、复制、重置或删除中的编辑器阻止普通退出并获焦；未保存修改先统一确认，取消保留所有窗口、输入及服务。
- 单个编辑器关闭仍限定自己的确认和待切换目标，不能用单窗确认授权退出整个应用。重复关闭/退出只保留一份对话框；其他编辑器的切换请求不会遗留成稍后误执行的切换。确认期间与恢复退出事件之前均复核草稿、原始排序、凭据意图、保存代次及窗口集合，变化后原确认失效，不据此丢弃新修改。
- 关闭确认使用当前语言，明确区分单窗和全部编辑器，标记放弃修改为破坏性操作，取消是 Enter/Escape 的默认动作。OS 注销/关机遵循 MyGo 的不可取消会话终止语义，不启动异步确认；`OnQuit` 同步收尾 host，保持后台服务和数据库关闭先于资料锁释放。强杀、系统终止以及未保存表单仍不能视为自动恢复已验收。
- macOS ARM 独立合成资料实机验证：同时保留人员和模板两份未保存草稿，应用退出与关闭主窗口均先显示统一确认，取消后主窗口和两个编辑器继续存在，两个草稿仍可保存；保存后关闭主窗口能正常退出。重开该资料后又在英文深色界面修改人员排序，Enter 与 Escape 均取消退出且保留排序 17，之后保存并正常退出。离线 `integrity_check` 为 `ok`，人员、部门、排序 17 与自定义模板仍在。没有使用生产资料、启动旧 React/Tauri 或重启用户正在操作的候选实例。
- 关闭协调、忙碌拒绝、重复请求、授权范围、确认过期、窗口集合变化、取消后保存及会话终止分支有回归。`native:check`、`native:compat`、`native:build:matrix` 及 `go test -race ./cmd/liberty-native ./internal/desktop ./internal/host ./internal/client` 通过；最后改用真实 ResourceEditor 基线的关闭回归也在 race 下通过。整仓检查首次在既有 Rust 进程超时测试的三秒断言失败；该用例单独复跑以 0.48 秒通过，随后完整 `pnpm check` 重跑通过（Rust 213 passed、9 ignored，包含前端/Python、Web、fmt、Clippy 和发布规则）。未修改时间限制或实现，既有外置盘硬链接回退与大 chunk 警告保留。整仓检查不替代真实供应链扫描。

本批只补齐上述生命周期缺口，不代表全部 UI/业务、其他平台或生产切换通过。原安全、远端、实机、安装升级回退与完整差分门槛继续有效；旧生产入口和代码仍保留。后续并行整改按互不重叠的文件范围执行，并由主代理统一集成和验证。

## 2026-11-19 资源管理、编辑保护与模板复制校准

- 对照原 `resource-management.css`、三类管理/编辑页面及 `windows.ts`，修正模型和模板卡片把整窗宽度误当内容宽度的问题。按实际侧栏、内容留白、320 DIP 最小列宽和 16 DIP 间隔计算 Grid，末行不再单独拉伸；1440/1100/760 DIP 分别验证为三/二/一列，720 DIP 同步原版标题纵排及两列指标。恢复独立的启用/默认/内置标签、卡片标题和详情字号、两行模板说明及含年份的更新时间。默认模型指标优先选启用项，部门数按 trim 后的非空值去重。
- 人员表沿用 260/240/110/150/170/160 DIP 六列及 1090 DIP 最小宽度，补齐表头、行列位置、圆形姓名头像、角色标签和右侧固定操作；横纵组合滚动后操作仍对应原行。资源卡片与人员行支持双击和聚焦后的 Enter，长按 Enter 不重复打开；编辑按钮具有包含资源名称的可读标签，点击/键盘操作不会冒泡为额外打开。首次加载与读取失败不再冒充空列表，主窗口重新获焦时刷新当前资源。
- 三类编辑器最小尺寸分别校准为模型 760×680、模板 820×720、人员 640×560，初始尺寸保持原版；回归直接读取旧 `windows.ts` 验证尺寸来源。语言修改同步已打开窗口的标题、表单和后续确认框。字段标签关联输入框，补齐原占位提示、首错校验、无效状态和聚焦；内置模板保持只读和复制引导。
- 排序原始输入移入 ResourceEditor 状态，保留清空、非法数字及 int64 越界，不再每帧被最后一个整数填回。保存前验证，合法值才进入持久化草稿；无效原始输入也参与关闭与重置的 dirty 判断。取消重置保留输入，成功保存/重置后同步原始值和清洁基线。覆盖 int64 最大/最小值、空白修剪、非法输入、不写入和取消路径。
- 继续对照发现旧版“复制模板”会立即保存，新版原先只生成未保存草稿。现从授权 ID 的已保存源生成独立副本并立即持久化，成功后刷新管理页并将编辑 grant 转到副本；忽略未保存的源表单内容，不覆盖内置原件。失败、取消及伪造 ID 不改变原 scope。应用测试覆盖失败/重试和授权转移；真实 SQLite 测试注入 INSERT 失败，再重试、关闭并重开库，确认副本已保存且原件不变。
- macOS ARM 独立合成资料的原生窗口实际验证：人员排序清空后保持空值，保存报错并回焦，取消关闭/重置保留表单，修正为 17 后保存并在列表回读；内置模板只读，复制后无需另按保存，列表从 5 项变为 6 项并出现一个可编辑副本。保持编辑器打开并在主窗口切换为英文，确认窗口标题、字段及后续关闭确认均为英文；取消确认保留修改，把开关恢复原值后可直接关闭。验收程序正常退出后离线 integrity_check 为 ok，仍有一条排序 17 的合成人员及 5 个内置/1 个自定义模板。当前用户使用的候选窗口没有被重启，旧 Tauri 客户端没有被启动。

最终 `pnpm native:check`、`native:compat`、`native:build:matrix`、四目标 `native:package` 的构建/归档解包/逐文件哈希检查通过；`go test -race ./internal/application ./internal/client ./internal/desktop ./internal/infrastructure/repositories/jobdb` 通过。`pnpm check` 通过：47 个前端测试、12 个 Python 测试、213 个 Rust 测试及 9 个默认忽略的专用用例，包含 fmt、Clippy、Web 和发布策略检查。最后的 Go 应用层调整由上述最终原生检查、真实 Rust 夹具兼容及 race 覆盖。`git diff --check` 通过，既有外置盘硬链接回退和 Vite 大 chunk 警告保留。

本批截图验证的是原生组件渲染；原生窗口验收仅限上述合成流程，没有新增模型真实凭据、Excel 文件对话框、输入法、完整读屏或新旧同数据截图差分证据。Windows x64/x86 与 Intel Mac 仍只有编译和候选归档验证，未做实机运行、安装器、签名或公证。未重跑真实漏洞扫描，生产安全门禁、远端真实验收及其余全量切换条件仍未关闭；候选继续标记 `productionReady:false`，旧生产入口及代码保留，完整重构继续执行。

## 2026-11-19 队列语义与设置七分区校准

- 队列改为同一原生表格行内的数据及右侧固定操作单元格，横向滚动保持操作可见，纵向滚动保持操作与对应任务对齐。补齐十列表头、分页后的全局行号和单元格位置；工作台最近任务表也补齐行列语义。Tab 从当前任务到同行操作，点击其他任务结果按钮不误改当前选择；远端没有详情读取权限时不禁用独立获准的删除操作，处理中没有可执行菜单项时不显示空菜单。进度条数值与绘制统一限制在 0–100，无效浮点值归零。
- 设置页审查发现当前 `App.tsx`、`SettingsView.tsx` 和 `SettingsView.css` 已调整，原指纹检查因此拒绝通过。保留这些当前工作区变化，以其标题、说明、侧栏和表单层叠为对照，同步原生七分区：248 DIP 侧栏、760 DIP 顶部导航、1180 DIP 表单堆叠、底部页脚、选中指示、主题及玻璃渐变预览和强调色点击区域。运行时、远端与诊断共用表单行布局，没有改变其业务服务、凭据权限或持久化契约。
- 设置搜索同时匹配名称和说明，修剪首尾空白、忽略大小写，隐藏空分组并提供无匹配状态。当前分区使用页面语义，主题/玻璃/强调色使用可读名称与 pressed 状态，键盘选择仍通过 SettingsController 保存。运行概览在运行时刷新失败后撤销就绪文案，避免把过期快照当作当前有效结果。
- 设置回归覆盖七分区 × 中英文 × 1440/1100/760 共 42 组布局，以及搜索、横向导航、缺失能力、键盘保存、大小写颜色选择、页脚和运行时失败状态。队列回归覆盖固定操作的横纵组合滚动、分页行号和远端独立权限。PNG 仅输出到忽略的 `target/`，已检查代表性的宽窄与深浅截图；本批没有接管正在操作的客户端、访问生产资料、做新的原生读屏/输入法验收或启动旧 Tauri 客户端。
- 审查并同步上述三处 UI 指纹，同时把设置导航定义加入迁移基线输入。清单仍为 25 路由、90 command、8 窗口 capability、schema v10；没有跳过漂移检查、删除旧入口或放宽切换门禁。默认生产入口及未满足的全业务、平台、安全与发布退出条件仍见下方实施顺序。
- 根 `AGENTS.md` 已把本次重构的启动约定改为优先激活已运行的 Go/MyGo 候选，重建后按实际输出的确切应用路径启动；仅在明确要求旧版时使用 Tauri 开发命令。同步记录本次任务不使用子代理；此约定没有改变生产构建入口或删除旧代码。

本批 `pnpm native:check`、`go test -race ./internal/desktop ./internal/client`、最后控件尺寸调整后的设置定向 race、`native:build:matrix`、四目标 `native:package` 及解包哈希复核通过。最终 `pnpm check` 通过：47 个前端测试、12 个 Python 测试、213 个 Rust 测试及 9 个默认忽略的专用用例，包含 Web 构建、fmt、Clippy 和发布策略。整仓检查首次因本地缺少 `tsc` 启动脚本中断，按固定 `pnpm@10.30.3` 执行 `pnpm install --frozen-lockfile` 修复安装后重跑通过，没有升级依赖或修改锁定版本；外置盘硬链接回退与现有 Vite 大 chunk 警告保留。`git diff --check` 通过。

四目标产物仍是未签名、未公证且 `productionReady:false` 的候选，Windows 为便携目录；编译和归档验证不代表其他平台实机或安装验收。本批没有重跑真实漏洞扫描、`native:compat` 或原生 GUI/读屏验收；真实远端验收所需 URL、系统凭据引用和合成任务 ID 在当前环境均未配置。已有 Python 供应链阻断、全业务/UI 差分、跨平台、升级回退及生产切换退出条件继续有效，完整重构仍在执行中。

## 2026-11-19 工作台、市场与设置导航校准

- 依据当前 React 页面及最终 CSS 层叠调整市场：总览图限制在容器内，地图封面保持 4:3，四列/两列/单列沿用原窗口断点；补回空闲、照看、可收获和未开放徽章及独立统计项。未开放和无目标路由的卡片实际禁用，提供双语状态与无障碍说明。实机发现首张卡片因文案较短而被垂直居中，已固定从顶端布局，并加入封面顶边断言；锁定卡片避免叠加两次禁用透明度。
- 工作台恢复运行时、模型、模板、人员、伙伴、LP、签到和待处理的八个入口，并按导航能力禁用不可用操作。运行时就绪值由 Workspace 的运行时概览和错误状态传入，不能在缺少验证时显示就绪；完整值也通过提示/无障碍说明暴露。远端概览显示快照标记，隐藏本地资源和伙伴区块。趋势图补齐 132px 绘图区、零基线与网格，正值最小可见高度、24px 柱宽上限、空态以及分钟/任务单位；单点与零值不虚增柱宽或高度。
- 设置返回记住进入前的应用页面，包括已完成队列筛选；离开时清空设置搜索。原本从新建页进入设置后若改为远端模式，返回首页，避免重新打开不支持的本地上传。
- 合成组件测试覆盖中英文、深浅主题、1440/1100/980/760 窗口布局、八个入口、禁用能力、运行时状态传播与错误降级、远端本地区块隔离、单点/零值/单位换算和设置返回。市场另覆盖四/二/一列封面比例、总览图边界及安全错误文案；PNG 仅写入忽略的 `target/`。已检查代表性宽窄窗口截图，不能据此代替全页面新旧同数据截图差分或真实读屏验收。
- Apple Silicon 实际候选窗口确认工作台资源/伙伴入口显示、人员库快捷入口、从人员库进入设置后返回原页、市场总览图边界和状态徽章。最后一次顶边修正已通过组件几何与截图检查，重新打包并启动最终候选；实机复核期间窗口切到盲盒，未再覆盖其当前操作，故不将最后顶边修正记为已完成原生窗口截图复核。旧 React/Tauri 进程及残留 Vite 已停止，本轮只启动独立资料库的 Go/MyGo 候选。

本批 `pnpm native:check`、`go test -race ./internal/desktop ./internal/client`、最后布局修正后的市场/工作台/设置返回定向 race、`native:build:matrix`、`native:package` 与归档哈希复核通过。四目标主程序/测试包编译及文件架构检查通过，不代表 Intel Mac 或 Windows 实机运行。一次资源生成器的 `gofmt` 等待输入未结束，保留诊断后终止该检查；独立生成检查及随后完整 `native:check` 重跑通过，没有跳过生成漂移检查或放宽门禁。本轮未修改持久化协议/依赖、未重跑整仓 `pnpm check`、真实漏洞扫描或 Rust/Go 资料库差分；原有供应链阻断、其他平台实机/安装升级回退与全业务/UI 退出条件仍有效。未提交、推送、发布或切换默认生产入口。

## 2026-11-19 兑换兼容与供应链复扫

- `scripts/redeem-key.mjs generate` 默认签发 Ed25519 LIB1；可显式选择 LIB2。新签发清单与旧码换发清单均以仅所有者可读写的权限写出，不回显密钥内容/发行路径，写文件前拒绝覆盖已有文件。发行方可通过私有文本清单或旧发放 CSV 调用 `reissue --input`：只在发行端校验旧短码 MAC，并换发保留活动、nonce、奖励及到期日的 LIB1。客户端没有包含或新增旧共享 MAC 密钥。
- 当前 Rust/Tauri 与 Go 原生 repository 都在同一 SQLite 事务内按 `pet_id + campaign_id + nonce` 检查已兑换身份；签名换发码与原短码命中相同记录并返回重复结果，不会再次发奖。Go 有跨 Key 哈希的事务测试，Rust 有 legacy 元数据查找测试；发行脚本集成测试覆盖签名格式、短码篡改/过期拒绝、nonce/奖励保真、私有文件权限、重复运行及输出不覆盖。
- 已发行的旧 Tauri 二进制并不含跨格式去重代码，也没有新的 Go profile lock，故它们仍不得与换发流程/新客户端共享资料库交错启动。上线或批量换发前必须先关闭旧进程，并先为 Tauri 发布包含此去重修正的版本；不得把原型脚本当作已完成兑换运营迁移。
- 使用固定版本 OSV Scanner 2.5.0 逐锁新扫：`pnpm-lock.yaml` 与 Go 锁无命中；三个当前 Python 生产锁各有 54 项告警，类别含 6 Critical、29 High、16 Medium、2 Low、1 Unknown，集中在 `filelock==3.29.7`、`Pillow==12.3.0`、`transformers==4.53.3`。`cargo-deny 0.20.2` advisories/bans/sources 通过，但总安全门禁仍因 Python 锁以退出码 1 阻断。未扩大 OSV 例外。
- 临时验证过 Python 3.11.18 ARM 锁：升级 FileLock 4.2.1 与 Pillow 13 后仍有 Transformers 两项 High；三平台 pip hash 锁、OSV、`pip check`、FunASR runtime import/依赖版本验证通过。另一隔离试验将 Transformers 升为 5.23.0 后扫描无命中，但运行时报告 PyTorch 2.2.2 被禁用，而 Transformers 5.23 要求 PyTorch >=2.6；对应 PyTorch 2.6 没有 macOS Intel CPython 3.11 轮子。该试验仅在 `target/`，没有提升到产品锁、manifest 或 runtime。当前仍缺覆盖四平台和固定模型的端到端安全升级路径，因此原 Python 3.10.17 产品清单不变、安全门禁仍阻断切换。

本节依据锁文件逐项扫描输出、PyPI 项目元数据、OSV 公告、隔离 Python 3.11.18 runtime 检查，以及 Go/Rust 定向测试。`pnpm security:test`、`release:check-runtime-locks`、`native:runtime:check`、`native:check`、`native:compat`、`release:check-versions/platforms/security`、`go test -race ./...`、`cargo fmt --check`、Rust workspace 测试与 Clippy、`pnpm check:fast` 和 Web 构建通过；Rust 为 213 passed、9 ignored，前端 47 tests、Python 12 tests。`pnpm security:check` 用真实固定版 OSV Scanner 复跑仍以退出码 1 阻断，三个 Python 生产锁各 54 项（6 Critical、29 High、16 Medium、2 Low、1 Unknown）；不得以其余检查通过代替供应链门禁。Web 构建保留既有的大 chunk 警告，Rust 外置盘保留硬链接回退警告。临时工具与依赖只写入忽略的 `target/`，未访问用户资料库、运行远端发放服务或发布任何产物。

## 2026-11-19 继续复核增量

- 发行脚本的篡改拒绝回归发现 Crockford Base32 解码接受了末尾未使用位非零的非规范编码；这些不同的文本会落到同一 token 字节。解码现在拒绝非零填充位，`pnpm security:test` 中的短码末字符篡改用例通过。`pnpm native:check` 和 `git diff --check` 也通过。
- 对最新 Apple Silicon 候选包再次实际打开原生窗口：候选标题正确显示 `Liberty Native Candidate`，截图可见原版主要侧栏导航、仪表盘、顶部系统按钮边距、状态栏和版本。主窗口 1440 × 960 与窗口选项回归测试通过；这次只验收候选窗口与首页，不代表逐页新旧截图差分或完整 UI 等价。
- 本次用锁定版 cargo-deny 与 OSV Scanner 重跑 `pnpm security:check`：cargo-deny advisories/bans/sources 通过，OSV 仍以退出码 1 拦截三个生产 Python 锁，各 54 项（6 Critical、29 High、16 Medium、2 Low、1 Unknown）。尝试未扩大豁免，亦未将未兼容的试验依赖升入生产锁。安全门禁仍阻断切换。
- 在忽略的 `target/` 用 Python 3.10 解析候选哈希锁：`filelock==4.2.1` 与 `Pillow==13.0.0` 均要求 Python ≥3.11，不能放进当前 Python 3.10.17 运行时。只升级 Transformers 并把 Hub 提到 1.5 时，解析仍保留 `torch==2.2.2`、FileLock 4.1.0 与 Pillow 12.3.0；OSV 按现有精确公告例外配置复扫仍报 60 项（6 Critical、27 High、22 Medium、4 Low、1 Unknown），并提示 Transformers 例外已不再匹配。此候选既不通过安全门禁，也没有安装、FunASR/固定模型运行或四平台实机证据，未采用。
- 本次没有重跑整仓 `pnpm check`；当前 JS 兑换更改由完整 `pnpm security:test` 覆盖，Go 代码及生成漂移由 `pnpm native:check` 覆盖。不得把本次范围外的平台、媒体、安装或 UI 验收记为已完成。

## 2026-11-19 启动、恢复、候选包与窗口对齐

- 启动选择已拆成可审查的预览与候选通道：预览要求显式选择快照、资料目录或现有生产资料（版本/帮助输出除外），隔离写入必须显式资料目录；候选无参数启动只使用独立 `com.westng.liberty.native-candidate` 资料目录。生产 Tauri 目录必须显式 `--default-profile`，可写接管先获取共享资料锁，再制作或复用含 WAL 的一致性备份；`--database` 始终只读，不能借命令行组合绕过接管流程。版本输出不访问资料目录，启动错误使用稳定代码和双语恢复指引，不回显原始路径或参数。
- 显式恢复仅接受同目录、受认可命名且非链接的 `.bak` 文件；源快照和目标库在预检阶段验证 schema、完整性及凭据引用，恢复前保存 `pre-restore` 安全副本，并通过 SQLite Online Backup API 原地恢复。源或目标预检失败的测试确认当前库不变；恢复执行后的 checkpoint、校验或同步失败可能发生在数据已写入之后，故实现和文档不再承诺所有恢复错误都代表当前字节未变化，安全副本必须保留。
- 候选归档脚本已覆盖 `darwin-arm64`、`darwin-x64`、`windows-x64`、`windows-x86` 四个目标的版本元数据、隔离身份、Mach-O/PE 架构、图标、NOTICE、逐文件 SHA-256 清单和解包复核；Windows 仍是便携目录，不是安装器。产物明确标为未签名、未公证、`productionReady:false`，本轮没有签名、公证、安装、发布或生产入口切换。
- 主窗口已按当前 Tauri 配置对齐为 1440 × 960、最小 1100 × 760。macOS 使用隐藏标题栏 inset overlay 和旧版 traffic-light 坐标，Windows 使用隐藏标题栏并设置 42 DIP 标题栏高度；MyGo `Context.TitleBar()` 为左/右系统控件动态预留空间，标题栏中间保留拖拽区。新增测试读取旧 `tauri.conf.json` 对比尺寸/overlay/traffic-light 配置，并检查指标与 GitHub 按钮不覆盖系统控件；本机真实窗口截图确认三色交通灯、左侧指标和右侧操作均在同一标题栏内。
- 本机实际 GUI 仍只证明 macOS Apple Silicon 的隔离候选窗口和部分人员输入法/保存流程；拼音在姓名、部门字段可以组合并提交，不能外推到 Windows、所有控件、取消路径、读屏或完整旧版逐像素一致。候选启动、资料锁冲突和标题栏实机均未改变默认 React/Tauri 入口。
- 最终构建批次的 `native:check`、四目标 `native:build:matrix`、`go test -race ./...`、四目标候选包归档/解包哈希复核和 `git diff --check` 均通过。使用新建的隔离 HOME 启动最终 macOS ARM 候选包，在 GUI 新增合成人员后关闭并重新打开同一程序/资料目录，人员列表仍显示该记录；退出后对关闭状态下的隔离 SQLite 做 immutable 只读检查，`integrity_check` 为 `ok` 且姓名/部门持久化。该项只验证合成资料目录，不是用户生产库接管或安装器升级验收。

本节依据当前源码、`tauri.conf.json`、Go 单元测试、候选归档校验输出和本机隔离窗口观察记录；不读取生产资料库或用户凭据，也不替代四平台实机、安装器回退、安全扫描和完整 UI 验收。

## 2026-11-19 界面对照

- 商品详情采用旧版 720 ms 透视翻牌、悬停与键盘焦点提示、方形预览及双列信息卡，窗口宽度不超过 760 时改用单列。MyGo 渲染测试覆盖翻牌中间帧、移出复位、减少动态效果、双语与宽窄布局。使用同一份合成“阅读书架”数据，在 920 × 640 内容区对照了旧 React 生产构建的 Chromium 渲染与 MyGo 测试渲染，覆盖浅色中文、深色英文的正面。已据此校正分类文案、颜色与行距。字重和阴影仍有差别，原生窗口合成与动画逐帧仍待对照。
- 盲盒恢复 430 高度的抽取区域、底部居中按钮、抽取进度边框、中心旋转线框和中奖物品浮动。渲染回归覆盖按钮位置、结算前仍处于抽取状态、结算后的奖励及次数、减少动态效果下的静止画面。Three.js 旧场景的网格细分、粒子和相机效果仍需并排对照和实机性能测量。
- 标题栏从旧端 JSX 同源生成三个指标图标和 GitHub 图标。桌宠开关保留文字，来源状态在本地运行时就绪或远端能力握手通过后显示旧版就绪文案。此前仅按模式显示就绪文案的旧逻辑可能误报，原生端缺少就绪依据时显示当前模式。双语用例覆盖这两种展示、桌宠切换与项目链接。

## 2026-11-19 小游戏工作线拆分

- 将主重构 P5 的业务迁移（P5-A）与主动小游戏体验升级（P5-G）分开规划。矿场、工厂、便利店已有持久化真实时间任务、照看/领奖及幂等奖励；工作线拆分时尚无验证过的主动小游戏循环，分别按业务兼容与玩法体验处理。
- 最初仅形成设计与计划；获准执行后，已实现 P5-G0 工厂基础装配练习。使用固定 MyGo `plugins/game` 和现有素材，在“牛马市场 → 工厂打螺丝”追加“练习基础装配”，保留原岗位操作。外部引擎没有引入；Ebitengine/Godot 仍只在明确能力不足时进入比较。
- `domain/assembly.go` 是无 UI/持久化依赖的确定性状态机，稳妥单 6 件/60 秒/错配扣 3 秒，急单 10 件/45 秒/扣 5 秒。`desktop/assembly_game.go` 负责固定步长、画布、双语原生控件及会话状态，`workspace.go` 负责路由和窗口暂停。开始、错配、装配、成功/超时、暂停/继续、重开均已落地；离开入口丢弃短局。
- 数字键及配件按钮的 Enter/Space 都忽略系统按键重复。复核发现原生按钮默认重复激活会连续扣时，新增真实 Tab 聚焦的回归测试先复现失败，局部修复后通过。暂停卸载活动循环，继续时重建时钟；减少动态效果停装饰动画。
- 本机 macOS ARM 的 `pnpm native:preview --writable --locale zh-CN --theme dark` 使用临时合成资料，实际观察开始、故意错配扣时、数字键输入、Esc 暂停/继续、6/6 完成与 86% 准确率，以及重开归零和更换订单。失焦/隐藏、超时、最终 Enter/Space 重复保护、双语及窄布局另有组件测试，未把这些全部列为实机已验收。
- 本批 `pnpm check`、`pnpm native:check`、`go test -race ./internal/domain ./internal/desktop`、四目标 `pnpm native:build:matrix` 通过；预览启动还执行并通过 Rust/Go 合成数据库、写入、迁移、profile 锁和导出兼容检查。四目标仅编译及文件头校验，不代表 Windows x64/x86 或 Intel Mac 窗口运行通过。
- 本轮只涉及临时练习玩法、工厂入口、主窗口生命周期和文档，不修改 DB/schema、LP、物品、care/claim、锁文件或外部引擎依赖。默认生产客户端仍为 React/Tauri。P5-G1 的玩法评价/输入延迟/资源预算、P5-G2 care 接入、其他地图与 P5-G3 外部引擎门禁均未完成。设计与验收边界见[小游戏体验工作线](../designs/2026-11-19-native-gameplay-experience-design.md)，持续使用说明见 `apps/native/README.md`。

## 2026-11-18 整改增量

- 可写 host 已装配创建、转写和 AI 总结奖励；工作台在文本或 Word 发布成功后才记账。奖励使用旧端事件类型和键，不以文件选择、Runner 退出或收到 AI 响应作为完成证据。独立账本失败不回滚任务、总结或已发布文件；没有引入崩溃后补发承诺。
- 可写 host 成功启动后记录每日打开；外观按实际深色状态及 UTC 日期记录。宠物动画按账本的 `event_source` 识别工作流事件。农场将拖拽背景与地块点击区分开，当前地块操作按钮提供带编号的双语名称。
- 证据：`native:compat` 的合成任务重试/删除/重开验证三种任务奖励各一条，工作台及总结测试覆盖失败和运行 ID。该次兼容检查也对照了旧 JSON 导入、AI、纪要和 Word；下方旧快照的“尚未迁移”不是上述模块的当前状态。
- 本机 macOS ARM 已通过 `native:meeting:acceptance`。从原生文件对话框选择合成中文录音，经真实 ffmpeg/FunASR 转写，在独立总结窗口向回环测试服务发出一次请求，用系统对话框保存完整 Markdown，退出后重新打开 SQLite。断言覆盖识别关键词、返回的摘要、结构化纪要和五种奖励各一条。首次验收暴露总结加载键缺失导致进程退出，修复后双语首帧测试和实际窗口复验通过。
- 独立桌宠已接入。macOS 窗口菜单确认两个桌宠实例，点击角色出现互动气泡，关闭桌宠设置后只剩主窗口。主窗口位置与用户设置分别写入，合成库验证互不覆盖。双屏不同缩放坐标用例已覆盖。真实跨屏拖动、透明边缘穿透、Windows DPI 与全量视觉差分仍待验收。盲盒从静态图改为原奖池素材、深度排序和透视粒子场景，需与旧 Three.js 版本进行视觉及资源预算对照。
- 原生进程树 CPU 与驻留内存已接入主窗口，Windows 第一次采样保留未知 CPU，图形数值标为估算。迁移兼容测试将升级前数据库备份还原到独立目录，比较旧数据，再升级比对 Rust 预期库。生产安装器回退和切换后新增资料的保全仍需实机演练。
- Rust 生产路径使用 `jiff` 处理时间，示例程序复用生产 Word 渲染器。`cargo-deny 0.20.2` 对配置的目标依赖图报告 advisories、bans、sources 通过。时间测试覆盖持久化的 `+00:00` 及小数精度、闰日签到、计时截断、跨夏令时的工作台范围、午夜跳过和重复。Go 的 OSV 扫描零命中，17 个 Go 模块及工具链归集 50 份声明。`licenses:check` 通过。
- 用 `osv-scanner 2.5.0` 逐锁复扫，pnpm 从 10 包 17 条降到 1 包 1 条（`picomatch 4.0.5`，High，扫描时无修复版本）。三个生产 Python 锁各从 8 包 63 条降到 3 包 54 条，其中各有 6 条 Critical。相同公告跨平台重复出现。开发 Python 锁零命中。`filelock 4.2.1`、`Pillow 13.0.0` 需要 Python 3.11，`transformers 5.10.0` 要求的 Hub 版本与当前精确锁冲突，另有一条无修复版本。项目沿用已有的逐公告例外。生产安全门禁仍阻断。
- `2026.11.18-supply-chain.1` 沿用 Python 3.10.17、PyTorch 2.2.2 和原模型 revision，更新 certifi、hydra-core、msgpack、setuptools、urllib3 的三平台哈希锁。安装与复用托管 Python 前后校验实际包版本，系统 Python 继续按功能检测。macOS ARM 真实依赖安装、四模型离线验证和合成语音转写通过。说话人关闭时为 1 段，开启时为 6 段。Intel 和 Windows x64 的新版锁仍需安装实测。
- 商品商店与仓库的详情按钮及双击打开独立的只读商品窗口。子窗口只接收选中商品投影，显示双语锁定原因、价格、持有数及来源，跟随主窗口语言与主题。盲盒已在真实窗口观察到抽取禁用、完成后次数和最新奖励更新。实际会议处理时观察到顶部进程资源采样变化。商品窗口与原版卡片动画及完整视觉差分仍待实测。
- 总结状态读取添加超时后，重新执行 `native:meeting:acceptance`。macOS ARM 原生总结窗口从“生成中”自动变为“已保存结果”，显示回环服务返回的摘要。退出后断言确认一次请求、完整 Markdown 内容、持久化纪要及五种奖励均符合预期。历史偶发延迟的根因仍待核实。商品卡片增加命名分组后，macOS 系统辅助功能树实际列出多个“查看详情”按钮。通过其中一个按钮打开“阅读书架”，Tab 将焦点移到商品预览，关闭后返回商店。完整读屏及输入法验收仍需实机操作。
- 路由、命令与原生方法的对应表已进入 Go 测试。测试对照基线的 25 条路由、90 个命令检查遗漏、失效方法及遗留映射。两个旧结果入口重定向到已完成队列。对照还补齐了农场收获流水的有界查询、运行时合并日志、桌宠实例数量、标题栏桌宠开关和项目链接。权限矩阵与各平台交互的逐项验收继续独立记录。
- `native:remote:acceptance` 已提供只读真实服务验收入口，使用操作方指定的测试服务、系统凭据引用和已完成的合成任务。回环测试验证其请求均为 GET，缺少指定任务时验收失败。本机没有配套测试服务及凭据，真实验收待执行。
- 本记录写入时尚有的兑换兼容缺口，已由前面的 2026-11-19 兑换发行端换发与跨格式幂等修复处理；安全运营仍要求使用匹配的签名密钥、封存输出并让旧客户端完全退出。未实现跨设备/远端兑换状态服务。
- `native:bench:reads` 用 Rust repository 生成 1000 个任务、1000 个文件及一份 10000 段逐字稿，两端顺序读取同一隔离库。每项预热 2 次、测量 20 次。本机 macOS ARM 上队列读取 P50/P95 为 Rust 2.523/2.724 ms、Go 3.740/4.083 ms，长逐字稿为 Rust 3.752/3.911 ms、Go 6.769/7.284 ms。本次两项 Go 读取均较慢。采样包含结果投影与正确性检查，界面绘制、冷启动及 ASR 在测量范围之外。
- 剩余退出条件包括旧端依赖整改与运行时重新验证、真实远端服务验收、原生全路由与权限用例对照、视觉及性能基准、双语输入法与无障碍、Windows x86/x64 和 Intel Mac 实机、安装包升级回退及签名公证。对照日期较早的记录时，以本节和当前源码为准。

本批在 macOS ARM 上通过 `pnpm check`（47 个前端测试、12 个 Python 测试、212 个 Rust 测试）、`native:check`、`native:compat`、Go race 和四目标编译。默认 Rust 测试跳过的 9 个专用用例分别由兼容与基准命令运行。真实系统凭据验收使用独立合成条目。发布策略及自测通过。安全门禁以退出码 1 阻止切换。原生端保持隔离预览，生产入口仍为 Tauri 客户端。

Go 写入的宠物、总结、资源、凭据清理和迁移时间沿用 Rust 的 UTC 偏移和小数秒精度。`native:compat` 对照七个固定时刻的 Rust 实际输出，包括 Unix 纪元之前的时刻。

质量工作流已配置在 macOS ARM64、Intel Mac 和 Windows x64 runner 上执行 Go 测试和独立系统凭据读写。Windows x86 用例在 Windows x64 runner 上运行 32 位程序。本机已解析该工作流并通过发布策略检查，四组托管 runner 的执行结果仍待 GitHub CI 返回。

## 阶段状态（以下为 2026-11-17 快照）

| 阶段 | 已落地 | 尚未满足的退出条件 |
| --- | --- | --- |
| P0 | 25 路由、90 command、8 capability、schema v10 的可执行基线；关键业务与 UI 源码指纹 | 全业务用例/故障场景逐项覆盖 |
| P1 | 固定 Go 1.27.4/MyGo/SQLite；macOS ARM 窗口；四目标编译；Go checksum/OSV 输入/SBOM | Windows x64/x86、Intel Mac 实机；输入法、凭据、桌宠、3D；许可证与真实安全扫描 |
| P2 | 首页/队列/结果；日志与总结选择；Client 代次；窗口授权；协议/契约；旧库对照；原消息/图标/动画/主题；当前 lease 进度与后台刷新 | 完整 DTO、全部结果标签页；全 UI 截图/交互/权限验收 |
| P3 | 新旧资料目录锁；v10 写入；attempt/lease、进程身份、心跳、完成/失败/重试；并发/独占调度、删除补偿；运行时下载/校验/安装、离线模型、系统检测；新建/重试/删除/运行时 UI；host 启动恢复和关闭装配；整条合成流程；macOS ARM 真实运行时安装和合成语音转写；显式初始化/v1–v10 升级/凭据转换 | GUI 整条真实媒体流程、其他平台安装/转写、旧 JSON 导入与真实旧进程交接、完整故障演练 |
| P4 | 结果独立窗口只读接口；模型/模板/人员仓库、用例、管理页和独立编辑作用域；系统凭据暂存/发布/清理；设置 CAS/脏字段合并及外观/处理默认值；人员 Excel 双向文件兼容；讲话人改名与 TXT/Markdown 导出 | 完整 AI/纪要流程、Word/宠物奖励联动、远端、诊断；完整编辑/文件对话框/主题 UI 差分 |
| P5 | 首页宠物等级投影与 765 组等级边界对照 | 宠物经济、签到、商店、兑换、盲盒、农场、打工、桌宠 |
| P6/P7 | 旧端可运行；独立验收数据；新增 Go CI 配置 | 全功能/UI 等价、性能、升级回退、四平台实机/安装包、签名/公证、生产入口切换 |

## 本轮实现

1. Rust 启动在桌宠、数据库/任务恢复之前持有 profile 锁；Go 写入仓库使用同一文件和 OS 锁原语。文件不删除，拒绝链接型锁文件；不读取真实资料目录进行测试。现有已发行旧二进制没有这项锁，不能用分支内互斥测试证明它们已兼容。
2. `jobdb` 使用 `mode=rw`、foreign keys 和 immediate transaction，`Open` 接收显式既有 v10 验收库，新增 `OpenPrepared` 显式初始化/升级。创建、完成、失败与事件同事务；重复/过期 lease、待删除任务均拒绝写回。事务失败保留原执行状态。
3. Runner 使用独立 attempt 目录、有界 JSON Lines/stdout、脱敏 stderr/事件和大小受限的结果文件。macOS 使用进程组；Windows 实现挂起启动→加入 kill-on-close Job Object→恢复，避免启动后再加入导致子孙进程逃离管理。Windows 代码只有编译证据，尚无实机证据。
4. 应用执行服务将成功结果落库，失败保存失败状态，取消在进程回收后重新入队。终止无法核实时保留执行记录。调度器限制并发、去重，并在重试/删除前取得独占预约。启动恢复沿用旧端顺序：删除补偿→核验并终止旧进程→fence/requeue→全部核验通过后重新调度；失败可重试，重复成功启动不会重放。现已接到可写模式的 `host.Profile`；关闭时等待子进程与安装收尾后释放所有权锁。
5. 删除用例遵循 prepared→fenced→trashed→database_deleted，再 purge/finish。真实合成文件与数据库测试覆盖清理失败后的重启续做；没有删除或重建用户库。
6. UI 修正队列列宽合计与结果关闭按钮对齐；动画帧内存计算使用 64 位，避免 Windows x86 溢出。可写模式接通原生选择器、新建/重试/删除、实时进度和环境管理。新建页按原双栏配置、默认设置、双语与未就绪警告实现；环境页按原设置侧栏和组件行布局实现。现已追加外观、主题和处理默认值；运行概览/远端/诊断仍未实现，不能算完整设置迁移。
7. `managed` 同源生成原清单、三平台依赖锁、Runner/validate/warmup 与四模型固定 revision。下载通过 HTTPS、大小/SHA-256 验证后原子发布；ZIP/TAR 全量预检、路径与链接约束；组件按 generation 安装，失败保留上一份可用 artifact。模型可先下载，再等待 Python 就绪离线校验。x86 延续原 unsupportedReason。macOS ARM 真实上游包已在独立目录安装并通过校验，其他平台未执行。
8. 组件状态和设置 revision 用旧 schema 事务保存，拒绝过期 generation；重复轮询不增加设置 revision，也不重复保存未变组件。系统检测迟到不能覆盖已切回托管的配置。修复损坏 FFmpeg 未重新解压、模型状态读取创建目录和 marker 无界读取的问题。
9. `host` 连接运行时解析、系统内存预算、调度、恢复、媒体授权和查询。媒体路径只由原生选择器授予 token，创建前复核文件身份；任务已持久化后即使退出阻止调度，也返回已创建 ID，留待下次恢复，避免用户重试重复创建。
10. 模型同尺寸篡改在校验时被拒绝，并撤销 acquired marker，使下一次重试重新获取损坏文件，保留其他有效缓存。真实 FunASR 日志出现 ANSI 终端控制码后，诊断适配增加控制码清理；先清理再脱敏，防止控制码拆分敏感字段名。

## 验证口径

- `native:compat` 对照真实 Rust schema/repository 生成的 14 个可见任务、各任务结果、首页 all 聚合、偏好与 765 个等级边界；读取前后主文件哈希一致。另在独立副本测试写入、失败回滚、删除恢复；真实合成子进程验证成功/失败/取消的落库结果。
- 资料目录互斥包含 Go/Go 同进程与子进程、Go 持锁时 Rust 拒绝、释放后 Rust 可获取。只在 macOS 实际执行。
- Runner 测试包含无效协议、超长无换行 stdout、退出失败、缺结果、回调失败、取消、子孙进程终止、进程身份与 PID 复用拒绝、参数边界、离线模型环境；race 在当前 macOS 执行。
- 新增 TLS 安装测试覆盖三平台 hash-lock 命令、校验后发布、依赖失败重试、损坏 FFmpeg 重装、模型固定 revision/hash、离线预热/缓存元数据、篡改与失败不发布。运行时状态机覆盖等待 Python、安装中切换来源、重启中断、保留旧 artifact、关闭取消、过期代次和 x86 拒绝。
- `native:compat` 新增运行时 settings revision、generation CAS、数据库重启持久化、凭据字段写入禁令；新增 `host` 整条合成流程，实际启动子进程并验证创建→完成结果→重试→删除→退出后重新获取 profile 锁。此处 Python/模型路径是合成测试适配，不代表真实 FunASR 验收。
- `native:runtime:acceptance` 在本机 macOS ARM 实际下载原清单资源、三方哈希锁定 Python 依赖和四个固定 revision 模型，通过原版 runtime_validate、runtime_warmup 离线验证及模型缓存发布；复用安装后，用系统 Tingting 生成纯合成中文录音，运行真实 ffmpeg 与 FunASR Runner。关闭说话人得到 1 段且 disabled，开启得到 6 段且 completed，两次均断言包含“会议”“测试”。没有用户音视频、没有人工准确率评测，也未把此结果泛化为其他平台验收。
- Windows x86 初次编译暴露 SQL 大整数默认推断为 `int` 的溢出，已显式转换 `int64`。最终四目标主程序与全部测试包（含 compatibility tag）编译通过，主程序 Mach-O/PE 架构校验通过；其他平台测试程序未实机执行。
- P3 阶段的 `pnpm check` 通过：契约/TS、47 个前端测试、Python lint 与 11 个测试、Rust fmt 与 205 个测试（当时 2 个专用测试默认 ignored，由兼容脚本单独执行）、Web 构建、Clippy warnings-as-errors、版本/平台/安全/运行时锁及相关阻断样本。`native:check`、带 race 的写入/执行兼容测试、`go test -race ./internal/...`、`native:build:matrix`、`git diff --check` 均通过。既有外置盘硬链接回退和 Vite 大 chunk 警告仍存在。
- 本轮实际 macOS 窗口确认新建页显示、运行时页面选择官方源后下载操作启用、返回主界面和退出；发现步骤 02 换行后已改为固定大小居中布局，后续仍需原生复验。双语组件测试覆盖选文件、默认标题/热词、未就绪提示、提交参数和环境字段。未点击安装、未从 GUI 真正处理媒体；没有新旧截图差分。
- 未执行：其他平台真实托管运行时与媒体处理、GUI 整条媒体流程、生产数据升级/回退、用户系统凭据访问、新 UI 全流程、Windows/Intel Mac 实机、真实漏洞扫描、许可证归集、签名/公证/安装包、GitHub CI 与发布。

## P4 资源与设置增量

- 新增 `domain/resources.go`、`settings.go`，`application/Resources`、资源编辑 grant，以及 `jobdb` 资源/设置事务操作。模型默认策略对照原 `useAiStore`；人员保持单一记录人、按姓名导入与整批回滚；内置模板只读，复制后可编辑。编辑 grant 只能写当前类型及当前 ID，关闭撤销。
- 新增系统凭据端口，公共模型只传九个元数据字段，设置仅传是否配置 Token。Secret 的打印/JSON 输出脱敏。暂存凭据写入前持久化 rollback intent；事务发布引用并退休旧引用；失败和中断可恢复，清理失败不回滚已保存数据。Rust 清理保护同步覆盖远端 Token。
- macOS 真实验收发现直接 Security.framework 新调用者存在 Keychain ACL 差异，未保留该方案。最终使用与旧端相同的 `/usr/bin/security`；另实测发现旧端裸 `-w`/stdin 写入返回 0 但存空值，已同步修复 Go/Rust 为单条交互命令 stdin，并覆盖引用/特殊字符/显式 hex 读取；写入后回读值校验，不能只凭退出码报告成功。随机独立服务名验证已通过；没有访问用户凭据。Windows API 及 x86/x64 CREDENTIALW 布局检查已写，实机尚未验证。
- 设置仅更新公共列，保留 Python/ffmpeg 私有路径和来源；revision CAS 与运行时检测并发更新不冲突。Client 保留保存途中新增编辑，冲突时只重放脏字段，并覆盖“改为深色后马上改回原值”的竞态。并发设置影响后续调度，降低限制不终止正在执行的任务；新建页重新进入时刷新默认值。
- 管理页沿用原文案、四项统计、模型/模板卡片和人员列表；独立编辑窗口沿用原字段、尺寸、底部操作，包含密钥保留/替换/清除、模板复制、未保存关闭路径。外观/语言变更传播至主窗口、结果与编辑窗口。尚未完成全部双击/表格无障碍和像素级对照。
- `memberxlsx` 无新增外部依赖，实现有界 ZIP/XML、首表/共享字符串/inlineString、原表头别名、整数/记录人校验；导出中文工作表/原四列，公式样式文本保持字符串，大整数以文本保真。原生文件对话框拥有文件路径，仓库只收到人员值。合成文件验证 Rust→Go 和 Go→Rust，完整 Excel GUI 对话框操作尚未验收。
- 原生 GUI 实际观察并操作：新增合成人员、部门/排序/记录人保存后列表回读；浅色与英文切换；英文模板保存后管理页更新。键盘直接输入的中文未得到有效状态变化，粘贴可用；中文 IME 必须单独验收。没有操作真实资料库/真实 API Key。截图显示外观卡片选中色块与原样式有差异，已调整代码，仍需截图复核。
- 追加测试覆盖凭据写入失败、旧凭据退休失败、重启恢复、活跃 Token 不清除、设置竞态、只读内置模板、人员整批导入回滚、编辑权限及关闭；双语组件测试与设置异步竞态测试通过。完整检查曾发现 Clippy 的测试模块位置错误，已移动到文件末尾，不降低质量规则。

### P4 本批验证结果

- 最终 `pnpm check` 通过：47 前端测试、11 Python 测试、208 Rust 测试（3 个专用 helper 默认 ignored，兼容脚本单独调用）、fmt、Clippy warnings-as-errors、Web 构建、版本/平台/安全/运行时锁检查。硬链接缓存回退和既有 Vite chunk 警告仍存在。
- `pnpm native:check`、`go test -race ./internal/...`、`pnpm native:compat`、`pnpm native:build:matrix` 与 `git diff --check` 通过。四平台只验证编译和文件格式；Windows/Intel Mac 运行、安装器、签名、公证均未执行。
- `pnpm native:credentials:acceptance` 在 macOS ARM 的随机独立服务中通过真实写后回读/替换/删除和旧工具读取验收。未访问真实用户凭据。Go `memberxlsx` 与 Rust xlsxwriter/calamine 双向合成文件验证通过。
- 原生 GUI 只验收上文列出的人员、模板、语言/主题流程。模型密钥保存 GUI、人员 Excel 对话框、全部主题/输入法/无障碍、新旧截图差分、退出保存竞态仍未全部验收。

## 剩余验收与实施顺序

1. 真实 FunASR/托管运行时目前只在 macOS ARM 验收；补齐 Intel Mac 与 Windows 运行时安装、媒体流程、失败/重试/关闭恢复和资源预算实测。四目标编译不是实机替代。
2. 对 P5-A 的宠物、农场、打工和兑换语义做旧/新固定时钟与随机样本逐项差分，覆盖跨日、重复请求、恢复和奖励幂等；当前实现及合成测试不等于完整业务验收。
3. 完成 P5-G1 玩法/输入延迟/资源预算实机验收，继续覆盖可选照看与既有 care/claim 的固定时钟经济差分、其他地图评估及 P5-G3 渲染路线门禁；维持小游戏与 P5-A 持久化经济验收分离。范围见[小游戏体验工作线](../designs/2026-11-19-native-gameplay-experience-design.md)。
4. 完成所有旧端页面及弹窗的同数据新旧截图/交互差分，覆盖中英文、主题、输入法、快捷键、多窗口焦点、读屏与关闭路径；当前已验收的主窗口/部分表单/商品页不等于完整 UI 一致。
5. 取得真实远端测试服务与凭据后执行只读验收；解决 Python 生产锁的 OSV 阻断，不扩大豁免；完成 Windows x64/x86、Intel Mac 实机、安装/升级/回退/崩溃恢复以及签名与公证证据。
6. 旧版已发行二进制不支持新资料锁和跨格式兑换去重。正式切换不能依赖长期维护 Tauri 修正版：Native 活跃资料库必须与旧二进制写入隔离，并通过一致性快照完成导入；如果不能证明旧客户端无法写入 Native 活跃资料库，切换门禁保持阻断。通过所有差分、安全、平台、安装升级/恢复和发布门槛后，切换默认入口、验证 Native 烟测/恢复，并在同一退役变更中删除 `apps/desktop`、旧端构建发布链及仅服务旧端的依赖。回退按已演练的快照恢复流程进行，不依靠旧 Tauri 并行写入。

## 工作区与历史记录修正（早期阶段快照）

以下版本、运行时和修改范围描述限该早期阶段；后续 manifest 修正与候选接线见各带日期的增量，不能据此认定整个当前工作区的运行时清单未变。

- 分支尚未提交、推送、打标签或发布。版本与原运行时清单未改变，依赖只把已锁定的 Go 模块标为直接使用，未升级版本。
- 修改包括 `apps/native`、Rust profile 锁与启动装配、凭据 stdin/读取及清理保护修正、只读投影提取、合成测试 fixture、脚本/CI/治理文档。不是仅“测试专用 Rust 改动”。
- 前一版记录中的 28 媒体分钟/1 总结和全布局 GUI 验收描述不再适用，已移除；扩展 fixture 后应以 Rust 自动产生的期望结果为准。
- 进度字段存在于 DTO 不等于已接入实时进度。协议解析、底层执行通过不等于 GUI 主流程或运行时安装完成。

## 资料库升级与工作台增量（2026-11-17）

- `schema` 直接生成原 Rust 的迁移 SQL 和五个内置模板，`native:check` 检查漂移。版本 0/1–10 的每个合成起点由 Rust 生成，Go 升级后的表、索引、业务行与 Rust 升级结果比较；凭据随机引用和迁移时间仅作规范化，内容不写日志。升级前 backup API 读取完整 WAL 快照，备份做 integrity/schema 检查；未来/损坏版本在业务 DDL/WAL 切换前拒绝。
- v3 凭据先持久化仅含引用的恢复日志，再调用 OS store 并回读验证；CAS 比较原始明文，存入系统的值才 trim，避免带空格旧值迁移失败。发布阶段/版本与明文清除同事务；失败后保留恢复日志，保护所有仍被模型或远端 Token 使用的引用。没有明文回退。
- 该批新增显式 `--writable --prepare-database` 入口，当时要求新资料目录已存在；先锁定再创建库，未来连接保留 foreign keys/busy timeout/synchronous。新库 host 启动、五内置模板/设置/成员持久化、重启和所有权释放有集成测试。默认资料目录发现与旧 JSON 任务导入当时未接入；后续已实现，当前 `--profile-directory <绝对目录> --writable --prepare-database` 可创建缺失目录，详见原生模块说明。
- 工作台接讲话人批量改名；原始转写和历史 AI 总结保持原值，待删除任务拒绝写入。原版“编辑”仅修改讲话人，并非正文编辑。双语组件验证内联输入、确认、Enter 保存、Escape 取消和三种文本导出动作。
- 文本导出按原三种格式（TXT 逐字稿、Markdown 纪要/完整结果），36 个 Rust 合成期望逐字对照通过；只使用已验证讲话人投影。原生保存对话框保管路径，文件有界作用域暂存、sync 后发布、身份复核；失败保留原目标。工作台能力仅允许绑定任务，子窗口及撤销 grant 不可执行改名/导出。
- 本批仍缺：旧 JSON 导入、Word、纪要窗口编辑、AI、远端、诊断、导出宠物奖励与全部 P5；新工作台真实窗口/对话框验收、全 UI 差分及跨平台实机尚未执行。

### 本批验证结果

- `pnpm check` 通过：47 前端测试、11 Python 测试、208 Rust 测试；5 个专用 helper 默认 ignored，均由兼容脚本单独调用。fmt、Clippy warnings-as-errors、Web 构建、发布/平台/安全/运行时锁规则通过。外置盘硬链接回退及 Vite chunk 警告仍存在。
- `pnpm native:check`、`go test -race ./internal/...`、`pnpm native:compat`、`pnpm native:build:matrix`、迁移生成一致性及 `git diff --check` 通过。四目标只验证编译与 Mach-O/PE 文件架构，未执行 Windows/Intel Mac 二进制。
- `native:compat` 最终对照包含表和显式索引定义、业务行、重复启动不重放；另含 36 组 Rust 文本导出对照、讲话人修改范围与回滚。普通测试包含新 profile host 初始化/重启、连接替换后的外键设置、凭据失败/清理恢复、WAL 备份、窗口撤销、双语编辑与导出动作、文件覆盖失败保留原件。
- 未访问用户实际资料库/凭据/媒体，没有执行新一轮真实 GUI 对话框验收，也没有提交、推送或发布。

## 2026-11-20 启动、恢复、打包与窗口验证增量

- 已在 macOS ARM 的隔离测试 HOME 中启动原生候选，并观察到主窗口。无参数候选启动创建独立的 `com.westng.liberty.native-candidate` 资料目录；另一次候选 GUI 操作通过 macOS 拼音输入法保存人员“会议 / 测试”，进程退出后检查隔离 SQLite 完整性为 `ok` 且记录仍存在。此证据只覆盖候选启动和该人员表单，不表示生产资料接管或全 UI 验收。
- 接管/恢复目前有离线自动化证据，没有真实生产库恢复操作：写入接管先取得 profile 锁并建立、校验及复用首次 WAL 一致恢复点；显式恢复预检源/目标完整性、schema 和系统凭据引用，并先创建经验证的 `pre-restore` 安全副本。无效源的预检失败测试确认当前库不变；进入 SQLite backup 写入后的 checkpoint、读回或同步失败仍可能在数据变化后返回错误，必须保留安全副本。恢复对话框未做完整原生窗口验收。
- 四目标 `native:package` 产物已有构建及归档核验记录，覆盖 macOS ARM/Intel 与 Windows x64/x86；核验六个版本来源（根与桌面 `package.json`、Tauri 配置、`Cargo.toml`、`Cargo.lock`、Go host）、候选身份、Mach-O/PE 架构、NOTICE、逐文件摘要和解包内容。Windows 仍是便携目录，不是安装器；包标记 `productionReady:false`，未签名、未公证，非本机平台没有运行证据。
- 主窗口选项和回归测试对照旧 Tauri 几何：默认 `1440×960`、最小 `1100×760`，并覆盖 macOS traffic-light/inset 与 Windows 标题栏高度及控件避让。曾实际确认 macOS ARM 原生主窗口可显示；测试/配置对照和一次窗口显示不替代完整页面截图差分、读屏或其他平台实机验收。
- 本增量仅整理文档，没有启动/关闭客户端、访问生产资料或凭据、改代码、提交或发布。已有 P5-A/P5-G 内容保持原样；其玩法差分、真实输入/辅助技术及资源预算退出条件仍按原章节跟踪。

生产切换尚有明确阻塞：React/Tauri 仍是默认发布入口和部分共享 schema、迁移、运行时清单与素材的生成来源；Python 生产锁的漏洞扫描门禁未通过；Intel Mac、Windows x64/x86 的实机运行时和 GUI/IME 未完成；完整 UI、读屏、真实远端与性能差分未关闭；安装升级/回退及中断恢复、签名、公证和发布证据未完成。候选可运行与四目标可打包不等于可以切换默认入口或删除旧端。

## 2026-11-21 日常入口与旧端冻结

- 当前本机仅运行 Go/MyGo 原生候选进程；未发现 `target/debug/liberty`、旧 `Liberty.app` 或 React/Tauri 客户端进程。此次启动使用 `pnpm native:launch` 选出的确切候选包，未通过通用应用名启动旧端，也未访问生产资料。
- 根目录新增 `pnpm start` 和 `pnpm native:start`，两者均指向 `pnpm native:launch`。新版因此成为唯一日常启动入口；启动器仍只接受已打包候选，不会自动编译、强杀进程或替换正在运行的窗口。
- `apps/desktop` 已明确标记为冻结兼容目录；根级旧端 GUI 启动入口仅保留在 `desktop:compat:*` 命名空间，测试、类型检查和构建仍有供 CI 使用的 `desktop:*` 命令。旧端没有从仓库删除：schema/迁移、兼容夹具、正式 Tauri 发布回退和版本/安全检查仍依赖其源码；现在整体删除会破坏可验证的迁移与回退链。
- 本批只完成日常启动入口和冻结标识，不改变正式生产发布入口，不执行签名、公证、发布、部署或生产资料接管。候选仍为 `productionReady:false`；完整 UI/输入法/读屏、Intel/Windows 实机、真实远端、供应链、安装升级回退和发布门槛仍按发布就绪检查追踪。

## 2026-11-21 当前新版启动复核

- 运行 `pnpm native:launch` 后，启动器按候选清单、文件大小和 SHA-256 校验，激活 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-vHxK1o/` 中的 `Liberty Native Candidate.app`；当前进程 PID `88302` 的文本映像指向该候选包内 `liberty-native`，应用身份为 `com.westng.liberty.native-candidate`。
- 当前进程清单未发现 `target/debug/liberty`、旧 `Liberty.app`、Tauri 或 React 客户端；本次没有启动旧端，没有通过通用应用名解析，也没有接管生产资料。`go test -race ./internal/desktop ./internal/host ./cmd/liberty-native`、`git diff --check` 通过。
- 当前结论只证明新版原生候选已在本机运行，不把候选运行、四目标编译或未签名/未公证包当成正式生产切换证据。旧 React/Tauri 源码和 Tauri 发布回退仍保留，直到事实源迁出、原生安装/升级/回退及跨平台和安全门槛关闭后再按计划退役。

## 2026-11-21 模式切换审计

- 复核 `apps/native/internal/desktop` 与 `apps/native/internal/client` 的本地/远端双向切换、设置返回、队列与工作台刷新、过期模式拒绝、保存后立即应用和读取失败重试路径；未发现需要新增修复的状态错乱。
- `go test ./internal/desktop ./internal/client -run 'ProcessingMode|RemoteState|Mode|Back|Refresh' -count=1`、完整 `go test ./internal/desktop ./internal/client -count=1` 及覆盖模式/返回/刷新用例的 `go test -race` 通过。本次审计没有启动旧 React/Tauri，也没有修改原生代码。

## 2026-11-21 主窗口对齐与候选重建

- 主窗口复用平台化窗口选项，尺寸按旧版 `tauri.conf.json` 对齐为 `1440×960`、最小 `1100×760`。macOS 使用隐藏内嵌标题栏及旧版交通灯坐标；Windows 保留系统标题栏。标题栏状态指标和动作按 MyGo 实际原生控件区域留白，避免与系统按钮重叠。
- 新增配置驱动的窗口几何/平台 chrome 回归及标题栏最小、默认尺寸布局回归。`go test ./internal/desktop ./internal/host ./cmd/liberty-native -count=1`、`pnpm native:check`、`pnpm native:inventory`、`pnpm native:build:matrix` 和 `git diff --check` 均通过；四目标结果只证明构建和二进制格式，不代表其他平台实机运行。
- `pnpm native:package` 生成 macOS ARM 候选 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-j2CEtw/`；归档 SHA-256 为 `fe989b3d0a41173f80fc77838cca7f65ab173be171ed3d7c0c47e56adc26a11d`。包标记仍为 `productionReady:false`、未签名、未公证。当前 PID `88302` 仍运行先前 `...-vHxK1o/` 构建；新包不会热替换该窗口，需正常退出后再运行 `pnpm native:launch` 才能载入本轮窗口修正。
- 当前运行进程仍是 Go/MyGo 候选；未发现旧 React/Tauri 进程。旧端源码及生产发布/兼容依赖仍保留，未删除、未切换默认生产入口。完整 UI/无障碍/输入法和跨平台实机、真实远端、供应链漏洞门禁、安装升级回退及签名公证仍阻止正式切换；没有提交、推送或发布。
- `git ls-files apps/native packages/desktop-resources` 在当前工作区均为 0；这些目录的新版实现和共享资源目前没有进入 Git 索引。此次没有暂存用户已有的大批工作区变更，因此不能声称干净检出可重建本候选。先安全保全并纳入版本控制，再从干净检出重复生成、检查和打包，是删除旧端或切生产入口之前的首要仓库门槛。

## 2026-11-21 新版窗口与旧端退役审计

- 按用户要求确认并保持新版候选运行：PID `15402` 的映像位于 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-j2CEtw/`；CUA 按完整 `.app` 路径读取到标题 `Liberty Native Candidate`、版本 `v1.1.24` 和工作台主窗口。进程打开独立 `com.westng.liberty.native-candidate` 资料库与 profile lock。进程清单没有发现旧 React/Tauri 客户端；没有打开旧 bundle、切换到生产资料或重启用户窗口。
- 路由审计确认旧版列出的 25 个入口在原生代码中有视图或打开方法映射；这不等于 URL/动态参数及独立窗口生命周期完全等价，也没有完成同数据、同主题、同尺寸的全路由新旧截图差分。Windows/Intel 实机、完整读屏与 IME 验收仍缺。
- 旧 React/Tauri 尚未全部下线或删除：正式 Release、质量门禁、schema/凭据迁移 oracle、security/runtime 检查和部分共享素材仍依赖它。并且 `git ls-files apps/native packages/desktop-resources` 均为 0；当前工作区的原生实现尚不能由干净 checkout 复现。直接删除会丢失生产发布回退与迁移验证，不是本次安全整改范围。
- 当前入口事实是 `pnpm start` 指向新版 `native:launch`；这只改变日常启动选择，不等于正式生产发布入口已切换。候选仍为 `productionReady:false`、未签名/未公证。完成资源与版本事实源迁出、等价安全/兼容门禁、跨平台安装升级回退验收、干净检出复现及生产观察/回滚演练前，不退役旧端、不切换生产发布链。
- 只更新本计划中的当前状态记录；未删除旧端文件、未修改生产发布配置、未提交/推送/发布。

## 2026-11-21 来源绑定、设置状态与候选边界复核

- 来源和窗口授权由进程内 capability 绑定任务来源及任务 ID；模式切换不会把既有窗口重定向到另一来源。远端连接目标或凭据变化后，旧授权会失效；来源不匹配的写操作不会回退到同 ID 的本地任务。当前代码与回归覆盖见 `application/windows_test.go`、`application/remote_test.go`、`application/remote_windows_integration_test.go` 和 `client/job_actions_source_test.go`；这是代码/测试证据，不代表真实远端服务验收。
- 设置控制器以版本比较和冲突 rebase 保存草稿，启动 locale/theme 参数只覆盖当前会话，异步初始读取不会覆盖启动参数；用户明确选择对应设置后才持久化。保存失败保留草稿并支持重试，关闭流程等待在途设置保存完成。处理模式由 `processingMode` 决定，读取失败保留最后有效模式并呈现可重试错误，不从 `backendUrl` 推断。相邻设置、恢复及关闭测试覆盖这些状态；本轮未重新运行 Go 测试。
- 主窗口尺寸对齐旧端默认 `1440×960`、最小 `1100×760`；macOS 使用内嵌标题栏与交通灯位置，Windows 使用系统标题栏，并为标题栏控件留出平台 chrome 空间。配置驱动测试覆盖几何与平台选项；测试对照不等于 Intel/Windows 实机或全 UI 截图验收。
- 启动行为仍以 `pnpm start` / `pnpm native:launch` 为日常候选入口：校验并选择已打包候选，不编译源码、不启动旧 Tauri、不强杀进程。历史本机复核记录了确切候选包启动及原生主窗口；本轮只审阅源码和既有记录，没有启动客户端，也未访问用户资料。
- `native:package` 负责选定目标的候选构建、版本来源一致性、候选清单/逐文件 SHA-256 和归档回读核验；六个版本来源包括两个 `package.json`、Tauri 配置、桌面 `Cargo.toml`、`Cargo.lock` 中的 `liberty` 包和 Go host 版本。四目标编译及测试包交叉编译由 `native:build:matrix` 负责。既有四目标包记录标注 `productionReady:false`、未签名/未公证；不能据此声称 Intel/Windows 实机、安装器、签名、公证或生产切换已验证。
- 上述文档核对子任务仅更新三份指定文档，没有重跑测试；既有 P5-A/P5-G 章节与验收条件保留原样。

## 2026-11-21 最新原生候选打包与启动

- `pnpm native:package` 成功构建并校验本机 macOS ARM 候选 `target/native/packages/liberty-native-candidate-1.1.24-darwin-aarch64-TLyYYr/`。候选清单确认版本 `1.1.24`、candidate 通道、隔离身份 `com.westng.liberty.native-candidate`；包内 6 个文件均纳入完整性清单，`productionReady:false`、未签名、未公证。
- 先正常结束旧构建进程，再运行 `pnpm native:launch`。新 PID `48071` 的可执行映像指向上述确切包内 `Liberty Native Candidate.app/Contents/MacOS/liberty-native`；按该完整 `.app` 路径检查无障碍树和截图，窗口标题为 `Liberty Native Candidate`，主工作台已显示。未调用通用 `Liberty` 名称解析，没有启动 React/Tauri，也没有选择生产资料目录。
- 本机进程检查只发现该 Native 客户端，没有旧 `target/debug/liberty`、Tauri 或 React 客户端进程。该结论限于当前机器、当前检查时点，不表示旧端源码、外部发行版或正式生产发布入口已退役。
- `git diff --check` 通过。`git ls-files apps/native packages/desktop-resources` 仍为 0；两目录源码当前未纳入 Git 索引，干净 checkout 无法重现此候选。没有擅自暂存这批文件或提交。
- 旧 React/Tauri 仍保留为冻结兼容代码、迁移/安全门禁来源和正式发布回退链；生产依赖安全门禁、全量 UI/IME/读屏与真实远端验收、Intel/Windows 实机、安装升级/回退、签名公证及可复现的版本控制状态尚未完成。因此本次只是重新构建并启动新版候选，不是正式切换或删除旧端的授权与证据。

## 2026-11-21 装配练习状态反馈回归

- `assemblyGameView.feedback` 现在按暂停、完成、超时、错配、正确和普通进行中状态生成双语反馈；恢复练习会清除上一轮命中提示，终态不会继续播报旧的错配或正确文案。状态分支在读取当前订单位置前先拦截完成/超时，避免终态索引越界。
- 新增 `TestAssemblyStatusAnnouncesCurrentFeedbackAcrossPhases`，通过 MyGo 组件无障碍树逐项核对 `RoleStatus`：开始后的当前步骤、错配扣时、暂停与恢复、正确后的进度/下一步、完成摘要和超时摘要；同时断言完成/超时不残留上一条错配状态。覆盖 `zh-CN` 和 `en-US`。
- 普通装配定向测试、`go vet ./internal/desktop`、定向 `go test ./internal/domain ./internal/desktop -run 'Assembly|WorkCarePractice'`、装配定向 race 和原生完整检查均通过。`pnpm native:check` 的 pnpm 依赖前置曾因无 TTY 拒绝清理 `node_modules`，未进入检查；随后直接运行等价的 `node scripts/run-native.mjs check` 通过 Node 生成物/策略检查、Go 模块校验、`go vet ./...` 和全量 `go test ./...`。
- 这批证据限于 headless 组件和 Go 并发测试：没有把 `RoleStatus` 树断言写成 macOS VoiceOver/Windows Narrator 验收，也没有新增物理键盘到画面反馈延迟、闲置/会议并行资源预算或 Intel/Windows 实机证据。P5-G1 仍未完成。

## 2026-11-21 旧 Tauri 退役决策收口

- 用户已明确要求：Native 重构完成后删除旧 Tauri，不保留“旧 Tauri 兼容保留”作为长期方案。这里的“保留”只表示在切换门槛关闭前冻结旧生产入口、迁移对照和回滚保护；它不是第二条产品线，也不是无限期维护承诺。
- 退役前必须完成三件事：把 schema/迁移、运行时与模型清单、模板、消息、素材和仍需长期回归的脱敏黄金夹具迁出 `apps/desktop`；从干净检出独立生成、兼容和打包；完成 Native 的业务、UI/无障碍、四平台、安全、资料接管、安装升级/恢复、签名/公证和生产观察/回滚证据。
- 以上条件通过后，采用一个可审查的切换变更：切换默认入口和 Release workflow，执行 Native 启动/锁/迁移/恢复烟测，然后删除 `apps/desktop` 的 React/Tauri/Rust GUI、commands/capabilities、旧构建发布链以及仅供旧端使用的依赖和检查。SQLite 迁移逻辑、共享资源和脱敏黄金夹具留在 Native/共享包中；不保留旧 Tauri 可执行文件或永久兼容发布。
- 当前条件尚未满足：Python 生产锁漏洞门禁仍阻断，Intel/Windows 实机、完整 UI/IME/读屏、真实远端、安装升级/回退、签名/公证和干净检出复现仍未闭环。因此本轮只修正终态和退出条件，暂不提前删除旧端源码或发布链，也不声称已完成 Native 切换。
