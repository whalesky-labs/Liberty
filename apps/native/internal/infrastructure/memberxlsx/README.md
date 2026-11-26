# 人员 Excel 文件边界

[原生客户端说明](../../../README.md) · [文档总览](../../../../../docs/README.md)

本模块负责四列人员工作簿的 XLSX 编解码及文件读写。原生主窗口的 `desktop.NativeMemberFiles` 通过系统对话框选择文件；人员校验、按姓名合并与事务写入仍由 `application.Resources` 和 repository 负责。

## 导出

`SaveFile(ctx, filename, values)` 只接受绝对 `.xlsx` 路径，扩展名不区分大小写。工作簿编码完成后复用 `infrastructure/fileout` 发布文件：检查链接目录和目标类型，在同目录暂存并同步文件数据，发布前复核目标及取消状态。编码、写入或检查失败时保留原目标，清理暂存文件；`context.Canceled` 和 `context.DeadlineExceeded` 保留原错误，其余文件错误映射为不含路径的 `ErrFile`。

取消信号在编码前及文件发布流程内检查。编码不是可抢占操作；发布完成后发生的取消不撤销已经保存的文件。目录同步依赖底层平台支持，不据此承诺物理断电或所有文件系统上的持久性。

名字和部门始终写为字符串，包括看起来像公式的文本。超出 Excel 精确整数范围的 `SortOrder` 写为文本，保留 int64 值。编码失败不会替换已有文件。

## 导入与页面状态

导入先完整解析工作簿的文件、XML 与单元格格式，再交给应用服务。批次非空、姓名唯一及记录人数量只在 `application.Resources` 入口校验，生成私有存储的 `domain.MemberImport` 后传给仓储；仓储不重复执行这些业务校验。应用入口的业务错误由文件适配器映射为工作簿错误，保留页面提示契约。系统对话框取消、返回多文件、文件损坏或解析后已取消，都不能调用人员导入用例。模块不计算公式，不获取外部关系资源；ZIP 大小、解压总量、行数与单元格数有独立上限。

每个已读取的 XML 部件必须包含一个匹配的根节点，完整读取至文档末尾。工作表只读取第一张表的 `worksheet/sheetData/row`，共享字符串只读取 `sst` 的直接 `si` 子项；扩展区和其他命名空间的同名行或字符串不会成为人员数据。额外根节点、非空尾部文本、重复 `sheetData`、重复关系 ID、缺失的共享字符串部件及错误的工作表关系均拒绝导入，不返回此前已解析出的部分人员。标准与 Strict OOXML 命名空间、UTF-8 BOM、注释及共享富文本继续支持；这不是完整 OOXML XSD 校验。

命名空间限制同样应用于工作簿的 `sheets/sheet`、关系、单元格、值及富文本片段，外来同名元素或属性不能改变姓名、部门或首表选择。字符串读取按 OOXML 单次解码 `_xHHHH_` 转义，导出时保护字面转义串，避免姓名 `_x0041_` 在旧客户端变成 `A`。回车、Unicode 代理对、孤立代理及跨富文本片段边界分别验证，并以真实 Rust 导出和旧解析器读入核对，不能用单端往返替代兼容性。

每次新的人员导入或导出先清除之前的成功提示。导入已经提交后，即使列表刷新失败，也保留本次新增/更新数量；刷新重试只重新读取列表，不重复导入。关闭资源控制器会取消操作，迟到的回调不能更新关闭后的界面。

## 验证

在仓库根目录执行：

```sh
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go -C apps/native test -race ./internal/client ./internal/infrastructure/memberxlsx ./internal/infrastructure/fileout -count=1
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go -C apps/native test -race ./internal/desktop -run '^TestNativeMember' -count=1
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go -C apps/native test -race ./internal/desktop -run '^TestMemberFile(Transaction|Rejected)' -count=1
pnpm native:compat
```

测试覆盖实际 XLSX 编解码、XML 部件及数据区域边界、文件替换与失败保留、链接目录拒绝、取消、资源读取失败、成功导入后刷新失败以及关闭后的回调。对话框适配另验证“前部人员行合法、XML 尾部损坏”的文件不会进入导入服务。兼容测试使用 Rust 导出的工作簿交给 Go，再将 Go 导出的工作簿交给 Rust 读取。

`desktop/member_files_persistence_test.go` 将受控文件选择、真实 XLSX、应用服务和新建的临时 SQLite 资料库串联验证。末行插入失败、记录人转移失败后重开三次核对全部人员字段；移除合成故障后重试，检查新增/更新计数、既有 ID/创建时间、唯一记录人、无关人员不变、int64 排序与重复导入不增人，再导出重开后的持久数据。无效姓名/记录人、附加 XML 根节点、取消和关闭服务同样必须在重开后保持原人员数据。故障触发器只存在于临时测试数据库，不进入迁移或生产代码。

对话框适配测试使用受控对话框替身及临时合成文件，不是系统对话框或 Excel 应用的实机交互验收。Windows、Intel Mac、输入法和读屏验收须另行记录。
