//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/datafoxlabs/storekit/migrator"
	"github.com/datafoxlabs/storekit/sqlstore"
	"github.com/datafoxlabs/storekit/sqlstore/mysql"
	"github.com/datafoxlabs/storekit/sqlstore/postgres"
	"go.opentelemetry.io/otel/trace/noop"
	"gorm.io/gorm/logger"
)

// TestMigrationLockSessionLoss simulates losing the exact session that holds
// the advisory lock AFTER a DDL statement was issued. The migration must never
// record success using an unrelated pooled connection after the lock is lost.
func TestMigrationLockSessionLoss(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		env    string
		driver sqlstore.Driver
	}{
		{"postgres", "STOREKIT_TEST_POSTGRES_DSN", postgres.Open},
		{"mysql", "STOREKIT_TEST_MYSQL_DSN", mysql.Open},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			dsn := os.Getenv(fixture.env)
			if dsn == "" {
				t.Fatal("disposable PostgreSQL/MySQL fixtures required")
			}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			open := func() *sqlstore.Store {
				t.Helper()
				store, err := sqlstore.Open(&sqlstore.DatabaseConfig{
					Type: fixture.name, ConnectionString: dsn,
					MaxOpenConns: 2, MaxIdleConns: 2, LogLevel: logger.Silent,
				}, fixture.driver, log, noop.NewTracerProvider())
				check(t, err)
				t.Cleanup(func() { check(t, store.Close()) })
				return store
			}
			worker := open()
			killer := open()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			table := "storekit_session_loss_" + fixture.name
			history := "storekit_loss_history_" + fixture.name
			t.Cleanup(func() {
				check(t, killer.GetDB().Exec("DROP TABLE IF EXISTS "+table).Error)
				check(t, killer.GetDB().Exec("DROP TABLE IF EXISTS "+history).Error)
			})
			engine := migrator.New(worker.GetDB(), log, migrator.WithHistoryTable(history))
			var calls int
			killOnce := true
			migration := &migrator.Migration{
				ID: "session_lost_after_ddl",
				Up: func(_ context.Context, mg migrator.MigrationContext) error {
					calls++
					if err := mg.RawSQL("CREATE TABLE IF NOT EXISTS " + table + " (id INTEGER)"); err != nil {
						return err
					}
					if !killOnce {
						return nil
					}
					killOnce = false
					var sessionID int64
					identitySQL := "SELECT pg_backend_pid()"
					if fixture.name == "mysql" {
						identitySQL = "SELECT CONNECTION_ID()"
					}
					if err := mg.GetDB().Raw(identitySQL).Scan(&sessionID).Error; err != nil {
						return err
					}
					if fixture.name == "postgres" {
						var terminated bool
						if err := killer.GetDB().Raw("SELECT pg_terminate_backend(?)", sessionID).Scan(&terminated).Error; err != nil {
							return err
						}
						if !terminated {
							return fmt.Errorf("PostgreSQL session %d was not terminated", sessionID)
						}
					} else {
						// The ID is returned from the database, not caller input.
						if err := killer.GetDB().Exec(fmt.Sprintf("KILL CONNECTION %d", sessionID)).Error; err != nil {
							return err
						}
					}
					return nil
				},
			}
			err := engine.Run(ctx, migration)
			if err == nil {
				t.Fatal("lost advisory-lock session must fail migration")
			}
			// Use an unrelated surviving pool for verification: the engine's
			// pinned session was deliberately terminated.
			var succeeded int64
			check(t, killer.GetDB().Table(history).Where("migration_id = ? AND success = ?", migration.ID, true).Count(&succeeded).Error)
			if succeeded != 0 {
				t.Fatal("history recorded success after advisory-lock session loss")
			}
			if fixture.name == "postgres" && killer.GetDB().Migrator().HasTable(table) {
				t.Fatal("PostgreSQL transactional DDL was not rolled back on session loss")
			}
			if fixture.name == "mysql" && !killer.GetDB().Migrator().HasTable(table) {
				t.Fatal("MySQL non-transactional DDL must be explicitly recovered")
			}
			// A new executor with the same migration ID can recover safely
			// only because this migration uses IF NOT EXISTS.
			recovery := migrator.New(killer.GetDB(), log, migrator.WithHistoryTable(history))
			check(t, recovery.Run(ctx, migration))
			check(t, recovery.Run(ctx, migration))
			if calls != 2 {
				t.Fatalf("migration replayed %d times, expected first attempt + recovery", calls)
			}
			historyRows, err := recovery.GetHistory(ctx, 0)
			check(t, err)
			if len(historyRows) != 1 || !historyRows[0].Success {
				t.Fatalf("unexpected recovery history: %+v", historyRows)
			}
		})
	}
}

// Regression: a pinned migration session does not deadlock a pool with only
// one connection when the callback uses the supplied MigrationContext.
func TestMigrationSingleConnectionPool(t *testing.T) {
	dsn := os.Getenv("STOREKIT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("disposable PostgreSQL fixture required")
	}
	store, err := sqlstore.Open(&sqlstore.DatabaseConfig{
		Type: "postgres", ConnectionString: dsn, MaxOpenConns: 1,
		MaxIdleConns: 1, LogLevel: logger.Silent,
	}, postgres.Open, slog.Default(), noop.NewTracerProvider())
	check(t, err)
	t.Cleanup(func() { check(t, store.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	engine := migrator.New(store.GetDB(), slog.Default(), migrator.WithHistoryTable("single_connection_lock_history"))
	t.Cleanup(func() { check(t, store.GetDB().Exec("DROP TABLE IF EXISTS single_connection_lock_history").Error) })
	m := &migrator.Migration{
		ID: "single_conn",
		Up: func(_ context.Context, mg migrator.MigrationContext) error {
			return mg.RawSQL("SELECT 1")
		},
	}
	check(t, engine.Run(ctx, m))
	check(t, engine.Run(ctx, m))
}
