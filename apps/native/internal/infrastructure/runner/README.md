# 原生 Runner 协议与进程适配

[原生客户端说明](../../../README.md) · [文档总览](../../../../../docs/README.md)

本模块负责受控子进程、JSON Lines 事件、进度快照和结果读取。运行时安装与任务调度分别位于 `managed` / `execution` 和应用层；任务终态由应用服务与 SQLite repository 持久化。

## V1 兼容边界

`protocol.go` 依据精确的 `protocolVersion` 字段选择版本。沿用旧端规则：缺失或非无符号整数值进入 V1 兼容；整数 2 进入 V2；其他无符号整数版本拒绝。

V1 进度和结果先按 JSON 对象选取精确字段，再解码具体类型。`STAGE`、`TEXT`、`FAILUREREASON` 和 Unicode 大小写别名是未知扩展，不能提供缺失的必需字段，也不能覆盖阶段、失败原因或逐字稿。未知扩展保持忽略。

原 Rust Runner 先读取 `serde_json::Value`，再转换为结构体，所以重复的精确字段以最后一个值为准，包含嵌套分段；先前出现的不合法值不污染最终值。此规则与 `legacyjobs` 直接读取旧 `job.json` 时拒绝重复正式字段的规则不同，不能共用不加区分的重复键策略。

V1 分段的 `id`、`startMs`、`endMs` 和 `text` 必须存在且非 null；时间保持 u64 精度。可选值保持 nullable，非空正式失败原因拒绝成功结果。启用说话人时，旧分段继续标为 `legacy_unverified`，不因兼容解析而变成已验证结果。

V2 继续由共享 JSON Schema 验证字段、版本、事件类型及状态组合。stdout 普通日志仍会触发协议错误，诊断文本由 stderr 与结构化事件提供。

## 验证

在 `apps/native` 下运行：

```sh
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go test -race ./internal/infrastructure/runner -count=1
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go test -race ./internal/infrastructure/execution ./internal/infrastructure/legacyjobs -count=1
```

`v1_decoding_test.go` 覆盖字段大小写、重复精确键、整数精度及必需字段，也通过真实子进程写入 `progress.json`，验证轮询读取正式阶段并由回调终止进程。

`v1_persistence_test.go` 使用真实测试子进程、生产 `execution.Runner`、应用执行器和临时 SQLite。失败原因后附空的大小写别名仍应失败，重开后无逐字稿和完成事件；显式重试提升 attempt，成功时仅持久化正式文本，完成事件只产生一次。失败和成功分别执行两次关闭重开。所有文件和输出均为合成数据，不访问用户媒体、生产资料、系统凭据或实际 ASR 模型。

这些检查不代表全部 JSON 接受范围、其他操作系统实机或真实 Python/FunASR 运行时已经重新验收。
