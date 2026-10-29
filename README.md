# storekit

storekit 是一组独立版本的 Go 存储工具库，面向使用 GORM 和 ClickHouse 的服务。它将数据库连接、事务与重试、结构迁移以及 ClickHouse DDL 定义拆分为三个可单独引入的模块。

## 功能与模块

| 模块 | 适用场景 | 已发布版本 |
|---|---|---|
| [`sqlstore`](sqlstore/README.md) | GORM 连接管理、事务传播、可配置重试、结构化日志与 OpenTelemetry | [v0.1.0](https://github.com/datafoxlabs/storekit/tree/sqlstore%2Fv0.1.0) |
| [`migrator`](migrator/README.md) | PostgreSQL、MySQL、SQLite、ClickHouse 的 Schema 迁移和历史记录 | [v0.1.0](https://github.com/datafoxlabs/storekit/tree/migrator%2Fv0.1.0) |
| [`clickhouse/schema`](clickhouse/schema/README.md) | ClickHouse 表、列、物化视图和 DDL SQL 的结构化定义 | [v0.3.0](https://github.com/datafoxlabs/storekit/tree/clickhouse%2Fschema%2Fv0.3.0) |

各模块分别维护 `go.mod` 和版本号，仓库根目录没有 Go module。按需引入即可，`sqlstore` 的数据库驱动通过子包显式选择。

## 安装

需要 Go **1.25.8** 或更新版本。在应用的 Go module 中安装需要的组件：

```bash
go get github.com/datafoxlabs/storekit/sqlstore@v0.1.0
go get github.com/datafoxlabs/storekit/migrator@v0.1.0
go get github.com/datafoxlabs/storekit/clickhouse/schema@v0.3.0
```

以上命令安装的是**已发布版本**；仓库 `main` 的最新实现可能包含尚未发布的调整。具体行为以应用实际使用的模块版本为准。

## 快速开始

### 1. 连接 PostgreSQL

```go
package main

import (
    "context"
    "log"
    "log/slog"
    "os"

    "github.com/datafoxlabs/storekit/sqlstore"
    "github.com/datafoxlabs/storekit/sqlstore/postgres"
    "go.opentelemetry.io/otel/trace/noop"
)

func main() {
    dsn := os.Getenv("DATABASE_URL")
    if dsn == "" {
        log.Fatal("DATABASE_URL is required")
    }

    cfg, err := sqlstore.NewDatabaseConfig(
        sqlstore.WithDBType("postgres"),
        sqlstore.WithConnectionString(dsn),
    )
    if err != nil {
        log.Fatal(err)
    }

    store, err := sqlstore.Open(cfg, postgres.Open, slog.Default(), noop.NewTracerProvider())
    if err != nil {
        log.Fatal(err)
    }
    defer store.Close()

    if err := store.Ping(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

`sqlstore` 也提供 MySQL、SQLite 和 ClickHouse 驱动适配器。事务、重试配置和追踪使用方法见 [sqlstore 文档](sqlstore/README.md)。

### 2. 执行数据库迁移

定义带有稳定 ID 的 `migrator.Migration`，通过 `Run(ctx, migrations...)` 执行。迁移历史用于跳过已成功执行的迁移；失败时应检查数据库实际状态。

迁移必须由宿主保证**单执行者**，推荐放在独立的部署 Migration Job 中，不要让多个服务副本启动时同时执行。具体示例与数据库兼容性说明见 [migrator 文档](migrator/README.md)。

### 3. 生成 ClickHouse DDL

使用 `clickhouse/schema` 描述表结构，再调用渲染方法生成 SQL。可配置表引擎、排序键、列类型及显式时区等选项。示例见 [ClickHouse schema 文档](clickhouse/schema/README.md)。

## 开发与测试

在仓库根目录执行：

| 命令 | 用途 |
|---|---|
| `make test` | 格式检查、依赖边界、Go Vet、Race 测试和独立模块消费验证 |
| `make test-integration` | 使用临时 Docker 数据库运行服务端集成测试 |
| `make tidy` | 更新本地验证用的模块元数据 |
| `make check-harness` | 检查仓库工程规范与 Harness |

集成测试目前按 [验证脚本](scripts/verify-modules.py) 的选择器运行，详细前提和测试覆盖范围见 [integration 文档](integration/README.md)。

更多信息：[架构](ARCHITECTURE.md) · [可靠性与恢复](docs/RELIABILITY.md) · [安全边界](docs/SECURITY.md) · [文档索引](docs/index.md)。
