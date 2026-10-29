package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/datafoxlabs/storekit/migrator"
	storePlugin "github.com/datafoxlabs/storekit/sqlstore/plugin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

// Store wraps GORM database connection with enhanced features
type Store struct {
	db     *gorm.DB
	config *DatabaseConfig
	logger *slog.Logger
	tracer trace.Tracer
}

// Driver constructs a GORM dialector from a connection string. Import only the
// driver subpackage needed by the application and pass its Open function.
type Driver func(string) gorm.Dialector

// Open creates a new Store from database config.
func Open(dbCfg *DatabaseConfig, driver Driver, logger *slog.Logger, provider trace.TracerProvider) (*Store, error) {
	if dbCfg == nil {
		return nil, fmt.Errorf("database config cannot be nil")
	}

	if logger == nil {
		return nil, fmt.Errorf("database logger cannot be nil")
	}
	if provider == nil {
		return nil, fmt.Errorf("tracer provider cannot be nil")
	}

	if driver == nil {
		return nil, fmt.Errorf("database driver cannot be nil")
	}
	dialector := driver(dbCfg.ConnectionString)
	dbType := dbCfg.Type
	if dbType == "sqlite3" {
		dbType = "sqlite"
	}
	if dialector.Name() != dbType {
		return nil, fmt.Errorf("database type %q does not match driver %q", dbCfg.Type, dialector.Name())
	}

	// Configure GORM logger adapter
	gormLogger := newGormLogger(logger, dbCfg.LogLevel)

	// Open database connection
	gdb, err := gorm.Open(dialector, &gorm.Config{
		Logger:         gormLogger,
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	if dbCfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(dbCfg.MaxOpenConns)
	}
	if dbCfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(dbCfg.MaxIdleConns)
	}
	if dbCfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(dbCfg.ConnMaxLifetime) * time.Second)
	}

	store := &Store{
		db:     gdb,
		config: dbCfg,
		logger: logger,
		tracer: provider.Tracer(instrumentationScope),
	}

	// Register OTel GORM plugin for automatic SQL-level tracing
	otelPlugin := storePlugin.NewOTelPlugin(provider)
	if err := gdb.Use(otelPlugin); err != nil {
		return nil, errors.Join(fmt.Errorf("failed to register OTel plugin: %w", err), sqlDB.Close())
	}

	return store, nil
}

// Exec executes a database operation with built-in retry.
// If context contains a transaction (from InTransaction), it reuses that transaction.
// Otherwise, it runs against the default connection with retry controlled by QueryRetries.
// When tracing is enabled, automatically creates a span.
func (s *Store) Exec(ctx context.Context, fn func(*gorm.DB) error) error {
	// Check for existing transaction in context (from InTransaction)
	if tx, ok := txFromContext(ctx); ok {
		return fn(tx)
	}

	ctx, span := s.startSpan(ctx, "sqlstore.exec")
	defer span.End()

	return s.execWithRetry(ctx, s.config.QueryRetries, func() error {
		return fn(s.db.WithContext(ctx))
	})
}

// Tx executes a function within a database transaction with built-in retry.
// On retryable failure, the transaction is rolled back and retried.
// When tracing is enabled, automatically creates a span.
func (s *Store) Tx(ctx context.Context, fn func(*gorm.DB) error) error {
	ctx, span := s.startSpan(ctx, "sqlstore.tx")
	defer span.End()

	return s.execWithRetry(ctx, s.config.TransactionRetries, func() error {
		return s.db.WithContext(ctx).Transaction(fn)
	})
}

// GetDB returns the underlying GORM database instance for direct queries.
// Note: operations via GetDB do NOT benefit from built-in retry.
func (s *Store) GetDB() *gorm.DB {
	return s.db
}

// Close closes the database connection
func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}
	return sqlDB.Close()
}

// Ping checks if the database connection is alive
func (s *Store) Ping(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return fmt.Errorf("%w: failed to get underlying sql.DB: %w", ErrPingFailed, err)
	}

	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrPingFailed, err)
	}

	return nil
}

// Stats returns database connection pool statistics
func (s *Store) Stats() sql.DBStats {
	sqlDB, err := s.db.DB()
	if err != nil {
		s.logger.Error("failed to get database stats", "error", err)
		return sql.DBStats{}
	}
	return sqlDB.Stats()
}

// AutoMigrate runs automatic migration for given models
func (s *Store) AutoMigrate(models ...interface{}) error {
	if err := s.db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("%w: auto-migration: %w", ErrMigrationFailed, err)
	}

	s.logger.Info("database schema migration completed",
		"models", len(models))

	return nil
}

// RunMigrations executes migrations
// This should be called during application startup before the service starts serving requests
func (s *Store) RunMigrations(ctx context.Context, migrations ...*migrator.Migration) error {
	s.logger.InfoContext(ctx, "starting database migrations",
		"total_migrations", len(migrations))

	// Create migrator instance with logger
	m := migrator.New(s.db, s.logger.With("component", "migrator"))

	// Run migrations
	if err := m.Run(ctx, migrations...); err != nil {
		return fmt.Errorf("%w: %w", ErrMigrationFailed, err)
	}

	s.logger.InfoContext(ctx, "database migrations completed successfully")
	return nil
}

// RunMigrationsWithOptions executes migrations with custom options
func (s *Store) RunMigrationsWithOptions(ctx context.Context, opts *migrator.Options, migrations ...*migrator.Migration) error {
	s.logger.InfoContext(ctx, "starting database migrations with options",
		"total_migrations", len(migrations),
		"continue_on_error", opts.ContinueOnError)

	// Create migrator instance with logger and options
	m := migrator.New(s.db, s.logger.With("component", "migrator"), migrator.WithOptions(opts))

	// Run migrations
	if err := m.Run(ctx, migrations...); err != nil {
		return fmt.Errorf("%w: %w", ErrMigrationFailed, err)
	}

	s.logger.InfoContext(ctx, "database migrations completed successfully")
	return nil
}

var instrumentationScope = "github.com/datafoxlabs/storekit/sqlstore"

// startSpan uses the same injected provider as the GORM plugin.
func (s *Store) startSpan(ctx context.Context, name string, attributes ...attribute.KeyValue) (context.Context, trace.Span) {
	return s.tracer.Start(ctx, name, trace.WithAttributes(attributes...))
}
