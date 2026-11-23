# 原生 DOCX 导出视觉验收

> 本文为指定日期与输入的专项证据，不是全项目当前状态。操作入口见[开发指南](../../development.md)，正式约束见[原生架构](../../architecture/native-desktop-architecture.md)与[发布要求](../../architecture/release-readiness.md)。

文档类型：调研
状态：已完成（三个代表案例修复后通过视觉等价复验）
创建日期：2026-11-19
最后核实：2026-11-19
适用范围：Liberty 原生重构 P4/P7 的会议纪要 DOCX 导出视觉子项；macOS arm64；现有合成夹具中的三个代表案例
权威边界：本文证明指定输入在本次打包渲染器与字体条件下的实际分页和可见差异，不代表 P4/P7 整体完成，不替代 Word、WPS、Windows 或 Intel Mac 的实机验收，也不验证保存对话框、原子发布和导出奖励
依据：当前 Rust/Go 生产导出代码、原 DOCX 模板、现有 Rust 合成夹具、定向测试、实际 PDF/PNG 及逐页目视检查。最终源码和产物摘要位于本次忽略目录下的 `repair-02/visual-evidence.json`。

## 结论

Rust 属性命名空间与纯空白 Word 文本已修复。18 个相同 DTO 的严格 XML 差分全部通过。打包 LibreOffice 实际重渲染 case 02、05、06，两端分别为 2、2、1 页。逐页查看了 10 张整页 PNG 及 20 张原像素裁剪，五对整页 RGB 差异像素均为 0。中文可读，表格外框、网格、合并单元格、标题居中、红色小标题和分页均一致，未见页外裁切或文字覆盖。Go 生产源码和两份模板的摘要与修复前一致，抽样 Go DOCX 解包后所有部件与修复前逐字一致。

## 修复与回归

- Rust `apps/desktop/src-tauri/src/local_export/renderer.rs:147` 通过 `local_export/xml.rs:5` 的 `parse_document_xml` 读模板。锁文件已有的 `xml-rs 0.8.28` 增加为固定直接依赖，包版本未变。事件解析保存每个元素的有效 namespace 绑定和属性的限定名，`xmltree` writer 根据限定名及声明写回。回归用真实模板格式属性及合成探针验证 `w`、`r:id`、`xml:space`、同 local name 多 namespace、嵌套重绑定及作用域恢复。只读 ZIP/XML 校验路径保持原有行为。
- 补充回归先复现了纯空白 `w:t` 被当作结构缩进丢弃的问题。修复后仅保留 `t` 元素内纯空白，测试同时核对带与不带 `xml:space` 的空格、混合文本、实体转义、CDATA 和序列化往返。向内缩进仍被丢弃。
- `apps/native/internal/infrastructure/minutesdocx/compatibility_test.go:92` 按解析后的属性 namespace URI、local name 和值比较，保留 `t` 内纯空白。同 URI 的前缀别名、属性顺序及等价转义可比较。修复前的夹具已被严格差分拒绝，首个差异是 `mc:Ignorable` 的 URI 丢失。回归日志保留于 `repair-01/`，当前通过日志位于 `repair-02/`。

## 最终渲染复验

相同 18 个输入 DTO 重新生成 Rust XML 和两端 DOCX。Rust DOCX 仍用原模板打包，只替换 `word/document.xml`。Go 仍由临时 overlay 测试调用生产 `Encode`。所有复验图像均由技能附带的 `render_docx.py`、打包 `LibreOfficeDev 26.8.0.0.alpha0 2c87e51eeaa2b413ff4ae097b2705eea1995d8e5` 与 Poppler 生成。两端共用与修复前对照相同的任务内 Fontconfig，PDF 嵌入 `STSongti-SC-Regular/Bold`。PNG 为 `1547 × 2002 px`，PDF 为 Letter `612 × 792 pt`。

| 案例 | Rust 页数（修复前 → 后） | Go 页数 | 修复后两端 Word 命名空间属性数 | 逐页 RGB 差异像素 |
| --- | ---: | ---: | ---: | --- |
| 02 | 2 → 2 | 2 | 582 | 0、0 |
| 05 | 3 → 2 | 2 | 641 | 0、0 |
| 06 | 2 → 1 | 1 | 437 | 0 |

case 02、05 的发言行仍跨页，部门与姓名位于相邻页面，承接页不重复表头。此现象在修复前 Go 基线中也存在，模板未变。case 06 在一页内排完表格及行动项。六份 DOCX 都没有页眉页脚部件，实际页面也无页眉、页脚或页码。10 页均未见缺字、文字重叠或横向越界，PDF 页外字符也都是 0。

| 案例 | Rust 最终 PNG | Go 最终 PNG |
| --- | --- | --- |
| 02 第 1 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/repair-02/render-cjk/rust-case-02/page-1.png` | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/repair-02/render-cjk/go-case-02/page-1.png` |
| 02 第 2 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/repair-02/render-cjk/rust-case-02/page-2.png` | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/repair-02/render-cjk/go-case-02/page-2.png` |
| 05 第 1 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/repair-02/render-cjk/rust-case-05/page-1.png` | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/repair-02/render-cjk/go-case-05/page-1.png` |
| 05 第 2 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/repair-02/render-cjk/rust-case-05/page-2.png` | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/repair-02/render-cjk/go-case-05/page-2.png` |
| 06 第 1 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/repair-02/render-cjk/rust-case-06/page-1.png` | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/repair-02/render-cjk/go-case-06/page-1.png` |

`repair-02/` 另保存 PDF、`qa-100pct/` 裁剪图、36 个 DOCX、重生成夹具及 `code-freeze.sha256`。定向命令分别通过，完整输出见 `rust-regression.log`、`rust-export.log` 和 `go-strict.log`。

```sh
QA="$PWD/target/native/docx-visual-20261119/repair-02"
mkdir -p "$QA/fixtures"
cargo +1.94.0 test --locked --offline -p liberty --lib local_export:: -- --nocapture
LIBERTY_NATIVE_FIXTURE_DIR="$QA/fixtures" cargo +1.94.0 test --locked --offline -p liberty --lib \
  local_export::source::tests::export_native_minutes_fixtures -- --ignored --exact
LIBERTY_NATIVE_FIXTURE_DIR="$QA/fixtures" GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly \
  go -C apps/native test -race -tags compatibility ./internal/infrastructure/minutesdocx \
  -run '^(TestWriteRustWordDocumentCompatibility|TestCanonicalXMLPreservesAttributeNamespaceIdentity|TestCanonicalXMLPreservesWordTextWhitespace|TestOriginalTemplatePartsAndSpecialTextRemainValid)$' -count=1 -v
```

Rust `local_export` 38 项通过、2 项按标记忽略，另单独执行的 Word 夹具导出通过。Go 4 项通过，含 18 个案例的严格差分，包测试耗时 3.649 秒。两个修改的 Rust 文件通过 `rustfmt --check`。全仓检查交由主代理整合执行。当前结论限于 macOS arm64、上述渲染器与字体及三个抽样案例。其余 15 例只做结构差分，未验证 Word、WPS、真实 SimSun 字体度量、Windows x86/x64、Intel Mac、真实页眉页脚模板、长篇边界输入或客户端导出交互。

## 修复前的验收快照

以下记录初次发现缺陷时的源码和产物。路径、页数及“未修复”结论对应当时快照，当前复验结果见上文。

本次完成真实 DOCX → PDF → PNG 对照，视觉等价子项未通过。两端内容相同，但 Rust 输出丢失 Word 属性的命名空间，表格边框、字号、对齐、颜色和页边距未按模板呈现；Go 保留这些格式。case 05 为 Rust 三页、Go 两页，case 06 为 Rust 两页、Go 一页。case 02 虽然同为两页，页面内容分布仍明显不同。

生产根因位于写范围之外的 Rust `local_export/renderer.rs`，本次只报告，未修改 Rust，也未让 Go 模仿有缺陷的旧端 XML。未复现需要修改 `minutesdocx` 的 Go 生产缺陷，因此没有修改或新增该包的 Go 文件。唯一仓库交付为本文；合成文件、辅助脚本、字体配置和图像均留在被忽略的 `target/native/`。

默认打包渲染环境还出现中文缺字；使用已预装的系统中文字体、仅对子进程生效的任务内配置后，中文恢复正常，而 Rust 格式与分页差异仍存在。字体环境问题与 Rust 输出缺陷分开记录，没有修改输入 DOCX 来修饰验收结果。

### 样本与生成路径

1. 运行现有 Rust `local_export::source::tests::export_native_minutes_fixtures`，得到 18 组 `document` DTO 和生产 `render_document_xml` 生成的 XML。输入只有“人员A”“合成会议”等合成内容，不读取用户数据库、媒体、资料或凭据。
2. 现有夹具只直接导出 XML。任务内 `prepare_visual.py` 按 Rust `write_summary_docx` 的生产打包方式，将它替换进原模板的 `word/document.xml`，其余 ZIP 部件逐字保留，生成 18 个 Rust DOCX。此步骤不等同于执行 Rust 保存/发布路径。
3. 任务内 Go 测试通过 `go test -overlay` 调用未修改的生产 `minutesdocx.Encode`，直接读取同一批 DTO，生成 18 个 Go DOCX；没有在仓库包目录落入临时测试文件。
4. 抽取 case 02、05、06 共六个 DOCX 渲染。中文字体就绪后的 12 个页面全部目视检查，并查看每页原始像素的上下裁剪图，两幅裁剪重叠 80 像素。默认字体环境的 case 02 四页另作失败环境证据。

两端模板 SHA-256 相同：`9b4fb0ef26947abae744c12e1bdca937b9f60f7b4a32091ea8eb79b0095d6e62`。三个抽样案例的有序 `w:t` 文本完全一致，非 `word/document.xml` 部件均与原模板相同。这些检查只确认输入和归因，不能代替目视检查。

| 案例（零基序号） | 现有输入分支 | 发言块 | Rust 页数 | Go 页数 | 主要视觉结果 |
| --- | --- | ---: | ---: | ---: | --- |
| 02 | completed；人员排序、遗漏人员、全局总结与行动项 | 3 | 2 | 2 | Go 保留网格、合并单元格、居中标题和红色小标题；Rust 无可见边框，标题和标签改为左侧排列，跨页位置不同 |
| 05 | completed；未登记人员、缺失字段回退 | 4 | 3 | 2 | Rust 的行动项延至第三页且大面积留白；Go 在第二页完成；两端正文均可见 |
| 06 | legacy_unverified；只采用原始转写标签 | 1 | 2 | 1 | Go 一页完整排出表格和行动项；Rust 的表格内容与行动项分散至两页 |

三个样本足以复现真实视觉差异；未把其余 15 个未渲染案例计为视觉通过，也未将此抽样扩大为长篇纪要、超长单元格或所有语言的覆盖承诺。

### 渲染器与字体条件

- 已读取指定 `documents/SKILL.md` 和渲染核查说明，使用技能附带的 `render_docx.py`；没有使用系统安装的桌面 LibreOffice。
- Python 与 `soffice`、Poppler 来自 workspace dependency loader 返回的打包运行时。实际 LibreOffice 版本为 `LibreOfficeDev 26.8.0.0.alpha0 2c87e51eeaa2b413ff4ae097b2705eea1995d8e5`，不是 Microsoft Word。
- 原模板直接指定 `SimSun`。默认环境下，两端中文缺字或不可见，PDF 中主要出现 `FrankRuhlHofshi` 等字体；即使 PDF 文本提取包含中文，PNG 仍不合格。默认输出保留于 `render/`，未覆盖。
- 未下载字体。任务内 `system-fonts/` 只链接系统预装的 `/System/Library/Fonts/Supplemental/Songti.ttc` 和 `/System/Library/Fonts/STHeiti Medium.ttc`；任务内 Fontconfig 配置对 `SimSun/宋体` 使用 `Songti SC` 回退，并将缓存限制在任务目录。通过进程级 `FONTCONFIG_FILE` 传给渲染器，没有更改全局环境或受管运行时文件。
- 修正环境后的六个渲染日志没有字体警告，PDF 实际嵌入 `STSongti-SC-Regular/Bold`，12 页中文均可读。输出位于 `render-cjk/`。这不证明宋体与 Songti SC 的字宽、换行完全相同；Windows 宋体和 Word 的精确分页仍未验证。
- 六个 PDF 的页面均为 Letter，`612 × 792 pt`；原始 PNG 均为 `1547 × 2002 px`。

### 逐项视觉观察

#### 中文与排版

字体环境修正后，三组的标题、人员、部门、“待补充”、总结和行动项均可见，没有继续观察到默认环境的中文方框或空白。Go 保留模板的大号居中标题、红色小标题以及正常/加粗区分；Rust 标题缩小并靠左，小标题颜色丢失，正文呈现不同的粗细与间距。

#### 表格与分页

Go 三组均保留外框、内部横纵线、上部元数据合并和发言两列结构；Rust 虽仍有表格 XML 元素，但渲染的边框不可见，列宽及行布局偏离模板。case 02 同页数不能视作分页一致；例如 Go 第一页已排出人员A的个人总结，Rust 把该内容放到第二页。

Go 也有需保留说明的模板现象：case 02、05 的发言行跨页，部门在上一页、姓名在承接页，承接页没有重复“发言内容”表头。原模板没有 `w:tblHeader` 或 `w:cantSplit`，样板发言行最小高度为 `3855 twips`、`hRule="atLeast"`。本次没有把继承的分页策略当作新增 Go 缺陷，也没有为美化样本重做模板。后续若要求发言身份与内容整行保持，应先确认模板/跨端契约及写范围。

#### 页眉页脚

原模板和六个产物都没有 `word/header*`、`word/footer*` 部件；实际页面也没有页眉、页脚或页码，未出现两端某一方漏掉已有页眉页脚的情况。仅有节中的 header/footer 距离设置，不能据此声称已验证带页码、分节页眉或重复页脚的模板。

#### 溢出与内容

12 页均已打开，含整页和原始像素裁剪。抽样中未观察到字形被页面边缘裁断、文字互相覆盖或表格横向越出纸面；PDF 字符边界检查的页外字符数均为零，该数值只作辅助证据。跨页内容在后续页继续可见，并非把溢出文字隐藏。Rust 的额外页和大面积留白、Go 的跨页身份分离均已如实保留，不将“未见页外裁切”等同于整体版式通过。

### 原生产缺陷及语义检查的盲区

根因定位到 Rust `/Volumes/NQJL/每日博士/开发项目/Liberty/apps/desktop/src-tauri/src/local_export/renderer.rs`：`render_document_xml` 第 147 行起通过 `xmltree::Element::parse` 读入模板，第 223 行起用 `write_with_config` 输出；夹具在 `local_export/source.rs:416` 直接调用同一生产函数。

在原模板及 Go case 02 中，第一个边框的属性保留 Word 命名空间，例如 `w:val="single" w:sz="8" w:color="000000"`；Rust 输出变成无命名空间的 `val="single" sz="8" color="000000"`。不是只换了前缀名，而是属性的解析后 namespace URI 已丢失。`rFonts` 等格式属性同样受影响。

| 案例 | Rust document.xml 中 Word 命名空间属性数 | Go 中相同属性数 | 有序正文文本 |
| --- | ---: | ---: | --- |
| 02 | 0 | 582 | 相同 |
| 05 | 0 | 641 | 相同 |
| 06 | 0 | 437 | 相同 |

Go `/Volumes/NQJL/每日博士/开发项目/Liberty/apps/native/internal/infrastructure/minutesdocx/xml.go:32` 使用 `RawToken` 保留前缀与声明。当前 `/Volumes/NQJL/每日博士/开发项目/Liberty/apps/native/internal/infrastructure/minutesdocx/compatibility_test.go:79` 明确按属性 local name 和 value 对照，忽略属性 namespace，因此下面的 18 案例语义测试仍通过。它不能证明边框、格式或分页等价。本次没有伪造 Rust 属性、重排 Go ZIP 字节或改写期望来消除视觉差异。

建议由拥有 Rust 写权限的主代理修复此命名空间丢失，再用相同 DTO、相同字体环境重做六个 DOCX 的逐页对照；届时应同时复核兼容测试的命名空间盲区。本次未越界修复，也没有将未验证的修复方案计为完成。

### 图像与产物位置

本次合成证据根目录为 `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119`，已核实位于 Git 忽略范围。该目录是可清理的本机 QA 产物，不是受版本管理的长期视觉基线。

下列 12 张是中文字体就绪后逐页检查的完整图像，路径均为绝对路径：

| 产物 | 已检查图像 |
| --- | --- |
| Rust 02 第 1 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/rust-case-02/page-1.png` |
| Rust 02 第 2 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/rust-case-02/page-2.png` |
| Go 02 第 1 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/go-case-02/page-1.png` |
| Go 02 第 2 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/go-case-02/page-2.png` |
| Rust 05 第 1 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/rust-case-05/page-1.png` |
| Rust 05 第 2 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/rust-case-05/page-2.png` |
| Rust 05 第 3 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/rust-case-05/page-3.png` |
| Go 05 第 1 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/go-case-05/page-1.png` |
| Go 05 第 2 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/go-case-05/page-2.png` |
| Rust 06 第 1 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/rust-case-06/page-1.png` |
| Rust 06 第 2 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/rust-case-06/page-2.png` |
| Go 06 第 1 页 | `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render-cjk/go-case-06/page-1.png` |

其余证据均位于同一根目录：

- `artifacts/rust/rust-case-00.docx` 至 `rust-case-17.docx`；`artifacts/go/go-case-00.docx` 至 `go-case-17.docx`。
- `render-cjk/<exporter>-case-<NN>/` 同目录保存对应 PDF；`qa-100pct/` 保存 12 页共 24 幅原始像素上下裁剪图。
- 默认字体失败证据示例：`/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render/go-case-02/page-1.png` 和 `/Volumes/NQJL/每日博士/开发项目/Liberty/target/native/docx-visual-20261119/render/rust-case-02/page-1.png`。
- `fixture-manifest.json`、`visual-evidence.json` 记录输入 DTO、源码摘要、DOCX/XML/PNG 摘要、页数、实际 PDF 字体、边界辅助检查和模板部件保留情况。
- `prepare_visual.py`、`export_visual_test.go`、`overlay.json`、`collect_visual_evidence.py` 和 `fonts.conf` 为任务内复现辅助文件；没有加入仓库源码或依赖声明。
- `rust-export.log`、`go-semantic-tests.log`、`render-*.log`、`render-cjk-*.log` 保留命令实际输出。Rust exporter 有一次编译缓存 hard-link 不支持而回退复制的警告；测试没有失败，未将它隐藏为零警告构建。

### 修复前定向验证

工具链实际核实为 Go `go1.27.4 darwin/arm64`、Rust `1.94.0`。下列命令从仓库根目录运行；`QA` 为本次忽略目录，`PY` 和 `RENDER` 分别取已解析的打包 Python 与所读技能的渲染脚本。

```sh
QA="$PWD/target/native/docx-visual-20261119"

LIBERTY_NATIVE_FIXTURE_DIR="$QA/fixtures" \
  cargo +1.94.0 test --locked -p liberty --lib \
  local_export::source::tests::export_native_minutes_fixtures -- --ignored --exact

"$PY" "$QA/prepare_visual.py"

LIBERTY_NATIVE_DOCX_VISUAL_DIR="$QA" GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly \
  go -C apps/native test -overlay "$QA/overlay.json" \
  ./internal/infrastructure/minutesdocx -run '^TestExportVisualFixtureSet$' -count=1 -v

FONTCONFIG_FILE="$QA/fonts.conf" "$PY" "$RENDER" \
  "$QA/artifacts/go/go-case-02.docx" \
  --output_dir "$QA/render-cjk/go-case-02" --emit_pdf --verbose

LIBERTY_NATIVE_FIXTURE_DIR="$QA/fixtures" GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly \
  go -C apps/native test -tags compatibility ./internal/infrastructure/minutesdocx \
  -run '^(TestWriteRustWordDocumentCompatibility|TestOriginalTemplatePartsAndSpecialTextRemainValid)$' \
  -count=1 -v

"$PY" "$QA/collect_visual_evidence.py"
```

Rust exporter：1 项通过、223 项过滤，测试执行 0.48 秒。任务内 Go exporter：通过，生成 18 份 Go 文件。最后一条 Go 定向测试：2 项通过，包测试 0.182 秒，其中语义兼容测试逐一检查 18 个案例。六次正式对照渲染均成功，且 12 页均实际目视检查；这些通过项没有被混同为视觉等价通过。渲染命令同样用于 Rust 02、两端 05 和两端 06；默认字体环境另渲染两端 02。

辅助脚本的 DOCX 输出采用不覆盖已有文件的创建方式；重复导出应选新的忽略子目录并相应调整任务内路径。恢复已有产物不需要再导出，直接查看上列 PNG 即可。

### 初次验收的范围

- Rust 命名空间缺陷未修复，是本次等价验收未通过的生产阻塞；修复权限在本次范围外。
- 未验证 Microsoft Word、WPS、Windows x86/x64、Intel Mac、真实 SimSun 字体度量、打印机差异、含页眉页脚的其他模板或长篇边界输入。
- 未运行全量 Go/Rust 检查、`native:check`、root check 或性能基准；未启动 Liberty 客户端、访问真实远端、发布、提交或推送。主代理的其他验收结果不作为本次独立验证结论。
- 本次代码零修改。报告与忽略目录的合成证据齐备后停写，交由主代理决定越界 Rust 修复及 P4/P7 后续门槛。
