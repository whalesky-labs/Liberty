# 原生实时转写与翻译

本文维护当前 `apps/native` 源码行为。当前支持麦克风中英文实时转写，并默认逐段自动选择翻译方向：中文译成英文，英文译成中文，无需手选语种。识别使用 FunASR 流式 Paraformer，翻译使用内置管理的 Argos 中英双向模型及 CTranslate2；无需配置第三方服务或 API Key。系统声音已有 macOS 13+ ScreenCaptureKit 和 Windows WASAPI 回环适配，可选择系统声音或系统声音与麦克风混合输入；权限、设备切换与跨平台实机验收尚未完成。其他语种、悬浮字幕和译文朗读尚未实现。包版本、模型来源以共享 [运行时清单](../packages/desktop-resources/runtime/runtime-manifest.json) 为准；源码能力不代表所有平台实机或生产切换验收完成。

## 使用

1. 首次进入“实时翻译”时，点击“下载本地模型”跳转到“系统设置 → 运行与模型 → 语音模型”。先在“下载与安装记录”选择下载源，再通过“运行环境”的一键准备安装 Python、FFmpeg 和默认转写模型，最后返回“语音模型”下载流式识别和双向翻译模型；实时模型下载量约 990 MiB，运行环境与默认模型另计。首次下载需要网络，完成后收听和翻译均可离线运行。
2. 在“系统设置 → 会议处理 → 实时语音”中选择默认音频输入并开启或关闭自动中英互译；设置会保存到本机，并用于后续实时会话。当前会话继续使用开始时选定的设备和翻译开关。实时页的“收听设置”也可为当前应用进程调整输入设备和翻译开关。当前只支持中英文，按每段语音自动判断翻译方向。此页面不依赖“模型管理”里的总结模型。未选择音频输入时点击“开始收听”会先展开设备设置。
3. 空闲和会话结束后，中央机器人按原组件顺序循环播放全部 14 种动画，一轮 31.2 秒。点击“开始收听”。模型加载和预热完成后才打开所选音频输入；主界面以中央机器人动画和当前双语字幕为中心，只显示最近定稿及正在识别的原文。机器人使用 bloub 原始动画核心生成的形状、眼神与眨眼，随真实输入音量轻微形变；暂停时呈休息表情，模型准备时播放原版彗星，保存时显示思考动画。“查看完整字幕”展开完整记录，翻译针对定稿片段异步执行。含汉字的片段译成英文，纯拉丁字母片段译成中文；数字和标点片段保留原文。中英夹杂按中文方向处理，不自动切换总结或界面语言。
4. “暂停”关闭采集、处理尾部音频并保留已加载模型；“继续收听”重新打开所选设备。“结束”保存尾句并终止识别和采集进程。切换页面、查看历史或最小化窗口继续收听；历史/完整字幕页面仍提供暂停和结束，应用退出会收尾。
5. 点击右上角“历史记录”可分页查看、编辑定稿原文与译文、补译失败片段，并导出双语 SRT、VTT 或 TXT。修改原文后，应同步修改译文，或清空译文后补译。删除会话会删除关联字幕和事件，操作前有确认对话框。

音频不保存到文件。音频、原文和译文均在本机处理，不会发送给翻译服务。本地 SQLite 只保存定稿、译文与会话状态；临时识别文字保留在内存。重新启动后，未结束会话标记中断，可保留历史并补译，不能恢复原流式缓存。

翻译超时、模型加载或协议失败不阻塞原文采集。失败片段有明确状态，结束后可手动补译；取消勾选译文可仅转写。运行中不需要联网，也没有云端回退。语种判断基于双语 ASR 的定稿文字，识别错误也会影响翻译方向；当前范围限定中文和英文，不提供其他语言自动识别承诺。模型可能误译数字、币种、人名和专业词；应对照原文检查并使用定稿编辑，当前不提供术语表或准确率承诺。

## 生命周期和性能边界

- 每个进程最多一个实时会话，整个会话复用 ASR/VAD；实时会话与文件 ASR 互斥。已有文件任务不被强行中断，实时会话结束后排队文件继续执行。
- 使用单声道 16 kHz PCM16，采集帧 100 ms，ASR 块 600 ms，VAD 块 200 ms。600 ms 是块大小，不是端到端翻译延迟。
- 采集和识别输入各最多缓存 25 帧，合计约 5 秒。超过处理能力时停止并标明积压，不静默丢帧或无限增长。持续语音最多 12 秒分一次片段，长静音周期清理 VAD cache。推理线程等待音频时使用 OpenMP passive/零 block time，避免持续忙等；实际 CPU 与延迟仍随设备和音频变化。
- 主界面默认收起设备配置、历史和字幕列表。中央机器人复用 [bloub 固定源码](../packages/desktop-resources/presentation/bloub/README.md)，在构建时生成原始矢量动画帧，由 MyGo 绘制，无需 Vue、WebView、网络或视频解码。真实输入电平驱动额外形变与光晕，以 180 ms 平滑过渡；原版眼神和眨眼动画最高请求 30 fps，仅重绘动画区域。暂停、窗口隐藏或失焦时停止装饰动画，并遵循系统减少动态效果设置。眼神动画不代表正在采集声音，是否收听以文字状态为准。内存快照仍仅保留最近 100 个定稿和一个临时片段，完整字幕使用 MyGo 虚拟列表，历史按页读取。标题栏统计客户端及实际子进程占用，完整字幕中的翻译耗时来自实际运行。
- 麦克风：macOS 通过固定 ffmpeg 的 AVFoundation 输入采集，Windows 使用 DirectShow；系统声音：macOS 13+ 使用 ScreenCaptureKit（需系统屏幕与音频录制授权），Windows 使用 WASAPI 共享模式回环。混合模式按 100 ms 时钟将两个输入以各一半增益混合；静音也推进时间轴，每源缓存上限 25 帧，积压明确失败。扬声器外放可能使麦克风重复收录，建议使用耳机；当前不提供声学回声消除承诺。这复用项目已有受控进程和运行时，不引入未锁定采集库。Windows 实际设备采集仍需实机验证。
- 采集错误后可检查系统麦克风权限、设备是否移除或占用，再刷新设备。麦克风设备 ID 绑定本次枚举名称与索引，避免索引变化后静默切到其他麦克风。

## 协议和存储

`live_runner.py` 使用独立的 `liberty-live-asr` v1 JSON Lines 协议；[输入](../packages/shared-types/schemas/live/v1/input.schema.json)携带 base64 PCM、严格递增序号及音频时间偏移，[事件](../packages/shared-types/schemas/live/v1/event.schema.json)携带临时/最终片段、revision 和时间区间。每行最多 8192 字节，非法字段、重复键、倒退时间和序号拒绝；普通日志不得进入 stdout。文件 Runner v2 不变，旧 Tauri 不消费实时协议。

Go `runner.LiveCommand` 负责 stdin/stdout、超时和进程树退出；`application.LiveService` 负责预算、状态机、定稿事务和翻译；`client.LiveController` 管理异步 UI；`desktop.LiveView` 仅渲染和调用控制器。翻译独立于总结服务，`execution.LocalTranslator` 管理本地 `translation_runner.py` 进程。独立 [输入](../packages/shared-types/schemas/translation/v1/input.schema.json)与[输出](../packages/shared-types/schemas/translation/v1/event.schema.json)协议使用严格字段、递增序号、UTF-8 和长度检查；启动上限 30 秒、单段翻译上限 20 秒，取消及结束回收进程。CTranslate2 固定 CPU int8、单推理线程，两个翻译方向按需启动各自进程，在会话内复用，停止时一并回收。

schema v12 增加 `live_sessions.auto_detect_language`，旧会话默认关闭自动方向以保留历史语义，新会话默认开启。v11 新增 `live_sessions`、`live_segments` 和 `live_session_events`，来自共享迁移 SQL。Go 与仍保留的 Rust 注册同一迁移。升级失败整体回滚，旧数据保留；重复启动不重建会话表。转移资料拒绝活动实时会话。修改字幕递增 revision，旧编辑或迟到翻译不能覆盖新版本。`model_config_id` 存储本地模型标识；为兼容本轮早期候选保留的 `glossary` 列不在当前产品流程中使用。

## 本地模型来源

- 流式 Paraformer revision `70e272c03055313e90f34db80bef9c19745e41cf`；模型权重约 840 MiB，Apache-2.0。
- Argos 中文→英文 1.9 原始模型包约 71 MiB，英文→中文约 67 MiB，基于 OPUS-MT，CC-BY-4.0；署名与原始 README 随应用和模型保留。
- CTranslate2 4.9.1 官方 CPython 3.11 wheel 分平台锁定；MIT 是引擎自身许可证，不代表内嵌原生依赖的完整分发审查。

归档及展开后的所有文件固定 SHA-256 与大小。加载前检查精确目录清单，拒绝额外 Python 文件、符号链接、内容变化及缺失文件；每个会话启动前也重新核对内置识别/翻译脚本。Python 使用 `-I -B` 运行，不生成会破坏安装清单的字节码缓存。来源和声明见 [本地翻译 notices](../packages/desktop-resources/runtime/live-translation-NOTICES.txt)。原生依赖的完整分发许可仍属于生产运行时发布门槛。

## FunASR 升级候选

默认生产 profile 仍固定 `1.3.9+liberty.local2`。`funasr-upgrade` 是显式隔离候选，固定 `1.4.29+liberty.local1`、`rapidfuzz==3.14.6` 和 `websockets==17.4`，保留既有四模型 revision。local1 只调整 `oss2`/`six` 的依赖元数据与版本记账，不修改上游 Python 源码。仅切换候选不能声称生产升级已通过。

在仓库根目录准备候选输入，所有输入/输出路径必须为绝对路径，输出目录须不存在：

```sh
python3 -I -S python/funasr-runner/tools/build_upgrade_wheel.py \
  --upstream-wheel /absolute/funasr-1.4.29-py3-none-any.whl \
  --output-dir /absolute/new-upgrade-vendor
pnpm native:runtime:prepare --profile funasr-upgrade \
  --locks-directory "$PWD/packages/desktop-resources/runtime/candidates/funasr-upgrade" \
  --vendor-directory /absolute/new-upgrade-vendor \
  --output-directory /absolute/new-upgrade-inputs
pnpm native:runtime:security --profile funasr-upgrade \
  --runtime-input-directory /absolute/new-upgrade-inputs
pnpm native:package --target darwin-arm64 --runtime-profile funasr-upgrade \
  --runtime-input-directory /absolute/new-upgrade-inputs
```

按实际构建输出的原生可执行路径，使用 `--profile-directory <独立目录> --writable --prepare-database --runtime-profile funasr-upgrade` 启动。默认仍选 production，候选输入不覆盖旧环境；已升级到 v12 的数据库不能直接交给只支持 v11 或更早版本的旧二进制。回滚应使用升级前备份或保留的独立目录。

固定上游 wheel、候选配方及三锁可以复现并验收。目标实机、识别质量与 8 GiB 设备证据不足时，不切换默认 profile；Torch 既有安全例外不因 FunASR 升级而消失。

自动中英互译的真实链路验收可在既有 runtime_acceptance 测试中设置 `LIBERTY_NATIVE_AUTO_LANGUAGE_FIXTURE` 为固定上游英文样本的绝对路径。测试校验样本 SHA-256，将公开中英文样本串联后通过真实 ASR、双向本地翻译、SQLite、VTT 导出和重开验证；不使用用户录音。
