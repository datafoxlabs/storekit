# sqlstore

基于 GORM 提供事务传播、重试、连接池、OpenTelemetry 追踪和结构化日志。当前已发布版本为 [v0.1.0](https://github.com/datafoxlabs/storekit/tree/sqlstore%2Fv0.1.0)，对应 Git tag `sqlstore/v0.1.0`。

## 安装

需要 Go 1.25.8 或更新版本。在使用方的 Go module 中执行：

```sh
go get github.com/datafoxlabs/storekit/sqlstore@v0.1.0
```

## 打开连接

```go
import (
    "log/slog"

    "github.com/datafoxlabs/storekit/sqlstore"
    "github.com/datafoxlabs/storekit/sqlstore/postgres"
    "go.opentelemetry.io/otel/trace/noop"
)

cfg, err := sqlstore.NewDatabaseConfig(
    sqlstore.WithDBType("postgres"),
    sqlstore.WithConnectionString(dsn), // 由宿主配置提供
)
if err != nil {
    return err
}
store, err := sqlstore.Open(cfg, postgres.Open, slog.Default(), noop.NewTracerProvider())
if err != nil {
    return err
}
defer store.Close()
```

`Open(cfg *DatabaseConfig, driver Driver, logger *slog.Logger, provider trace.TracerProvider) (*Store, error)` 使用已构建的连接串。`Driver` 是 `func(string) gorm.Dialector`；可用四个驱动子包的 `Open`，也可由宿主传入自定义 GORM dialector 工厂。工厂必须返回有效 dialector，名称与 `cfg.Type` 一致（`sqlite3` 对应 `sqlite`）。缺少必要参数或类型不匹配时，`Open` 在连接数据库前返回错误。

核心包不导入任何驱动。仅导入 `sqlstore/mysql` 的应用不会链接 PostgreSQL、SQLite 或 ClickHouse 驱动。四个适配器仍位于同一个 module，go.mod 的依赖列表不等同于程序实际链接的包。

## 配置与行为

`DatabaseConfig` 支持 `Type`、`Host`、`Name`、`User`、`Password`、`Path`、`ConnectionString` 和 `UrlQueryParams`。`NewDatabaseConfig` 应用选项后构建连接串；已提供 `ConnectionString` 时保留原值。SQLite 必须显式提供 `Path` 或连接串，没有默认文件路径；文件路径的父目录仍由 `BuildConnectionString` 创建。

默认配置保持：SQLite、private cache、2 个空闲连接、14400 秒连接寿命、0 次查询重试、5 次事务重试、10ms 重试间隔、Warn 日志。直接创建 `DatabaseConfig` 不会自动应用这些默认值。

| API | 行为 |
|---|---|
| `Exec(ctx, fn)` | 普通查询按 `QueryRetries` 重试；context 已有事务时直接复用 |
| `Tx(ctx, fn)` | 事务失败先回滚，再按 `TransactionRetries` 重试 |
| `InTransaction(ctx, fn)` | 在 context 中传播事务；嵌套调用复用外层事务 |
| `GetDB()` | 返回 GORM 连接，不自动应用封装层的重试 |
| `WithTimeout(ctx, fn)` | 按 `QueryTimeout` 限制调用时间 |
| `AutoMigrate(models...)` | 执行 GORM 自动迁移 |
| `RunMigrations` / `RunMigrationsWithOptions` | 执行迁移引擎；应由宿主独立的 Migration Job 调用（后者需传非空 `*migrator.Options`） |
| `BulkInsert` / `InBatches` | 按批处理，默认 batch size 为 1000 |
| `Ping` / `Stats` / `Close` | 健康检查、连接池统计、关闭连接 |

重试只接受所选驱动的明确白名单：PostgreSQL `40001`/`40P01`，MySQL `1213`/`1205`，SQLite `SQLITE_BUSY`（含 BUSY_SNAPSHOT）。业务错误、唯一键冲突、语法错误、取消和网络断连不自动重试；网络失败时写入是否提交可能未知。ClickHouse 当前没有自动重试白名单。自定义 dialector 可实现 `IsRetryable(error) bool`；未实现时只执行一次。分类代码留在驱动子包，核心包不导入驱动。

`Open` 开启 GORM `TranslateError`，关系型驱动的重复键错误可用 `errors.Is(err, gorm.ErrDuplicatedKey)` 判断。分类支持包装错误；事务重试前由 GORM 回滚整次事务。回调必须适合重跑，事务外副作用仍由调用方负责。保留原次数语义：正数是最大总尝试次数，非正数执行一次。取消会立即中断重试等待，已取消的 context 不调用回调。

`DB()` 和 `WithRetry()` 已在首次发布前移除。直接访问 GORM 使用 `GetDB()`；带重试的操作使用 `Exec()`。
`Exec()` 在 `InTransaction()` 的 context 中复用事务，不单独重试事务内操作；可重试错误交给外层事务整体回滚后重试。

## Schema 迁移的调用边界

`RunMigrations` / `RunMigrationsWithOptions` 只是封装 `migrator`，**不自动提供跨实例锁或部署排他机制**。建议由独立的单执行者 Migration Job 调用，而不是由多个业务副本同时在启动时触发迁移。迁移执行失败时 `sqlstore` 返回包装了底层错误的 `ErrMigrationFailed`，调用方应中止部署并核查数据库状态。

`main` 中的 [PR #1](https://github.com/datafoxlabs/storekit/pull/1) 已修复迁移历史一致性及错误传播；该修复**尚未包含在已发布的 `migrator/v0.1.0` 和 `sqlstore/v0.1.0` 中**。请区分当前源码与已发布 tag。具体的事务能力、ClickHouse 历史语义及 `ContinueOnError` 行为见 [migrator 使用说明](../migrator/README.md)。

所有存储操作 span 与 GORM span 都使用传入的 TracerProvider，不读取或修改全局 provider。无追踪时传入 `noop.NewTracerProvider()`。宿主负责 provider 的生命周期，`Store.Close` 只关闭数据库连接。

`errors.Is` 可判断 `ErrPingFailed`、`ErrMigrationFailed`，包装错误保留底层 cause。应用错误分类由宿主适配。

## 测试辅助

```go
import "github.com/datafoxlabs/storekit/sqlstore/sqlstoretest"

store := sqlstoretest.New(t, sqlstoretest.WithModels(&User{}))
```

每次 `New` 创建独立数据库，并通过 `testing.TB.Cleanup` 关闭连接和删除临时资源。默认使用 SQLite 临时文件，`WithInMemory()` 使用唯一命名的内存库。`WithModels` 执行 AutoMigrate，`WithTruncation` 删除所提供模型的全部行。

用 `WithDBType("postgres")` 或 `WithDBType("mysql")` 显式选择服务端测试库。测试辅助会创建 `storekit_test_<hex>` 数据库并在结束时删除，测试账号需要建库和删库权限。连接参数取自以下通用环境变量：

| 前缀 | 变量与默认值 |
|---|---|
| `POSTGRES_` | HOST=localhost、PORT=5432、USER=postgres、PASSWORD=空、DB=postgres、SSL=disable |
| `MYSQL_` | HOST=localhost、PORT=3306、USER=root、PASSWORD=空、DB=mysql |

不再由应用专有环境变量选择数据库，也不读取 `SQLITE_INMEMORY`。`sqlstoretest` 为测试便利会导入三个测试驱动，生产程序不要导入此包。

## API 迁移

以下入口从未在 storekit 正式发布，不提供旧名别名：

| 迁入时入口 | 当前入口 |
|---|---|
| `sqlstore.ProvideService` | `sqlstore.Open`，增加显式驱动参数 |
| `sqlstore.SQLStore` | `sqlstore.Store` |
| `(*Store).DB()` | `(*Store).GetDB()` |
| `(*Store).WithRetry(ctx, fn)` | `(*Store).Exec(ctx, fn)`，同时提供事务复用和 tracing |
| `sqlstore.NewTestStore` | `sqlstoretest.New` |
| `sqlstore.WithTestDBType` | `sqlstoretest.WithDBType` |
| `sqlstore.WithModels` / `WithTruncation` | `sqlstoretest` 下同名选项 |
| 包级 `sqlstore.StartSpan` | 封装内部使用注入 provider；宿主自建 span 使用自己的 tracer |

迁移宿主时，应显式保留原 SQLite 路径、测试数据库选择及连接参数；使用生成式 ClickHouse DDL 的宿主还需显式指定原时区。历史表与迁移目标的变化见 [migrator](../migrator/README.md)。新旧 module 路径的类型不互换。本轮没有修改现有应用的依赖或数据库。
