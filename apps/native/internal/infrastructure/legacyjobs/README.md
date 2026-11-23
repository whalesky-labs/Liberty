# 旧 JSON 任务读取与导入边界

[原生客户端说明](../../../README.md) · [文档总览](../../../../../docs/README.md)

本模块只读取显式资料目录中的 `jobs/<id>/`，向应用层提供旧任务快照。SQLite 事务、已有任务保护和导入完成标记由 `repositories/jobdb` 负责。

## JSON 兼容语义

`job.json` 与可选 `result.json` 按当前旧版 Rust 的结构体字段名精确匹配。大小写不同的字段（例如 `TITLE`、`TEXT`、`PATH`）是未知扩展，不能覆盖正式字段或提供原本缺失的值；未知扩展仍忽略。

旧端直接使用 Serde 结构体反序列化。这与先解析为普通 JSON 对象不同：正式字段重复出现时必须拒绝，不能由 map 静默保留最后一个值。该规则递归覆盖源文件、逐字稿、摘要与摘要运行记录，包含转义后相同的字段名和先为 null 后再次出现的可选字段。未知扩展的重复字段继续忽略。

`json.go` 在合并字段前检测重复，按既有 DTO 标签匹配，以具体标量类型解码，保留整数精度和空值边界。嵌入的公开 DTO 提供公共字段，`legacySnapshot.Files` 覆盖其同名的 `sourceFiles` 字段，保留内部导入所需的原始路径。解析失败不返回部分更新。

既有处理策略保持：无效 `job.json` 跳过该目录；无效的可选 `result.json` 整份忽略，仍可导入有效的主快照。超限或读取失败由文件读取层返回错误，不通过忽略错误提交导入完成标记。结果补充仅填入主快照尚无的逐字稿、说话人、时长及失败原因。

`progress.json` 继续交由 Runner 协议解析器处理，不属于上述字段匹配修正范围。本文不表示所有 JSON 转义或完整新旧接受范围已经完成等价验收。

## 回归验证

在 `apps/native` 下运行：

```sh
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go test -race ./internal/infrastructure/legacyjobs -count=1
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go test -race ./internal/infrastructure/repositories/jobdb -run '^TestLegacy' -count=1
```

`decoding_test.go` 覆盖精确字段、大小写与 Unicode 别名、重复键、转义字段名、空值、未知扩展、嵌入源文件字段和超过浮点精确范围的整数。

`persistence_test.go` 创建四个合成旧任务目录，以生产读取器和 repository 导入临时 SQLite：一个坏主快照跳过，一个坏补充结果忽略，两个有效快照保留正确内容。三次关闭重开核对标题、逐字稿、摘要、源文件路径、可执行字段和幂等完成标记；同时直接查询持久表。测试不读取用户资料或系统凭据，不启动媒体处理。

本机文件与 SQLite 回归不替代生产资料迁移、安装器升级回退、其他操作系统实机或物理断电验收。
