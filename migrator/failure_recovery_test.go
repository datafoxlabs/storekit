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

// Simulate failures at the durable history boundary without replacing the
// database under test. Custom history stores do not opt in to transaction
// binding, so their post-commit failure window remains observable.
type faultingHistoryStore struct {
	migrator.HistoryStore
	recordSuccessErr error
	recordFailureErr error
	deleteErr        error
}

func (h *faultingHistoryStore) RecordSuccess(ctx context.Context, id, description string) error {
	if h.recordSuccessErr != nil {
		err := h.recordSuccessErr
		h.recordSuccessErr = nil
		return err
	}
	return h.HistoryStore.RecordSuccess(ctx, id, description)
}

func (h *faultingHistoryStore) RecordFailure(ctx context.Context, id, description string, cause error) error {
	if h.recordFailureErr != nil {
		err := h.recordFailureErr
		h.recordFailureErr = nil
		return err
	}
	return h.HistoryStore.RecordFailure(ctx, id, description, cause)
}

func (h *faultingHistoryStore) DeleteMigration(ctx context.Context, id string) error {
	if h.deleteErr != nil {
		err := h.deleteErr
		h.deleteErr = nil
		return err
	}
	return h.HistoryStore.DeleteMigration(ctx, id)
}

func TestHistoryWriteFailureRollsBackTransactionalDDL(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	engine := migrator.New(db, slog.Default())
	require.NoError(t, engine.Run(ctx, &migrator.Migration{
		ID: "bootstrap_history",
		Up: func(context.Context, migrator.MigrationContext) error { return nil },
	}))

	// Fail only successful history INSERTs, allowing the failure marker.
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_success BEFORE INSERT ON migration_log
		WHEN NEW.migration_id = 'atomic_create' AND NEW.success = 1
		BEGIN SELECT RAISE(ABORT, 'injected history failure'); END`).Error)

	upCalls := 0
	migration := &migrator.Migration{
		ID: "atomic_create",
		Up: func(_ context.Context, mg migrator.MigrationContext) error {
			upCalls++
			return mg.RawSQL("CREATE TABLE atomic_create (id INTEGER)")
		},
	}
	err := engine.Run(ctx, migration)
	require.ErrorContains(t, err, "injected history failure")
	require.False(t, db.Migrator().HasTable("atomic_create"), "DDL must roll back with its history marker")

	failed, err := engine.GetFailedMigrations(ctx)
	require.NoError(t, err)
	require.Len(t, failed, 1)
	require.Equal(t, "atomic_create", failed[0].MigrationID)

	// Recovery after the injected fault: failed history becomes success.
	require.NoError(t, db.Exec("DROP TRIGGER reject_success").Error)
	require.NoError(t, engine.Run(ctx, migration))
	require.True(t, db.Migrator().HasTable("atomic_create"))
	require.NoError(t, engine.Run(ctx, migration))
	require.Equal(t, 2, upCalls, "successful history must prevent replay")
	failed, err = engine.GetFailedMigrations(ctx)
	require.NoError(t, err)
	require.Empty(t, failed)
}

func TestHistoryDeletionFailureRollsBackTransactionalDown(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	engine := migrator.New(db, slog.Default())
	migration := &migrator.Migration{
		ID: "atomic_drop",
		Up: func(_ context.Context, mg migrator.MigrationContext) error {
			return mg.RawSQL("CREATE TABLE atomic_drop (id INTEGER)")
		},
		Down: func(_ context.Context, mg migrator.MigrationContext) error {
			return mg.RawSQL("DROP TABLE atomic_drop")
		},
	}
	require.NoError(t, engine.Run(ctx, migration))
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_delete BEFORE DELETE ON migration_log
		WHEN OLD.migration_id = 'atomic_drop'
		BEGIN SELECT RAISE(ABORT, 'injected history deletion failure'); END`).Error)

	err := engine.Rollback(ctx, migration.ID, migration)
	require.ErrorContains(t, err, "injected history deletion failure")
	require.True(t, db.Migrator().HasTable("atomic_drop"), "Down must roll back when history deletion fails")
	history, err := engine.GetHistory(ctx, 0)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.True(t, history[0].Success)

	require.NoError(t, db.Exec("DROP TRIGGER reject_delete").Error)
	require.NoError(t, engine.Rollback(ctx, migration.ID, migration))
	require.False(t, db.Migrator().HasTable("atomic_drop"))
	history, err = engine.GetHistory(ctx, 0)
	require.NoError(t, err)
	require.Empty(t, history)
}

func TestCustomHistoryCommitFailureIsReturnedAndRecoverable(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	cause := errors.New("injected success marker failure")
	history := &faultingHistoryStore{
		HistoryStore:     migrator.NewDefaultHistoryStore(db, slog.Default()),
		recordSuccessErr: cause,
	}
	engine := migrator.New(db, slog.Default(),
		migrator.WithHistoryFactory(func(*gorm.DB, *slog.Logger, migrator.Dialect) migrator.HistoryStore {
			return history
		}))
	calls := 0
	migration := &migrator.Migration{
		ID: "custom_recovery",
		Up: func(_ context.Context, mg migrator.MigrationContext) error {
			calls++
			// Custom stores cannot be atomically coupled to Up by this engine:
			// callers must make recovery re-entrant for this crash window.
			return mg.RawSQL("CREATE TABLE IF NOT EXISTS custom_recovery (id INTEGER)")
		},
	}
	require.ErrorIs(t, engine.Run(ctx, migration), cause)
	require.True(t, db.Migrator().HasTable("custom_recovery"))
	historyRows, err := engine.GetHistory(ctx, 0)
	require.NoError(t, err)
	require.Empty(t, historyRows)

	require.NoError(t, engine.Run(ctx, migration))
	require.NoError(t, engine.Run(ctx, migration))
	require.Equal(t, 2, calls)
	historyRows, err = engine.GetHistory(ctx, 0)
	require.NoError(t, err)
	require.Len(t, historyRows, 1)
	require.True(t, historyRows[0].Success)
}

func TestContinueOnErrorReturnsFailuresWhileRunningRemainingMigrations(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	cause := errors.New("migration failed intentionally")
	engine := migrator.New(db, slog.Default(), migrator.WithOptions(&migrator.Options{
		ContinueOnError: true,
	}))
	err := engine.Run(ctx,
		&migrator.Migration{
			ID: "failed_first",
			Up: func(context.Context, migrator.MigrationContext) error { return cause },
		},
		&migrator.Migration{
			ID: "successful_second",
			Up: func(_ context.Context, mg migrator.MigrationContext) error {
				return mg.RawSQL("CREATE TABLE successful_second (id INTEGER)")
			},
		},
	)
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "1 failed, 1 succeeded")
	require.True(t, db.Migrator().HasTable("successful_second"))
	history, err := engine.GetHistory(ctx, 0)
	require.NoError(t, err)
	require.Len(t, history, 2)
}

func TestHistoryFailureWriteErrorIsNotDiscarded(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	upErr := errors.New("up failure")
	historyErr := errors.New("history failure")
	history := &faultingHistoryStore{
		HistoryStore:     migrator.NewDefaultHistoryStore(db, slog.Default()),
		recordFailureErr: historyErr,
	}
	engine := migrator.New(db, slog.Default(),
		migrator.WithHistoryFactory(func(*gorm.DB, *slog.Logger, migrator.Dialect) migrator.HistoryStore {
			return history
		}))
	err := engine.Run(ctx, &migrator.Migration{
		ID: "both_failed",
		Up: func(context.Context, migrator.MigrationContext) error { return upErr },
	})
	require.ErrorIs(t, err, upErr)
	require.ErrorIs(t, err, historyErr)
}

func TestContinueOnErrorPreservesPriorFailureWhenHistorySuccessWriteFails(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	upErr := errors.New("first migration failed")
	historyErr := errors.New("second migration history write failed")
	history := &faultingHistoryStore{
		HistoryStore:     migrator.NewDefaultHistoryStore(db, slog.Default()),
		recordSuccessErr: historyErr,
	}
	engine := migrator.New(db, slog.Default(),
		migrator.WithOptions(&migrator.Options{ContinueOnError: true}),
		migrator.WithHistoryFactory(func(*gorm.DB, *slog.Logger, migrator.Dialect) migrator.HistoryStore {
			return history
		}),
	)

	thirdRan := false
	err := engine.Run(ctx,
		&migrator.Migration{
			ID: "prior_failure",
			Up: func(context.Context, migrator.MigrationContext) error { return upErr },
		},
		&migrator.Migration{
			ID: "history_write_failure",
			Up: func(context.Context, migrator.MigrationContext) error { return nil },
		},
		&migrator.Migration{
			ID: "must_not_run",
			Up: func(context.Context, migrator.MigrationContext) error {
				thirdRan = true
				return nil
			},
		},
	)
	require.ErrorIs(t, err, upErr, "return must retain prior migration errors")
	require.ErrorIs(t, err, historyErr, "return must retain the history-write error")
	require.False(t, thirdRan, "history success-write failure must stop the batch")
}
