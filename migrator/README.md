# migrator

通用 GORM 迁移引擎，支持 PostgreSQL、MySQL、SQLite 和 ClickHouse。当前已发布版本为 [v0.1.0](https://github.com/datafoxlabs/storekit/tree/migrator%2Fv0.1.0)，对应 Git tag `migrator/v0.1.0`。

## 安装

需要 Go 1.25.8 或更新版本。在使用方的 Go module 中执行：

```sh
go get github.com/datafoxlabs/storekit/migrator@v0.1.0
```

## 使用

```go
engine := migrator.New(db, logger,
    migrator.WithHistoryTable("worker_migration_log"),
)
err := engine.Run(ctx, migrations...)
```

默认历史表仍是 `migration_log`。`WithHistoryTable` 接受不带数据库或 schema 前缀的标识符（字母或下划线开头，随后为字母、数字或下划线）。非法标识符在 `New` 中 panic，发生在 SQL 执行前。自定义 `WithHistoryFactory` 自行负责历史存储，不能与非默认的 `WithHistoryTable` 同用。

切换历史表不会自动搬迁记录。已有应用切换前必须迁移其原历史记录，否则已执行的迁移可能再次运行。共用历史表的应用必须使用互不冲突的 migration ID 前缀；`DatabaseRelational`/`DatabaseClickHouse` 只是宿主选择目标的标记，不写入历史表，也不使引擎自动连接另一数据库。

关系型历史表继续按 `migration_id` upsert；成功重试覆盖旧失败。ClickHouse 历史继续追加记录，`IsApplied` 在存在任一成功记录时返回 true，失败列表保留所有失败记录；删除仍使用异步 mutation。本轮没有把其语义改成“以最后一条记录为准”。事务、执行顺序和失败处理沿用原实现。

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

## 迁移状态一致性与恢复

在 PostgreSQL/SQLite 等支持事务的方言上，使用默认关系型历史存储时，`Up` 与成功历史记录在同一事务中提交；`Down` 与删除历史记录也在同一事务中提交。写入历史失败会使事务回滚，并向调用方返回错误。MySQL/ClickHouse 的 DDL 不具备同等事务保证，自定义 `HistoryStore` 也不会自动绑定迁移事务；这些模式下历史写入失败会返回错误，但 SQL 可能已经生效。调用方必须检查数据库实际状态后再尝试恢复，`Up`/`Down` 应设计为可重入。

`ContinueOnError=true` 会尝试执行后续迁移，但只要有失败，`Run` 最终仍返回非空的聚合错误，并保留底层错误的 `errors.Is/As` 身份。历史成功记录写入失败时立即返回，不继续执行后续迁移。不要只依据执行日志或成功数量判断整体迁移结果。

PostgreSQL/MySQL 的跨实例迁移会话锁见下文；SQLite/ClickHouse 仍由宿主保证单执行者。

## 跨实例迁移互斥（PostgreSQL / MySQL）

`Run` 和 `Rollback` 在 PostgreSQL、MySQL 上默认获取数据库原生会话锁，并在整个历史检查、DDL 与历史记录写入期间保持锁；获取失败不会继续执行迁移。PostgreSQL 使用数据库级 advisory lock，MySQL 使用带数据库与历史表标识的 `GET_LOCK`。等待采用 Context 可取消轮询；异常断开锁连接后数据库会自动释放会话锁。

锁和迁移 SQL 现在使用同一条由 GORM `Connection` 固定的物理连接，默认关系型 `HistoryStore` 也复用该连接。使用 `MigrationContext` 提供的 DB 时，`MaxOpenConns=1` 不再因锁占用额外连接而自锁；如迁移回调自行使用外部数据库连接池，则应单独配置足够的连接。锁名按历史表隔离；不同历史表不会相互串行。

**限制**：锁与内置迁移操作处于同一个物理会话，锁连接失效后这些操作无法自动切换到其他连接继续执行。PostgreSQL 事务内的 DDL 会随连接断开回滚；MySQL 已提交的 DDL 仍需通过幂等迁移或人工恢复。自定义 `HistoryStore` 及调用方在 `Up`/`Down` 中打开的外部连接不受此会话约束，因此它仍不构成通用分布式 fencing。SQLite/ClickHouse 需要外部单执行者编排。
