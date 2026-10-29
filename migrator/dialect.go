package migrator

import (
	"context"

	"gorm.io/gorm"
)

// Dialect 数据库方言抽象接口
type Dialect interface {
	Name() string
	SupportsDropColumn() bool
	SupportsTransactions() bool
	RequiresTableRebuild(operation string) bool
	CreateTableSQL(table *Table) ([]string, error)
	AddColumnSQL(tableName string, col *Column) ([]string, error)
	DropColumnSQL(tableName, columnName string) ([]string, error)
	AlterColumnSQL(tableName string, col *Column) ([]string, error)
	RenameColumnSQL(tableName, oldName, newName string) ([]string, error)
	AddIndexSQL(tableName string, idx *Index) ([]string, error)
	DropIndexSQL(tableName, indexName string) ([]string, error)
	RebuildTable(ctx context.Context, db *gorm.DB, tableName string, newSchema *Table) error
}

// NewDialect 根据 GORM DB 创建方言实例
func NewDialect(db *gorm.DB) Dialect {
	dialectName := db.Dialector.Name()
	switch dialectName {
	case "postgres":
		return &PostgresDialect{}
	case "sqlite":
		return &SQLiteDialect{}
	case "clickhouse":
		return &ClickHouseDialect{}
	case "mysql":
		return &MySQLDialect{}
	default:
		panic("unsupported dialect: " + dialectName)
	}
}
