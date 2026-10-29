package schema

import (
	"fmt"
	"strings"
	"time"
)

const (
	// GlobalSuffix 全局（Distributed）表名后缀
	GlobalSuffix = "_all"
)

// Table ClickHouse 表定义
type Table struct {
	Version         string             // 表版本，用于表结构变更时做自动更新
	ID              uint8              // id
	Database        string             // 所属数据库名
	DBType          string             // clickhouse or byconity
	Timezone        string             // 显式时区；为空时省略 SQL 时区参数，由 ClickHouse 决定
	LocalName       string             // 本地表名
	GlobalName      string             // 全局表名
	Global          bool               // 是否创建 Distributed 表
	SourceName      string             // 物化视图源表
	Columns         []*Column          // 表列结构
	SummingKey      string             // SummingMergeTree 聚合 key
	TTL             time.Duration      // 数据保留时长
	TTLFunc         TimeFuncType       // TTL 函数类型
	ColdStorage     ColdStorage        // 冷存储配置
	PartitionFunc   TimeFuncType       // Partition 函数类型
	Cluster         string             // 集群名
	StoragePolicy   string             // 存储策略
	Engine          TableEngine        // 表引擎
	OrderKeys       []string           // 排序 key
	PrimaryKeyCount int                // 一级索引 key 个数（从 OrderKeys 前 n 个取）
	Constraints     []*TableConstraint // 表级 CHECK 约束
	Indexes         []*TableIndex      // 表级索引，支持表达式索引
	Settings        []string           // 额外 SETTINGS 项
}

// TableConstraint defines a named ClickHouse table-level CHECK constraint.
type TableConstraint struct {
	Name       string
	Expression string
}

// String renders the constraint body used by CREATE TABLE and ALTER TABLE.
func (c *TableConstraint) String() string {
	return fmt.Sprintf("CONSTRAINT %s CHECK %s", c.Name, c.Expression)
}

// TableIndex ClickHouse 表级索引定义，可用于函数/表达式索引。
type TableIndex struct {
	Name        string
	Expression  string
	Type        string
	Granularity int
}

// makeLocalTableCreateSQL 生成本地表的 CREATE TABLE SQL
func (t *Table) makeLocalTableCreateSQL(database string) string {
	tz := t.Timezone

	var (
		columns  []string
		settings = []string{
			"index_granularity=8192",
			"ttl_only_drop_parts = 1",
		}
		timeFiled string
	)

	for _, c := range t.Columns {
		if c.IsTimeFiled() {
			timeFiled = c.Name
			c.Comment = t.Version
		}
		if c.Type == Nested {
			var nestedColumns []string
			for _, nc := range c.Nested {
				// nested 内部不设 codec
				nc.Codec = CodecDefault
				nestedColumns = append(nestedColumns, nc.StringWithTimezone(tz))
				if nc.Index != IndexNone {
					columns = append(columns, renderTableIndex(&TableIndex{
						Name:        fmt.Sprintf("idx_%s_%s", c.Name, nc.Name),
						Expression:  fmt.Sprintf("%s.%s", c.Name, nc.Name),
						Type:        nc.Index.String(),
						Granularity: 1,
					}))
				}
			}
			c.SetTypeArgs(strings.Join(nestedColumns, ",\n"))
		}

		columns = append(columns, c.StringWithTimezone(tz))

		if c.Index != IndexNone {
			columns = append(columns, renderTableIndex(&TableIndex{
				Name:        fmt.Sprintf("idx_%s", c.Name),
				Expression:  c.Name,
				Type:        c.Index.String(),
				Granularity: 1,
			}))
		}

		if c.MapIndex != IndexNone {
			for _, accessor := range []string{"Keys", "Values"} {
				columns = append(columns, renderTableIndex(&TableIndex{
					Name:        fmt.Sprintf("idx_%s_%s", c.Name, strings.ToLower(accessor)),
					Expression:  fmt.Sprintf("map%s(%s)", accessor, c.Name),
					Type:        c.MapIndex.String(),
					Granularity: 1,
				}))
			}
		}

		if c.Projection {
			columns = append(columns, fmt.Sprintf("PROJECTION projection_by_%s ( SELECT _part_offset ORDER BY %s ) ", c.Name, c.Name))
		}
	}

	for _, constraint := range t.Constraints {
		columns = append(columns, constraint.String())
	}

	for _, idx := range t.Indexes {
		columns = append(columns, renderTableIndex(idx))
	}

	partition := ""
	if t.PartitionFunc != TimeFuncNone {
		partition = fmt.Sprintf("PARTITION BY %s", t.PartitionFunc.String(timeFiled))
	}

	ttl := genTTLExpr(t.TTL, t.TTLFunc.String(timeFiled))

	if t.StoragePolicy != "" {
		settings = append(settings, fmt.Sprintf("storage_policy = '%s'", t.StoragePolicy))
	}
	if len(t.Settings) > 0 {
		settings = append(settings, t.Settings...)
	}

	name := fmt.Sprintf("`%s`", t.LocalName)
	if database != "" {
		name = database + "." + name
	}
	createTable := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s
(%s)
ENGINE = %s
PRIMARY KEY (%s)
ORDER BY (%s)
%s
%s
SETTINGS %s`,
		name,
		strings.Join(columns, ",\n"),
		t.Engine.String(),
		strings.Join(t.OrderKeys[:t.PrimaryKeyCount], ", "),
		strings.Join(t.OrderKeys, ", "),
		partition,
		ttl,
		strings.Join(settings, ", "))
	return createTable
}

// genTTLExpr 生成 TTL 表达式
func genTTLExpr(ttl time.Duration, timeField string) string {
	if ttl > 0 {
		switch {
		case ttl%(24*time.Hour) == 0:
			return fmt.Sprintf(`TTL %s + toIntervalDay(%d)`, timeField, ttl/(24*time.Hour))
		case ttl%(time.Hour) == 0:
			return fmt.Sprintf(`TTL %s + toIntervalHour(%d)`, timeField, ttl/time.Hour)
		case ttl%(time.Minute) == 0:
			return fmt.Sprintf(`TTL %s + toIntervalMinute(%d)`, timeField, ttl/time.Minute)
		default:
			return fmt.Sprintf(`TTL %s + toIntervalSecond(%d)`, timeField, ttl/time.Second)
		}
	}
	return ""
}

// FullLocalName 返回完整的本地表名（database.table）
func (t *Table) FullLocalName() string {
	return t.Database + "." + t.LocalName
}

// FullSourceName 返回完整的源表名
func (t *Table) FullSourceName() string {
	return t.Database + "." + t.SourceName
}

// RenderLocalTableCreateSQL 渲染本地表建表 SQL
func (t *Table) RenderLocalTableCreateSQL() string {
	if t.DBType == CKDBTypeByconity {
		return "SELECT VERSION()"
	}
	return t.makeLocalTableCreateSQL(t.Database)
}

// RenderDropSQL 渲染本地表的 DROP TABLE SQL，与 RenderLocalTableCreateSQL 对应。
//
// Migration 的 Down 用这个而不是自己拼串，理由和 Up 一样：表名和限定方式只在一个
// 地方决定。手写的 DROP 与建表用的名字来自不同来源时，Down 会静默失效 —— DROP 的
// 是一张不存在的表，IF EXISTS 让它照样返回成功。
func (t *Table) RenderDropSQL() string {
	return fmt.Sprintf("DROP TABLE IF EXISTS %s", qualifyObjectName(t.Database, t.LocalName))
}

// RenderGlobalTableCreateSQL 渲染全局（Distributed）表建表 SQL
func (t *Table) RenderGlobalTableCreateSQL() string {
	if t.DBType == CKDBTypeByconity {
		t.LocalName = t.GlobalName
		return t.makeLocalTableCreateSQL(t.Database)
	}
	distributedTableEngine := TableEngine{
		Name:   EngineTypeDistributed,
		Params: fmt.Sprintf("'%s', '%s', '%s', rand()", t.Cluster, t.Database, t.LocalName),
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.`%s` AS %s.`%s` ENGINE=%s",
		t.Database, t.GlobalName, t.Database, t.LocalName, distributedTableEngine.String())
}

// RenderPrepareTableInsertSQL 渲染 INSERT 预编译 SQL
func (t *Table) RenderPrepareTableInsertSQL() string {
	var values []string
	if t.DBType == CKDBTypeByconity {
		t.LocalName = t.GlobalName
	}

	columns := t.InsertColumnNames()
	for range columns {
		values = append(values, "?")
	}

	prepare := fmt.Sprintf("INSERT INTO %s.`%s` (%s) VALUES (%s)",
		t.Database, t.LocalName,
		strings.Join(columns, ","),
		strings.Join(values, ","))

	return prepare
}

// InsertColumnNames 返回 INSERT 语句使用的列顺序。
// Nested 列会展开为 parent.child 形式，顺序与 RenderPrepareTableInsertSQL 保持一致。
func (t *Table) InsertColumnNames() []string {
	columns := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		if c.MaterializedExpr != "" {
			continue
		}
		if len(c.Nested) > 0 {
			for _, nc := range c.Nested {
				columns = append(columns, c.Name+"."+nc.Name)
			}
			continue
		}
		columns = append(columns, c.Name)
	}
	return columns
}

// RenderExistsSQL 渲染 ClickHouse 表存在性检查 SQL。
func (t *Table) RenderExistsSQL() string {
	return fmt.Sprintf("EXISTS TABLE %s.`%s`", t.Database, t.LocalName)
}

func renderTableIndex(idx *TableIndex) string {
	granularity := idx.Granularity
	if granularity <= 0 {
		granularity = 1
	}
	return fmt.Sprintf("INDEX %s (%s) TYPE %s GRANULARITY %d",
		idx.Name, idx.Expression, idx.Type, granularity)
}

// ColdStorage 冷存储配置
type ColdStorage struct {
	Enabled   bool
	Type      DiskType
	Name      string
	TTLToMove int // 多少小时后移到冷存储
}

// GetColdStorage 从配置 map 中获取冷存储配置
func GetColdStorage(coldStorages map[string]*ColdStorage, db, table string) *ColdStorage {
	if coldStorage, ok := coldStorages[db+table]; ok {
		return coldStorage
	}

	if coldStorage, ok := coldStorages[db]; ok {
		return coldStorage
	}
	return &ColdStorage{}
}
