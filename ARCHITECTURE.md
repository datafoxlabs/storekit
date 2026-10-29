# storekit Architecture

storekit 提供 ClickHouse 建表 DSL、迁移引擎和 GORM 存储封装。仓库没有应用入口、业务 schema 或根 Go module。
当前仍为未发布候选。代码边界由各 module 的 `go.mod` 和 `scripts/verify-modules.py` 的依赖检查持有。

| Module | 职责 | 仓库内依赖 |
|---|---|---|
| `clickhouse/schema` | 表、列、视图和 SQL 渲染 | 无 |
| `migrator` | 方言、迁移执行和历史表 | schema |
| `sqlstore` | 连接、事务、重试和 tracing | migrator |
| `integration` | 外部消费者、驱动及服务端验收 | 上述三个库；只含测试，不发 tag |

```mermaid
flowchart TD
    Host[使用方] --> Store[sqlstore]
    Host --> Schema[clickhouse/schema]
    Store --> Migrator[migrator]
    Migrator --> Schema
    Tests[integration 测试] --> Store
    Tests --> Migrator
    Tests --> Schema
```

生产库不得依赖 integration 或宿主应用。schema 不带查询语言元数据、宿主配置或时区默认策略。
sqlstore 核心不导入驱动；宿主显式选用 mysql/postgres/sqlite/clickhouse 子包。
`sqlstoretest` 为测试便利会导入多个驱动，不用于生产程序集成。

验证脚本按依赖顺序将本地候选发布到临时 Go proxy，再复制 integration 到隔离目录测试。
测试关闭 workspace、拒绝 replace，使用全新候选缓存；integration 不加入候选发布集合。
普通 `make test` 不连接服务端；集成模式单独启用 Docker 夹具或 GitHub Actions 服务。

模块边界由本仓库维护；实际使用方的接入和发布验收由各使用方记录。

目前没有正式发布 tag，实际使用方接入尚未验收。CI 通过不代表正式发布或业务环境迁移完成。
