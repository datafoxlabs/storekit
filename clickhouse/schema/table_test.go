package schema_test

import (
	"strings"
	"testing"
	"time"

	"github.com/datafoxlabs/storekit/clickhouse/schema"
)

// ─── 层1：RenderLocalTableCreateSQL 系列 ───

func TestRenderCreateTable_Basic(t *testing.T) {
	table := &schema.Table{
		Database:  "test_db",
		DBType:    schema.CKDBTypeClickhouse,
		LocalName: "events",
		Engine:    schema.TableEngine{Name: schema.EngineTypeMergeTree},
		Columns: []*schema.Column{
			schema.NewColumn("id", schema.UInt64),
			schema.NewColumn("name", schema.String),
		},
		OrderKeys:       []string{"id"},
		PrimaryKeyCount: 1,
	}

	sql := table.RenderLocalTableCreateSQL()

	assertContains(t, sql, "CREATE TABLE IF NOT EXISTS test_db.`events`")
	assertContains(t, sql, "ENGINE = MergeTree()")
	assertContains(t, sql, "ORDER BY (id)")
	assertContains(t, sql, "PRIMARY KEY (id)")
	assertContains(t, sql, "`id` UInt64")
	assertContains(t, sql, "`name` String")
}

func TestRenderCreateTable_WithTTL(t *testing.T) {
	table := &schema.Table{
		Database:  "test_db",
		DBType:    schema.CKDBTypeClickhouse,
		LocalName: "logs",
		Engine:    schema.TableEngine{Name: schema.EngineTypeMergeTree},
		Columns: []*schema.Column{
			schema.NewColumn("timestamp", schema.DateTime64ns).SetTimeFiled(),
			schema.NewColumn("msg", schema.String),
		},
		TTL:             15 * 24 * time.Hour,
		TTLFunc:         schema.TimeFuncDateTime,
		OrderKeys:       []string{"timestamp"},
		PrimaryKeyCount: 1,
	}

	sql := table.RenderLocalTableCreateSQL()

	// 15 天 = toIntervalDay(15)
	assertContains(t, sql, "TTL")
	assertContains(t, sql, "toIntervalDay(15)")
}

func TestRenderCreateTable_WithPartition(t *testing.T) {
	table := &schema.Table{
		Database:  "test_db",
		DBType:    schema.CKDBTypeClickhouse,
		LocalName: "spans",
		Engine:    schema.TableEngine{Name: schema.EngineTypeMergeTree},
		Columns: []*schema.Column{
			schema.NewColumn("timestamp", schema.DateTime64ns).SetTimeFiled(),
			schema.NewColumn("data", schema.String),
		},
		PartitionFunc:   schema.TimeFuncDate,
		OrderKeys:       []string{"timestamp"},
		PrimaryKeyCount: 1,
	}

	sql := table.RenderLocalTableCreateSQL()

	assertContains(t, sql, "PARTITION BY toDate(timestamp)")
}

func TestRenderCreateTable_WithCustomSettings(t *testing.T) {
	table := &schema.Table{
		Database:  "test_db",
		DBType:    schema.CKDBTypeClickhouse,
		LocalName: "summary",
		Engine:    schema.TableEngine{Name: schema.EngineTypeMergeTree},
		Columns: []*schema.Column{
			schema.NewColumn("date", schema.Date).SetTimeFiled(),
			schema.NewColumn("service_name", schema.LowCardinalityString),
		},
		TTL:             30 * 24 * time.Hour,
		TTLFunc:         schema.TimeFuncDate,
		PartitionFunc:   schema.TimeFuncDate,
		OrderKeys:       []string{"date", "service_name"},
		PrimaryKeyCount: 2,
		Settings: []string{
			"allow_part_offset_column_in_projections = 1",
		},
	}

	sql := table.RenderLocalTableCreateSQL()

	assertContains(t, sql, "allow_part_offset_column_in_projections = 1")
}

func TestRenderCreateTable_WithExpressionIndexes(t *testing.T) {
	table := &schema.Table{
		Database:  "test_db",
		DBType:    schema.CKDBTypeClickhouse,
		LocalName: "event_logs",
		Engine:    schema.TableEngine{Name: schema.EngineTypeMergeTree},
		Columns: []*schema.Column{
			schema.NewColumn("event_timestamp", schema.DateTime).SetTimeFiled(),
			schema.NewColumn("attributes", schema.Map).SetTypeArgs("LowCardinality(String), String"),
		},
		OrderKeys:       []string{"event_timestamp"},
		PrimaryKeyCount: 1,
		Indexes: []*schema.TableIndex{
			{
				Name:        "idx_map_keys",
				Expression:  "mapKeys(attributes)",
				Type:        schema.IndexBloomfilter.String(),
				Granularity: 1,
			},
			{
				Name:        "idx_map_values",
				Expression:  "mapValues(attributes)",
				Type:        schema.IndexBloomfilter.String(),
				Granularity: 1,
			},
		},
	}

	sql := table.RenderLocalTableCreateSQL()

	assertContains(t, sql, "INDEX idx_map_keys (mapKeys(attributes)) TYPE bloom_filter(0.01) GRANULARITY 1")
	assertContains(t, sql, "INDEX idx_map_values (mapValues(attributes)) TYPE bloom_filter(0.01) GRANULARITY 1")
}

func TestRenderCreateTable_WithConstraints(t *testing.T) {
	table := &schema.Table{
		Database:  "test_db",
		DBType:    schema.CKDBTypeClickhouse,
		LocalName: "raw_events",
		Engine:    schema.TableEngine{Name: schema.EngineTypeMergeTree},
		Columns: []*schema.Column{
			schema.NewColumn("id", schema.UInt64),
			schema.NewColumn("raw", schema.String),
		},
		Constraints: []*schema.TableConstraint{
			{
				Name:       "ck_raw_object",
				Expression: "JSONType(raw) = 'Object'",
			},
		},
		OrderKeys:       []string{"id"},
		PrimaryKeyCount: 1,
	}

	sql := table.RenderLocalTableCreateSQL()

	assertContains(t, sql, "CONSTRAINT ck_raw_object CHECK JSONType(raw) = 'Object'")
	if strings.Index(sql, "`raw` String") > strings.Index(sql, "CONSTRAINT ck_raw_object") {
		t.Fatalf("constraint rendered before its columns: %s", sql)
	}
}

func TestRenderCreateTable_WithLongDayTTL(t *testing.T) {
	table := &schema.Table{
		Database:  "test_db",
		DBType:    schema.CKDBTypeClickhouse,
		LocalName: "alerts",
		Engine:    schema.TableEngine{Name: schema.EngineTypeMergeTree},
		Columns: []*schema.Column{
			schema.NewColumn("timestamp", schema.DateTime).SetTimeFiled(),
		},
		TTL:             180 * 24 * time.Hour,
		TTLFunc:         schema.TimeFuncDate,
		PartitionFunc:   schema.TimeFuncDate,
		OrderKeys:       []string{"timestamp"},
		PrimaryKeyCount: 1,
	}

	sql := table.RenderLocalTableCreateSQL()

	assertContains(t, sql, "TTL toDate(timestamp) + toIntervalDay(180)")
}

// ─── 层1：列类型渲染 ───

func TestColumnType_DateTime64ns(t *testing.T) {
	col := schema.NewColumn("ts", schema.DateTime64ns)
	rendered := col.StringWithTimezone("Asia/Shanghai")

	assertContains(t, rendered, "DateTime64(9, 'Asia/Shanghai')")
}

func TestColumnType_FixedString(t *testing.T) {
	col := schema.NewColumn("trace_id", schema.FixedString).SetTypeArgs("32")
	rendered := col.StringWithTimezone("Asia/Shanghai")

	assertContains(t, rendered, "FixedString(32)")
}

func TestColumnType_Map(t *testing.T) {
	col := schema.NewColumn("attributes", schema.Map).
		SetTypeArgs("LowCardinality(String), String")
	rendered := col.StringWithTimezone("Asia/Shanghai")

	assertContains(t, rendered, "Map(LowCardinality(String), String)")
}

func TestColumnType_WithDefault(t *testing.T) {
	col := schema.NewColumn("updated_at", schema.DateTime).
		SetDefault("now()")
	rendered := col.StringWithTimezone("Asia/Shanghai")

	assertContains(t, rendered, "DEFAULT now()")
}

func TestColumnType_Nested(t *testing.T) {
	col := schema.NewColumn("events", schema.Nested).SetNested([]*schema.Column{
		schema.NewColumn("timestamp", schema.DateTime64ns),
		schema.NewColumn("name", schema.LowCardinalityString),
		schema.NewColumn("attributes", schema.Map).
			SetTypeArgs("LowCardinality(String), String"),
	})

	// 构造包含 Nested 列的 Table，需要走 RenderLocalTableCreateSQL 来触发 Nested 处理
	table := &schema.Table{
		Database:  "test_db",
		DBType:    schema.CKDBTypeClickhouse,
		LocalName: "test_nested",
		Engine:    schema.TableEngine{Name: schema.EngineTypeMergeTree},
		Columns: []*schema.Column{
			schema.NewColumn("id", schema.UInt64),
			col,
		},
		OrderKeys:       []string{"id"},
		PrimaryKeyCount: 1,
	}

	sql := table.RenderLocalTableCreateSQL()

	// Nested 应展开为 Nested(子列定义)
	assertContains(t, sql, "Nested(")
	assertContains(t, sql, "`timestamp` DateTime64(9)")
	assertContains(t, sql, "`name` LowCardinality(String)")
	assertContains(t, sql, "`attributes` Map(LowCardinality(String), String)")
}

// ─── 层1：Codec ───

func TestCodecs_Multiple(t *testing.T) {
	col := schema.NewColumn("ts", schema.DateTime64ns).
		SetCodecs(schema.CodecDelta, schema.CodecZSTD)
	rendered := col.StringWithTimezone("Asia/Shanghai")

	assertContains(t, rendered, "CODEC(Delta, ZSTD(1))")
}

// ─── 层1：时区参数化 ───

func TestTimezoneParameterization(t *testing.T) {
	// 未指定时区，由 ClickHouse 使用自身默认值
	table := &schema.Table{
		Database:  "test_db",
		DBType:    schema.CKDBTypeClickhouse,
		LocalName: "tz_test",
		Engine:    schema.TableEngine{Name: schema.EngineTypeMergeTree},
		Columns: []*schema.Column{
			schema.NewColumn("ts", schema.DateTime64ns).SetTimeFiled(),
		},
		OrderKeys:       []string{"ts"},
		PrimaryKeyCount: 1,
	}

	sqlDefault := table.RenderLocalTableCreateSQL()
	assertContains(t, sqlDefault, "`ts` DateTime64(9)")
	assertNotContains(t, sqlDefault, "'Asia/Shanghai'")

	// 自定义时区
	table.Timezone = "UTC"
	sqlUTC := table.RenderLocalTableCreateSQL()
	assertContains(t, sqlUTC, "'UTC'")
	assertNotContains(t, sqlUTC, "'Asia/Shanghai'")
}

// ─── 层1：APM Spans 列定义验证 ───

// TestAPMSpansColumns 验证 apmSpansColumns 构造的列数量和关键列类型
// 在 schema 包测试中独立构造同等列定义，验证列类型和 Insert 渲染。
func TestAPMSpansColumns(t *testing.T) {
	columns := buildAPMSpansColumns()

	// 顶层列总数 = 22（包含 retained_by、events、links；Nested 不在这里展开）
	expectedCount := 22
	if len(columns) != expectedCount {
		t.Errorf("期望 %d 个顶层列，实际: %d", expectedCount, len(columns))
	}

	// 验证关键列类型
	colMap := make(map[string]*schema.Column)
	for _, c := range columns {
		colMap[c.Name] = c
	}

	assertColumnType(t, colMap, "timestamp", schema.DateTime64ns)
	assertColumnType(t, colMap, "trace_id", schema.FixedString)
	assertColumnType(t, colMap, "duration", schema.UInt64)
	assertColumnType(t, colMap, "resource_attributes", schema.JSON)
	assertColumnType(t, colMap, "env", schema.LowCardinalityString)
	assertColumnType(t, colMap, "events", schema.Nested)
	assertColumnType(t, colMap, "links", schema.Nested)

	// 验证 events Nested 有 3 个子列
	eventsCol := colMap["events"]
	if eventsCol == nil {
		t.Fatal("events 列不存在")
	}
	if len(eventsCol.Nested) != 3 {
		t.Errorf("events Nested 期望 3 个子列，实际: %d", len(eventsCol.Nested))
	}
}

func TestInsertColumnNames_WithNestedColumns(t *testing.T) {
	table := buildAPMSpansTable()

	columns := table.InsertColumnNames()

	if len(columns) != 26 {
		t.Fatalf("InsertColumnNames() got %d columns, want 26", len(columns))
	}

	expected := []string{
		"timestamp",
		"trace_id",
		"span_id",
		"parent_span_id",
		"env",
		"service_name",
		"resource_name",
		"operation_name",
		"span_name",
		"span_kind",
		"status_code",
		"status_message",
		"exception_type",
		"exception_message",
		"duration",
		"resource_attributes",
		"span_attributes",
		"scope_name",
		"scope_version",
		"retained_by",
		"events.timestamp",
		"events.name",
		"events.attributes",
	}
	for i, want := range expected {
		if columns[i] != want {
			t.Fatalf("columns[%d] = %q, want %q", i, columns[i], want)
		}
	}

	lastExpected := []string{"links.trace_id", "links.span_id", "links.attributes"}
	gotLast := columns[len(columns)-3:]
	for i, want := range lastExpected {
		if gotLast[i] != want {
			t.Fatalf("tail[%d] = %q, want %q", i, gotLast[i], want)
		}
	}
}

func TestRenderPrepareTableInsertSQL_UsesInsertColumnNames(t *testing.T) {
	table := buildAPMSpansTable()

	sql := table.RenderPrepareTableInsertSQL()
	columns := table.InsertColumnNames()

	assertContains(t, sql, "INSERT INTO apm.`spans`")
	assertContains(t, sql, strings.Join(columns, ","))

	placeholderCount := strings.Count(sql, "?")
	if placeholderCount != len(columns) {
		t.Fatalf("placeholder count = %d, want %d", placeholderCount, len(columns))
	}
}

func TestRenderExistsSQL(t *testing.T) {
	table := buildAPMSpansTable()

	if got, want := table.RenderExistsSQL(), "EXISTS TABLE apm.`spans`"; got != want {
		t.Fatalf("RenderExistsSQL() = %q, want %q", got, want)
	}
}

// ─── 辅助函数 ───

func buildAPMSpansTable() *schema.Table {
	return &schema.Table{
		Database:      "apm",
		DBType:        schema.CKDBTypeClickhouse,
		Timezone:      "Asia/Shanghai",
		LocalName:     "spans",
		Engine:        schema.TableEngine{Name: schema.EngineTypeMergeTree},
		TTL:           24 * time.Hour,
		TTLFunc:       schema.TimeFuncDateTime,
		PartitionFunc: schema.TimeFuncDate,
		OrderKeys: []string{
			"env",
			"service_name",
			"operation_name",
			"resource_name",
			"toUnixTimestamp(timestamp)",
			"duration",
		},
		PrimaryKeyCount: 6,
		Columns:         buildAPMSpansColumns(),
		Settings: []string{
			"allow_part_offset_column_in_projections = 1",
		},
	}
}

func buildAPMSpansColumns() []*schema.Column {
	return []*schema.Column{
		schema.NewColumn("timestamp", schema.DateTime64ns).
			SetTimeFiled().
			SetCodecs(schema.CodecDelta, schema.CodecZSTD),
		schema.NewColumn("trace_id", schema.FixedString).
			SetProjection().
			SetTypeArgs("32").
			SetCodec(schema.CodecZSTD),
		schema.NewColumn("span_id", schema.FixedString).
			SetTypeArgs("16").
			SetCodec(schema.CodecZSTD),
		schema.NewColumn("parent_span_id", schema.FixedString).
			SetTypeArgs("16").
			SetCodec(schema.CodecZSTD),

		schema.NewColumn("env", schema.LowCardinalityString),
		schema.NewColumn("service_name", schema.LowCardinalityString),
		schema.NewColumn("resource_name", schema.LowCardinalityString),
		schema.NewColumn("operation_name", schema.LowCardinalityString),

		schema.NewColumn("span_name", schema.LowCardinalityString),
		schema.NewColumn("span_kind", schema.LowCardinalityString),
		schema.NewColumn("status_code", schema.LowCardinalityString),
		schema.NewColumn("status_message", schema.String),
		schema.NewColumn("exception_type", schema.LowCardinalityString).
			SetIndex(schema.IndexBloomfilter),
		schema.NewColumn("exception_message", schema.String),

		schema.NewColumn("duration", schema.UInt64).
			SetIndex(schema.IndexMinmax),
		schema.NewColumn("resource_attributes", schema.JSON).
			SetCodec(schema.CodecZSTD),
		schema.NewColumn("span_attributes", schema.JSON).
			SetCodec(schema.CodecZSTD),
		schema.NewColumn("scope_name", schema.String),
		schema.NewColumn("scope_version", schema.String),
		schema.NewColumn("retained_by", schema.LowCardinalityString).
			SetCodec(schema.CodecZSTD),

		schema.NewColumn("events", schema.Nested).SetNested([]*schema.Column{
			schema.NewColumn("timestamp", schema.DateTime64ns),
			schema.NewColumn("name", schema.LowCardinalityString),
			schema.NewColumn("attributes", schema.Map).
				SetTypeArgs("LowCardinality(String), String"),
		}).SetCodec(schema.CodecZSTD),

		schema.NewColumn("links", schema.Nested).SetNested([]*schema.Column{
			schema.NewColumn("trace_id", schema.FixedString).
				SetTypeArgs("32"),
			schema.NewColumn("span_id", schema.FixedString).
				SetTypeArgs("16"),
			schema.NewColumn("attributes", schema.Map).
				SetTypeArgs("LowCardinality(String), String"),
		}).SetCodec(schema.CodecZSTD),
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("期望包含 %q\n实际 SQL:\n%s", substr, s)
	}
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("不应包含 %q\n实际 SQL:\n%s", substr, s)
	}
}

func assertColumnType(t *testing.T, colMap map[string]*schema.Column, name string, expected schema.ColumnType) {
	t.Helper()
	col, ok := colMap[name]
	if !ok {
		t.Errorf("列 %q 不存在", name)
		return
	}
	if col.Type != expected {
		t.Errorf("列 %q 类型: 期望 %v，实际 %v", name, expected, col.Type)
	}
}

// TestRenderTableDropSQL pairs with RenderLocalTableCreateSQL: both take the
// name and its qualification from the same Table, so a migration's Down cannot
// drop something its Up never created. A hand-written DROP that named a
// different table would still return success, because IF EXISTS makes dropping
// nothing indistinguishable from dropping the right thing.
func TestRenderTableDropSQL(t *testing.T) {
	table := &schema.Table{
		Database:  "apm",
		LocalName: "retained_spans",
	}

	if got, want := table.RenderDropSQL(), "DROP TABLE IF EXISTS apm.`retained_spans`"; got != want {
		t.Errorf("RenderDropSQL() = %q, want %q", got, want)
	}

	// The created name and the dropped name have to be the same identifier.
	created := table.RenderLocalTableCreateSQL()
	if !strings.Contains(created, "apm.`retained_spans`") {
		t.Errorf("create SQL does not name the table the way drop does: %s", created)
	}
}

// Generic schemas must not inject a host timezone when none is declared.
func TestTemporalTypesWithoutTimezonePolicy(t *testing.T) {
	for _, tc := range []struct {
		kind     schema.ColumnType
		plain    string
		explicit string
	}{
		{schema.DateTime, "DateTime", "DateTime('UTC')"},
		{schema.DateTime64, "DateTime64(0)", "DateTime64(0, 'UTC')"},
		{schema.DateTime64ms, "DateTime64(3)", "DateTime64(3, 'UTC')"},
		{schema.DateTime64us, "DateTime64(6)", "DateTime64(6, 'UTC')"},
		{schema.DateTime64ns, "DateTime64(9)", "DateTime64(9, 'UTC')"},
		{schema.ArrayDateTime64ns, "Array(DateTime64(9))", "Array(DateTime64(9, 'UTC'))"},
	} {
		t.Run(tc.plain, func(t *testing.T) {
			if got := tc.kind.String(); got != tc.plain {
				t.Fatalf("String()=%q, want %q", got, tc.plain)
			}
			if got := tc.kind.StringWithTimezone(""); got != tc.plain {
				t.Fatalf("empty timezone=%q, want %q", got, tc.plain)
			}
			if got := tc.kind.StringWithTimezone("UTC"); got != tc.explicit {
				t.Fatalf("explicit timezone=%q, want %q", got, tc.explicit)
			}
		})
	}
}

func TestRenderTableUsesConnectionDatabaseWhenUnqualified(t *testing.T) {
	table := &schema.Table{
		LocalName:       "events",
		Columns:         []*schema.Column{schema.NewColumn("id", schema.UInt64)},
		Engine:          schema.TableEngine{Name: schema.EngineTypeMergeTree},
		OrderKeys:       []string{"id"},
		PrimaryKeyCount: 1,
	}
	unqualified := table.RenderLocalTableCreateSQL()
	assertContains(t, unqualified, "CREATE TABLE IF NOT EXISTS `events`")
	if got := table.RenderDropSQL(); got != "DROP TABLE IF EXISTS `events`" {
		t.Fatal(got)
	}
	table.Database = "analytics"
	qualified := table.RenderLocalTableCreateSQL()
	if qualified != strings.Replace(unqualified, "EXISTS `events`", "EXISTS analytics.`events`", 1) {
		t.Fatal("database qualification changed other DDL")
	}
	if got := table.RenderDropSQL(); got != "DROP TABLE IF EXISTS analytics.`events`" {
		t.Fatal(got)
	}
}
