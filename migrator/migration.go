package migrator

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/gorm"
)

// DatabaseTarget 目标数据库类型
type DatabaseTarget string

const (
	// DatabaseRelational 关系型数据库（PostgreSQL、MySQL 或 SQLite）
	DatabaseRelational DatabaseTarget = "relational"
	// DatabaseClickHouse ClickHouse 数据库
	DatabaseClickHouse DatabaseTarget = "clickhouse"
	// DatabaseDefault 默认数据库（等同于关系型数据库，用于向后兼容）
	DatabaseDefault DatabaseTarget = ""
)

// Migration 单个迁移定义
type Migration struct {
	ID          string
	Description string
	Database    DatabaseTarget // 目标数据库，默认为 relational（向后兼容）
	Up          func(ctx context.Context, mg MigrationContext) error
	Down        func(ctx context.Context, mg MigrationContext) error
}

// GetTargetDatabase 获取目标数据库（处理默认值）
func (m *Migration) GetTargetDatabase() DatabaseTarget {
	if m.Database == "" || m.Database == DatabaseDefault {
		return DatabaseRelational
	}
	return m.Database
}

// MigrationContext 提供迁移操作 API
type MigrationContext interface {
	TableExists(tableName string) bool
	ColumnExists(tableName, columnName string) bool
	IndexExists(tableName, indexName string) bool
	CreateTable(table *Table) error
	DropTable(tableName string) error
	AddColumn(tableName string, col *Column) error
	DropColumn(tableName, columnName string) error
	AlterColumn(tableName string, col *Column) error
	RenameColumn(tableName, oldName, newName string) error
	AddIndex(tableName string, idx *Index) error
	DropIndex(tableName, indexName string) error
	RawSQL(sql string, values ...interface{}) error
	Exec(fn func(*gorm.DB) error) error
	GetDB() *gorm.DB
	GetDialect() Dialect
}

// migrationContext MigrationContext 接口的实现
type migrationContext struct {
	db      *gorm.DB
	dialect Dialect
	logger  *slog.Logger
	ctx     context.Context
}

func newMigrationContext(ctx context.Context, db *gorm.DB, dialect Dialect, logger *slog.Logger) *migrationContext {
	return &migrationContext{
		db:      db,
		dialect: dialect,
		logger:  logger.With("component", "migration_context"),
		ctx:     ctx,
	}
}

func (m *migrationContext) TableExists(tableName string) bool {
	exists := m.db.Migrator().HasTable(tableName)
	m.logger.DebugContext(m.ctx, "check table exists",
		"table_name", tableName,
		"exists", exists)
	return exists
}

func (m *migrationContext) ColumnExists(tableName, columnName string) bool {
	exists := m.db.Migrator().HasColumn(tableName, columnName)
	m.logger.DebugContext(m.ctx, "check column exists",
		"table_name", tableName,
		"column_name", columnName,
		"exists", exists)
	return exists
}

func (m *migrationContext) IndexExists(tableName, indexName string) bool {
	exists := m.db.Migrator().HasIndex(tableName, indexName)
	m.logger.DebugContext(m.ctx, "check index exists",
		"table_name", tableName,
		"index_name", indexName,
		"exists", exists)
	return exists
}

func (m *migrationContext) CreateTable(table *Table) error {
	if err := table.Validate(); err != nil {
		return fmt.Errorf("invalid table definition: %w", err)
	}

	if m.TableExists(table.Name) {
		m.logger.WarnContext(m.ctx, "table already exists, skipping",
			"table_name", table.Name)
		return nil
	}

	m.logger.InfoContext(m.ctx, "creating table",
		"table_name", table.Name,
		"columns_count", len(table.Columns))

	sqlStatements, err := m.dialect.CreateTableSQL(table)
	if err != nil {
		return fmt.Errorf("failed to generate CREATE TABLE SQL: %w", err)
	}

	for _, sql := range sqlStatements {
		if err := m.db.WithContext(m.ctx).Exec(sql).Error; err != nil {
			return fmt.Errorf("failed to execute SQL: %s, error: %w", sql, err)
		}
	}

	m.logger.InfoContext(m.ctx, "table created successfully",
		"table_name", table.Name)
	return nil
}

func (m *migrationContext) DropTable(tableName string) error {
	if !m.TableExists(tableName) {
		m.logger.WarnContext(m.ctx, "table does not exist, skipping",
			"table_name", tableName)
		return nil
	}

	m.logger.InfoContext(m.ctx, "dropping table",
		"table_name", tableName)

	if err := m.db.Migrator().DropTable(tableName); err != nil {
		return fmt.Errorf("failed to drop table %s: %w", tableName, err)
	}

	m.logger.InfoContext(m.ctx, "table dropped successfully",
		"table_name", tableName)
	return nil
}

func (m *migrationContext) AddColumn(tableName string, col *Column) error {
	if err := col.Validate(); err != nil {
		return fmt.Errorf("invalid column definition: %w", err)
	}

	if m.ColumnExists(tableName, col.Name) {
		m.logger.WarnContext(m.ctx, "column already exists, skipping",
			"table_name", tableName,
			"column_name", col.Name)
		return nil
	}

	m.logger.InfoContext(m.ctx, "adding column",
		"table_name", tableName,
		"column_name", col.Name,
		"column_type", col.Type)

	sqlStatements, err := m.dialect.AddColumnSQL(tableName, col)
	if err != nil {
		return fmt.Errorf("failed to generate ADD COLUMN SQL: %w", err)
	}

	for _, sql := range sqlStatements {
		if err := m.db.WithContext(m.ctx).Exec(sql).Error; err != nil {
			return fmt.Errorf("failed to execute SQL: %s, error: %w", sql, err)
		}
	}

	m.logger.InfoContext(m.ctx, "column added successfully",
		"table_name", tableName,
		"column_name", col.Name)
	return nil
}

func (m *migrationContext) DropColumn(tableName, columnName string) error {
	if !m.ColumnExists(tableName, columnName) {
		m.logger.WarnContext(m.ctx, "column does not exist, skipping",
			"table_name", tableName,
			"column_name", columnName)
		return nil
	}

	m.logger.InfoContext(m.ctx, "dropping column",
		"table_name", tableName,
		"column_name", columnName)

	if !m.dialect.SupportsDropColumn() {
		m.logger.WarnContext(m.ctx, "database does not support DROP COLUMN, using table rebuild",
			"table_name", tableName,
			"column_name", columnName)
		return fmt.Errorf("DROP COLUMN not supported by %s, please use RebuildTable with new schema", m.dialect.Name())
	}

	sqlStatements, err := m.dialect.DropColumnSQL(tableName, columnName)
	if err != nil {
		return fmt.Errorf("failed to generate DROP COLUMN SQL: %w", err)
	}

	for _, sql := range sqlStatements {
		if err := m.db.WithContext(m.ctx).Exec(sql).Error; err != nil {
			return fmt.Errorf("failed to execute SQL: %s, error: %w", sql, err)
		}
	}

	m.logger.InfoContext(m.ctx, "column dropped successfully",
		"table_name", tableName,
		"column_name", columnName)
	return nil
}

func (m *migrationContext) AlterColumn(tableName string, col *Column) error {
	if err := col.Validate(); err != nil {
		return fmt.Errorf("invalid column definition: %w", err)
	}

	m.logger.InfoContext(m.ctx, "altering column",
		"table_name", tableName,
		"column_name", col.Name,
		"column_type", col.Type)

	sqlStatements, err := m.dialect.AlterColumnSQL(tableName, col)
	if err != nil {
		return fmt.Errorf("failed to generate ALTER COLUMN SQL: %w", err)
	}

	for _, sql := range sqlStatements {
		if err := m.db.WithContext(m.ctx).Exec(sql).Error; err != nil {
			return fmt.Errorf("failed to execute SQL: %s, error: %w", sql, err)
		}
	}

	m.logger.InfoContext(m.ctx, "column altered successfully",
		"table_name", tableName,
		"column_name", col.Name)
	return nil
}

func (m *migrationContext) RenameColumn(tableName, oldName, newName string) error {
	m.logger.InfoContext(m.ctx, "renaming column",
		"table_name", tableName,
		"old_name", oldName,
		"new_name", newName)

	sqlStatements, err := m.dialect.RenameColumnSQL(tableName, oldName, newName)
	if err != nil {
		return fmt.Errorf("failed to generate RENAME COLUMN SQL: %w", err)
	}

	for _, sql := range sqlStatements {
		if err := m.db.WithContext(m.ctx).Exec(sql).Error; err != nil {
			return fmt.Errorf("failed to execute SQL: %s, error: %w", sql, err)
		}
	}

	m.logger.InfoContext(m.ctx, "column renamed successfully",
		"table_name", tableName,
		"old_name", oldName,
		"new_name", newName)
	return nil
}

func (m *migrationContext) AddIndex(tableName string, idx *Index) error {
	if m.IndexExists(tableName, idx.Name) {
		m.logger.WarnContext(m.ctx, "index already exists, skipping",
			"table_name", tableName,
			"index_name", idx.Name)
		return nil
	}

	m.logger.InfoContext(m.ctx, "adding index",
		"table_name", tableName,
		"index_name", idx.Name,
		"columns", idx.Columns)

	sqlStatements, err := m.dialect.AddIndexSQL(tableName, idx)
	if err != nil {
		return fmt.Errorf("failed to generate CREATE INDEX SQL: %w", err)
	}

	for _, sql := range sqlStatements {
		if err := m.db.WithContext(m.ctx).Exec(sql).Error; err != nil {
			return fmt.Errorf("failed to execute SQL: %s, error: %w", sql, err)
		}
	}

	m.logger.InfoContext(m.ctx, "index added successfully",
		"table_name", tableName,
		"index_name", idx.Name)
	return nil
}

func (m *migrationContext) DropIndex(tableName, indexName string) error {
	if !m.IndexExists(tableName, indexName) {
		m.logger.WarnContext(m.ctx, "index does not exist, skipping",
			"table_name", tableName,
			"index_name", indexName)
		return nil
	}

	m.logger.InfoContext(m.ctx, "dropping index",
		"table_name", tableName,
		"index_name", indexName)

	sqlStatements, err := m.dialect.DropIndexSQL(tableName, indexName)
	if err != nil {
		return fmt.Errorf("failed to generate DROP INDEX SQL: %w", err)
	}

	for _, sql := range sqlStatements {
		if err := m.db.WithContext(m.ctx).Exec(sql).Error; err != nil {
			return fmt.Errorf("failed to execute SQL: %s, error: %w", sql, err)
		}
	}

	m.logger.InfoContext(m.ctx, "index dropped successfully",
		"table_name", tableName,
		"index_name", indexName)
	return nil
}

func (m *migrationContext) RawSQL(sql string, values ...interface{}) error {
	m.logger.InfoContext(m.ctx, "executing raw SQL",
		"sql_length", len(sql))

	if err := m.db.WithContext(m.ctx).Exec(sql, values...).Error; err != nil {
		return fmt.Errorf("failed to execute raw SQL: %w", err)
	}

	m.logger.InfoContext(m.ctx, "raw SQL executed successfully")
	return nil
}

func (m *migrationContext) Exec(fn func(*gorm.DB) error) error {
	m.logger.InfoContext(m.ctx, "executing custom function")

	if err := fn(m.db.WithContext(m.ctx)); err != nil {
		return fmt.Errorf("custom function failed: %w", err)
	}

	m.logger.InfoContext(m.ctx, "custom function executed successfully")
	return nil
}

func (m *migrationContext) GetDB() *gorm.DB {
	return m.db.WithContext(m.ctx)
}

func (m *migrationContext) GetDialect() Dialect {
	return m.dialect
}
