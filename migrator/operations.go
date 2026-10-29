package migrator

import (
	"fmt"

	"gorm.io/gorm"
)

// ColumnType 列类型枚举
type ColumnType string

const (
	TypeBigInt    ColumnType = "BIGINT"
	TypeInt       ColumnType = "INT"
	TypeVarchar   ColumnType = "VARCHAR"
	TypeText      ColumnType = "TEXT"
	TypeTimestamp ColumnType = "TIMESTAMP"
	TypeBoolean   ColumnType = "BOOLEAN"
	TypeBytea     ColumnType = "BYTEA" // PostgreSQL: BYTEA, SQLite: BLOB
)

// Column 列定义
type Column struct {
	Name          string
	Type          ColumnType
	Size          int // VARCHAR size
	NotNull       bool
	PrimaryKey    bool
	AutoIncrement bool
	Unsigned      bool
	Default       string
	OnUpdate      string
	Unique        bool
	Comment       string
}

// Table 表定义
type Table struct {
	Name           string
	Columns        []*Column
	Indexes        []*Index
	PrimaryKeyName string
	Engine         string
	Charset        string
	Collation      string
	Comment        string
}

// Index 索引定义
type Index struct {
	Name    string
	Columns []string
	Unique  bool
}

// ToGORMColumnType 转换为 GORM 列类型
func (c *Column) ToGORMColumnType(dialect string) string {
	switch c.Type {
	case TypeBigInt:
		if dialect == "sqlite" {
			return "INTEGER"
		}
		return "BIGINT"
	case TypeInt:
		return "INTEGER"
	case TypeVarchar:
		if c.Size > 0 {
			return fmt.Sprintf("VARCHAR(%d)", c.Size)
		}
		return "VARCHAR(255)"
	case TypeText:
		return "TEXT"
	case TypeTimestamp:
		if dialect == "sqlite" {
			return "DATETIME"
		}
		return "TIMESTAMP"
	case TypeBoolean:
		if dialect == "sqlite" {
			return "INTEGER"
		}
		if dialect == "mysql" {
			return "TINYINT(1)"
		}
		return "BOOLEAN"
	case TypeBytea:
		if dialect == "sqlite" {
			return "BLOB"
		}
		if dialect == "mysql" {
			return "BLOB"
		}
		return "BYTEA"
	default:
		return string(c.Type)
	}
}

// ToGORMModel 转换为 GORM 模型（用于 AutoMigrate）
func (t *Table) ToGORMModel() interface{} {
	return nil
}

// Validate 验证列定义
func (c *Column) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("column name cannot be empty")
	}
	if c.Type == "" {
		return fmt.Errorf("column type cannot be empty for column %s", c.Name)
	}
	if c.Type == TypeVarchar && c.Size == 0 {
		c.Size = 255
	}
	return nil
}

// Validate 验证表定义
func (t *Table) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("table name cannot be empty")
	}
	if len(t.Columns) == 0 {
		return fmt.Errorf("table %s must have at least one column", t.Name)
	}
	for _, col := range t.Columns {
		if err := col.Validate(); err != nil {
			return fmt.Errorf("invalid column in table %s: %w", t.Name, err)
		}
	}
	return nil
}

// GetPrimaryKeyColumns 获取主键列
func (t *Table) GetPrimaryKeyColumns() []*Column {
	var pks []*Column
	for _, col := range t.Columns {
		if col.PrimaryKey {
			pks = append(pks, col)
		}
	}
	return pks
}

// HasColumn 检查表是否包含指定列
func (t *Table) HasColumn(columnName string) bool {
	for _, col := range t.Columns {
		if col.Name == columnName {
			return true
		}
	}
	return false
}

// GetColumn 获取指定列
func (t *Table) GetColumn(columnName string) *Column {
	for _, col := range t.Columns {
		if col.Name == columnName {
			return col
		}
	}
	return nil
}

// CloneWithoutColumn 复制表结构但排除指定列（用于 SQLite 表重建）
func (t *Table) CloneWithoutColumn(columnName string) *Table {
	newTable := &Table{
		Name:           t.Name,
		Columns:        make([]*Column, 0, len(t.Columns)-1),
		Indexes:        make([]*Index, 0, len(t.Indexes)),
		PrimaryKeyName: t.PrimaryKeyName,
		Engine:         t.Engine,
		Charset:        t.Charset,
		Collation:      t.Collation,
		Comment:        t.Comment,
	}
	for _, col := range t.Columns {
		if col.Name != columnName {
			newTable.Columns = append(newTable.Columns, col)
		}
	}
	// 复制索引，但需要过滤包含被删除列的索引
	for _, idx := range t.Indexes {
		hasColumn := false
		for _, colName := range idx.Columns {
			if colName == columnName {
				hasColumn = true
				break
			}
		}
		if !hasColumn {
			newTable.Indexes = append(newTable.Indexes, idx)
		}
	}
	return newTable
}

// GetColumnNames 获取所有列名（用于 SQLite 表重建的 INSERT SELECT）
func (t *Table) GetColumnNames() []string {
	names := make([]string, 0, len(t.Columns))
	for _, col := range t.Columns {
		names = append(names, col.Name)
	}
	return names
}

// GetDialectName 从 GORM DB 获取方言名称
func GetDialectName(db *gorm.DB) string {
	return db.Dialector.Name()
}
