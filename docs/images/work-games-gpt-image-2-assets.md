# 打工小游戏 gpt-image-2 资产说明

最后核实：2026-11-21，核对素材路径和原生同步入口；生成服务可用性未在本轮调用验证。

本文记录「矿场挖矿」「工厂打螺丝」「便利店值班」三款游戏的图片资产口径。验收标准对齐农场：运行时图片必须来自 `gpt-image-2` 生成或基于 `gpt-image-2` 地图源裁切/转制，不能混入 SVG 占位、照片素材或纯 CSS 假图。

## 运行时资产

| 游戏 | 地图资产 | 岗位内容资产 |
| --- | --- | --- |
| 矿场挖矿 | `packages/desktop-resources/images/work-maps/mine-map.webp` | `packages/desktop-resources/images/work-maps/content/mine-*-content.png` |
| 工厂打螺丝 | `packages/desktop-resources/images/work-maps/factory-map.webp` | `packages/desktop-resources/images/work-maps/content/factory-*-content.png` |
| 便利店值班 | `packages/desktop-resources/images/work-maps/convenience-store-map.webp` | `packages/desktop-resources/images/work-maps/content/store-*-content.png` |

三张旧 SVG 占位图已移除。页面只引用 `.webp` 地图和 `.png` 岗位内容贴图。

## 生成记录

地图 prompt:

- `docs/images/prompts/work-games-mine-map-prompt.txt`
- `docs/images/prompts/work-games-factory-map-prompt.txt`
- `docs/images/prompts/work-games-store-map-prompt.txt`

岗位内容贴图 prompt:

- `docs/images/prompts/work-game-content-icons-jobs.jsonl`

岗位贴图的独立生成命令：

```bash
node scripts/run-codex-image-gen.mjs generate-batch \
  --model gpt-image-2 \
  --size 1024x1024 \
  --quality high \
  --output-format png \
  --input docs/images/prompts/work-game-content-icons-jobs.jsonl \
  --out-dir output/imagegen/work-games/content-key \
  --concurrency 2 \
  --max-attempts 2 \
  --force
```

独立贴图生成后，使用 chroma-key 去底并复制到运行时目录：

```bash
for file in output/imagegen/work-games/content-key/*-key.png; do
  name="$(basename "$file" -key.png).png"
  python "${CODEX_HOME:-$HOME/.codex}/skills/.system/imagegen/scripts/remove_chroma_key.py" \
    --input "$file" \
    --out "packages/desktop-resources/images/work-maps/content/$name" \
    --auto-key border \
    --soft-matte \
    --transparent-threshold 12 \
    --opaque-threshold 220 \
    --despill \
    --force
done
```

## 两端使用与同步

- `WorkGameView` 使用 1536x1024 世界坐标，与农场一样支持拖拽、滚轮缩放、热区聚焦。
- 每个岗位热区必须显示岗位内容图片、状态气泡、进度条和操作反馈。
- 纯 CSS 圆点只作为缺失资产兜底，不应出现在最终验收路径。
- 牛马市场卡片使用同一套地图资产作为封面。

Go/MyGo 候选沿用这些来源，通过以下命令生成嵌入素材：

```sh
pnpm native:work-art:generate
pnpm native:work-art:check
```

原生地图由 `apps/native/internal/desktop/work_scene.go` 绘制；市场卡片和练习入口不能各自复制另一套素材规则。角色标准帧以生成器清单为准；历史 `.txt`/`.jsonl` 是美术输入，不证明当前服务或模型可用。配方中的去底工具需本机已安装相应图像工具，正常构建不需要执行生成命令。总入口见[素材说明](README.md)。
