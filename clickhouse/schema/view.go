package schema

import (
	"fmt"
	"strings"
)

// MaterializedView ClickHouse 物化视图定义
type MaterializedView struct {
	Database    string           // 所属数据库
	Name        string           // 物化视图名
	ToTable     string           // TO 目标表
	SourceTable string           // FROM 源表
	SourceJoin  string           // 紧跟 FROM 的 JOIN 子句，整段原样渲染
	IfNotExists bool             // 是否添加 IF NOT EXISTS
	SelectExprs []string         // SELECT 列表达式
	Where       string           // WHERE 条件
	GroupBy     []string         // GROUP BY 表达式
	Refresh     *RefreshSchedule // 非 nil 时渲染为 refreshable 物化视图
}

// RefreshSchedule turns a materialized view from insert-triggered into
// timer-driven: ClickHouse re-runs the whole SELECT on a schedule rather than
// against each inserted block.
//
// The zero value appends, and that is deliberate. A refreshable view with a TO
// target and no APPEND replaces the entire target table on every refresh --
// atomically, silently, and including every row other writers put there. Making
// the safe form the default means forgetting a field cannot destroy data; the
// destructive form has to be asked for by name.
type RefreshSchedule struct {
	// Every is the REFRESH EVERY interval, e.g. "1 HOUR". Required.
	Every string
	// Offset shifts the schedule within its period, e.g. "30 MINUTE". Optional.
	Offset string
	// ReplaceTarget drops APPEND, so each refresh replaces the whole target
	// table. Correct only when this view is the sole writer and the SELECT
	// reproduces the complete desired contents every time.
	ReplaceTarget bool
}

func (r *RefreshSchedule) render() string {
	clause := "REFRESH EVERY " + r.Every
	if r.Offset != "" {
		clause += " OFFSET " + r.Offset
	}
	if !r.ReplaceTarget {
		clause += " APPEND"
	}
	return clause
}

// Validate reports configurations ClickHouse would reject or silently misread.
// Callers that build DDL from user-facing config should run it before rendering.
func (mv *MaterializedView) Validate() error {
	if mv.Refresh == nil {
		return nil
	}
	if mv.Refresh.Every == "" {
		return fmt.Errorf("materialized view %q: refresh schedule needs an EVERY interval", mv.Name)
	}
	if mv.Refresh.ReplaceTarget && mv.ToTable != "" {
		return fmt.Errorf(
			"materialized view %q: ReplaceTarget wipes %q on every refresh; "+
				"leave it false unless this view owns the whole table",
			mv.Name, mv.ToTable)
	}
	return nil
}

func qualifyObjectName(database, name string) string {
	if strings.Contains(name, ".") {
		return name
	}
	if database == "" {
		return fmt.Sprintf("`%s`", name)
	}
	return fmt.Sprintf("%s.`%s`", database, name)
}

// RenderCreateSQL 渲染 CREATE MATERIALIZED VIEW SQL
func (mv *MaterializedView) RenderCreateSQL() string {
	ifNotExists := ""
	if mv.IfNotExists {
		ifNotExists = " IF NOT EXISTS"
	}

	// ClickHouse fixes this order: the REFRESH clause sits between the view name
	// and TO, and APPEND is the last thing before TO.
	refresh := ""
	if mv.Refresh != nil {
		refresh = "\n" + mv.Refresh.render()
	}

	sql := fmt.Sprintf(
		"CREATE MATERIALIZED VIEW%s %s%s\nTO %s\nAS SELECT\n    %s\nFROM %s",
		ifNotExists,
		qualifyObjectName(mv.Database, mv.Name),
		refresh,
		qualifyObjectName(mv.Database, mv.ToTable),
		strings.Join(mv.SelectExprs, ",\n    "),
		qualifyObjectName(mv.Database, mv.SourceTable),
	)

	// A join belongs to the FROM, so it renders before WHERE. Joining a small
	// configuration table is how a view with fixed SQL reads settings that
	// change; the joined side must alias its columns away from the source's, or
	// every ambiguous name in the projection would need qualifying too.
	if mv.SourceJoin != "" {
		sql += "\n" + mv.SourceJoin
	}

	if mv.Where != "" {
		sql += "\nWHERE " + mv.Where
	}
	if len(mv.GroupBy) > 0 {
		sql += "\nGROUP BY\n    " + strings.Join(mv.GroupBy, ",\n    ")
	}

	return sql
}

// RenderDropSQL 渲染 DROP MATERIALIZED VIEW 对应的 DROP TABLE SQL
func (mv *MaterializedView) RenderDropSQL() string {
	return fmt.Sprintf("DROP TABLE IF EXISTS %s", qualifyObjectName(mv.Database, mv.Name))
}
