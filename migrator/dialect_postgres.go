package migrator

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// PostgresDialect PostgreSQL 方言实现
type PostgresDialect struct{}

func (d *PostgresDialect) quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (d *PostgresDialect) quoteIdentifiers(names []string) []string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, d.quoteIdentifier(name))
	}
	return quoted
}

func (d *PostgresDialect) Name() string {
	return "postgres"
}

func (d *PostgresDialect) SupportsDropColumn() bool {
	return true
}

func (d *PostgresDialect) SupportsTransactions() bool {
	return true
}

func (d *PostgresDialect) RequiresTableRebuild(operation string) bool {
	return false
}

// CreateTableSQL 生成 CREATE TABLE 语句
func (d *PostgresDialect) CreateTableSQL(table *Table) ([]string, error) {
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
		constraints = append(constraints, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(pkColumns, ", ")))
	}

	createTableSQL := fmt.Sprintf("CREATE TABLE %s (\n  %s",
		d.quoteIdentifier(table.Name),
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

func (d *PostgresDialect) buildColumnDefinition(col *Column) string {
	parts := []string{col.Name}
	parts = []string{d.quoteIdentifier(col.Name)}

	colType := col.ToGORMColumnType("postgres")

	if col.AutoIncrement {
		if col.Type == TypeBigInt {
			colType = "BIGSERIAL"
		} else {
			colType = "SERIAL"
		}
	}

	parts = append(parts, colType)

	if col.NotNull {
		parts = append(parts, "NOT NULL")
	}

	if col.Unique {
		parts = append(parts, "UNIQUE")
	}

	if col.Default != "" {
		parts = append(parts, "DEFAULT", col.Default)
	}

	return strings.Join(parts, " ")
}

func (d *PostgresDialect) AddColumnSQL(tableName string, col *Column) ([]string, error) {
	if err := col.Validate(); err != nil {
		return nil, err
	}

	colDef := d.buildColumnDefinition(col)
	sql := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", d.quoteIdentifier(tableName), colDef)
	return []string{sql}, nil
}

func (d *PostgresDialect) DropColumnSQL(tableName, columnName string) ([]string, error) {
	sql := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", d.quoteIdentifier(tableName), d.quoteIdentifier(columnName))
	return []string{sql}, nil
}

func (d *PostgresDialect) AlterColumnSQL(tableName string, col *Column) ([]string, error) {
	if err := col.Validate(); err != nil {
		return nil, err
	}

	var statements []string
	colType := col.ToGORMColumnType("postgres")

	statements = append(statements,
		fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s", d.quoteIdentifier(tableName), d.quoteIdentifier(col.Name), colType))

	if col.NotNull {
		statements = append(statements,
			fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL", d.quoteIdentifier(tableName), d.quoteIdentifier(col.Name)))
	} else {
		statements = append(statements,
			fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL", d.quoteIdentifier(tableName), d.quoteIdentifier(col.Name)))
	}

	if col.Default != "" {
		statements = append(statements,
			fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s", d.quoteIdentifier(tableName), d.quoteIdentifier(col.Name), col.Default))
	}

	return statements, nil
}

func (d *PostgresDialect) RenameColumnSQL(tableName, oldName, newName string) ([]string, error) {
	sql := fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s",
		d.quoteIdentifier(tableName),
		d.quoteIdentifier(oldName),
		d.quoteIdentifier(newName),
	)
	return []string{sql}, nil
}

func (d *PostgresDialect) AddIndexSQL(tableName string, idx *Index) ([]string, error) {
	var indexType string
	if idx.Unique {
		indexType = "UNIQUE INDEX"
	} else {
		indexType = "INDEX"
	}

	sql := fmt.Sprintf("CREATE %s %s ON %s (%s)",
		indexType,
		d.quoteIdentifier(idx.Name),
		d.quoteIdentifier(tableName),
		strings.Join(d.quoteIdentifiers(idx.Columns), ", "))
	return []string{sql}, nil
}

func (d *PostgresDialect) DropIndexSQL(tableName, indexName string) ([]string, error) {
	sql := fmt.Sprintf("DROP INDEX IF EXISTS %s", d.quoteIdentifier(indexName))
	return []string{sql}, nil
}

func (d *PostgresDialect) RebuildTable(ctx context.Context, db *gorm.DB, tableName string, newSchema *Table) error {
	return fmt.Errorf("PostgreSQL does not require table rebuild, use ALTER TABLE instead")
}
