# storekit

通用 Go 存储工具库，提供 ClickHouse 建表 DSL、迁移引擎和 GORM 存储封装。三个库分别维护版本，仓库没有根 Go module。

| Module | 已发布版本 | 职责 |
|---|---|---|
| `github.com/datafoxlabs/storekit/clickhouse/schema` | [v0.3.0](https://github.com/datafoxlabs/storekit/tree/clickhouse%2Fschema%2Fv0.3.0) | ClickHouse 表、列、视图与 SQL 渲染 |
| `github.com/datafoxlabs/storekit/migrator` | [v0.1.0](https://github.com/datafoxlabs/storekit/tree/migrator%2Fv0.1.0) | 数据库方言、迁移执行与历史记录 |
| `github.com/datafoxlabs/storekit/sqlstore` | [v0.1.0](https://github.com/datafoxlabs/storekit/tree/sqlstore%2Fv0.1.0) | 连接、事务、重试与 tracing |
| `github.com/datafoxlabs/storekit/integration` | 不发布 | 仅测试的独立消费者 |

`sqlstore.Open` 接收显式驱动，调用方按需导入 `sqlstore/mysql`、`sqlstore/postgres`、`sqlstore/sqlite` 或 `sqlstore/clickhouse`。核心包不导入数据库驱动；测试辅助位于 `sqlstore/sqlstoretest`。用法见 [sqlstore](sqlstore/README.md)、[migrator](migrator/README.md) 和 [schema](clickhouse/schema/README.md)。

> **版本边界：** 上表是已发布的 Git tag，不是 `main` 的当前功能清单。[PR #1](https://github.com/datafoxlabs/storekit/pull/1) 的迁移历史一致性修复已合入 `main`（[c7ece647](https://github.com/datafoxlabs/storekit/commit/c7ece647e6d54ae97b8bff68ec08b430bff7865a)），但**不包含在已发布的 `migrator/v0.1.0` 中**。需要修复的使用方应等待新版本发布并完成自己的接入验证；本仓库的 CI 结果不等于生产验收。

## 使用

需要 Go 1.25.8 或更新版本。在自己的 Go module 中按需执行对应库的安装命令：

```sh
go get github.com/datafoxlabs/storekit/clickhouse/schema@v0.3.0
go get github.com/datafoxlabs/storekit/migrator@v0.1.0
go get github.com/datafoxlabs/storekit/sqlstore@v0.1.0
```

每个库使用带模块目录前缀的 Git tag，例如 `clickhouse/schema/v0.3.0`；`go get` 使用模块路径与版本号。安装成功后，Go 会在使用方的 `go.mod` 和 `go.sum` 中记录依赖，不需要 workspace 或 replace。

## 迁移执行约束

**单执行者、宿主调度。** `migrator` 不实现跨进程或跨应用实例锁；推荐使用独立的部署 Migration Job，在服务副本开始提供请求前完成迁移。同一数据库不要由多个服务实例同时自动运行迁移。`IsApplied` 仅检查历史记录，不能替代并发互斥。跨实例锁方案未纳入当前代码（[#3](https://github.com/datafoxlabs/storekit/pull/3)、[#4](https://github.com/datafoxlabs/storekit/pull/4) 已关闭且未合并）。

以下示例使用宿主已建立的关系型 GORM 数据库连接；仅演示迁移引擎 API，**不是**负责单执行者调度的代码：

```go
func runSchemaMigrations(ctx context.Context, db *gorm.DB, logger *slog.Logger) error {
    migration := &migrator.Migration{
        ID:          "20261029_create_example_records",
        Description: "create example records",
        Up: func(_ context.Context, mg migrator.MigrationContext) error {
            return mg.RawSQL("CREATE TABLE IF NOT EXISTS example_records (id BIGINT PRIMARY KEY)")
        },
    }
    return migrator.New(db, logger).Run(ctx, migration)
}
```

其中 `context`、`log/slog`、`gorm.io/gorm` 和 `github.com/datafoxlabs/storekit/migrator` 由使用方导入。迁移 ID 必须稳定且避免冲突，失败时应检查数据库实际状态再重试；MySQL/ClickHouse DDL 不保证与历史记录原子提交。详情见 [迁移引擎说明](migrator/README.md) 和 [可靠性边界](docs/RELIABILITY.md)。

## 验证

需要 Python 3、Git 和 C 编译器；从仓库根目录执行：

- `make test`：四个 module 的 tidy/no-diff、vet、race 测试，独立 integration 消费者，以及核心和四种驱动的依赖图与二进制检查。
- `make tidy`：在同样的验证流程中刷新本地验证候选的元数据；运行后检查差异。
- `make test-integration`：使用临时 PostgreSQL 16、MySQL 8.2、ClickHouse 26.2 服务，执行当前验证脚本指定的服务端测试（目前仅选择 `TestDriverIntegration`）；需要本机 Docker。
- `make test-integration-ci`：使用 GitHub Actions job 的隔离数据库服务，执行相同的服务端测试选择器；非 CI 环境直接失败。
- `make check-harness`：从公开仓库下载固定 commit 的 RepoFoundry 0.15.0，在临时目录执行 Harness、锁定 Spec 和文档完整性校验。

验证使用临时 Go proxy 提供当前工作树的候选包，关闭 workspace，禁止 replace；不会发布版本。本地验证版本由 [scripts/verify-modules.py](scripts/verify-modules.py) 定义，使用 `-0.local` 后缀，与上表的已发布版本分开。源码或模块文档变化后，用 `make tidy` 更新本地候选校验和；已发布 tag 保持不变。集成测试只使用新建容器，使用合成数据，结束时删除容器与关联匿名卷。

[GitHub Actions](.github/workflows/verify.yml) 复用相同脚本，服务镜像固定 digest，不执行发布或部署。远端检查以对应提交的 workflow 结果为准。**服务端验证当前只选取 `TestDriverIntegration`**；仅有相关测试文件或 CI 绿灯，不代表其他 `integration` 标签下的测试已经执行。三个库的已发布版本仍为上表所列；生产接入、恢复演练及新版本发布均需另行验收。

更多边界见 [integration](integration/README.md)、[API 迁移](sqlstore/README.md#api-迁移)、[AGENTS.md](AGENTS.md) 和 [文档导航](docs/index.md)。
