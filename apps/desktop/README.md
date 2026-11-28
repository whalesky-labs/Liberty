# 临时生产与迁移对照客户端 / Temporary production client

`apps/desktop` 是冻结的 React/Tauri/Rust 实现，当前仍承担临时生产发布与迁移对照。Native 门槛通过后，旧 GUI 与专属发布、依赖和检查在同一退役变更中删除，不长期双轨维护。

日常启动使用仓库根目录的 `pnpm start`，进入 `apps/native` 原生候选。根目录没有旧 GUI 启动别名。只有明确维护旧端时才使用本目录的 `dev`、`dev:web` 或 `tauri` 脚本；具体命令见[开发指南](../../docs/development.md#选择启动方式)。

本目录只接受必要的安全、数据恢复、兼容、共享来源迁移及门禁维护。两端使用[共享资源](../../packages/desktop-resources/README.md)；Native 生成器与冻结兼容样本已独立维护，不以当前 Rust 可执行程序作为动态 oracle。已停用的 Native 样本导出器、跨进程互操作探针和读取基准已删除；旧端自身的数据恢复、锁和业务测试继续保留。

`apps/desktop` is the frozen React/Tauri/Rust implementation and temporary production release entry. After the [Native cutover gates](../../docs/architecture/release-readiness.md#cutover-and-retirement) pass, remove this GUI and its dedicated delivery chain, dependencies and checks together.

Daily startup uses root `pnpm start` for the Native candidate. Old GUI startup aliases are absent. Direct directory scripts are only for explicit maintenance of the retained client. Changes here are limited to security, data recovery, compatibility, source migration and required gate maintenance; shared sources and frozen fixtures support Native independently.
