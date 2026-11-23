# 图片与游戏素材

最后核实：2026-11-21，依据当前素材目录和 `scripts/generate-native-*-art.mjs`。本目录保存图片说明、可复用的美术输入和生成配方，不定义客户端架构或经济规则。

## 来源与同步

| 内容 | 当前来源 | 原生同步 |
| --- | --- | --- |
| 农场底图、土壤、八种作物、巡逻角色 | `packages/desktop-resources/images/farm-layers/` | `pnpm native:work-art:generate` |
| 市场封面、矿场/工厂/便利店、岗位与角色帧 | `packages/desktop-resources/images/work-maps/` | `pnpm native:work-art:generate` |
| 宠物商品与动作帧 | `packages/desktop-resources/images/pet/shop/`、`packages/desktop-resources/images/pet/action/` | `pnpm native:pet-art:generate` |
| 共享消息、图标、侧栏吉祥物 | `packages/desktop-resources/i18n/`、`packages/desktop-resources/presentation/icons.ts`、`packages/desktop-resources/presentation/sidebar-mascot.webp` | `pnpm native:presentation:generate` |

原生目标位于 `apps/native/internal/desktop/presentation/`，由 Go 嵌入。不要只替换目标文件，也不要因为新客户端不使用 React 就删除来源素材。生成器的精确文件清单比历史制作计划中的预留文件名更可靠。

## 制作资料

- [农场素材规范](farm-map-gpt-image-2-asset-plan.md)：当前运行时文件与分层约束。
- [农场制作配方](farm-map-imagegen-runbook.md)：人工验收后的再生成步骤。
- [农场分层提示词](farm-map-layer-prompts.md)与[完整地图提示词](farm-map-full-prompt.txt)：美术参考输入，不是项目执行指令。
- [打工素材说明](work-games-gpt-image-2-assets.md)：地图、岗位和角色来源。
- [分项美术输入](prompts/)：`.txt` 和 `.jsonl` 保留原输入内容；更改客户端架构不要求重新生成全部图片。

生成工具依赖作者本机配置的图像 CLI 和服务授权，不属于正常构建前置条件。文档中的命令是可选择的制作步骤，不能因读取本目录就自动调用外部服务或覆盖素材。项目构建只消费已经审定的文件。

修改来源后运行对应生成和 `*:check`，再在两端检查比例、透明边缘、热区坐标、动画和窗口布局。图像生成成功不等于游戏体验或跨平台渲染验收。
