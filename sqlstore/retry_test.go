//go:build cgo

package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetryBusinessErrorDoesNotRepeatTransaction(t *testing.T) {
	for _, api := range []string{"Tx", "InTransaction", "Exec"} {
		t.Run(api, func(t *testing.T) {
			store := setupTestStore(t)
			require.NoError(t, store.GetDB().Exec("CREATE TABLE retry_rows (id INTEGER PRIMARY KEY)").Error)
			cause := errors.New("business rule rejected")
			calls := 0
			write := func(db *gorm.DB) error {
				calls++
				if err := db.Exec("INSERT INTO retry_rows VALUES (1)").Error; err != nil {
					return err
				}
				return cause
			}
			ctx := context.Background()
			var err error
			switch api {
			case "Tx":
				err = store.Tx(ctx, write)
			case "InTransaction":
				err = store.InTransaction(ctx, func(ctx context.Context) error { return store.Exec(ctx, write) })
			case "Exec":
				err = store.Exec(ctx, write)
			}
			require.ErrorIs(t, err, cause)
			require.Equal(t, 1, calls)
			var count int64
			require.NoError(t, store.GetDB().Table("retry_rows").Count(&count).Error)
			want := int64(0)
			if api == "Exec" {
				want = 1
			}
			require.Equal(t, want, count)
		})
	}
}

func TestExecInTransactionRetriesWholeTransaction(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.GetDB().Exec("CREATE TABLE retry_rows (id INTEGER PRIMARY KEY)").Error)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transactions, operations := 0, 0
	err := store.InTransaction(ctx, func(txCtx context.Context) error {
		transactions++
		return store.Exec(txCtx, func(db *gorm.DB) error {
			operations++
			if err := db.Exec("INSERT INTO retry_rows VALUES (1)").Error; err != nil {
				return err
			}
			if transactions == 1 {
				return sqlite3.Error{Code: sqlite3.ErrBusy}
			}
			return nil
		})
	})
	require.NoError(t, err)
	require.Equal(t, 2, transactions)
	require.Equal(t, 2, operations, "Exec must not retry inside an existing transaction")
	var count int64
	require.NoError(t, store.GetDB().Table("retry_rows").Count(&count).Error)
	require.Equal(t, int64(1), count, "the first transaction must roll back before retry")
}

func TestRetryTransientTransactionRollsBackBeforeRetry(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.GetDB().Exec("CREATE TABLE retry_rows (id INTEGER PRIMARY KEY)").Error)
	calls := 0
	err := store.Tx(context.Background(), func(db *gorm.DB) error {
		calls++
		if err := db.Exec("INSERT INTO retry_rows VALUES (1)").Error; err != nil {
			return err
		}
		if calls == 1 {
			return fmt.Errorf("busy snapshot: %w", sqlite3.Error{Code: sqlite3.ErrBusy, ExtendedCode: sqlite3.ErrBusySnapshot})
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	var count int64
	require.NoError(t, store.GetDB().Table("retry_rows").Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestRetryPreservesAttemptBudgetAndCause(t *testing.T) {
	for _, limit := range []int{-1, 0, 1, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			store := setupTestStore(t)
			store.config.QueryRetries = limit
			store.config.RetryInterval = 0
			cause := fmt.Errorf("locked database: %w", sqlite3.Error{Code: sqlite3.ErrBusy})
			calls := 0
			err := store.Exec(context.Background(), func(*gorm.DB) error { calls++; return cause })
			require.ErrorIs(t, err, cause)
			require.Equal(t, max(1, limit), calls)
		})
	}
}

func TestRetryDoesNotReplayUnknownOrAmbiguousErrors(t *testing.T) {
	for _, cause := range []error{errors.New("deadlock"), io.EOF, context.DeadlineExceeded, gorm.ErrDuplicatedKey} {
		t.Run(cause.Error(), func(t *testing.T) {
			store := setupTestStore(t)
			calls := 0
			err := store.Exec(context.Background(), func(*gorm.DB) error { calls++; return cause })
			require.ErrorIs(t, err, cause)
			require.Equal(t, 1, calls)
		})
	}
}

func TestRetryCancellationDuringBackoff(t *testing.T) {
	store := setupTestStore(t)
	store.config.RetryInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	calls := 0
	go func() {
		done <- store.Exec(ctx, func(*gorm.DB) error {
			calls++
			close(started)
			return sqlite3.Error{Code: sqlite3.ErrBusy}
		})
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("retry returned before cancellation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, calls)
	case <-time.After(time.Second):
		t.Fatal("cancellation waited for the retry interval")
	}
}

func TestRetryCanceledContextHasNoEffects(t *testing.T) {
	store := setupTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := store.Exec(ctx, func(*gorm.DB) error { calls++; return nil })
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls)
}

func TestRetryTranslatedDuplicateKeyFailsOnce(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.GetDB().Exec("CREATE TABLE retry_rows (id INTEGER PRIMARY KEY)").Error)
	require.NoError(t, store.GetDB().Exec("INSERT INTO retry_rows VALUES (1)").Error)
	calls := 0
	err := store.Tx(context.Background(), func(db *gorm.DB) error {
		calls++
		return db.Exec("INSERT INTO retry_rows VALUES (1)").Error
	})
	require.ErrorIs(t, err, gorm.ErrDuplicatedKey)
	require.Equal(t, 1, calls)
}

// A custom dialect has to opt in explicitly; known-looking errors alone must
// not enable retries for an unknown driver.
type noRetryDialect struct{ gorm.Dialector }

func TestRetryCustomDialectDefaultsToSingleAttempt(t *testing.T) {
	store := setupTestStore(t)
	store.db.Dialector = noRetryDialect{store.db.Dialector}
	cause := sqlite3.Error{Code: sqlite3.ErrBusy}
	calls := 0
	err := store.Exec(context.Background(), func(*gorm.DB) error { calls++; return cause })
	require.ErrorIs(t, err, cause)
	require.Equal(t, 1, calls)
}
