# 原生诊断文本投影

[原生客户端说明](../../../README.md) · [文档总览](../../../../../docs/README.md)

`Text(value, limit)` 是 Runner 输出、运行时安装日志、任务失败原因和界面错误提示共用的纯文本脱敏入口。返回值按 Unicode 字符数限制长度，移除终端控制序列，最后归一化空白。返回内容用于诊断显示，不保证仍是可解析的 JSON。

## 字段与路径边界

- 识别既有字段 `api_key` / `api-key` / `apiKey`、`token`、`secret`、`transcript`、`providerBody` 和 `petText`，不区分大小写；支持裸键、双引号 JSON 键和单引号字典键。
- 引号值按转义字符读取，嵌套对象与数组按配对括号整体隐藏，值内的逗号、空格或转义引号不会提前结束脱敏。未闭合或括号不匹配时隐藏该值到输入末尾，避免截断日志暴露后半段。
- 隐藏 Bearer 凭据及可识别的 URL、POSIX 绝对路径、Windows 盘符和 UNC 网络路径。带引号的路径隐藏到对应结束引号，保留引号外的状态；没有引号的路径可能包含空格，因此保守隐藏到当前行末。先处理路径，再合并换行，保留下一个日志行。
- 脱敏在长度截断之前完成。正常 Unicode 文案、状态字段、时间戳与已有脱敏标记保留。

这不是任意自由文本的秘密识别器。不要主动把凭据、用户媒体路径、逐字稿或远端响应正文写入诊断，也不要依赖未列出的字段名或编码形式自动被识别。业务正文仍由其业务数据通道传递，不能用诊断日志代替。

## 回归验证

`boundary_test.go` 覆盖转义、嵌套、截断、路径空格、UNC、状态保留与重复投影；模糊测试检查任意输入的字符边界、控制字符和结构化字段隔离。

`persistence_test.go` 使用实际测试子进程和生产 `runner.RunCommand`，将两条 stdout 和两条 stderr 诊断写入新建临时 SQLite。关闭后重复回读进程日志，并直接检查 `jobs.failure_reason`、`job_runs.last_error`、`job_events.message` 的持久值。所有样本均为合成数据，不访问默认资料目录或系统凭据。

在 `apps/native` 下使用固定工具链运行：

```sh
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go test -race ./internal/infrastructure/diagnostics
GOTOOLCHAIN=go1.27.4 GOFLAGS=-mod=readonly go test ./internal/infrastructure/diagnostics -run '^$' -fuzz '^FuzzTextBoundsAndStructuredValues$' -fuzztime=20s -parallel=2
```

这些检查不代表其他操作系统实机、所有原生日志入口或生产依赖安全已经验收。
