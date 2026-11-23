# 农场地图素材规范

最后核实：2026-11-21，依据两端当前素材与 `scripts/generate-native-work-art.mjs`。文件名保留原制作计划路径以维持引用；本文现维护运行时素材说明。原始完整地图和分层提示词分别见[完整图输入](farm-map-full-prompt.txt)、[分层输入](farm-map-layer-prompts.md)。

## 当前使用的分层

来源目录为 `packages/desktop-resources/images/farm-layers/`。早期示意文件名 `plot-1-soil.png` 等不作为运行时契约。

| 文件 | 用途 |
| --- | --- |
| `farm-background.png` | 农场场地底图 |
| `plot-wheat-soil.png`、`plot-carrot-soil.png`、`plot-tomato-soil.png` | 三个地块的土壤层 |
| `crop-<key>-overlay.png` | 八种作物覆盖层；key 为 wheat、carrot、tomato、pumpkin、corn、strawberry、blueberry、potato |
| `dog-patrol/dog-walk-01.png` 至 `dog-walk-08.png` | 巡逻角色帧 |

市场卡片另用 `work-maps/farm-isometric.webp`。原生 UI 通过 `presentation.WorkArt` 读取生成嵌入资源，地图与热区使用同一变换。透明度、地块轮廓和相对位置应与现有场景保持一致；不能以整幅验收图替换交互分层。

## 再制作规则

1. 用现有整图或已审定素材作为视觉参考，保持手绘卡通风格、视角、光照和比例。
2. 地图图像不烘焙 UI 文本、数值、按钮或水印；交互状态继续由客户端绘制。
3. 土壤与作物保留可组合的透明边缘。生成后人工检查边缘和热区，不凭文件存在判断验收。
4. 历史提示词中的 `plot-1/2/3-*` 是制作中间名称；发布前按上表与生成器清单转换，不批量覆盖运行时目录。
5. 新素材先在制作输出目录审查，再更新来源；不引入另一套 Go 专属图像副本维护流程。

## 同步与检查

```sh
pnpm native:work-art:generate
pnpm native:work-art:check
```

前端资源解析变化再运行 `pnpm desktop:build:web`；原生相关界面运行组件检查与实际目视验证。具体制作命令见[配方](farm-map-imagegen-runbook.md)，玩法数值见[宠物系统](../pet-system.md)，不要从素材文件名推断奖励或解锁条件。
