package migrator

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// SQLiteDialect SQLite 方言实现
type SQLiteDialect struct{}

func (d *SQLiteDialect) Name() string {
	return "sqlite"
}

func (d *SQLiteDialect) SupportsDropColumn() bool {
	return false
}

func (d *SQLiteDialect) SupportsTransactions() bool {
	return true
}

func (d *SQLiteDialect) RequiresTableRebuild(operation string) bool {
	switch operation {
	case "DROP_COLUMN", "ALTER_COLUMN", "RENAME_COLUMN":
		return true
	default:
		return false
	}
}

// CreateTableSQL 生成 CREATE TABLE 语句
func (d *SQLiteDialect) CreateTableSQL(table *Table) ([]string, error) {
	if err := table.Validate(); err != nil {
		return nil, err
	}

	var statements []string
	var columns []string
	var constraints []string

	for _, col := range table.Columns {
		colDef := d.buildColumnDefinition(col)
		columns = append(columns, colDef)
	}

	// 只有在主键不是 AutoIncrement 时才添加 PRIMARY KEY 约束
	// SQLite 的 AutoIncrement 列已经在 buildColumnDefinition 中声明了 PRIMARY KEY
	pks := table.GetPrimaryKeyColumns()
	if len(pks) > 0 {
		// 检查是否有 AutoIncrement 列
		hasAutoIncrement := false
		for _, pk := range pks {
			if pk.AutoIncrement {
				hasAutoIncrement = true
				break
			}
		}

		// 只有在没有 AutoIncrement 列时才添加表级 PRIMARY KEY 约束
		if !hasAutoIncrement {
			pkColumns := make([]string, 0, len(pks))
			for _, pk := range pks {
				pkColumns = append(pkColumns, pk.Name)
			}
			constraints = append(constraints, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(pkColumns, ", ")))
		}
	}

	createTableSQL := fmt.Sprintf("CREATE TABLE %s (\n  %s",
		table.Name,
		strings.Join(append(columns, constraints...), ",\n  "))
	createTableSQL += "\n)"
	statements = append(statements, createTableSQL)

	for _, idx := range table.Indexes {
		indexSQL, err := d.AddIndexSQL(table.Name, idx)
		if err != nil {
			return nil, fmt.Errorf("failed to generate index SQL: %w", err)
		}
		statements = append(statements, indexSQL...)
	}

	return statements, nil
}

func (d *SQLiteDialect) buildColumnDefinition(col *Column) string {
	parts := []string{col.Name}

	colType := col.ToGORMColumnType("sqlite")

	if col.AutoIncrement && col.PrimaryKey {
		parts = append(parts, "INTEGER", "PRIMARY KEY", "AUTOINCREMENT")
	} else {
		parts = append(parts, colType)

		if col.PrimaryKey {
			parts = append(parts, "PRIMARY KEY")
		}
	}

	if col.NotNull && !col.PrimaryKey {
		parts = append(parts, "NOT NULL")
	}

	if col.Unique && !col.PrimaryKey {
		parts = append(parts, "UNIQUE")
	}

	if col.Default != "" {
		parts = append(parts, "DEFAULT", col.Default)
	}

	return strings.Join(parts, " ")
}

func (d *SQLiteDialect) AddColumnSQL(tableName string, col *Column) ([]string, error) {
	if err := col.Validate(); err != nil {
		return nil, err
	}

	if col.PrimaryKey {
		return nil, fmt.Errorf("SQLite does not support adding PRIMARY KEY columns, use table rebuild")
	}

	if col.NotNull && col.Default == "" {
		return nil, fmt.Errorf("SQLite requires DEFAULT value for NOT NULL columns when adding")
	}

	colDef := d.buildColumnDefinition(col)
	sql := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", tableName, colDef)
	return []string{sql}, nil
}

func (d *SQLiteDialect) DropColumnSQL(tableName, columnName string) ([]string, error) {
	return nil, fmt.Errorf("SQLite does not support DROP COLUMN, use RebuildTable instead")
}

func (d *SQLiteDialect) AlterColumnSQL(tableName string, col *Column) ([]string, error) {
	return nil, fmt.Errorf("SQLite does not support ALTER COLUMN, use RebuildTable instead")
}

func (d *SQLiteDialect) RenameColumnSQL(tableName, oldName, newName string) ([]string, error) {
	sql := fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", tableName, oldName, newName)
	return []string{sql}, nil
}

func (d *SQLiteDialect) AddIndexSQL(tableName string, idx *Index) ([]string, error) {
	var indexType string
	if idx.Unique {
		indexType = "UNIQUE INDEX"
	} else {
		indexType = "INDEX"
	}

	sql := fmt.Sprintf("CREATE %s %s ON %s (%s)",
		indexType,
		idx.Name,
		tableName,
		strings.Join(idx.Columns, ", "))
	return []string{sql}, nil
}

func (d *SQLiteDialect) DropIndexSQL(tableName, indexName string) ([]string, error) {
	sql := fmt.Sprintf("DROP INDEX IF EXISTS %s", indexName)
	return []string{sql}, nil
}

// RebuildTable 重建表（用于不支持的 ALTER TABLE 操作）
func (d *SQLiteDialect) RebuildTable(ctx context.Context, db *gorm.DB, tableName string, newSchema *Table) error {
	if err := newSchema.Validate(); err != nil {
		return fmt.Errorf("invalid new schema: %w", err)
	}

	tempTableName := tableName + "_new"

	// 步骤 1: 创建新表
	createSQL, err := d.CreateTableSQL(&Table{
		Name:    tempTableName,
		Columns: newSchema.Columns,
		Indexes: newSchema.Indexes,
	})
	if err != nil {
		return fmt.Errorf("failed to generate CREATE TABLE SQL: %w", err)
	}

	for _, sql := range createSQL {
		if err := db.WithContext(ctx).Exec(sql).Error; err != nil {
			return fmt.Errorf("failed to create temp table: %w", err)
		}
	}

	// 步骤 2: 复制数据
	columnNames := newSchema.GetColumnNames()
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s",
		tempTableName,
		strings.Join(columnNames, ", "),
		strings.Join(columnNames, ", "),
		tableName)

	if err := db.WithContext(ctx).Exec(insertSQL).Error; err != nil {
		_ = db.WithContext(ctx).Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", tempTableName))
		return fmt.Errorf("failed to copy data: %w", err)
	}

	// 步骤 3: 删除旧表
	if err := db.WithContext(ctx).Exec(fmt.Sprintf("DROP TABLE %s", tableName)).Error; err != nil {
		_ = db.WithContext(ctx).Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", tempTableName))
		return fmt.Errorf("failed to drop old table: %w", err)
	}

	// 步骤 4: 重命名新表
	if err := db.WithContext(ctx).Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", tempTableName, tableName)).Error; err != nil {
		return fmt.Errorf("failed to rename temp table: %w", err)
	}

	return nil
}
