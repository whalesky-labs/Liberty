# Intel macOS 更新运行时的隔离验证

文档类型：调研<br>
状态：已完成（技术试验）；生产替换未完成<br>
创建日期：2026-11-25<br>
最后核实：2026-11-25<br>
适用范围：Liberty 固定 FunASR 四模型的 Intel macOS 运行时替代路径<br>
权威边界：证明下列精确输入在 Rosetta 下能导入并执行模型；不证明 Intel 实机、完整供应链、最低系统、托管安装或生产就绪。生产配置仍以共享 runtime manifest 为准。<br>
依据：conda-forge 包元数据、固定摘要安装、本轮 Python 库存与 OSV 扫描、当前 Runner 的两种说话人模式结果；项目许可证规则与发布要求。

## 结论

官方 PyPI 的 CPython 3.11 Intel macOS Torch/Torchaudio wheel 仍停在 2.2.2，但这不意味着 Intel 完全没有较新二进制来源。conda-forge 仍提供较新 Intel 包。本轮隔离验证的 Torch 2.14.1 能运行当前 FunASR 四模型，Python 库存也通过无例外扫描。

该结果尚不能直接替换生产运行时：解析出的完整 Conda 环境包含项目当前拒绝分发的 `readline 8.3`（元数据许可证 `GPL-3.0-only`），同时引入原生库分发、重定位和不同最低系统要求。未修改许可证策略，未延长 Torch 例外，未把此试验注册为可安装的生产 profile。

## 输入与复现边界

试验保持现有 FunASR `1.3.9+liberty.local2`、NumPy 1.26.4、CPython 3.11.18 与四个固定模型 revision。新增输入从官方或维护者渠道按确切 URL 和 SHA-256 下载；安装使用显式文件清单，不在安装阶段重新求解依赖。

| 输入 | 固定版本 / 构建 | SHA-256 |
| --- | --- | --- |
| [Micromamba 官方发行](https://github.com/mamba-org/micromamba-releases/releases/tag/2.10.0-0) 的 macOS ARM 工具 | 2.10.0-0 | `c547639f5caf742236641c362746abb4a3505774753ecd08fe341643670c4c01` |
| [conda-forge PyTorch](https://api.anaconda.org/package/conda-forge/pytorch) | `2.14.1 cpu_generic_py311_h2e46ac2_2`，osx-64 | `53854408a6e4c2c427f2de3c90b5871959b92ca7bfa4dd33fbcd9115ed7e670b` |
| [conda-forge libtorch](https://api.anaconda.org/package/conda-forge/libtorch) | `2.14.1 cpu_generic_hd61afe1_2`，osx-64 | `9f6a31c0d126e6aeff3f56da2c2e6d7bbc4c69f754721551b10a12645cde7796` |
| [conda-forge Torchaudio](https://api.anaconda.org/package/conda-forge/torchaudio) | `2.11.1 cpu_py311hb91dbf8_1`，osx-64 | `91931de62f93a85c991999f35a26bf1ef8b67cda963c30437e3e37faa11e6793` |
| [conda-forge Torchvision](https://api.anaconda.org/package/conda-forge/torchvision) | `0.29.2 cpu_py311_hd4ff0fd_0`，osx-64 | `d706b548bb2ccc197c0b26226a1fce49f8d9251202faceda0ac2ff963d1d422d` |

完整求解含 144 个 Conda 包，下载约 248 MB，逐件固定 SHA-256。其显式清单摘要为 `525b116c8de95c78f4db02c5f4949438b4859d91022384750a4dce78b2029e9b`。随后按现有 Intel 平台哈希锁安装 77 项 Python 依赖，排除已由本试验固定的 Torch、Torchaudio、Torchvision、NumPy 四项；FunASR wheel 继续使用已核验的 local2 配方产物。试验锁摘要为 `363a638f32d766222fe053153c9e99fe3e64e5d29d65dcaa2bbb3b0bd984eec4`。这些本地试验文件在忽略目录中，不是正式支持的锁或长期可用的发布资产。

## 本轮实际结果

- Python 二进制为 Mach-O x86_64，运行时报告 `x86_64`；实际主机为 Apple Silicon，通过 Rosetta 执行。不能标记为物理 Intel 验收。
- Torch、Torchaudio、Torchvision 和 NumPy 导入及矩阵运算通过，`pip check` 无冲突。当前 `runtime_validate.py` 通过；Transformers 的 Torch 后端在此试验中为 enabled，不能据此扩展产品支持到任意 Transformers 模型。
- 使用当前 Runner、CPU、2 线程、固定模型附带音频和离线模型路径，关闭说话人模式耗时 68.24 秒，得到一段逐字稿和 `disabled`；开启模式耗时 130.92 秒，得到一段逐字稿、一段说话人结果和 `completed`。输出均为协议 v2。这不是 CER/WER/DER 或性能门槛验收，也没有经过 Native 托管安装和 host 持久化链路。
- 固定 OSV Scanner 2.5.0 对 93 个 Python 身份执行无例外扫描，未发现已知漏洞。包含 local2 和上游 FunASR 1.3.9 身份，扫描覆盖逐项核对通过；该库存不覆盖 Conda 的全部原生库。不能将 Python 扫描结果称为完整环境无漏洞。
- 生产策略另行删除了 11 项已无实际发现的 Transformers 历史例外，完整生产扫描仍通过；20 项 Torch 例外的到期日保持 2026-11-30。

## 仍需解决

1. 依据[依赖治理配置](../../../config/dependency-governance.json)，当前整套 Conda 解不能直接作为交付产物。应评估可审核的 Torch wheel 构建，或仅打包经完整依赖与许可证验证的必要原生库；不能直接删除许可证信息或隐去包身份。
2. PyTorch/libtorch 包元数据要求 macOS 12 及以上。最低系统支持、动态库搜索路径、解释器 ABI、可复现构建与固定发布来源需独立确认。
3. 正式替换必须同步平台锁、共享 manifest、builder/validate、库存/SBOM、安装失败重试与恢复，再完成目标实机和真实媒体差分。当前实验没有改变生产清单或默认引擎。

长期门槛以[发布要求](../../architecture/release-readiness.md)为准；本文记录可行性证据与尚未通过的条件，不授权生产切换。
