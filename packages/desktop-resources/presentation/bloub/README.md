# bloub 原生动画来源

本目录维护 Liberty 实时翻译页使用的第三方动画来源。上游为 [jeremy-prt/bloub](https://github.com/jeremy-prt/bloub)，固定提交 `b4bb3c1b5f93c7b87a2e8d620f667c4093d97749`，核实日期 2026-11-26。[source.json](source.json) 记录源码及许可证 SHA-256；`upstream/` 的 11 个 TypeScript 文件与该提交一致，未修改上游动画参数。

上游 `BloubBot.vue` 是展示层，`src/bot` 是不依赖 Vue 或浏览器的动画引擎。Liberty 构建脚本直接运行固定的 `BotEngine.sample(t)`，生成 30 fps 的矢量帧；MyGo 播放这些原始轮廓、眼睛变换、遮罩与彗星彩色轨迹。它不是挂载 Vue 组件，也不是手工仿画。运行时不执行 JavaScript、不加载网站、不逐帧解码视频。

空闲和会话结束时，按上游 `SEQUENCE` 顺序、原始持续时间循环播放全部 14 种动画：静止、思考、眨眼、睁大眼睛、警示、通知、感叹号、休眠、蛋形、六边形、播放、轨道、爆散、彗星；一轮 31.2 秒。同一个上游引擎生成整轮，保留相邻状态及末尾回到开头的形变过渡，通知缺口和粒子前后遮挡也按原组件渲染。

收听时使用 idle 眼神动画及真实音量反馈；准备模型使用 comet，结束保存使用 thinking，暂停和失败使用 somnolent、triste 固定表情。上游编辑器、鼠标跟随和不属于默认序列的 swirl 页面转场未接入。深色主题调整身体/背景色，真实拾音另加轻微缩放和光晕；不改变原始眼神数据。窗口隐藏、失焦或减少动态效果时停止播放。

生成：

```sh
node scripts/generate-native-bloub.mjs
node scripts/generate-native-bloub.mjs --check
```

也由 `pnpm native:presentation:generate` 和 `pnpm native:presentation:check` 调用。压缩生成物嵌入 `apps/native/internal/desktop/presentation/`，不可手工修改。修改上游版本时应重新核对源码身份、生成数据、原生/SVG 渲染对照、主题与性能。

[MIT 许可证](LICENSE)及 [NOTICE](NOTICE) 随候选包合并到 `THIRD_PARTY_NOTICES.txt`。上游明确说明许可证覆盖代码，不涵盖其模仿的 x.ai 角色设计；署名及无关联声明保留在 NOTICE 中。
