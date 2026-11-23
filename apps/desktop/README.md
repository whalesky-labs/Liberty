# React/Tauri compatibility client

`apps/native` is the only daily development and candidate-launch entry. Use
`pnpm start` or `pnpm native:launch`; these commands never resolve the generic
`Liberty` application name and never start this React/Tauri client.

This directory is currently **frozen**, not deleted. It remains the production
release entry, compatibility reference and source for schema, migrations, Rust
fixtures and some presentation and gameplay assets. Both clients use the
[shared desktop resources](../../packages/desktop-resources/README.md) for the
runtime manifest, model pins, Word template, messages and work-map assets.

Allowed changes are limited to:

- security, data-loss and compatibility fixes;
- source migration required by `apps/native` generators or fixtures;
- CI, release-fallback and test maintenance required to keep the compatibility
  contract executable.

Do not add new product features here. Root commands that start the old UI use
the explicit `desktop:compat:*` namespace; ambiguous `desktop:dev` and
`desktop:tauri` aliases are intentionally absent. Direct commands such as
`pnpm --dir apps/desktop dev`, `dev:web` and `tauri` also start this old client
and are not the daily launch path. CI and release checks still use narrowly
scoped `desktop:test`, `desktop:typecheck` and build commands.

The existing Tauri workflow remains the default production release path and a
release fallback. Its presence does not mean that the old client is running
locally; the native candidate has not replaced this production entry.

## React/Tauri 兼容客户端

`apps/native` 是唯一的日常开发和候选启动入口。使用 `pnpm start` 或
`pnpm native:launch`；这些命令不会通过通用 `Liberty` 应用名解析，也不会
启动此 React/Tauri 客户端。

本目录当前是**冻结而非删除**的兼容实现，仍承担生产发布和兼容对照职责，并保留
schema、迁移、Rust 夹具及部分展示、玩法素材。运行时清单、模型版本、Word 模板、
文案与打工地图素材已迁至[桌面共享资源](../../packages/desktop-resources/README.md)，由两端共用。

根目录旧端 GUI 启动命令统一使用明确的 `desktop:compat:*` 前缀；易误用的
`desktop:dev`、`desktop:dev:web` 和 `desktop:tauri` 别名已移除。CI 与发布检查仍保留
必要的旧端测试、类型检查和构建命令。直接运行 `pnpm --dir apps/desktop dev`、
`dev:web` 或 `tauri` 也会启动旧端，不是新版启动方式。现有 Tauri workflow 仍是默认生产发布路径；
这不代表本机正在运行旧客户端，原生候选也尚未替代该生产入口。
