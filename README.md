# storekit

通用 Go 存储工具库，提供 ClickHouse 建表 DSL、迁移引擎和 GORM 存储封装。三个库分别维护版本，仓库没有根 Go module。

| Module | 职责 |
|---|---|
| `github.com/datafoxlabs/storekit/clickhouse/schema` | ClickHouse 表、列、视图与 SQL 渲染 |
| `github.com/datafoxlabs/storekit/migrator` | 数据库方言、迁移执行与历史记录 |
| `github.com/datafoxlabs/storekit/sqlstore` | 连接、事务、重试与 tracing |
| `github.com/datafoxlabs/storekit/integration` | 仅测试的独立消费者，不发布版本 |

`sqlstore.Open` 接收显式驱动，调用方按需导入 `sqlstore/mysql`、`sqlstore/postgres`、`sqlstore/sqlite` 或 `sqlstore/clickhouse`。核心包不导入数据库驱动；测试辅助位于 `sqlstore/sqlstoretest`。用法见 [sqlstore](sqlstore/README.md)、[migrator](migrator/README.md) 和 [schema](clickhouse/schema/README.md)。

## 使用

需要 Go 1.25.8 或更新版本。当前没有正式版本 tag；可以在自己的 Go module 中获取公开分支，Go 会将依赖固定到对应提交的伪版本：

```sh
go get github.com/datafoxlabs/storekit/sqlstore@main
```

只使用建表 DSL 或迁移引擎时，分别获取 `github.com/datafoxlabs/storekit/clickhouse/schema@main` 或 `github.com/datafoxlabs/storekit/migrator@main`。模块之间的生产依赖固定到本仓库公开提交，不需要 workspace 或 replace。

## 验证

需要 Python 3、Git 和 C 编译器；从仓库根目录执行：

- `make test`：四个 module 的 tidy/no-diff、vet、race 测试，独立 integration 消费者，以及核心和四种驱动的依赖图与二进制检查。
- `make tidy`：在同样的验证流程中刷新未发布候选 module 的元数据；运行后检查差异。
- `make test-integration`：增加临时 PostgreSQL 16、MySQL 8.2、ClickHouse 26.2 服务验证，需要本机 Docker。
- `make test-integration-ci`：使用 GitHub Actions job 的隔离数据库服务；非 CI 环境直接失败。
- `make check-harness`：从公开仓库下载固定 commit 的 RepoFoundry 0.15.0，在临时目录执行 Harness、锁定 Spec 和文档完整性校验。

验证使用临时 Go proxy 提供本地候选版本，关闭 workspace，禁止 replace；不会发布版本。候选版本为 schema `v0.3.0`、migrator `v0.1.0` 和 sqlstore `v0.1.0`，不表示这些 tag 已发布。集成测试只使用新建容器，使用合成数据，结束时删除容器与关联匿名卷。

[GitHub Actions](.github/workflows/verify.yml) 复用相同脚本，服务镜像固定 digest，不执行发布或部署。远端检查以对应提交的 workflow 结果为准。当前实际使用方接入和正式版本发布尚未验收；本地测试或 CI 通过不代表生产验收。

更多边界见 [integration](integration/README.md)、[API 迁移](sqlstore/README.md#api-迁移)、[AGENTS.md](AGENTS.md) 和 [文档导航](docs/index.md)。
