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
- `ContinueOnError` 保留所有先前执行错误：它会继续后续迁移并在完成时返回聚合错误；成功历史写入失败立即中断，但仍保留此前错误链。
- **不提供跨实例迁移锁。** 迁移应由宿主通过单执行者发布 Job 统一调度；多个应用实例不得同时自动运行迁移。`IsApplied` 不提供互斥语义。崩溃恢复与服务重启需要宿主单独演练。
- **版本边界：** 上述事务历史一致性与聚合错误行为描述当前仓库 `main` 的实现，不应直接套用到已发布的 `migrator/v0.1.0`。使用方必须以实际依赖的版本为准，并针对升级执行验收。
- **验证边界：** `make test-integration` / CI 当前仅选择运行 `TestDriverIntegration`，不要据此断言所有带 `integration` 构建标签的测试均已执行。验证选择器见 [scripts/verify-modules.py](../scripts/verify-modules.py)。
