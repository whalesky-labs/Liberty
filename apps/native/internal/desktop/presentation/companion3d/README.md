# 原生像素模型 / Native pixel models

`PixelLibrary` 只读取仓库制作的 15 组动作，校验清单、逐资产 SHA-256 和
`native-review.json` 中允许继续原生接入的版本。目录通过只读 `fs.FS` 注入；不接受
模型中的外部 URL／路径。被否定的三个旧模型仍是独立测试夹具，不进入像素预览。

`DecodePixel` 是有界 glTF 子集：索引三角网格、归一化顶点色、四骨骼权重、逆绑定矩阵、
八个位置形变目标，以及共享的十点 STEP 时间轴。每个 300 ms 姿态同时改变所有骨骼和
形变，包括道具／绳索，避免旧版连续骨骼与定格形状产生分离。未知扩展、外部缓冲区、
无效层级、越界索引和不同步的动画拒绝加载。

GPU 上传格式为：64 字节顶点（位置 4×float32、颜色 4×float32、关节索引 4×float32、
权重 4×float32）；96 字节形变（8×3 float32）；uint32 索引。姿态包首个 4×4 矩阵是
相机，之后为 `globalJoint × inverseBindMatrix`。上传跨语言边界使用共享 `native.Buffer`，避免将整个模型复制进编码包和 Swift Data。索引和形变只上传一次；`Pose` 重用
调用方缓冲区，`ReleaseGeometry` 在上传成功后释放 CPU 数据副本。

`client.CompanionPreview` 串行加载并丢弃陈旧响应；隐藏／暂停没有动画计时唤醒。
Metal 直接写入 MyGo `native.Texture` 的 IOSurface，日常播放不进行 CPU 图片回读。
`Snapshot` 仅供验收截图；缓冲区统计不等于进程内存或真实显存。

```sh
pnpm native:pet3d:check
pnpm native:pet3d:acceptance
pnpm native:pet3d:preview
```

预览使用现有 MyGo 窗口与透明拖拽路径，不读取用户资料库。正常客户端仍使用原桌宠；
没有将实验模块加入正式打包。macOS 使用 Swift/Metal；Windows 当前返回不支持，不能
把 Go 入口交叉编译通过当作跨平台渲染完成。

Go 测试以固定 Three.js 0.180.0 产生的独立顶点样本核对全部 135 个姿态，样本由根脚本
`generate-companion-native-reference.mjs` 维护。模型变更须重新生成样本，并重新评审
`native-review.json` 绑定的资产，不能自动沿用旧评审。

The bounded GLB subset supports indexed meshes, colours, skins and eight position
morphs on a shared STEP timeline. Geometry uploads once; pose sampling reuses its
buffer. Native preview uses MyGo and Metal without a browser or per-frame image
readback. The review manifest pins the approved input hashes. This is a native
integration preview, not a production switch; Windows rendering is unsupported.
