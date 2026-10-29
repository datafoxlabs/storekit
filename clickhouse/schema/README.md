# ClickHouse schema

`clickhouse/schema` 提供 ClickHouse 专用的结构定义和 DDL SQL 渲染能力。可以用 Go 对象描述表、列、物化视图、索引和存储配置，再生成 SQL 供应用的迁移流程执行。

## 安装

需要 Go **1.25.8** 或更新版本：

```bash
go get github.com/datafoxlabs/storekit/clickhouse/schema@v0.3.0
```

## 快速开始：定义表并生成 SQL

```go
table := &schema.Table{
    Database:        "analytics",
    DBType:          schema.CKDBTypeClickhouse,
    LocalName:       "events",
    Columns:         []*schema.Column{
        schema.NewColumn("id", schema.UInt64),
    },
    Engine:          schema.TableEngine{Name: schema.EngineTypeMergeTree},
    OrderKeys:       []string{"id"},
    PrimaryKeyCount: 1,
}

createSQL := table.RenderLocalTableCreateSQL()
dropSQL := table.RenderDropSQL()
```

以上示例需导入 `github.com/datafoxlabs/storekit/clickhouse/schema`。生成的 SQL 不会自动发送到数据库；由使用方选择连接、执行时机以及失败恢复方式。执行前应确保 `analytics` 数据库存在。

## 主要能力

| 组件 | 用途 |
|---|---|
| `Table`、`Column` | 定义表名、列类型、主键和排序键 |
| `TableEngine` | 定义 MergeTree 等 ClickHouse 表引擎 |
| `RenderLocalTableCreateSQL` | 生成本地表的 CREATE TABLE SQL |
| `RenderDropSQL` | 生成对应的 DROP TABLE SQL |
| 物化视图相关类型与渲染方法 | 定义和生成物化视图 SQL |
| 时区、分区、TTL、索引和存储设置 | 配置 ClickHouse 专用 DDL 选项 |

该模块专注 ClickHouse 存储结构，不是通用数据库 Schema API，也不负责数据库连接、业务查询或迁移调度。需要执行迁移时可与 [migrator](../../migrator/README.md) 配合使用。

## 时区与使用边界

需要固定时间类型的时区时，可以设置 `Table.Timezone`，或使用 `ColumnType.StringWithTimezone`。时区为空时，生成的 SQL 不会显式指定时区，由 ClickHouse 的配置决定。

表定义应由可信的应用代码提供，不要直接将未经校验的用户输入写入 SQL 标识符或表达式。ClickHouse DDL 不提供通用多语句事务，部分执行成功时需由宿主处理恢复。

## 版本

当前已发布版本为 [v0.3.0](https://github.com/datafoxlabs/storekit/tree/clickhouse%2Fschema%2Fv0.3.0)，对应模块 Git tag `clickhouse/schema/v0.3.0`。使用方应以实际安装的版本为准。
