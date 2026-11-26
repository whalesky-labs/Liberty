# Native 旧数据回归样本

此目录保存旧客户端移除前，由 Liberty 1.1.24 Rust 测试导出的合成黄金样本。数据库、JSON、XLSX 和 DOCX 均由测试构造，不来自用户资料、真实凭据或媒体。`manifest.json` 记录每个文件及压缩包的大小和 SHA-256，并保存导出参考源码的集合摘要；样本不包含旧客户端源码或可执行文件。

`pnpm native:compat` 在新的临时目录解包并验证这些文件，运行 Go 的 schema v0–v10 升级、查询、业务事务、重开、协议及文档回归。`pnpm native:preview` 和 `pnpm native:bench:reads` 使用相同样本。解包器拒绝篡改、遗漏、重复路径、路径穿越、软硬链接及覆盖已有目录；所有样本通过校验后才写入。

样本是冻结的历史输入与期望输出。修改 Native 实现时不要刷新期望值来掩盖行为变化；有意改变业务规则时，新增独立 Native 回归并记录与旧格式的边界。当前系统的迁移规则仍由共享 `database/` 与 Native schema 执行器维护，此目录不作为新 schema 的生成源。

已确认的成长配饰缺陷是一个明确例外：旧样本在读取商店时会用解锁记录重新装备已卸下的 `clover-bow`。当前两端已改为保留库存中的装备选择。`TestWriteRustPetStoreActionsAndReopen` 从 `funded/use-over-99` 的商店读取开始，仅在内存中转换该配饰的装备标志、商品状态和配饰槽投影；原始归档、数量、时间、奖励、身份与其余字段不变。独立的卸下、替换、刷新和重开测试验证修复行为。

日期黄金样本的原始上下文为 UTC+08:00：输入保存 UTC 时刻，签到和每日上限的期望值按该固定偏移计算。带 `compatibility` 标签的 jobdb 测试显式恢复此上下文，避免使用 CI 主机时区改变历史输入含义；不修改应用的本地日期规则，也不修改未带该标签的时区测试。host 的 Runner 进程样本使用显式桌面目标和绑定当前输入摘要的合成运行时文件，不代表已安装真实运行时。

跨进程资料锁由 Native 子进程测试验证；恢复凭据由 Native 的实际 journal writer、recovery 和重放测试验证。静态样本验证历史数据与 Native 的兼容边界，不等于执行当前 Rust 客户端，也不提供向旧客户端回写的支持承诺。当前旧端仍为临时生产入口，切换与退役状态以[原生架构](../../../../docs/architecture/native-desktop-architecture.md)为准。签名、公证、真实平台与发布条件见[发布就绪检查](../../../../docs/architecture/release-readiness.md)。
