# Native 旧数据回归样本

此目录保存旧客户端移除前，由 Liberty 1.1.24 Rust 测试导出的合成黄金样本。数据库、JSON、XLSX 和 DOCX 均由测试构造，不来自用户资料、真实凭据或媒体。`manifest.json` 记录每个文件及压缩包的大小和 SHA-256，并保存导出参考源码的集合摘要；样本不包含旧客户端源码或可执行文件。

`pnpm native:compat` 在新的临时目录解包并验证这些文件，运行 Go 的 schema v0–v10 升级、查询、业务事务、重开、协议及文档回归。`pnpm native:preview` 和 `pnpm native:bench:reads` 使用相同样本。解包器拒绝篡改、遗漏、重复路径、路径穿越、软硬链接及覆盖已有目录；所有样本通过校验后才写入。

样本是冻结的历史输入与期望输出。修改 Native 实现时不要刷新期望值来掩盖行为变化；有意改变业务规则时，新增独立 Native 回归并记录与旧格式的边界。当前系统的迁移规则仍由共享 `database/` 与 Native schema 执行器维护，此目录不作为新 schema 的生成源。

旧客户端退役后，跨进程资料锁由 Native 子进程测试验证；恢复凭据由 Native 的实际 journal writer、recovery 和重放测试验证。这里的静态文件不再声称验证一个仍在维护的 Rust 客户端，也不提供向旧客户端回写的支持承诺。签名、公证、真实平台与发布条件见[发布就绪检查](../../../../docs/architecture/release-readiness.md)。
