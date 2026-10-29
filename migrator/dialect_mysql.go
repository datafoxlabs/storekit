package migrator

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// MySQLDialect MySQL 方言实现
type MySQLDialect struct{}

func (d *MySQLDialect) quoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (d *MySQLDialect) quoteIdentifiers(names []string) []string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, d.quoteIdentifier(name))
	}
	return quoted
}

func (d *MySQLDialect) Name() string {
	return "mysql"
}

func (d *MySQLDialect) SupportsDropColumn() bool {
	return true
}

func (d *MySQLDialect) SupportsTransactions() bool {
	// MySQL DDL commonly triggers implicit commit, migration transaction boundaries are unreliable.
	return false
}

func (d *MySQLDialect) RequiresTableRebuild(operation string) bool {
	return false
}

func (d *MySQLDialect) CreateTableSQL(table *Table) ([]string, error) {
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

	pks := table.GetPrimaryKeyColumns()
	if len(pks) > 0 {
		pkColumns := make([]string, 0, len(pks))
		for _, pk := range pks {
			pkColumns = append(pkColumns, d.quoteIdentifier(pk.Name))
		}
		if table.PrimaryKeyName != "" {
			constraints = append(constraints, fmt.Sprintf("CONSTRAINT %s PRIMARY KEY (%s)",
				d.quoteIdentifier(table.PrimaryKeyName),
				strings.Join(pkColumns, ", ")))
		} else {
			constraints = append(constraints, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(pkColumns, ", ")))
		}
	}

	createTableSQL := fmt.Sprintf("CREATE TABLE %s (\n  %s\n)",
		d.quoteIdentifier(table.Name),
		strings.Join(append(columns, constraints...), ",\n  "),
	)
	createTableSQL += d.buildTableOptions(table)
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

func (d *MySQLDialect) buildColumnDefinition(col *Column) string {
	parts := []string{d.quoteIdentifier(col.Name)}

	colType := col.ToGORMColumnType("mysql")
	parts = append(parts, colType)

	if col.Unsigned && (col.Type == TypeInt || col.Type == TypeBigInt) {
		parts = append(parts, "UNSIGNED")
	}

	if col.NotNull {
		parts = append(parts, "NOT NULL")
	}

	if col.Unique {
		parts = append(parts, "UNIQUE")
	}

	if col.AutoIncrement {
		parts = append(parts, "AUTO_INCREMENT")
	}

	if col.Default != "" {
		parts = append(parts, "DEFAULT", d.normalizeDefault(col))
	}

	if col.OnUpdate != "" {
		parts = append(parts, "ON UPDATE", col.OnUpdate)
	}

	if col.Comment != "" {
		parts = append(parts, "COMMENT", sqlStringLiteral(col.Comment))
	}

	return strings.Join(parts, " ")
}

func (d *MySQLDialect) normalizeDefault(col *Column) string {
	if col.Type != TypeBoolean {
		return col.Default
	}

	switch strings.ToLower(col.Default) {
	case "true":
		return "1"
	case "false":
		return "0"
	default:
		return col.Default
	}
}

func (d *MySQLDialect) buildTableOptions(table *Table) string {
	engine := table.Engine
	if engine == "" {
		engine = "InnoDB"
	}

	charset := table.Charset
	if charset == "" {
		charset = "utf8mb4"
	}

	options := []string{
		"ENGINE=" + engine,
		"DEFAULT CHARSET=" + charset,
	}

	if table.Collation != "" {
		options = append(options, "COLLATE="+table.Collation)
	}

	if table.Comment != "" {
		options = append(options, "COMMENT="+sqlStringLiteral(table.Comment))
	}

	return " " + strings.Join(options, " ")
}

func sqlStringLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func (d *MySQLDialect) AddColumnSQL(tableName string, col *Column) ([]string, error) {
	if err := col.Validate(); err != nil {
		return nil, err
	}

	colDef := d.buildColumnDefinition(col)
	sql := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", d.quoteIdentifier(tableName), colDef)
	return []string{sql}, nil
}

func (d *MySQLDialect) DropColumnSQL(tableName, columnName string) ([]string, error) {
	sql := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", d.quoteIdentifier(tableName), d.quoteIdentifier(columnName))
	return []string{sql}, nil
}

func (d *MySQLDialect) AlterColumnSQL(tableName string, col *Column) ([]string, error) {
	if err := col.Validate(); err != nil {
		return nil, err
	}

	colDef := d.buildColumnDefinition(col)
	sql := fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s", d.quoteIdentifier(tableName), colDef)
	return []string{sql}, nil
}

func (d *MySQLDialect) RenameColumnSQL(tableName, oldName, newName string) ([]string, error) {
	sql := fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s",
		d.quoteIdentifier(tableName),
		d.quoteIdentifier(oldName),
		d.quoteIdentifier(newName),
	)
	return []string{sql}, nil
}

func (d *MySQLDialect) AddIndexSQL(tableName string, idx *Index) ([]string, error) {
	var indexType string
	if idx.Unique {
		indexType = "UNIQUE INDEX"
	} else {
		indexType = "INDEX"
	}

	sql := fmt.Sprintf("CREATE %s %s USING BTREE ON %s (%s)",
		indexType,
		d.quoteIdentifier(idx.Name),
		d.quoteIdentifier(tableName),
		strings.Join(d.quoteIdentifiers(idx.Columns), ", "))
	return []string{sql}, nil
}

func (d *MySQLDialect) DropIndexSQL(tableName, indexName string) ([]string, error) {
	sql := fmt.Sprintf("DROP INDEX %s ON %s", d.quoteIdentifier(indexName), d.quoteIdentifier(tableName))
	return []string{sql}, nil
}

func (d *MySQLDialect) RebuildTable(_ context.Context, _ *gorm.DB, _ string, _ *Table) error {
	return fmt.Errorf("MySQL does not require table rebuild, use ALTER TABLE instead")
}
