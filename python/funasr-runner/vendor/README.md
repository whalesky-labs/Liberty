# FunASR 固定 wheel 构建

[文档总览](../../../docs/README.md) · [开发与验收](../../../docs/development.md)

`../tools/build_vendor_wheel.py` 用 Python 标准库从显式选择的本地上游 wheel 构建固定 `1.3.9+liberty.local2`，不下载、安装或执行包代码。生产运行时 `2026.11.22-python311-security.1` 使用 CPython 3.11.18 和该 wheel；运行时清单的 `productionInputs` 固定构建器与八个输入的摘要。历史 `security-candidate` 的配方和固定输入保持可复现，配方中的候选交付描述记录最初边界，本身不构成生产安全或平台验收结论。

在仓库根目录运行，输入必须是 [funasr-1.3.9.json](funasr-1.3.9.json) 指定的官方 wheel，输出目录必须尚不存在，其父目录应已存在：

```sh
python3 -I -S python/funasr-runner/tools/build_vendor_wheel.py \
  --upstream-wheel /absolute/path/funasr-1.3.9-py3-none-any.whl \
  --output-dir /absolute/path/new-vendor-output
```

配方固定来源 URL、SHA-256、文件大小、ZIP 资源上限和元数据前提。补丁将 `oss2` 移至显式 `oss` extra 并补充 `six`；发行版本、`version.txt`、dist-info 路径和 RECORD 随之更新。417 个 Python 源文件、许可证及其余载荷字节保持不变。路径穿越、链接、重复项、损坏或不完整的 RECORD、来源摘要不符、既有输出目录均拒绝。

成功输出五个文件：wheel、`METADATA.patch`、`recipe.json`、`provenance.json` 和 `source.cdx.json`。provenance 记录构建器、来源、配方、补丁与产物摘要，以及 Python 文件清单和许可证摘要。来源 SBOM 保留 `pkg:pypi/funasr@1.3.9` 上游身份，不能用本地版本名隐藏上游漏洞；它只描述该 wheel 的来源关系，不是全部运行时依赖的完整 SBOM。本目录中的 `source.cdx.json` 是与配方固定输出逐字节匹配的来源清单，供默认生产安全门禁读取；wheel 与运行时下载物留在忽略目录。

macOS ARM / Python 3.12.11 的两次独立构建逐文件一致；2026-11-22 又使用托管 CPython 3.11.18 重建并核验全部五个输出，复现 wheel SHA-256 `38919720a30ab86428e2aded9a9e12cd9800cc36ebebc17f5b6b33182364f78e`。Intel/Windows 解释器和其他压缩器组合尚未实跑；产物与固定摘要不符会失败，不能跳过校验。

生产安装链随包提供构建器和配方，从配方的固定官方 URL 下载上游 wheel 并验证大小和 SHA-256，使用托管解释器构建，并逐文件验证五个输出后再执行带 `--require-hashes` 和本地 `--find-links` 的依赖安装。构建失败或摘要不符必须阻断安装。`node scripts/check-native-runtime-security.mjs --production` 验证仓库内真实生产锁、构建器、配方和来源 SBOM，并对三平台依赖和上游身份各执行当前策略与无例外真实扫描。`node scripts/prepare-native-runtime.mjs --profile production --locks-directory <absolute> --vendor-directory <absolute> --output-directory <new-absolute>` 可独立核验完整八件输入；默认 profile 仍为显式候选。

最低系统、Intel/Windows 目标平台实际安装运行和安装器回退继续遵循 [发布门禁](../../../docs/architecture/release-readiness.md)。Torch 2.2.2 是 Intel macOS 最后一个官方 wheel；Transformers 5.23.0 的 Torch 后端要求 Torch ≥2.6，因此该后端处于禁用状态。固定 Paraformer/VAD/标点/说话人模型使用 FunASR 自身实现，须通过离线 warmup 和真实推理验收；导入成功不代表支持其他 Transformers 模型。当前策略扫描通过也不消除无例外扫描保留的 Torch 漏洞，不延长现有例外。
