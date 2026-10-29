package sqlstoretest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/datafoxlabs/storekit/sqlstore"
	"github.com/datafoxlabs/storekit/sqlstore/mysql"
	"github.com/datafoxlabs/storekit/sqlstore/postgres"
	"github.com/datafoxlabs/storekit/sqlstore/sqlite"
	"go.opentelemetry.io/otel/trace/noop"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testOptions holds configuration for New.
type testOptions struct {
	Models   []interface{}
	DBType   string
	Truncate bool
	InMemory bool
}

// Option configures a test store.
type Option func(*testOptions)

// WithModels specifies models to AutoMigrate when creating the test store.
func WithModels(models ...interface{}) Option {
	return func(o *testOptions) {
		o.Models = append(o.Models, models...)
	}
}

// WithDBType overrides the database type for the test store.
// The default is SQLite; selecting a server database is always explicit.
func WithDBType(dbType string) Option {
	return func(o *testOptions) {
		o.DBType = dbType
	}
}

// WithTruncation deletes all rows from the supplied models after migration.
func WithTruncation() Option {
	return func(o *testOptions) {
		o.Truncate = true
	}
}

// WithInMemory selects a uniquely named in-memory SQLite database.
func WithInMemory() Option {
	return func(o *testOptions) { o.InMemory = true }
}

// New creates a fully isolated Store for testing.
//
// Each call returns an independent database:
//   - SQLite: creates a temp file with optimized settings for speed
//   - Postgres: creates a temporary database (CREATE DATABASE storekit_test_<hex>),
//     dropped on cleanup
//   - MySQL: creates a temporary database (CREATE DATABASE storekit_test_<hex>),
//     dropped on cleanup
//
// All cleanup is performed via tb.Cleanup — no manual teardown required.
func New(tb testing.TB, opts ...Option) *sqlstore.Store {
	tb.Helper()

	options := &testOptions{
		DBType: "sqlite",
	}
	for _, opt := range opts {
		opt(options)
	}

	var driver sqlstore.Driver
	var dsn string
	switch options.DBType {
	case "sqlite", "sqlite3":
		driver, dsn = sqlite.Open, createSQLiteTestDB(tb, options.InMemory)
	case "postgres":
		driver, dsn = postgres.Open, createPostgresTestDB(tb)
	case "mysql":
		driver, dsn = mysql.Open, createMySQLTestDB(tb)
	default:
		tb.Fatalf("unsupported test db type: %s", options.DBType)
		panic("unreachable")
	}

	store, err := sqlstore.Open(&sqlstore.DatabaseConfig{
		Type:               options.DBType,
		ConnectionString:   dsn,
		LogLevel:           logger.Silent,
		TransactionRetries: 5,
		RetryInterval:      10 * time.Millisecond,
	}, driver, slog.Default(), noop.NewTracerProvider())
	if err != nil {
		tb.Fatalf("failed to open test database: %v", err)
	}
	tb.Cleanup(func() { _ = store.Close() })

	// AutoMigrate if models provided
	if len(options.Models) > 0 {
		if err := store.AutoMigrate(options.Models...); err != nil {
			tb.Fatalf("failed to auto-migrate test models: %v", err)
			panic("unreachable")
		}
	}

	// Truncate if requested (after migration)
	if options.Truncate && len(options.Models) > 0 {
		for _, model := range options.Models {
			if err := store.GetDB().Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(model).Error; err != nil {
				tb.Fatalf("failed to truncate table: %v", err)
				panic("unreachable")
			}
		}
	}

	return store
}

// createSQLiteTestDB creates a temporary SQLite database file with optimized test settings.
func createSQLiteTestDB(tb testing.TB, inMemory bool) string {
	tb.Helper()

	if inMemory {
		return "file:" + generateTestDBName() + "?mode=memory&cache=shared"
	}

	tmp, err := os.CreateTemp("", "storekit-test-*.db")
	if err != nil {
		tb.Fatalf("failed to create temp sqlite file: %v", err)
		panic("unreachable")
	}

	tb.Cleanup(func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		_ = os.Remove(tmp.Name() + "-wal")
		_ = os.Remove(tmp.Name() + "-shm")
	})

	// synchronous=OFF for fast test commits
	dsn := fmt.Sprintf("file:%s?cache=private&mode=rwc&_synchronous=OFF", tmp.Name())
	return dsn
}

// createPostgresTestDB creates a temporary Postgres database and returns its dialector.
// The database is dropped when the test finishes.
func createPostgresTestDB(tb testing.TB) string {
	tb.Helper()

	adminDSN := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		envOrDefault("POSTGRES_HOST", "localhost"),
		envOrDefault("POSTGRES_PORT", "5432"),
		envOrDefault("POSTGRES_USER", "postgres"),
		envOrDefault("POSTGRES_PASSWORD", ""),
		envOrDefault("POSTGRES_DB", "postgres"),
		envOrDefault("POSTGRES_SSL", "disable"),
	)

	// Connect to admin database to create the temporary test database
	adminDB, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		tb.Fatalf("failed to connect to admin postgres: %v", err)
		panic("unreachable")
	}
	adminSQLDB, err := adminDB.DB()
	if err != nil {
		tb.Fatalf("failed to get admin sql.DB: %v", err)
		panic("unreachable")
	}
	defer adminSQLDB.Close()

	dbName := generateTestDBName()
	if err := adminDB.Exec("CREATE DATABASE " + dbName).Error; err != nil {
		tb.Fatalf("failed to create temp database %s: %v", dbName, err)
		panic("unreachable")
	}

	// Schedule cleanup: drop the temporary database after test
	tb.Cleanup(func() {
		cleanupDB, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{
			Logger: logger.Discard,
		})
		if err == nil {
			cleanupDB.Exec("DROP DATABASE IF EXISTS " + dbName)
			sqlDB, _ := cleanupDB.DB()
			if sqlDB != nil {
				_ = sqlDB.Close()
			}
		}
	})

	// Build connection string for the temporary database
	testDSN := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		envOrDefault("POSTGRES_HOST", "localhost"),
		envOrDefault("POSTGRES_PORT", "5432"),
		envOrDefault("POSTGRES_USER", "postgres"),
		envOrDefault("POSTGRES_PASSWORD", ""),
		dbName,
		envOrDefault("POSTGRES_SSL", "disable"),
	)

	return testDSN
}

// createMySQLTestDB creates a temporary MySQL database and returns its dialector.
// The database is dropped when the test finishes.
func createMySQLTestDB(tb testing.TB) string {
	tb.Helper()

	host := envOrDefault("MYSQL_HOST", "localhost")
	port := envOrDefault("MYSQL_PORT", "3306")
	user := envOrDefault("MYSQL_USER", "root")
	password := envOrDefault("MYSQL_PASSWORD", "")
	adminDBName := envOrDefault("MYSQL_DB", "mysql")

	adminDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", user, password, host, port, adminDBName)

	adminDB, err := gorm.Open(mysql.Open(adminDSN), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		tb.Fatalf("failed to connect to admin mysql: %v", err)
		panic("unreachable")
	}
	adminSQLDB, err := adminDB.DB()
	if err != nil {
		tb.Fatalf("failed to get admin mysql sql.DB: %v", err)
		panic("unreachable")
	}
	defer adminSQLDB.Close()

	dbName := generateTestDBName()
	if err := adminDB.Exec("CREATE DATABASE `" + dbName + "`").Error; err != nil {
		tb.Fatalf("failed to create temp mysql database %s: %v", dbName, err)
		panic("unreachable")
	}

	tb.Cleanup(func() {
		cleanupDB, err := gorm.Open(mysql.Open(adminDSN), &gorm.Config{
			Logger: logger.Discard,
		})
		if err == nil {
			cleanupDB.Exec("DROP DATABASE IF EXISTS `" + dbName + "`")
			sqlDB, _ := cleanupDB.DB()
			if sqlDB != nil {
				_ = sqlDB.Close()
			}
		}
	})

	testDSN := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", user, password, host, port, dbName)
	return testDSN
}

// generateTestDBName returns a unique database name for testing.
func generateTestDBName() string {
	return "storekit_test_" + randomHex(9)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic("failed to read random bytes: " + err.Error())
	}
	return hex.EncodeToString(buf)[:n]
}

func envOrDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
