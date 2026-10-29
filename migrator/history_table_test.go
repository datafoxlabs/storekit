package migrator_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/datafoxlabs/storekit/migrator"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHistoryTablesAreIndependent(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	ctx := context.Background()
	for _, table := range []string{migrator.DefaultHistoryTable, "first_history", "second_history"} {
		t.Run(table, func(t *testing.T) {
			engine := migrator.New(db, slog.Default(), migrator.WithHistoryTable(table))
			attempts := 0
			cause := errors.New("retry me")
			migration := &migrator.Migration{
				ID: "same_id",
				Up: func(context.Context, migrator.MigrationContext) error {
					attempts++
					if attempts == 1 {
						return cause
					}
					return nil
				},
				Down: func(context.Context, migrator.MigrationContext) error { return nil },
			}
			require.ErrorIs(t, engine.Run(ctx, migration), cause)
			failed, err := engine.GetFailedMigrations(ctx)
			require.NoError(t, err)
			require.Len(t, failed, 1)
			require.Equal(t, "migration failed in transaction: retry me", failed[0].ErrorMsg)
			require.NoError(t, engine.Run(ctx, migration))
			require.NoError(t, engine.Run(ctx, migration))
			require.Equal(t, 2, attempts)
			history, err := engine.GetHistory(ctx, 1)
			require.NoError(t, err)
			require.Len(t, history, 1)
			require.True(t, history[0].Success)
			require.Empty(t, history[0].ErrorMsg)
			failed, err = engine.GetFailedMigrations(ctx)
			require.NoError(t, err)
			require.Empty(t, failed)
		})
	}
	// Deleting one table's record must not affect either other history.
	first := migrator.New(db, slog.Default(), migrator.WithHistoryTable("first_history"))
	require.NoError(t, first.Rollback(ctx, "same_id", &migrator.Migration{
		Down: func(context.Context, migrator.MigrationContext) error { return nil },
	}))
	for _, table := range []string{migrator.DefaultHistoryTable, "first_history", "second_history"} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		want := int64(1)
		if table == "first_history" {
			want = 0
		}
		require.Equal(t, want, count, table)
	}
}

func TestInvalidHistoryTableFailsBeforeFactoryOrSQL(t *testing.T) {
	db := setupTestDB(t)
	for _, name := range []string{"", "db.history", "history; DROP TABLE x", "history-name", "1history"} {
		t.Run(name, func(t *testing.T) {
			require.Panics(t, func() { migrator.New(db, slog.Default(), migrator.WithHistoryTable(name)) })
		})
	}
	factory := migrator.WithHistoryFactory(func(*gorm.DB, *slog.Logger, migrator.Dialect) migrator.HistoryStore {
		t.Fatal("factory must not run with conflicting options")
		return nil
	})
	for _, opts := range [][]migrator.Option{
		{factory, migrator.WithHistoryTable("custom_history")},
		{migrator.WithHistoryTable("custom_history"), factory},
	} {
		require.Panics(t, func() { migrator.New(db, slog.Default(), opts...) })
	}
	require.False(t, db.Migrator().HasTable(migrator.DefaultHistoryTable))
	require.False(t, db.Migrator().HasTable("custom_history"))
}

// A name-only dialector is sufficient to exercise constructor validation;
// any attempted database operation would fail because no connection exists.
type clickHouseNameOnly struct{ gorm.Dialector }

func (clickHouseNameOnly) Name() string { return "clickhouse" }

func TestTimezoneOptionsValidateBeforeHistoryConstruction(t *testing.T) {
	db := &gorm.DB{Config: &gorm.Config{Dialector: clickHouseNameOnly{}}}
	calls := 0
	var dialect *migrator.ClickHouseDialect
	factory := migrator.WithHistoryFactory(func(_ *gorm.DB, _ *slog.Logger, d migrator.Dialect) migrator.HistoryStore {
		calls++
		dialect = d.(*migrator.ClickHouseDialect)
		return &fakeHistoryStore{}
	})
	require.Panics(t, func() { migrator.New(db, slog.Default(), factory, migrator.WithClickHouseTimezone("not/a/timezone")) })
	require.Zero(t, calls)
	migrator.New(db, slog.Default(), factory, migrator.WithClickHouseTimezone("UTC"), migrator.WithClickHouseTimezone("Asia/Shanghai"))
	require.Equal(t, "Asia/Shanghai", dialect.Timezone)
	migrator.New(db, slog.Default(), factory, migrator.WithClickHouseTimezone("UTC"), migrator.WithClickHouseTimezone(""))
	require.Empty(t, dialect.Timezone)
	require.Panics(t, func() { migrator.New(setupTestDB(t), slog.Default(), migrator.WithClickHouseTimezone("UTC")) })
}
