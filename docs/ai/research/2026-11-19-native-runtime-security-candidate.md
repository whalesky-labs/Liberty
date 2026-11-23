# 原生迁移运行时安全候选隔离验证

> 本文为指定日期与输入的专项证据，不是全项目当前状态。操作入口见[开发指南](../../development.md)，正式约束见[原生架构](../../architecture/native-desktop-architecture.md)与[发布要求](../../architecture/release-readiness.md)。

文档类型：调研与诊断<br>
状态：2026-11-19 试验快照；2026-11-20 后续已隔离接入候选 manifest，生产切换仍阻断<br>
创建日期：2026-11-19<br>
最后核实：2026-11-20（仅核对后续状态标注）<br>
适用范围：Liberty 1.1.24；macOS ARM 实际推理，Intel Mac / Windows x64 资产和扫描；不改变 Windows x86 的既有运行时限制。<br>
权威边界：正文记录 2026-11-19 的一次有界试验；其后本候选已由独立固定 manifest 接入原生侧包并通过 2026-11-20 macOS ARM 运行时验收，但不构成生产安全放行、完整模型兼容或其他平台运行证明。当前正式发布结论以 `docs/architecture/release-readiness.md` 和实施计划后续快照为准。<br>
依据：当前生产文件、固定 OSV Scanner 2.5.0 的原始输出、官方 PyPI 元数据及资产哈希、原 validator/warmup/Runner 的隔离执行结果。完整本机证据位于忽略的 `target/native/security-remediation-20261119/REPORT.md`，不随源码分发。

## 结论

得到一条有 macOS ARM 实跑证据的有条件补丁候选，尚不能直接替换生产依赖：Python 3.11.18，Torch/Torchaudio 2.2.2，Transformers 5.23.0，FunASR `1.3.9+liberty.local2`，以及试验三锁中的其余精确版本。FunASR 补丁仅调整发行元数据，将未使用的 OSS2 依赖移到显式 extra，并补充 Six；没有修改 417 个 Python 源文件。

三候选锁按**未修改的现有例外**扫描均退出 0；空例外扫描均退出 1，Torch 2.2.2 仍有 **20 组漏洞、24 个公告记录**。原例外于 **2026-11-30** 到期，本候选不解决这些漏洞，也不能用延长或扩大例外宣称整改完成。主代理直接解析三份原始扫描输出复核了上述数量。

这不推翻当前安全阻断：候选尚未进入产品安装链，当前生产锁的扫描结论不能由候选锁的扫描替代。

## 实际执行及边界

| 验证 | 结果 | 不能推出的结论 |
| --- | --- | --- |
| 三候选哈希锁 | ARM/Intel 各 81 包，Windows x64 82 包；生成成功 | 不等于目标平台安装成功 |
| ARM 哈希安装及 pip check | 独立解包的 Python 3.11.18 创建全新 venv，最终 local2 锁完整哈希安装 81 包；安装集 82 项，额外仅 pip；包来源、库存及 pip check 通过 | 尚未通过产品安装器交付，不能替代 Intel/Windows 安装 |
| 原 validator、四模型离线 warmup | 全新环境明确使用四个本地模型目录后成功；43 文件、2,218,608,043 bytes 的校验清单保持原 revision | 不证明最低系统版本或其他平台可运行 |
| 原 Runner，speaker=false | 合成中文录音得到 1 段，disabled，5 个 V2 JSONL 事件，正文关键词与完成进度通过；该段 startMs/endMs 为 0/0 | 时间戳准确性未通过验收，不代表所有语言、时长或任务输入 |
| 原 Runner，speaker=true | 同一合成录音得到 6 段，completed，6 个 V2 JSONL 事件，包含 speakerSegments | 单人合成输入不能证明多人分离准确率 |
| Intel / Windows wheel | 各实际下载并验证 10 个关键官方 wheel，哈希属于最终候选锁 | 未执行两平台解释器、安装或推理 |
| 三平台 Python 归档 | 固定发行 URL 的 SHA 与大小核对成功 | 未执行 Intel/Windows 解释器 |
| 生产输入复核 | manifest、requirements、三锁、Runner、validator、warmup、例外配置共 9 项，与试验入场 SHA 一致；主代理另重算全新环境 19 项证据文件及两模式结果/进度摘要均匹配 | 不表示工作区此前没有其他改动；Go 宿主路径接线在试验期间由主代理另行整改 |

Transformers 的 Torch 后端因要求 >=2.6 仍处于禁用状态，没有修改其检测或伪造版本。四个固定模型通过 FunASR 原生实现完成实际推理，因此单凭该后端禁用不能断言这四个模型必然不可用；也不能将成功推广到其他 FunASR、Transformers、训练或服务端功能。

所有音频由系统语音生成，无用户媒体或生产资料。这里的“完成”指 Runner 结果及进度文件断言，不是产品 host 的端到端任务落库。

全新安装的早期阶段使用 Python audit guard，记录到两次被拒绝的 home 读取探测；续跑的离线 warmup 与两模式 Runner 进一步使用隔离 HOME、allowlist 环境、Python audit 及 macOS sandbox，拒绝真实 home 读取、目录外写入和网络。不能把续跑的原生系统调用限制倒推为原安装阶段已经具备相同隔离证据。中断和拒绝日志均保留，没有重复安装冒充一次连续成功。

与安全候选分开的生产路径整改已通过真实宿主验证：Go 宿主按选中 artifact 和同源固定清单解析四个模型本地目录，预热及转写共用映射，进程边界验证所属目录并覆盖继承的模型选择。使用原生产 Python 3.10 清单的独立 `TestPinnedRuntimeAcceptance` 通过，离线预热及原 Runner 两模式得到 1/6 段，disabled/completed。随后 `TestPinnedHostRuntimeAcceptance` 通过真实 host 装配、媒体授权、调度及 Runner 完成同样两模式，退出并重开资料库后逐项核对了逐字稿、说话人结果和每任务三项奖励；耗时 186.666 秒。这证明当前生产清单的新路径和宿主持久化流程，不构成 local2 候选已接入产品安装链、宿主或 GUI 的证据。

## 来源与交付要求

- 上游 FunASR 1.3.9 源码归档 SHA-256：`cb2b00d0b07d16d3c5996bfcd1c511dd676360a9831b1e92de8bd07eda355e82`。
- 上游 wheel SHA-256：`3a2be55d6b3abc24ebb5e537789f321076df3077b000ad29530d52b3c73eed72`。
- 本地补丁 wheel SHA-256：`38919720a30ab86428e2aded9a9e12cd9800cc36ebebc17f5b6b33182364f78e`；重复构建哈希一致，MIT 许可文件保留。
- 未核实 Git commit 与上游发行包的可验证构建关联，不声称 commit 溯源已验收。四个固定模型卡声明 Apache 2.0，模型许可与 FunASR 许可分别保留；不把包内声明当作完整法律审查。
- 本地发行不能仅改名以绕过扫描：试验另以安装集 CycloneDX 与独立上游 FunASR 1.3.9 来源 SBOM 扫描。把本地版本作为普通公共 PyPI requirements 扫描曾解析失败，生产治理工具的正式接线仍未完成。
- 当前安装需要本机 `--find-links`，尚无产品安装链验证过的不可变分发地址或随包 wheel 方案。正式交付须固定上游源码、补丁、构建工具、输出哈希、许可证和上游身份；不能只把本地版本号放入生产锁。

## 保留的失败与下一步

1. 直接升级官方包的 Intel wheel 缺口仍存在；降级 cryptography 到一个有 Intel wheel 的版本又触发真实漏洞扫描。失败已保留，没有新增例外。
2. 第一版仅移除 OSS2 的补丁通过 pip check，却在模型注册/warmup 缺 Six；补齐后才实跑成功。import-only 不替代模型验收。
3. 默认缓存别名的 warmup 试图访问用户 home，被隔离守卫拒绝；未放松守卫。Go 已改为显式固定本地路径并通过原生产运行时的两模式测试；候选的产品安装、首次下载、host 落库和回退仍需验收。
4. ARM 部分候选 wheel 的最低系统标签高于 macOS 11，最低系统承诺须重新核对；不得悄悄提高产品最低版本。
5. ARM 全新最终锁安装、固定四模型两模式推理及实际安装集扫描已有上述证据。下一阶段仍须确定上游修复或可审核的 vendor 交付路线，完成 Intel/Windows x64 同等实跑，以及产品安装、最低系统与回退；不能扩大现有例外或以本地源码不变宣称 Torch 漏洞消失。

本轮不再扩大版本组合试验，不修改生产 manifest、锁、例外或默认入口。完整重构继续按已确认方案执行，旧客户端代码保留，生产切换仍需全量业务/UI、远端、平台和发布验收。
