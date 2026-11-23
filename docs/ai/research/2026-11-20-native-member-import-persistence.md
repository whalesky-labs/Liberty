# 原生人员 Excel 导入持久化验收

> 本文为指定日期与输入的专项证据，不是全项目当前状态。操作入口见[开发指南](../../development.md)，正式约束见[原生架构](../../architecture/native-desktop-architecture.md)与[发布要求](../../architecture/release-readiness.md)。

文档类型：调研与诊断（专项验收）<br>
状态：本项持久化回归通过；全量原生验收未完成<br>
创建日期：2026-11-20<br>
最后核实：2026-11-20<br>
适用范围：Go 原生端人员 XLSX 导入/导出、应用服务与临时 SQLite 资料库；macOS ARM<br>
权威边界：证明本文列出的文件解析、事务和重开场景；不替代真实系统对话框、Excel/WPS、其他平台实机、所有 XML 兼容或生产切换验收。<br>
依据：当前源码、`desktop/member_files_persistence_test.go`、定向普通/race 结果、旧解析器的隔离 overlay 对照及本次整仓检查。

## 已核实的问题

旧解析器在处理完第一份工作表 XML 后，接受额外的 `<worksheet/>` 根节点。通过真实 XLSX 文件、应用服务和 SQLite 的隔离测试，这种文件会返回新增 1 人、更新 1 人；关闭并重开两次后，错误导入的数据仍存在。

对照使用本轮开始前保留的 `workbook.go`，通过 Go `-overlay` 将旧解析器只映射到测试编译，不替换共享工作区源码。当前解析器在相同输入上返回 `member_workbook_invalid`，新增/更新计数为零，两次重开后全部人员字段与原始快照相同。

外层 XML 文档、数据区域及关系边界修复已在此前工作中加入；命名空间和 OOXML 转义的后续修改由并行工作线继续。本项后续写入限定为独立的持久化测试、模块说明和本文，没有继续改动其解析器写集。

## 八组持久化场景

| 场景 | 核验结果 |
| --- | --- |
| 末行插入触发 SQLite ABORT | 前面已执行的合并、新增和记录人切换全部回滚；三次重开不变 |
| 记录人切换时触发 SQLite ABORT | 前面人员修改回滚，原记录人保留；三次重开不变 |
| 去除首尾空白后姓名重复 | 拒绝整批，两次重开不变 |
| 多位记录人 | 拒绝整批，两次重开不变 |
| 姓名为空白 | 拒绝整批，两次重开不变 |
| 合法人员行之后附加第二个 XML 根节点 | 拒绝整批，两次重开不变；旧实现隔离对照失败 |
| 文件选择返回时取消 | 不导入，两次重开不变 |
| 文件选择返回时资源服务已关闭 | 返回作用域错误，两次重开不变 |

两组 SQLite 故障仅在关闭测试 store 后向临时合成库安装触发器，再以生产入口重开；不会修改 schema 迁移或用户资料。移除触发器后，同一工作簿重试完成新增 2 人、更新 1 人，验证：

- 按姓名合并保留既有 ID 与创建时间，其他未参与合并的人员完整不变。
- 唯一记录人转移成功，中文、XML 特殊字符和 int64 最小/最大排序值完整保留。
- 重复导入仅更新 3 人，不新增重复人员；除正常更新时间外，身份和业务字段保持一致。
- 重开后通过真实导出适配生成 XLSX，再解码核对其内容与持久化人员投影相同。

## 验证与边界

本项定向普通测试、race、vet 及格式检查通过。复验命令：

```sh
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go -C apps/native test -race ./internal/desktop -run '^Test(MemberFile|NativeMember)' -count=1
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go -C apps/native test -race ./internal/infrastructure/memberxlsx -run '^TestMemberWorkbook(RejectsAmbiguous|IgnoresRows|AcceptsComments|ReadsOnlySelected)' -count=1
```

本次 `pnpm check` 退出 0，包含 47 个前端、105 个 Python、255 个 Rust 测试，另有 16 个默认忽略的专用用例。Web 构建、fmt、Clippy 和发布策略通过；该命令不是实际依赖漏洞扫描。

`pnpm native:check` 退出 1：`TestSettingsTrailingControlsAlignAcrossEverySection` 在运行时分区找不到原来的“下载/Download”控件标签。另一次完整 `memberxlsx` race 遇到并行工作线新增的 `TestMemberWorkbookDecodesSpreadsheetEscapes` 红测。它们不由本项修改解决，不能将定向通过写成全量原生通过。

Intel Mac、Windows x64/x86 的 desktop 与 memberxlsx 测试包编译及六个文件头检查通过；目标程序未实际执行。原生文件对话框使用替身，仅文件、应用服务和数据库是真实实现；未读写用户资料，也未访问系统凭据。

本地忽略目录 `target/native/member-persistence-20261120/` 保存 `before-persistence.log`、`final-race.log`、`structure-race.log`、`workspace-check.log`、`native-check.log` 和跨平台编译记录。此前 `target/native/member-parser-session/` 保存解析器初始红测和原始源码快照。长期行为说明见[人员 Excel 模块文档](../../../apps/native/internal/infrastructure/memberxlsx/README.md)，持续回归见[持久化测试](../../../apps/native/internal/desktop/member_files_persistence_test.go)。
