# migrator

通用 GORM 迁移引擎，支持 PostgreSQL、MySQL、SQLite 和 ClickHouse。

**版本说明：** 已发布版本为 [v0.1.0](https://github.com/datafoxlabs/storekit/tree/migrator%2Fv0.1.0)，对应 Git tag `migrator/v0.1.0`。历史记录事务一致性、错误传播和 `ContinueOnError` 错误聚合修复已通过 [PR #1](https://github.com/datafoxlabs/storekit/pull/1) 合入 `main`（[c7ece647](https://github.com/datafoxlabs/storekit/commit/c7ece647e6d54ae97b8bff68ec08b430bff7865a)），**但尚未包含在 `v0.1.0` tag 中**。下文的改进后行为以 `main` 代码为依据，不是旧 tag 的兼容承诺。

## 安装

需要 Go 1.25.8 或更新版本。在使用方的 Go module 中执行：

```sh
go get github.com/datafoxlabs/storekit/migrator@v0.1.0
```

## 使用（由宿主单执行者迁移流程调用）

```go
engine := migrator.New(db, logger,
    migrator.WithHistoryTable("worker_migration_log"),
)
if err := engine.Run(ctx, migrations...); err != nil {
    return err // 中止部署；不能把失败视为迁移完成
}
```

示例中的 `db` 为已连接的 `*gorm.DB`，`logger` 为非空 `*slog.Logger`，`migrations` 为 `*migrator.Migration` 切片。此代码片段须放在返回 `error` 的迁移函数内，不应由每个业务副本在启动时并发调用。Migration ID 一旦投入使用应保持稳定。

默认历史表仍是 `migration_log`。`WithHistoryTable` 接受不带数据库或 schema 前缀的标识符（字母或下划线开头，随后为字母、数字或下划线）。非法标识符在 `New` 中 panic，发生在 SQL 执行前。自定义 `WithHistoryFactory` 自行负责历史存储，不能与非默认的 `WithHistoryTable` 同用。

切换历史表不会自动搬迁记录。已有应用切换前必须迁移其原历史记录，否则已执行的迁移可能再次运行。共用历史表的应用必须使用互不冲突的 migration ID 前缀；`DatabaseRelational`/`DatabaseClickHouse` 只是宿主选择目标的标记，不写入历史表，也不使引擎自动连接另一数据库。

关系型历史表按 `migration_id` upsert；成功重试覆盖旧失败。ClickHouse 历史追加记录，`IsApplied` 在存在任一成功记录时返回 true，失败列表保留所有失败记录；删除依赖异步 mutation，不能假设立即完成。**`main` 中的 PR #1 已改变事务与失败处理行为**，详见下文；历史存储的数据格式及 ClickHouse 的 append-only 判定语义没有改为“以最后一条为准”。

## 时区与兼容

生成式 ClickHouse DDL 默认不指定时区，由服务端决定。原宿主若依赖特定时区，必须显式传入：

```go
engine := migrator.New(clickhouseDB, logger,
    migrator.WithClickHouseTimezone("Asia/Shanghai"),
)
```

该选项只影响 `MigrationContext.CreateTable`/`AddColumn` 生成的时间列；直接使用 `ClickHouseDialect` 时设置其 `Timezone` 字段。`New` 对未知时区或向非 ClickHouse 引擎设置非空时区 panic。固定 `RawSQL` 和迁移历史表的 DDL 不受此选项影响。显式设置原时区可保留原生成 SQL。

`DatabasePostgres` 已改名为 `DatabaseRelational`，值由 `"postgres"` 改为 `"relational"`。空目标仍代表关系型数据库。该值从未写入迁移历史，不需要更改已存储的 ID、描述、状态或时间。

使用 `sqlstore` 的应用可继续调用 `RunMigrations*`。需要配置历史表或时区时，用 `migrator.New(store.GetDB(), logger, ...)` 创建引擎。

## ClickHouse 建表目标

通用 `CreateTable` 使用连接的当前数据库，不生成空库名前导点；`schema.Table` 显式填写 `Database` 时仍生成原有的限定表名。`CreateTable`、读写、重复执行及 `Down` 已纳入 ClickHouse 26.2 临时实例测试，同时覆盖默认时区与显式时区。现有已限定数据库的 DDL 渲染保持不变。

## 迁移状态一致性与恢复（`main`，未发布至 `v0.1.0`）

- **PostgreSQL / SQLite**：使用内置 `DefaultHistoryStore` 时，`Up` 与成功历史记录在同一事务提交；`Down` 与删除历史记录在同一事务提交。历史操作失败使整个事务失败，避免 DDL 与成功标记部分提交。
- **MySQL / ClickHouse 与自定义 `HistoryStore`**：不保证 DDL 与历史记录原子提交。即使 `Run` / `Rollback` 返回错误，部分 SQL 也可能生效；必须检查实际数据库状态后选择修复或重试，不能直接重放非幂等 DDL。
- **`ContinueOnError`**：默认 `false`，迁移失败立即返回；为 `true` 时继续后续迁移，最终仍返回非空的聚合错误，并保留 `errors.Is/As` 可识别的原因。**成功历史写入失败会立即停止后续迁移**，并合并此前累计的错误。此返回行为不同于已发布的 `v0.1.0`，升级时须检查调用方是否依赖旧的 `nil` 返回。
- **历史表切换**：修改 `WithHistoryTable` 不会自动搬迁历史记录，必须先转移旧状态，避免已执行迁移再次运行。

## 部署边界：单执行者

`migrator` **不负责跨进程或跨应用实例加锁**。推荐由宿主执行独立的 Migration Job：确定单执行者及迁移窗口 → 执行迁移 → 确认调用返回 `nil`，必要时检查 Schema 和历史记录 → 再允许新版本实例处理请求。出现错误时应停止部署，核查数据库实际状态；尤其需要针对 MySQL/ClickHouse 的非事务 DDL 制定幂等恢复策略。

不要让多个业务副本在启动时并发执行同一组迁移。`IsApplied` 是历史状态检查，不是互斥锁。此前提出的跨实例锁 PR [#3](https://github.com/datafoxlabs/storekit/pull/3)、[#4](https://github.com/datafoxlabs/storekit/pull/4) 均已关闭且未合并，**不属于当前 `main`**。
