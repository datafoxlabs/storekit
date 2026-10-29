package integration_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datafoxlabs/storekit/clickhouse/schema"
	"github.com/datafoxlabs/storekit/migrator"
	"github.com/datafoxlabs/storekit/sqlstore"
	"github.com/datafoxlabs/storekit/sqlstore/sqlite"
	"go.opentelemetry.io/otel/trace/noop"
	"gorm.io/gorm"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSchema(t *testing.T) {
	table := &schema.Table{
		Database:        "consumer",
		LocalName:       "events",
		Engine:          schema.TableEngine{Name: schema.EngineTypeMergeTree},
		OrderKeys:       []string{"id"},
		PrimaryKeyCount: 1,
		Columns:         []*schema.Column{schema.NewColumn("id", schema.UInt64)},
	}
	if ddl := table.RenderLocalTableCreateSQL(); !strings.Contains(ddl, "consumer.`events`") {
		t.Fatal("schema lost target identity")
	}
}

func TestVersionedStoreAndMigrator(t *testing.T) {
	ctx := context.Background()
	cfg, err := sqlstore.NewDatabaseConfig(sqlstore.WithConnectionString(filepath.Join(t.TempDir(), "consumer.db")))
	check(t, err)
	store, err := sqlstore.Open(cfg, sqlite.Open, slog.Default(), noop.NewTracerProvider())
	check(t, err)
	t.Cleanup(func() { check(t, store.Close()) })
	check(t, store.Ping(ctx))
	engine := migrator.New(store.GetDB(), slog.Default())
	calls := 0
	create := &migrator.Migration{
		ID: "consumer_v1",
		Up: func(_ context.Context, mg migrator.MigrationContext) error {
			calls++
			return mg.RawSQL("CREATE TABLE consumer_rows (id INTEGER PRIMARY KEY)")
		},
		Down: func(_ context.Context, mg migrator.MigrationContext) error { return mg.DropTable("consumer_rows") },
	}
	check(t, store.RunMigrations(ctx, create))
	check(t, store.RunMigrations(ctx, create))
	if calls != 1 {
		t.Fatalf("migration executed %d times", calls)
	}
	history, err := engine.GetHistory(ctx, 10)
	check(t, err)
	if len(history) != 1 || !history[0].Success || history[0].MigrationID != create.ID {
		t.Fatalf("unexpected history: %+v", history)
	}
	rollback := errors.New("rollback transaction")
	err = store.InTransaction(ctx, func(txCtx context.Context) error {
		check(t, store.Exec(txCtx, func(db *gorm.DB) error {
			return db.Exec("INSERT INTO consumer_rows VALUES (1)").Error
		}))
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction lost cause: %v", err)
	}
	var count int64
	check(t, store.GetDB().Table("consumer_rows").Count(&count).Error)
	if count != 0 {
		t.Fatal("transaction propagation failed to roll back")
	}
	fail := true
	failed := &migrator.Migration{
		ID: "consumer_v2",
		Up: func(_ context.Context, mg migrator.MigrationContext) error {
			if err := mg.RawSQL("INSERT INTO consumer_rows VALUES (2)"); err != nil {
				return err
			}
			if fail {
				return rollback
			}
			return nil
		},
	}
	err = store.RunMigrations(ctx, failed)
	if !errors.Is(err, sqlstore.ErrMigrationFailed) || !errors.Is(err, rollback) {
		t.Fatalf("migration lost error identity: %v", err)
	}
	check(t, store.GetDB().Table("consumer_rows").Count(&count).Error)
	if count != 0 {
		t.Fatal("failed migration left partial data")
	}
	failures, err := engine.GetFailedMigrations(ctx)
	check(t, err)
	if len(failures) != 1 || failures[0].MigrationID != failed.ID {
		t.Fatalf("unexpected failure history: %+v", failures)
	}
	fail = false
	check(t, store.RunMigrations(ctx, failed))
	check(t, store.GetDB().Table("consumer_rows").Count(&count).Error)
	if count != 1 {
		t.Fatal("retry did not complete migration")
	}
	check(t, engine.Rollback(ctx, create.ID, create))
	if store.GetDB().Migrator().HasTable("consumer_rows") {
		t.Fatal("rollback did not remove table")
	}
}
