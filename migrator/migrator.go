package migrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"gorm.io/gorm"
)

// HistoryStore 定义迁移历史存储接口
type HistoryStore interface {
	EnsureTable(ctx context.Context) error
	IsApplied(ctx context.Context, migrationID string) (bool, error)
	RecordSuccess(ctx context.Context, migrationID, description string) error
	RecordFailure(ctx context.Context, migrationID, description string, err error) error
	GetHistory(ctx context.Context, limit int) ([]*MigrationLog, error)
	GetFailedMigrations(ctx context.Context) ([]*MigrationLog, error)
	DeleteMigration(ctx context.Context, migrationID string) error
}

// Migrator 核心迁移引擎
type Migrator struct {
	db           *gorm.DB
	logger       *slog.Logger
	dialect      Dialect
	history      HistoryStore
	historyTable string
	options      *Options
}

// Options 迁移配置选项
type Options struct {
	ContinueOnError  bool
	SkipHistoryCheck bool
}

const dialectNameClickHouse = "clickhouse"

// DefaultHistoryTable is the stable default used by existing installations.
const DefaultHistoryTable = "migration_log"

var historyTableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type HistoryFactory func(db *gorm.DB, logger *slog.Logger, dialect Dialect) HistoryStore

type Option func(*newConfig)

type newConfig struct {
	options            *Options
	historyFactory     HistoryFactory
	historyTable       string
	clickHouseTimezone string
}

// DefaultOptions 返回默认配置
func DefaultOptions() *Options {
	return &Options{
		ContinueOnError:  false,
		SkipHistoryCheck: false,
	}
}

func defaultNewConfig() *newConfig {
	return &newConfig{options: DefaultOptions(), historyTable: DefaultHistoryTable}
}

// WithHistoryTable selects an unqualified history table. New panics for an
// invalid identifier, or for a non-default table combined with a custom history factory.
func WithHistoryTable(name string) Option {
	return func(cfg *newConfig) { cfg.historyTable = name }
}

// WithClickHouseTimezone sets the timezone used by generated ClickHouse DDL.
// Empty leaves the timezone unspecified. New panics for an unknown timezone
// or for a non-ClickHouse database. Raw SQL migrations are unaffected.
func WithClickHouseTimezone(timezone string) Option {
	return func(cfg *newConfig) { cfg.clickHouseTimezone = timezone }
}

func WithOptions(options *Options) Option {
	return func(cfg *newConfig) {
		if options == nil {
			return
		}
		cfg.options = options
	}
}

func WithHistoryFactory(factory HistoryFactory) Option {
	return func(cfg *newConfig) {
		if factory == nil {
			return
		}
		cfg.historyFactory = factory
	}
}

// New 创建 Migrator 实例
func New(db *gorm.DB, logger *slog.Logger, opts ...Option) *Migrator {
	cfg := defaultNewConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(cfg)
	}

	if !historyTableName.MatchString(cfg.historyTable) {
		panic("invalid migration history table: " + cfg.historyTable)
	}
	if cfg.historyFactory != nil && cfg.historyTable != DefaultHistoryTable {
		panic("WithHistoryTable cannot configure a custom history factory")
	}
	dialect := NewDialect(db)
	if cfg.clickHouseTimezone != "" {
		if _, err := time.LoadLocation(cfg.clickHouseTimezone); err != nil {
			panic("invalid ClickHouse timezone: " + cfg.clickHouseTimezone)
		}
		ch, ok := dialect.(*ClickHouseDialect)
		if !ok {
			panic("WithClickHouseTimezone requires a ClickHouse database")
		}
		ch.Timezone = cfg.clickHouseTimezone
	}
	var history HistoryStore
	switch {
	case cfg.historyFactory != nil:
		history = cfg.historyFactory(db, logger, dialect)
	case dialect.Name() == dialectNameClickHouse:
		history = &ClickHouseHistoryStore{db: db, table: cfg.historyTable}
	default:
		history = newDefaultHistoryStore(db, logger, cfg.historyTable)
	}

	return &Migrator{
		db:           db,
		logger:       logger.With("component", "migrator"),
		dialect:      dialect,
		history:      history,
		historyTable: cfg.historyTable,
		options:      cfg.options,
	}
}

// Run 执行迁移
func (m *Migrator) Run(ctx context.Context, migrations ...*Migration) (runErr error) {
	if len(migrations) == 0 {
		m.logger.InfoContext(ctx, "no migrations to run")
		return nil
	}

	// Lock before EnsureTable and IsApplied to serialize the entire
	// check / migration / history-write sequence between processes.
	release, err := m.acquireMigrationLock(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire migration lock: %w", err)
	}
	defer func() {
		if err := release(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("failed to release migration lock: %w", err))
		}
	}()

	if err := m.history.EnsureTable(ctx); err != nil {
		return fmt.Errorf("failed to ensure migration history table: %w", err)
	}

	m.logger.InfoContext(ctx, "starting migrations",
		"total_migrations", len(migrations),
		"dialect", m.dialect.Name(),
		"continue_on_error", m.options.ContinueOnError)

	var failedCount int
	var successCount int
	var failures []error
	startTime := time.Now()

	for i, mig := range migrations {
		m.logger.InfoContext(ctx, "processing migration",
			"migration_id", mig.ID,
			"description", mig.Description,
			"progress", fmt.Sprintf("%d/%d", i+1, len(migrations)))

		if !m.options.SkipHistoryCheck {
			applied, err := m.history.IsApplied(ctx, mig.ID)
			if err != nil {
				m.logger.ErrorContext(ctx, "failed to check migration status",
					"migration_id", mig.ID,
					"error", err)
				failure := fmt.Errorf("failed to check migration status for %s: %w", mig.ID, err)
				if !m.options.ContinueOnError {
					return failure
				}
				failures = append(failures, failure)
				failedCount++
				continue
			}

			if applied {
				m.logger.InfoContext(ctx, "migration already applied, skipping",
					"migration_id", mig.ID)
				continue
			}
		}

		recordedInTx, err := m.runSingleMigration(ctx, mig)
		if err != nil {
			m.logger.ErrorContext(ctx, "migration failed",
				"migration_id", mig.ID,
				"description", mig.Description,
				"error", err)

			failure := fmt.Errorf("migration %s failed: %w", mig.ID, err)
			if recordErr := m.history.RecordFailure(ctx, mig.ID, mig.Description, err); recordErr != nil {
				m.logger.ErrorContext(ctx, "failed to record migration failure",
					"migration_id", mig.ID,
					"error", recordErr)
				failure = errors.Join(failure, fmt.Errorf("migration %s failed to record failure: %w", mig.ID, recordErr))
			}

			failedCount++
			if !m.options.ContinueOnError {
				return failure
			}
			failures = append(failures, failure)
			continue
		}

		// Non-transactional databases and custom history stores cannot be
		// committed atomically here. A failed write must never look successful.
		if !recordedInTx {
			if err := m.history.RecordSuccess(ctx, mig.ID, mig.Description); err != nil {
				m.logger.ErrorContext(ctx, "failed to record migration success",
					"migration_id", mig.ID,
					"error", err)
				return fmt.Errorf("migration %s executed but failed to record success; inspect database state before retry: %w", mig.ID, err)
			}
		}

		successCount++
		m.logger.InfoContext(ctx, "migration completed successfully",
			"migration_id", mig.ID)
	}

	elapsed := time.Since(startTime)
	m.logger.InfoContext(ctx, "migrations completed",
		"total_migrations", len(migrations),
		"success_count", successCount,
		"failed_count", failedCount,
		"duration_ms", elapsed.Milliseconds())

	if failedCount > 0 {
		return fmt.Errorf("migrations failed: %d failed, %d succeeded: %w",
			failedCount, successCount, errors.Join(failures...))
	}

	return nil
}

func (m *Migrator) runSingleMigration(ctx context.Context, mig *Migration) (bool, error) {
	startTime := time.Now()
	recordedInTx := false

	if m.dialect.SupportsTransactions() {
		err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			txMigCtx := newMigrationContext(ctx, tx, m.dialect, m.logger)
			if err := mig.Up(ctx, txMigCtx); err != nil {
				return err
			}
			// For the built-in relational history store, commit the DDL and
			// success marker together. A failed marker rolls back the migration.
			if history, ok := m.history.(*DefaultHistoryStore); ok {
				if err := history.withDB(tx).RecordSuccess(ctx, mig.ID, mig.Description); err != nil {
					return fmt.Errorf("failed to record migration success: %w", err)
				}
				recordedInTx = true
			}
			return nil
		})
		if err != nil {
			return false, fmt.Errorf("migration failed in transaction: %w", err)
		}
	} else {
		m.logger.WarnContext(ctx, "database does not support transactions, running without transaction",
			"migration_id", mig.ID,
			"dialect", m.dialect.Name())

		migCtx := newMigrationContext(ctx, m.db, m.dialect, m.logger)
		if err := mig.Up(ctx, migCtx); err != nil {
			return false, fmt.Errorf("migration failed: %w", err)
		}
	}

	elapsed := time.Since(startTime)
	m.logger.InfoContext(ctx, "migration executed",
		"migration_id", mig.ID,
		"duration_ms", elapsed.Milliseconds())

	return recordedInTx, nil
}

// Rollback 回滚迁移
func (m *Migrator) Rollback(ctx context.Context, migrationID string, migration *Migration) (rollbackErr error) {
	if migration == nil || migration.Down == nil {
		return fmt.Errorf("migration %s does not support rollback (no Down function)", migrationID)
	}

	release, err := m.acquireMigrationLock(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire migration lock for rollback: %w", err)
	}
	defer func() {
		if err := release(); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("failed to release migration lock: %w", err))
		}
	}()

	m.logger.InfoContext(ctx, "rolling back migration",
		"migration_id", migrationID)

	deletedInTx := false
	if m.dialect.SupportsTransactions() {
		err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			txMigCtx := newMigrationContext(ctx, tx, m.dialect, m.logger)
			if err := migration.Down(ctx, txMigCtx); err != nil {
				return err
			}
			if history, ok := m.history.(*DefaultHistoryStore); ok {
				if err := history.withDB(tx).DeleteMigration(ctx, migrationID); err != nil {
					return fmt.Errorf("failed to delete migration history: %w", err)
				}
				deletedInTx = true
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("rollback failed in transaction: %w", err)
		}
	} else {
		migCtx := newMigrationContext(ctx, m.db, m.dialect, m.logger)
		if err := migration.Down(ctx, migCtx); err != nil {
			return fmt.Errorf("rollback failed: %w", err)
		}
	}

	if !deletedInTx {
		if err := m.history.DeleteMigration(ctx, migrationID); err != nil {
			m.logger.ErrorContext(ctx, "failed to delete migration record after rollback",
				"migration_id", migrationID,
				"error", err)
			return fmt.Errorf("migration %s rollback executed but failed to delete history; inspect database state before retry: %w", migrationID, err)
		}
	}

	m.logger.InfoContext(ctx, "migration rolled back successfully",
		"migration_id", migrationID)
	return nil
}

// GetHistory 获取迁移历史
func (m *Migrator) GetHistory(ctx context.Context, limit int) ([]*MigrationLog, error) {
	return m.history.GetHistory(ctx, limit)
}

// GetFailedMigrations 获取失败的迁移
func (m *Migrator) GetFailedMigrations(ctx context.Context) ([]*MigrationLog, error) {
	return m.history.GetFailedMigrations(ctx)
}
