# ClickHouse schema

This module provides ClickHouse-specific schema and DDL builders for storage contracts and migrations.

Import path:

`github.com/datafoxlabs/storekit/clickhouse/schema`

## Installation

With Go 1.25.8 or newer, run this command in your application's Go module:

```sh
go get github.com/datafoxlabs/storekit/clickhouse/schema@v0.3.0
```

## Scope

- ClickHouse table definitions
- ClickHouse materialized view definitions
- SQL rendering helpers for DDL and insert contracts

This module is intentionally ClickHouse-specific. It is not a generic database schema abstraction.

Column definitions contain storage metadata only. Query aliases and attribute namespaces belong to the consuming application's query binding.

Set `Table.Timezone` or pass a timezone to `ColumnType.StringWithTimezone` when the storage contract requires one. An empty timezone omits the SQL timezone argument, leaving ClickHouse's default in effect. The application owns its timezone policy.

## Versioning

This is an independent nested Go module. The published version is [v0.3.0](https://github.com/datafoxlabs/storekit/tree/clickhouse%2Fschema%2Fv0.3.0), identified by the module-prefixed tag `clickhouse/schema/v0.3.0`.

Repository verification uses separate `-0.local` candidate versions for the current working tree. Those temporary artifacts do not change published versions; see the [verification commands](../../README.md#验证).
