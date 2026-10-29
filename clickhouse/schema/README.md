# ClickHouse schema

This module provides ClickHouse-specific schema and DDL builders for storage contracts and migrations.

Import path:

`github.com/datafoxlabs/storekit/clickhouse/schema`

Scope:

- ClickHouse table definitions
- ClickHouse materialized view definitions
- SQL rendering helpers for DDL and insert contracts

This module is intentionally ClickHouse-specific. It is not a generic database schema abstraction.

Column definitions contain storage metadata only. Query aliases and attribute namespaces belong to the consuming application's query binding.

Set `Table.Timezone` or pass a timezone to `ColumnType.StringWithTimezone` when the storage contract requires one. An empty timezone omits the SQL timezone argument, leaving ClickHouse's default in effect. The application owns its timezone policy.

## Versioning

This is an independent nested Go module. No version tag has been published. Consumers can use a public commit or `@main`; Go records the selected commit as a pseudo-version.

Future releases use module-prefixed tags, such as `clickhouse/schema/v0.3.0`. The candidate version used by the repository verification script is not a published release.
