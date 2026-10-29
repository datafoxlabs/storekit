# migrator

`migrator` 是基于 GORM 的数据库结构迁移引擎，支持 **PostgreSQL、MySQL、SQLite 和 ClickHouse**。它提供迁移定义、方言适配、执行历史、重复执行跳过和可选回滚。

## 安装

需要 Go **1.25.8** 或更新版本：

```bash
go get github.com/datafoxlabs/storekit/migrator@v0.1.0
```

上述命令安装已发布的 `v0.1.0`。本页的迁移一致性与错误处理说明基于当前 `main` 源码；已发布版本在这些细节上可能不同。升级前请核对实际依赖版本。

## 定义和执行迁移

在宿主创建好 `*gorm.DB` 后，定义具有稳定 ID 的迁移：

```go
func applyMigrations(ctx context.Context, db *gorm.DB) error {
    migration := &migrator.Migration{
        ID:          "20261029_create_users",
        Description: "Create users table",
        Up: func(_ context.Context, mg migrator.MigrationContext) error {
            return mg.CreateTable(&migrator.Table{
                Name: "users",
                Columns: []*migrator.Column{
                    {Name: "id", Type: migrator.TypeBigInt, PrimaryKey: true, AutoIncrement: true},
                    {Name: "email", Type: migrator.TypeVarchar, Size: 255, NotNull: true},
                },
            })
        },
        Down: func(_ context.Context, mg migrator.MigrationContext) error {
            return mg.DropTable("users")
        },
    }

    engine := migrator.New(db, slog.Default())
    return engine.Run(ctx, migration)
}
```

示例需导入 `context`、`log/slog`、`gorm.io/gorm` 和 `github.com/datafoxlabs/storekit/migrator`。由部署流程调用该函数，并在返回错误时停止部署。

`MigrationContext` 支持 `CreateTable`、`AddColumn`、`AddIndex`、`RawSQL` 和 `Exec` 等操作。重复运行同一迁移时，引擎会检查历史记录并跳过已成功的 ID。

### 历史记录和回滚

默认历史表为 `migration_log`。可以为不同使用方设置不同的历史表：

```go
engine := migrator.New(db, logger,
    migrator.WithHistoryTable("worker_migration_log"),
)
err := engine.Run(ctx, migrations...)
history, err := engine.GetHistory(ctx, 20)
```

`Rollback(ctx, migrationID, migration)` 会调用相应的 `Down`，成功后删除对应历史记录。迁移未定义 `Down` 时无法回滚。

`WithHistoryTable` 只接受不带数据库/schema 前缀的表名：以字母或下划线开头，仅包含字母、数字和下划线。无效表名会在 `New` 时触发 panic。切换历史表**不会自动迁移历史数据**；在切换前必须处理旧记录，否则可能再次执行已完成的迁移。

### 失败策略

```go
engine := migrator.New(db, logger,
    migrator.WithOptions(&migrator.Options{
        ContinueOnError: false,
        SkipHistoryCheck: false,
    }),
)
```

- `ContinueOnError=false`（默认）：迁移失败时停止执行并返回错误。
- `ContinueOnError=true`：继续尝试后续迁移；当前 `main` 最终仍返回非空的聚合错误，可通过 `errors.Is/As` 检查失败原因。如果成功历史记录无法写入，立即停止执行，并保留此前的错误。
- `SkipHistoryCheck=true`：跳过已执行判断，**可能重复运行迁移**，仅用于明确需要重新执行的场景。

## 数据库兼容性和恢复

| 数据库 | 迁移事务与历史记录 |
|---|---|
| PostgreSQL、SQLite | 在当前 `main` 上，默认关系型历史存储与 `Up` / `Down` 在同一事务中更新；历史操作失败会使事务失败 |
| MySQL | DDL 可能隐式提交，Schema 修改与历史记录不保证原子性 |
| ClickHouse | DDL 不提供多语句事务；历史记录采用追加写入，删除依赖异步 mutation |
| 自定义 `HistoryStore` | 由使用方负责历史存储的事务与一致性语义 |

对于非事务 DDL，错误不代表所有操作都已回滚。恢复前应检查 Schema 和历史记录，优先使用可重入的迁移语句，避免直接重放非幂等 SQL。

`DatabaseRelational` 和 `DatabaseClickHouse` 是供宿主识别迁移目标的标记；**引擎不会据此自动切换数据库连接**。

## ClickHouse 时区

生成式 DDL 默认不显式设置时区。如需固定迁移使用的时区：

```go
engine := migrator.New(clickhouseDB, logger,
    migrator.WithClickHouseTimezone("Asia/Shanghai"),
)
```

该选项影响 `MigrationContext.CreateTable` / `AddColumn` 生成的时间列，不改写手工编写的 `RawSQL`。未知时区或非 ClickHouse 连接使用非空时区选项会在 `New` 时触发 panic。

## 部署建议

**同一个数据库只运行一个迁移执行者。** 推荐由独立的 Migration Job 执行迁移，并在确认成功后再启动新版本服务。不要在多个应用副本启动时并发调用 `Run`；`IsApplied` 仅用于历史状态检查，不提供跨实例互斥。

详细边界参见 [可靠性说明](../docs/RELIABILITY.md)。
