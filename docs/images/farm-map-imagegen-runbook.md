# 农场地图 gpt-image-2 执行清单

适用范围：按需重新制作美术素材，非构建或客户端启动步骤。2026-11-20 核对 wrapper 和文件引用，未调用图像服务；模型、额度及本机 CLI 需由制作环境提供。生成中间文件名保留原配方，运行时命名与 Go 同步见[素材规范](farm-map-gpt-image-2-asset-plan.md)。

## 前置条件

```bash
export FARM_OUT=output/imagegen/farm-map
mkdir -p "$FARM_OUT"
```

正式执行前必须通过项目 wrapper 确认当前 Codex provider。wrapper 会读取 `~/.codex/config.toml` 的 `model_provider` 和对应 `base_url`，读取 `~/.codex/auth.json` 的 `OPENAI_API_KEY`，再显式注入给 Image API CLI。不要直接依赖 SDK 默认地址，也不要手写 `OPENAI_BASE_URL=https://api.openai.com/v1`。

```bash
node scripts/run-codex-image-gen.mjs --print-effective-config
```

## 1. 生成完整场地图

```bash
node scripts/run-codex-image-gen.mjs generate \
  --model gpt-image-2 \
  --size 1536x1024 \
  --quality high \
  --out "$FARM_OUT/farm-full-map.png" \
  --prompt-file docs/images/farm-map-full-prompt.txt
```

验收 `farm-full-map.png` 后，才能继续拆层。

## 2. 生成场地层

```bash
node scripts/run-codex-image-gen.mjs edit \
  --model gpt-image-2 \
  --quality high \
  --image "$FARM_OUT/farm-full-map.png" \
  --out "$FARM_OUT/farm-background.png" \
  --prompt-file docs/images/prompts/farm-background-prompt.txt

node scripts/run-codex-image-gen.mjs edit \
  --model gpt-image-2 \
  --quality high \
  --image "$FARM_OUT/farm-full-map.png" \
  --out "$FARM_OUT/farm-foreground.png" \
  --prompt-file docs/images/prompts/farm-foreground-prompt.txt
```

## 3. 生成空土地图

```bash
node scripts/run-codex-image-gen.mjs edit \
  --model gpt-image-2 \
  --quality high \
  --image "$FARM_OUT/farm-full-map.png" \
  --out "$FARM_OUT/plot-1-soil.png" \
  --prompt-file docs/images/prompts/plot-1-soil-prompt.txt

node scripts/run-codex-image-gen.mjs edit \
  --model gpt-image-2 \
  --quality high \
  --image "$FARM_OUT/farm-full-map.png" \
  --out "$FARM_OUT/plot-2-soil.png" \
  --prompt-file docs/images/prompts/plot-2-soil-prompt.txt

node scripts/run-codex-image-gen.mjs edit \
  --model gpt-image-2 \
  --quality high \
  --image "$FARM_OUT/farm-full-map.png" \
  --out "$FARM_OUT/plot-3-soil.png" \
  --prompt-file docs/images/prompts/plot-3-soil-prompt.txt
```

## 4. 生成作物种植图

```bash
node scripts/run-codex-image-gen.mjs edit \
  --model gpt-image-2 \
  --quality high \
  --image "$FARM_OUT/farm-full-map.png" \
  --out "$FARM_OUT/plot-1-wheat.png" \
  --prompt-file docs/images/prompts/plot-1-wheat-prompt.txt

node scripts/run-codex-image-gen.mjs edit \
  --model gpt-image-2 \
  --quality high \
  --image "$FARM_OUT/farm-full-map.png" \
  --out "$FARM_OUT/plot-2-carrot.png" \
  --prompt-file docs/images/prompts/plot-2-carrot-prompt.txt

node scripts/run-codex-image-gen.mjs edit \
  --model gpt-image-2 \
  --quality high \
  --image "$FARM_OUT/farm-full-map.png" \
  --out "$FARM_OUT/plot-3-tomato.png" \
  --prompt-file docs/images/prompts/plot-3-tomato-prompt.txt
```

## 5. 映射到当前运行资产

只有通过视觉验收并适配现有层尺寸后，才更新正式来源目录。初版中间文件与当前来源的映射为：

| 制作中间文件 | `packages/desktop-resources/images/farm-layers/` 下的来源 |
| --- | --- |
| `farm-background.png` | `farm-background.png` |
| `plot-1/2/3-soil.png` | 分别为 `plot-wheat-soil.png`、`plot-carrot-soil.png`、`plot-tomato-soil.png` |
| `plot-1-wheat.png` | `crop-wheat-overlay.png` |
| `plot-2-carrot.png` | `crop-carrot-overlay.png` |
| `plot-3-tomato.png` | `crop-tomato-overlay.png` |

`farm-foreground.png` 是制作参考，不在当前原生生成清单内。其余五种作物和巡逻帧继续使用已审定来源；是否新增图层需要同时修改场景与生成源，不能仅复制文件就宣称已接入。

## 验收规则

- `farm-full-map.png` 先验收，通过后才能拆层。
- 分层图必须和 `farm-full-map.png` 同风格、同坐标体系。
- 空土地图不能出现照片质感。
- 作物层不能包含整块土地图背景。
- 组装结果必须能接近 `farm-full-map.png`。
- 前端运行时不能直接把 `farm-full-map.png` 当背景图使用。

## 运行时交付

人工审查后，将中间图转换为[当前素材规范](farm-map-gpt-image-2-asset-plan.md)中的来源文件，核对透明边缘和坐标，再执行 `pnpm native:work-art:generate` 与 `pnpm native:work-art:check`。不要把本配方的输出目录直接作为 Go 运行时资源目录，也不要提交凭据、临时日志或未审定中间图。
