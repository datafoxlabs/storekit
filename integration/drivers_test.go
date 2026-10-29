//go:build integration

package integration_test

import (
	"context"
	"sync/atomic"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/datafoxlabs/storekit/clickhouse/schema"
	"github.com/datafoxlabs/storekit/migrator"
	"github.com/datafoxlabs/storekit/sqlstore"
	"github.com/datafoxlabs/storekit/sqlstore/clickhouse"
	"github.com/datafoxlabs/storekit/sqlstore/mysql"
	"github.com/datafoxlabs/storekit/sqlstore/postgres"
	"github.com/datafoxlabs/storekit/sqlstore/sqlstoretest"
	"go.opentelemetry.io/otel/trace/noop"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDriverIntegration(t *testing.T) {
	for _, fixture := range []struct {
		name, env string
		driver    sqlstore.Driver
	}{
		{"postgres", "STOREKIT_TEST_POSTGRES_DSN", postgres.Open},
		{"mysql", "STOREKIT_TEST_MYSQL_DSN", mysql.Open},
		{"clickhouse", "STOREKIT_TEST_CLICKHOUSE_DSN", clickhouse.Open},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			dsn := os.Getenv(fixture.env)
			if dsn == "" {
				t.Fatal("integration requires all disposable server fixtures; use make test-integration or CI service mode")
			}
			if fixture.name == "clickhouse" {
				// ClickHouse 26.2 ARM can SIGILL while JIT-compiling GORM's
				// system.tables predicate. This is fixture policy, not a driver default.
				u, err := url.Parse(dsn)
				check(t, err)
				settings := u.Query()
				for _, setting := range []string{"compile_expressions", "compile_aggregate_expressions", "compile_sort_description"} {
					settings.Set(setting, "0")
				}
				u.RawQuery = settings.Encode()
				dsn = u.String()
			}
			cfg := &sqlstore.DatabaseConfig{Type: fixture.name, ConnectionString: dsn, MaxOpenConns: 2, MaxIdleConns: 2, QueryRetries: 3, TransactionRetries: 3, RetryInterval: time.Millisecond, LogLevel: logger.Silent}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			var store *sqlstore.Store
			var err error
			deadline := time.Now().Add(90 * time.Second)
			for time.Now().Before(deadline) {
				store, err = sqlstore.Open(cfg, fixture.driver, log, noop.NewTracerProvider())
				if err == nil {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			check(t, err)
			t.Cleanup(func() { check(t, store.Close()) })
			ctx := context.Background()
			check(t, store.Ping(ctx))
			var version string
			check(t, store.GetDB().Raw("SELECT version()").Scan(&version).Error)
			t.Logf("server version: %s", version)
			if fixture.name == "clickhouse" && !strings.HasPrefix(version, "26.2.") {
				t.Fatalf("ClickHouse 26.2 baseline required, got %s", version)
			}
			if fixture.name == "clickhouse" {
				var enabled uint64
				check(t, store.GetDB().Raw("SELECT count() FROM system.settings WHERE name IN ('compile_expressions', 'compile_aggregate_expressions', 'compile_sort_description') AND value != '0'").Scan(&enabled).Error)
				if enabled != 0 {
					t.Fatal("ClickHouse fixture requires JIT disabled")
				}
				t.Log("ClickHouse fixture: expression/aggregate/sort JIT disabled")
			}
			createSQL := "CREATE TABLE driver_rows (id BIGINT PRIMARY KEY)"
			if fixture.name == "clickhouse" {
				createSQL = "CREATE TABLE driver_rows (id Int64) ENGINE=MergeTree ORDER BY id"
			}
			check(t, store.GetDB().Exec(createSQL).Error)
			write := func(db *gorm.DB) error { return db.Exec("INSERT INTO driver_rows VALUES (1)").Error }
			if fixture.name == "clickhouse" {
				check(t, store.Exec(ctx, write))
			} else {
				check(t, store.Tx(ctx, write))
			}
			var stored int64
			check(t, store.GetDB().Table("driver_rows").Count(&stored).Error)
			if stored != 1 {
				t.Fatal("committed write was not retained")
			}
			if fixture.name != "clickhouse" {
				cause := errors.New("rollback requested")
				err := store.InTransaction(ctx, func(txCtx context.Context) error {
					check(t, store.Exec(txCtx, func(db *gorm.DB) error { return db.Exec("INSERT INTO driver_rows VALUES (2)").Error }))
					return cause
				})
				if !errors.Is(err, cause) {
					t.Fatalf("lost rollback cause: %v", err)
				}
				var count int64
				check(t, store.GetDB().Table("driver_rows").Count(&count).Error)
				if count != 1 {
					t.Fatal("transaction did not roll back")
				}
			}
			if fixture.name != "clickhouse" {
				t.Run("retry-and-error-translation", func(t *testing.T) {
					calls := 0
					cause := errors.New("business rejected")
					err := store.Tx(ctx, func(db *gorm.DB) error {
						calls++
						if err := db.Exec("INSERT INTO driver_rows VALUES (3)").Error; err != nil {
							return err
						}
						return cause
					})
					if !errors.Is(err, cause) || calls != 1 {
						t.Fatalf("business error=%v calls=%d", err, calls)
					}
					calls = 0
					err = store.Tx(ctx, func(db *gorm.DB) error {
						calls++
						return db.Exec("INSERT INTO driver_rows VALUES (1)").Error
					})
					if !errors.Is(err, gorm.ErrDuplicatedKey) || calls != 1 {
						t.Fatalf("duplicate error=%v calls=%d", err, calls)
					}
					calls = 0
					// Server-generated transient errors exercise each real driver's error
					// decoding/translation and a complete rollback before the next attempt.
					injected := "DO $$ BEGIN RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'injected serialization failure'; END $$"
					if fixture.name == "mysql" {
						injected = "SIGNAL SQLSTATE '40001' SET MYSQL_ERRNO = 1213, MESSAGE_TEXT = 'injected deadlock'"
					}
					check(t, store.Tx(ctx, func(db *gorm.DB) error {
						calls++
						if err := db.Exec("INSERT INTO driver_rows VALUES (4)").Error; err != nil {
							return err
						}
						if calls == 1 {
							return db.Exec(injected).Error
						}
						return nil
					}))
					if calls != 2 {
						t.Fatalf("transient attempts=%d", calls)
					}
					var count int64
					check(t, store.GetDB().Table("driver_rows").Count(&count).Error)
					if count != 2 {
						t.Fatalf("rollback/retry retained unexpected rows: %d", count)
					}
				})
			}

			if fixture.name != "clickhouse" {
				t.Run("test-helper-isolation", func(t *testing.T) {
					type helperRow struct {
						ID int `gorm:"primaryKey"`
					}
					first := sqlstoretest.New(t, sqlstoretest.WithDBType(fixture.name), sqlstoretest.WithModels(&helperRow{}))
					second := sqlstoretest.New(t, sqlstoretest.WithDBType(fixture.name), sqlstoretest.WithModels(&helperRow{}))
					check(t, first.GetDB().Create(&helperRow{ID: 1}).Error)
					var count int64
					check(t, second.GetDB().Model(&helperRow{}).Count(&count).Error)
					if count != 0 {
						t.Fatal("test databases are not isolated")
					}
				})
				var leftovers int64
				query := "SELECT count(*) FROM pg_database WHERE datname LIKE 'storekit_test_%'"
				if fixture.name == "mysql" {
					query = "SELECT count(*) FROM information_schema.schemata WHERE schema_name LIKE 'storekit_test_%'"
				}
				check(t, store.GetDB().Raw(query).Scan(&leftovers).Error)
				if leftovers != 0 {
					t.Fatal("test helper did not remove temporary databases")
				}
			}
			for _, table := range []string{migrator.DefaultHistoryTable, "isolated_history"} {
				engine := migrator.New(store.GetDB(), log, migrator.WithHistoryTable(table))
				calls := 0
				migration := &migrator.Migration{
					ID: "same_id",
					Up: func(context.Context, migrator.MigrationContext) error {
						calls++
						if calls == 1 {
							return errors.New("first attempt fails")
						}
						return nil
					},
					Down: func(context.Context, migrator.MigrationContext) error { return nil },
				}
				if engine.Run(ctx, migration) == nil {
					t.Fatal("expected failure")
				}
				failures, err := engine.GetFailedMigrations(ctx)
				check(t, err)
				if len(failures) != 1 {
					t.Fatalf("failure history length: %d", len(failures))
				}
				check(t, engine.Run(ctx, migration))
				check(t, engine.Run(ctx, migration))
				if calls != 2 {
					t.Fatalf("repeat run executed migration: %d", calls)
				}
				history, err := engine.GetHistory(ctx, 0)
				check(t, err)
				want := 1
				if fixture.name == "clickhouse" {
					want = 2
				}
				if len(history) != want {
					t.Fatalf("history length: got %d want %d", len(history), want)
				}
				if fixture.name == "clickhouse" {
					// Preserve append-only history and any-success skip semantics,
					// including a later failure of an already successful migration.
					force := migrator.New(store.GetDB(), log, migrator.WithHistoryTable(table), migrator.WithOptions(&migrator.Options{SkipHistoryCheck: true}))
					migration.Up = func(context.Context, migrator.MigrationContext) error { return errors.New("later failure") }
					if force.Run(ctx, migration) == nil {
						t.Fatal("expected forced failure")
					}
					check(t, engine.Run(ctx, migration))
					failures, err = engine.GetFailedMigrations(ctx)
					check(t, err)
					if len(failures) != 2 {
						t.Fatal("ClickHouse must retain both failed records")
					}
				}
				check(t, engine.Rollback(ctx, migration.ID, migration))
				deadline := time.Now().Add(10 * time.Second)
				for {
					history, err = engine.GetHistory(ctx, 0)
					check(t, err)
					if len(history) == 0 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("history deletion did not finish")
					}
					time.Sleep(100 * time.Millisecond)
				}
			}
			if fixture.name == "clickhouse" {
				for _, timezone := range []string{"", "Asia/Shanghai"} {
					name := "generic_rows"
					if timezone != "" {
						name += "_timezone"
					}
					engine := migrator.New(store.GetDB(), log, migrator.WithClickHouseTimezone(timezone))
					migration := &migrator.Migration{
						ID: name,
						Up: func(_ context.Context, mg migrator.MigrationContext) error {
							return mg.CreateTable(&migrator.Table{
								Name: name,
								Columns: []*migrator.Column{
									{Name: "id", Type: migrator.TypeBigInt, PrimaryKey: true},
									{Name: "ts", Type: migrator.TypeTimestamp},
								},
							})
						},
						Down: func(_ context.Context, mg migrator.MigrationContext) error { return mg.DropTable(name) },
					}
					check(t, engine.Run(ctx, migration))
					check(t, engine.Run(ctx, migration))
					var columnType string
					check(t, store.GetDB().Raw("SELECT type FROM system.columns WHERE database=currentDatabase() AND table=? AND name='ts'", name).Scan(&columnType).Error)
					want := "DateTime"
					if timezone != "" {
						want = "DateTime('Asia/Shanghai')"
					}
					if columnType != want {
						t.Fatalf("timezone type=%s want=%s", columnType, want)
					}
					check(t, store.GetDB().Exec("INSERT INTO "+name+" VALUES (7, now())").Error)
					var id int64
					check(t, store.GetDB().Raw("SELECT id FROM "+name).Scan(&id).Error)
					if id != 7 {
						t.Fatalf("wrong created table data: %d", id)
					}
					check(t, engine.Rollback(ctx, migration.ID, migration))
					var count int64
					check(t, store.GetDB().Raw("SELECT count() FROM system.tables WHERE database=currentDatabase() AND name=?", name).Scan(&count).Error)
					if count != 0 {
						t.Fatal("generic table rollback did not drop table")
					}
				}
				var database string
				check(t, store.GetDB().Raw("SELECT currentDatabase()").Scan(&database).Error)
				table := &schema.Table{
					Database: database, LocalName: "qualified_rows", DBType: schema.CKDBTypeClickhouse,
					Columns:   []*schema.Column{schema.NewColumn("id", schema.UInt64)},
					Engine:    schema.TableEngine{Name: schema.EngineTypeMergeTree},
					OrderKeys: []string{"id"}, PrimaryKeyCount: 1,
				}
				check(t, store.GetDB().Exec(table.RenderLocalTableCreateSQL()).Error)
				check(t, store.GetDB().Exec(table.RenderDropSQL()).Error)
			}

		})
	}
}

// TestAdvisoryMigrationLock exercises independent connection pools against the
// same live server. A second worker must wait for the first worker to commit
// its migration and history record, not replay the Up callback.
func TestAdvisoryMigrationLock(t *testing.T) {
	for _, fixture := range []struct {
		name, env string
		driver    sqlstore.Driver
	}{
		{"postgres", "STOREKIT_TEST_POSTGRES_DSN", postgres.Open},
		{"mysql", "STOREKIT_TEST_MYSQL_DSN", mysql.Open},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			dsn := os.Getenv(fixture.env)
			if dsn == "" {
				t.Fatal("integration requires disposable database fixture")
			}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			open := func() *sqlstore.Store {
				t.Helper()
				cfg := &sqlstore.DatabaseConfig{
					Type: fixture.name, ConnectionString: dsn, MaxOpenConns: 2,
					MaxIdleConns: 2, LogLevel: logger.Silent,
				}
				store, err := sqlstore.Open(cfg, fixture.driver, log, noop.NewTracerProvider())
				check(t, err)
				t.Cleanup(func() { check(t, store.Close()) })
				return store
			}
			first := open()
			second := open()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			const historyTable = "migration_advisory_lock_test"
			firstEngine := migrator.New(first.GetDB(), log, migrator.WithHistoryTable(historyTable))
			secondEngine := migrator.New(second.GetDB(), log, migrator.WithHistoryTable(historyTable))
			t.Cleanup(func() { check(t, first.GetDB().Exec("DROP TABLE IF EXISTS " + historyTable).Error) })

			entered := make(chan struct{})
			unblock := make(chan struct{})
			var calls atomic.Int32
			migration := &migrator.Migration{
				ID: "exclusive_worker",
				Up: func(ctx context.Context, mg migrator.MigrationContext) error {
					if calls.Add(1) == 1 {
						close(entered)
						select {
						case <-unblock:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					return nil
				},
			}
			firstDone := make(chan error, 1)
			go func() { firstDone <- firstEngine.Run(ctx, migration) }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("first worker did not enter migration", ctx.Err())
			}

			// A waiting worker's Context must cancel without entering Up.
			waitCtx, stopWaiting := context.WithTimeout(ctx, 200*time.Millisecond)
			defer stopWaiting()
			err := secondEngine.Run(waitCtx, migration)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected cancelled lock wait, got %v", err)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("second worker entered Up while first held lock: %d", got)
			}

			// Retry the waiting worker; it must observe the committed history
			// after the first releases the advisory lock and skip the Up body.
			secondDone := make(chan error, 1)
			go func() { secondDone <- secondEngine.Run(ctx, migration) }()
			select {
			case err := <-secondDone:
				t.Fatalf("second worker finished before lock release: %v", err)
			case <-time.After(200 * time.Millisecond):
			}
			close(unblock)
			select {
			case err := <-firstDone:
				check(t, err)
			case <-ctx.Done():
				t.Fatal("first worker timed out", ctx.Err())
			}
			select {
			case err := <-secondDone:
				check(t, err)
			case <-ctx.Done():
				t.Fatal("second worker timed out", ctx.Err())
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("migration Up ran %d times, expected exactly once", got)
			}
			history, err := firstEngine.GetHistory(ctx, 0)
			check(t, err)
			if len(history) != 1 || !history[0].Success {
				t.Fatalf("unexpected migration history: %+v", history)
			}
		})
	}
}
