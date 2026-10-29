package migrator

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/datafoxlabs/storekit/clickhouse/schema"
)

// ClickHouseDialect 实现 ClickHouse 数据库的 Dialect 接口
//
// 所有 ClickHouse DDL 生成统一委托 clickhouse/schema 包。
// 推荐的迁移方式：直接使用 schema.Table + mg.RawSQL()，
// 此处的 CreateTableSQL / AddColumnSQL 作为 Dialect 接口的兼容实现，
// 将通用的 migrator.Table/Column 转换为 schema 类型后生成 SQL。
type ClickHouseDialect struct {
	// Timezone is supplied by the host. Empty uses the server timezone.
	Timezone string
}

// 编译时验证接口实现
var _ Dialect = (*ClickHouseDialect)(nil)

func (d *ClickHouseDialect) Name() string {
	return "clickhouse"
}

func (d *ClickHouseDialect) SupportsDropColumn() bool {
	return true
}

func (d *ClickHouseDialect) SupportsTransactions() bool {
	return false
}

func (d *ClickHouseDialect) RequiresTableRebuild(operation string) bool {
	switch operation {
	case "ALTER_COLUMN_TYPE":
		return true
	default:
		return false
	}
}

func (d *ClickHouseDialect) CreateTableSQL(table *Table) ([]string, error) {
	if err := table.Validate(); err != nil {
		return nil, fmt.Errorf("invalid table definition: %w", err)
	}

	schemaTable := d.convertTable(table)
	return []string{schemaTable.RenderLocalTableCreateSQL()}, nil
}

func (d *ClickHouseDialect) AddColumnSQL(tableName string, col *Column) ([]string, error) {
	if err := col.Validate(); err != nil {
		return nil, err
	}

	schemaCol := d.convertColumn(col)
	// StringWithTimezone 输出格式: `name` Type CODEC(...)
	colDef := schemaCol.StringWithTimezone(d.Timezone)

	sql := fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s", tableName, colDef)

	if col.Default != "" {
		sql += fmt.Sprintf(" DEFAULT %s", col.Default)
	}

	return []string{sql}, nil
}

func (d *ClickHouseDialect) DropColumnSQL(tableName, columnName string) ([]string, error) {
	sql := fmt.Sprintf("ALTER TABLE %s DROP COLUMN IF EXISTS %s",
		tableName, columnName)
	return []string{sql}, nil
}

func (d *ClickHouseDialect) AlterColumnSQL(_ string, _ *Column) ([]string, error) {
	return nil, fmt.Errorf("ClickHouse does not support ALTER COLUMN TYPE directly, use DROP + ADD instead")
}

func (d *ClickHouseDialect) RenameColumnSQL(tableName, oldName, newName string) ([]string, error) {
	sql := fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s",
		tableName, oldName, newName)
	return []string{sql}, nil
}

func (d *ClickHouseDialect) AddIndexSQL(_ string, _ *Index) ([]string, error) {
	return nil, fmt.Errorf("ClickHouse indexes must be defined during table creation, cannot add index after table exists")
}

func (d *ClickHouseDialect) DropIndexSQL(_, _ string) ([]string, error) {
	return nil, fmt.Errorf("ClickHouse does not support DROP INDEX")
}

func (d *ClickHouseDialect) RebuildTable(_ context.Context, _ *gorm.DB, _ string, _ *Table) error {
	return fmt.Errorf("table rebuild not implemented for ClickHouse yet")
}

// ---------------------------------------------------------------------------
// migrator.Table/Column → schema.Table/Column 转换
// ---------------------------------------------------------------------------

// mapColumnType 将通用 migrator.ColumnType 映射到 schema.ColumnType
func mapColumnType(colType ColumnType) schema.ColumnType {
	switch colType {
	case TypeBigInt:
		return schema.Int64
	case TypeInt:
		return schema.Int32
	case TypeVarchar:
		return schema.String
	case TypeText:
		return schema.String
	case TypeTimestamp:
		return schema.DateTime
	case TypeBoolean:
		return schema.UInt8
	case TypeBytea:
		return schema.String
	default:
		return schema.String
	}
}

// convertColumn 将 migrator.Column 转换为 schema.Column
func (d *ClickHouseDialect) convertColumn(col *Column) *schema.Column {
	return schema.NewColumn(col.Name, mapColumnType(col.Type))
}

// convertTable 将 migrator.Table 转换为 schema.Table
//
// 用于兼容通过 mg.CreateTable(&migrator.Table{...}) 创建简单 ClickHouse 表的场景。
// 复杂表（Nested、JSON、自定义 ORDER BY 表达式等）应直接使用 schema.Table + mg.RawSQL()。
func (d *ClickHouseDialect) convertTable(table *Table) *schema.Table {
	// 转换列
	columns := make([]*schema.Column, 0, len(table.Columns))
	for _, col := range table.Columns {
		columns = append(columns, d.convertColumn(col))
	}

	// 提取主键列名作为 OrderKeys
	pkColumns := table.GetPrimaryKeyColumns()
	orderKeys := make([]string, 0, len(pkColumns))
	for _, pk := range pkColumns {
		orderKeys = append(orderKeys, pk.Name)
	}
	// 没有主键时 schema.Table 仍需要 OrderKeys 为空切片，
	// RenderLocalTableCreateSQL 会生成 ORDER BY tuple()
	if len(orderKeys) == 0 {
		orderKeys = []string{"tuple()"}
	}

	return &schema.Table{
		Database:        "",
		Timezone:        d.Timezone,
		DBType:          schema.CKDBTypeClickhouse,
		LocalName:       table.Name,
		Engine:          schema.TableEngine{Name: schema.EngineTypeMergeTree},
		Columns:         columns,
		OrderKeys:       orderKeys,
		PrimaryKeyCount: len(orderKeys),
	}
}
