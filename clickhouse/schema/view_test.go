package schema_test

import (
	"strings"
	"testing"

	"github.com/datafoxlabs/storekit/clickhouse/schema"
)

func TestRenderCreateMaterializedView(t *testing.T) {
	mv := &schema.MaterializedView{
		Database:    "apm",
		Name:        "trace_metrics_15s_mv",
		ToTable:     "trace_metrics_15s",
		SourceTable: "spans",
		IfNotExists: true,
		SelectExprs: []string{
			"toStartOfInterval(timestamp, INTERVAL 15 SECOND) AS time_bucket",
			"env",
			"count() AS hits",
		},
		Where: "span_kind = 'Server'",
		GroupBy: []string{
			"time_bucket",
			"env",
		},
	}

	sql := mv.RenderCreateSQL()

	assertContains(t, sql, "CREATE MATERIALIZED VIEW IF NOT EXISTS apm.`trace_metrics_15s_mv`")
	assertContains(t, sql, "TO apm.`trace_metrics_15s`")
	assertContains(t, sql, "FROM apm.`spans`")
	assertContains(t, sql, "WHERE span_kind = 'Server'")
	assertContains(t, sql, "GROUP BY\n    time_bucket,\n    env")
}

// TestRenderRefreshableMaterializedViewAppendsByDefault covers the one way this
// type can destroy data. A refreshable view with a TO target and no APPEND does
// not add rows on each refresh -- it atomically replaces the whole target table,
// discarding whatever other writers put there, with no error and no warning.
// The zero value must therefore append, so that forgetting a field is safe and
// only an explicit ReplaceTarget can wipe the table.
func TestRenderRefreshableMaterializedViewAppendsByDefault(t *testing.T) {
	mv := &schema.MaterializedView{
		Database:    "apm",
		Name:        "retained_spans_context_mv",
		ToTable:     "retained_spans",
		SourceTable: "spans",
		IfNotExists: true,
		SelectExprs: []string{"timestamp", "trace_id"},
		Refresh:     &schema.RefreshSchedule{Every: "1 HOUR"},
	}

	sql := mv.RenderCreateSQL()

	assertContains(t, sql, "REFRESH EVERY 1 HOUR APPEND")
	// ClickHouse fixes the clause order: the refresh clause sits between the
	// view name and TO, with APPEND last before TO.
	assertContains(t, sql, "apm.`retained_spans_context_mv`\nREFRESH EVERY 1 HOUR APPEND\nTO apm.`retained_spans`")

	if err := mv.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestRenderRefreshableMaterializedViewOffset(t *testing.T) {
	mv := &schema.MaterializedView{
		Database:    "apm",
		Name:        "mv",
		ToTable:     "dst",
		SourceTable: "src",
		SelectExprs: []string{"timestamp"},
		Refresh:     &schema.RefreshSchedule{Every: "1 HOUR", Offset: "30 MINUTE"},
	}

	assertContains(t, mv.RenderCreateSQL(), "REFRESH EVERY 1 HOUR OFFSET 30 MINUTE APPEND")
}

// TestMaterializedViewValidateRejectsReplacingATarget makes the destructive form
// unreachable through the migration path, which calls Validate before rendering.
// Rendering it is still possible for a view that genuinely owns its whole target.
func TestMaterializedViewValidateRejectsReplacingATarget(t *testing.T) {
	mv := &schema.MaterializedView{
		Database:    "apm",
		Name:        "retained_spans_context_mv",
		ToTable:     "retained_spans",
		SourceTable: "spans",
		SelectExprs: []string{"timestamp"},
		Refresh:     &schema.RefreshSchedule{Every: "1 HOUR", ReplaceTarget: true},
	}

	err := mv.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error: ReplaceTarget discards the target table")
	}
	assertContains(t, err.Error(), "retained_spans")

	// The rendered SQL must actually lack APPEND, or the guard is protecting
	// against something that could not happen.
	if sql := mv.RenderCreateSQL(); strings.Contains(sql, "APPEND") {
		t.Errorf("ReplaceTarget still rendered APPEND: %s", sql)
	}
}

func TestMaterializedViewValidateRequiresInterval(t *testing.T) {
	mv := &schema.MaterializedView{
		Name:    "mv",
		Refresh: &schema.RefreshSchedule{},
	}

	if err := mv.Validate(); err == nil {
		t.Fatal("Validate() = nil, want an error: refresh schedule has no interval")
	}
}

// TestRenderMaterializedViewWithoutRefreshUnchanged pins the insert-triggered
// form. Adding the refresh clause must not alter the DDL of the views already
// deployed, so this is byte-for-byte rather than a contains check.
func TestRenderMaterializedViewWithoutRefreshUnchanged(t *testing.T) {
	mv := &schema.MaterializedView{
		Database:    "apm",
		Name:        "mv",
		ToTable:     "dst",
		SourceTable: "src",
		IfNotExists: true,
		SelectExprs: []string{"timestamp", "trace_id"},
		Where:       "duration > 1",
	}

	const want = "CREATE MATERIALIZED VIEW IF NOT EXISTS apm.`mv`\n" +
		"TO apm.`dst`\n" +
		"AS SELECT\n    timestamp,\n    trace_id\n" +
		"FROM apm.`src`\n" +
		"WHERE duration > 1"

	if got := mv.RenderCreateSQL(); got != want {
		t.Errorf("RenderCreateSQL()\n got: %s\nwant: %s", got, want)
	}
	if err := mv.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for a non-refreshable view", err)
	}
}

func TestRenderDropMaterializedView(t *testing.T) {
	mv := &schema.MaterializedView{
		Database: "apm",
		Name:     "trace_metrics_15s_mv",
	}

	sql := mv.RenderDropSQL()
	assertContains(t, sql, "DROP TABLE IF EXISTS apm.`trace_metrics_15s_mv`")
}
