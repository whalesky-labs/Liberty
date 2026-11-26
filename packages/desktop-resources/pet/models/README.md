# Libby 模型资源 / Libby model assets

本目录包含图形测试夹具和像素制作候选。当前像素外观与同步播放已获用户确认，可继续原生接入；**尚未替换正式客户端桌宠**。

This directory contains renderer fixtures and pixel candidates. The current
pixel appearance and synchronized playback are approved for continued native
integration; the production client companion has not been replaced.

## 资源状态 / Status

- **当前全套候选**：`candidates/pixel-actions/` 中的 15 个 GLB，编辑源为 `source/pixel-actions/` 中的 15 个 Blender 文件。已生成全部动作，但仍有可见姿态／接触差异，未完成逐帧 1:1 验收。`completedActions` 为空，不能将 `generatedActions` 当作验收通过。
- **原捧脸基准**：`candidates/voxel-study/libby-voxel.glb` 与 `source/libby-voxel.blend` 保留用户要求恢复的原像素资产，用于回归对照。
- **被否定的版本**：`candidates/cartoon-study/` 为用户否定的圆润造型；根目录的 `crush.glb`、`rope.glb`、`work.glb` 是更早被否定的图形夹具。根清单保持 `rejected`，这些资产不进入生产。

全套包含 `crush`、`defecate`、`drive`、`eat`、`gaming`、`pants`、`reading`、`rope`、`run`、`slack`、`sleep`、`snow`、`studying`、`toy`、`work`。每组使用对应 9 张原帧（合计 135 张），具备独立角色／道具体积、骨骼轨道及几何形变。GLB 不包含图像贴图或整帧角色网格切换。骨骼、角色／道具形变及绳索共用同一个 STEP 时间轴，每 300 ms 同步切换完整姿态，保持原图九帧节奏。禁止骨骼提前移动、形变仍停在上一帧，也不单独插值绳索与握柄；否则会造成部件断开。此修复采用像素定格节奏，不代表新增了连续补间动画。侧背面与遮挡结构是推定。

已按九个关键姿态修正写字／打工的手与道具、睡觉头部／身体、跑步手脚及跳绳投影。原图的捧脸／整理裤子／玩积木后几帧存在头套内部透明缺块，模型使用羊毛材质补齐，原始 PNG 保留。当前仍有 2 px 采样与颜色量化误差、侧转时的体积接缝、绳索细节／地面阴影差异，不能宣称所有视角达到精确 1:1。当前 macOS 原生预览已支持蒙皮／形变；正式客户端集成、长期运行占用及平台验收未完成。

The current full set is `candidates/pixel-actions/`, with fifteen editable
sources under `source/pixel-actions/`. All fifteen actions have been generated
from 135 reference frames with closed component volumes, skeletal weights and
geometric morphs, without exported image textures or whole-frame mesh swaps.
Nine-pose corrections now cover the body, hands, props and expressions. All bones, corrective shapes and the rope share one STEP
timeline and switch as a complete assembly every 300 ms. This preserves the
original nine-pose cadence; it is not newly interpolated continuous animation.
Known limitations remain: 2 px sampling, palette quantization, static side seams
and cable/shadow details. Transparent holes inside late crush/shorts/toy source
frames are completed with wool material; the original PNGs are unchanged.
Generated does not mean visually accepted or exact from every angle.
The original crush study remains a baseline. Native macOS preview integration supports these assets. Production integration,
long-running performance and platform acceptance remain incomplete. Do not promote by changing status.

## 生成与检查 / Build and verify

使用 Blender **4.5.17 LTS**，通过 `LIBBY_BLENDER` 指定可执行文件。开发机默认查找忽略目录 `target/native/pet3d-fidelity/tools/Blender.app/Contents/MacOS/Blender`，不是应用依赖或跨平台安装约定。

```sh
pnpm native:pet3d:actions:build
pnpm native:pet3d:actions:check
pnpm native:pet3d:actions:preview
```

当前生成器为 `scripts/blender/build-libby-actions.py`，部件边界、手势与绳索路径在 `libby_action_specs.py`，闭合姿态体积在 `libby_pose_volumes.py`，共用坐标／材质在 `libby_voxel_mesh.py`。`libby_glb_pack.py` 合并轨迹完全一致的顶点，将颜色／刚性权重压成标准归一化字节，并去除可重建的平面法线；不增加运行时解码扩展。原捧脸基准仍用 `native:pet3d:voxel:*` 命令。预览共用端口，切换前关闭旧服务。

全套构建先执行离线运动测量（`measure-libby-motion.py`，OpenCV **4.11.0.86**），再拟合骨骼与部件形变。OpenCV 仅放在忽略目录，不是应用依赖；本地制作安装流程目前只验证 macOS arm64，固定官方 wheel URL 和 SHA-256。原图校验和改变时拒绝使用旧运动测量。自动拟合结果不代表美术验收。

修改生成器或原图后重新生成。检查涵盖骨骼、蒙皮、形变轨道、无图像贴图、资产预算和校验和；要求固定的本地 Khronos validator（`gltf-validator@2.0.0-dev.3.10`），缺失时失败，不宣称执行了该项。生成阶段检查网格边界、非流形边、正体积，重开源文件检查九个姿态、关键帧中间时刻与循环首尾，并在全套验证通过后复制候选。Blender Python 异常按失败退出。`verify-libby-action-playback.mjs` 使用预览同版本 Three.js 加载最终 GLB，验证每个关键时刻的形变权重、每部件包围盒与 Blender 求值一致、过渡坐标和循环首尾；还检查每个 30 fps 播放采样及关键点前后，断言各部件保持同一完整姿态，防止帧间错位。候选制作上限为 150,000 三角面／18 MiB 每组，包含原帧校正与封闭道具，不是桌宠性能合格线。

`review-libby-actions.py` 使用 Pillow/numpy 输出 135 张原帧的并排图与轮廓差异图；数值用于定位问题，不能解释为“还原百分比”。

Set `LIBBY_BLENDER` to Blender 4.5.17 LTS when needed. Rebuild after reference or
source changes. Offline OpenCV fitting is verified on macOS arm64 only and does
not add an application dependency. Full-set checks require the pinned Khronos
validator and verify evaluated animation, geometry and hashes. Image comparison
metrics are diagnostic, not percentages of likeness or acceptance.

## 本地对照 / Local review

预览复用固定 `three@0.180.0` 的本地文件（通过 `native:pet3d:voxel:preview` 可下载并验证 SHA-512），只保存在 `target/`。全动作页通过回环地址 `http://127.0.0.1:43187/` 提供动作选择、九帧原图对照、时间轴、旋转、光照和骨架显示。默认暂停，时间轴上限 30 fps，模型只在姿态改变或视角／光照操作时重绘，页面隐藏时停止计时；切换后释放旧网格、材质与骨架，一次只保留当前动作，整套较大资产不预加载。

关闭终端进程即可关闭服务。该页不会启动 Liberty 客户端，不接触数据库，不构成原生宿主集成。

The loopback viewer reuses SHA-512-verified Three.js 0.180.0 under ignored
`target/`. It starts paused, caps playback at 30 fps, stops while hidden, and
releases previous meshes, materials and skeletons on action changes, and cancels
superseded downloads. It is an
asset review tool, not a production pet renderer or replacement UI framework.

## 原生接入预览 / Native integration preview

用户确认的像素版本通过 `candidates/pixel-actions/native-review.json` 绑定准确哈希，
用于原生接入；该评审不代表生产、跨平台或性能验收。`native-reference.json` 是独立
Three.js 顶点求值样本，由 `scripts/generate-companion-native-reference.mjs` 生成。

```sh
pnpm native:pet3d:acceptance
pnpm native:pet3d:preview
```

当前 macOS 使用 Swift/Metal 与 MyGo `native.Texture` 直接合成，支持 15 组动作选择、
逐帧、播放暂停、旋转和透明拖拽预览；保持原九帧的同步节奏。验收命令还执行 30 秒隔离渲染资源采样，并明确区分测试进程与完整客户端预算。
当前动作上传后释放 CPU
几何副本，切换释放旧 GPU 模型缓冲区，隐藏／暂停不安排动画重绘。窗口不打开用户
数据库。详见 [原生模块说明](../../../../apps/native/internal/desktop/presentation/companion3d/README.md)。

The native preview uses Swift/Metal and MyGo textures on macOS. It loads only the
selected action, releases CPU upload copies, and stops animation wakeups while
paused or hidden. Windows rendering, production integration and full-app budgets
remain unverified.
