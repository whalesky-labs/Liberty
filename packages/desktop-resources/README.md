# 桌面共享资源

这里维护 Go/MyGo 客户端与迁移期间 React/Tauri 对照端共用的源文件。React 通过 `@liberty-resources` 导入文案和展示素材，Rust 在编译时嵌入运行时、数据库和模板资源，Go 使用根目录生成脚本同步这些输入。Native 切换后，这里与 `apps/native` 继续承担共享事实源；旧端读取路径和旧端专属生成检查随退役变更删除，不构成长线兼容范围。

| 来源 | 维护内容 | 原生同步 |
| --- | --- | --- |
| `database/` | SQLite 迁移 SQL 和内置 AI 总结模板 | `pnpm native:migrations:generate` |
| `i18n/`、`presentation/` | 中英文文案、导航和标题栏图标、吉祥物 | `pnpm native:presentation:generate` |
| `images/farm-layers/`、`images/work-maps/` | 农场分层、市场地图、岗位贴图和角色帧 | `pnpm native:work-art:generate` |
| `pet/`、`images/pet/`、`redemption/` | 宠物阶段和商品目录、素材、兑换公钥 | `pnpm native:pet:generate`、`pnpm native:pet-art:generate`、`pnpm native:redemption:generate` |
| `runtime/` | 运行时清单、四个固定模型版本和 ModelScope 缓存脚本 | `pnpm native:runtime:generate` |
| `runtime/candidates/security-candidate/` | 三平台安全候选哈希锁 | `pnpm native:runtime:prepare`，操作见[原生模块说明](../../apps/native/README.md#运行时与安全候选) |
| `templates/meeting-minutes.docx` | Word 会议纪要模板 | `pnpm native:export:generate` |
| `fixtures/native-v10/` | Rust 旧实现导出的合成数据库、JSON、XLSX 和 DOCX 黄金样本 | `pnpm native:compat` 校验并在隔离目录重放 |

修改来源后执行对应生成命令和 `pnpm native:check`。旧端 inventory 脚本、基线及专用测试已移除，历史路由和 command 数量只保留为迁移记录，不代表当前持续检查。图片规范见[素材说明](../../docs/images/README.md)，运行时和发布要求见[发布就绪检查](../../docs/architecture/release-readiness.md)。

Native 生成器从共享来源读取输入，迁移由自己的 schema 执行器完成。兼容测试使用[冻结黄金样本](fixtures/native-v10/README.md)，不再构建或执行旧 Rust 客户端；当前检查证明历史格式重放与 Native 自身行为，不再提供向旧客户端回写的动态互操作证明。候选锁额外由 `native:check` 按清单核验名称、大小、SHA-256 和锁文件格式。
