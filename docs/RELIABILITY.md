# 可靠性边界

storekit 是嵌入式库，不定义宿主服务的 SLO。宿主拥有配置、连接生命周期、迁移窗口和恢复责任。

- 普通业务错误和未知错误不重试；所选驱动对白名单错误分类。网络错误可能有不确定提交结果，不自动重放。
- Exec 复用 context 中的事务；事务内错误交给外层整体回滚后重试。回调须适合重跑，事务外副作用由调用方负责。
- 重试退避可被 context 取消；Close 关闭数据库连接，TracerProvider 生命周期由宿主持有。
- 迁移历史记录迁移级结果，不是逐 SQL 审计。共享数据库使用不冲突的 ID 前缀；换历史表前先迁移记录。
- ClickHouse DDL 不是多语句事务，部分失败需要迁移自身提供可重跑 predicate 或显式恢复路径。
- 26.2 ARM 的既有集成夹具关闭表达式/聚合/排序 JIT，以避开观察到的服务端 SIGILL；库不改变宿主默认设置。

验证入口为 `make test` 和 `make test-integration`，测试定义在 sqlstore/*_test.go、migrator/*_test.go 与 integration。
服务端测试记录实际版本和镜像身份，ClickHouse 强制 26.2.*。历史验证不能替代修改后的当前验收。
生产使用方接入、业务数据恢复演练和运行 SLO 未由本仓库验收；不把本地或 CI 测试声明为生产验收。

### 迁移执行与历史一致性

- 默认关系型 `HistoryStore` 在支持事务的方言上将 `Up`/`Down` 和对应成功记录写入/删除放在同一个事务中。历史记录写入失败会使 DDL 回滚；失败历史记录单独写入，不参与已回滚的执行事务。
- 对 MySQL/ClickHouse 或自定义 `HistoryStore`，历史写入错误会明确返回，但已经执行的 DDL 仍可能生效。恢复前需要检查 Schema 实际状态，不可无条件重新执行非幂等 SQL。
- `ContinueOnError` 不会吞掉执行失败：它会继续后续迁移并在完成时返回聚合错误；成功历史写入失败直接中断。
- PostgreSQL/MySQL 已增加会话锁，但不具备分布式 fencing，数据库会话断开后存在锁丢失窗口。SQLite/ClickHouse 仍需宿主单执行者。

### PostgreSQL / MySQL 迁移会话锁

- 在 `Run` 和 `Rollback` 开始时获取数据库会话锁，锁住历史检查、迁移执行和记录持久化路径；锁等待可由 Context 中止。
- 锁连接由数据库连接池独立固定，执行迁移需要其他连接。连接池最大值为 1 时拒绝运行，避免自锁。
- 此锁没有跨连接的 fencing token：若锁会话意外断开，正在执行的 SQL 仍可能在另一连接上继续。生产多副本部署仍优先采用单实例迁移 Job，严禁将锁视为分布式 exactly-once 保证。
- SQLite / ClickHouse 的跨实例迁移继续需要外部编排；MySQL / ClickHouse 的非事务 DDL 遇到中途崩溃仍需人工核对数据库状态。
