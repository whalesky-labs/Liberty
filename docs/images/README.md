# 图片与游戏素材

本页维护当前共享图片的来源、分层约束和同步方式。旧客户端截图、一次性生成提示词及制作日志已清理；应用实际消费的素材保留在共享资源目录。清单以生成器输入为准。

## 来源与同步

| 内容 | 当前来源 | 原生同步 |
| --- | --- | --- |
| 农场底图、土壤、八种作物、巡逻角色 | `packages/desktop-resources/images/farm-layers/` | `pnpm native:work-art:generate` |
| 市场封面、矿场/工厂/便利店、岗位与角色帧 | `packages/desktop-resources/images/work-maps/` | `pnpm native:work-art:generate` |
| 宠物商品与动作帧 | `packages/desktop-resources/images/pet/shop/`、`images/pet/action/`（相对共享资源目录） | `pnpm native:pet-art:generate` |
| 消息、图标、侧栏吉祥物与实时动画 | `packages/desktop-resources/i18n/`、`presentation/` | `pnpm native:presentation:generate` |

Go 嵌入目标位于 `apps/native/internal/desktop/presentation/`。修改共享来源后生成，不手改目标，也不另存一套 Go 专属来源。素材不因旧 GUI 退役而删除。

## 农场与工作地图

农场使用 `farm-background.png`，三个 `plot-<wheat|carrot|tomato>-soil.png` 土壤层，八个 `crop-<key>-overlay.png` 作物层，以及 `dog-patrol/dog-walk-01.png` 至 `dog-walk-08.png`。作物 key 为 wheat、carrot、tomato、pumpkin、corn、strawberry、blueberry、potato。市场封面另用 `work-maps/farm-isometric.webp`。

矿场、工厂与便利店分别使用 `mine-map.webp`、`factory-map.webp`、`convenience-store-map.webp`；市场底图为 `niuma-market.webp`。岗位内容和三组各九帧的标准角色图位于 `work-maps/content/`。精确名称与目标映射见[工作素材生成器](../../scripts/generate-native-work-art.mjs)。

当前农场与工作地图延续 gpt-image-2 制作的手绘卡通来源，岗位图可由已审定地图裁切或转制；再制作保持原有角色、视角、光照和比例，不混入照片或 SVG 占位。地图不烘焙 UI 文本、按钮、数值或水印；土壤和作物保留透明边缘。图像与热区使用相同坐标变换，不能以整图替换可交互的分层。奖励与解锁规则见[宠物系统](../pet-system.md)，不由文件名推断。

## 宠物与动画

宠物的 15 组原动作各有九帧，保留共享 `images/pet/action/` 来源。3D 像素候选的制作、校验、资源预算和预览见[模型说明](../../packages/desktop-resources/pet/models/README.md)，当前仍未替换正式桌宠。

实时翻译页的矢量动画使用 [bloub 固定源码](../../packages/desktop-resources/presentation/bloub/README.md)。其源码身份、许可证和 NOTICE 随来源保留，不能以删除旧文档为由清理第三方署名。

## 修改与验证

新图片先在制作目录人工检查，再替换对应来源并运行：

```sh
pnpm native:work-art:generate
pnpm native:work-art:check
pnpm native:pet-art:generate
pnpm native:pet-art:check
pnpm native:presentation:generate
pnpm native:presentation:check
```

按实际修改的来源选择相应命令，无需无差别重生成。正常构建消费已审定的本地素材，不要求图像服务或图像生成工具。检查通过后仍须验证比例、透明边缘、热区、动画、双语主题和实际窗口布局；涉及旧前端资源解析时补 `pnpm desktop:build:web`。
