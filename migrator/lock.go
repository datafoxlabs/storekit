package migrator

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"gorm.io/gorm"
)

const migrationLockPollInterval = 50 * time.Millisecond

// withMigrationLock pins one physical database connection for the entire
// migration. The advisory lock, Up/Down SQL and the built-in relational history
// store all run on that same session. If the session dies, operations through the
// migration context cannot silently move to a different connection and continue.
//
// A custom HistoryStore may use other connections, and a user callback may
// explicitly escape its MigrationContext. Those external effects are not fenced.
// SQLite and ClickHouse still require single-executor deployment coordination.
func (m *Migrator) withMigrationLock(ctx context.Context, fn func(*Migrator) error) error {
	dialect := m.dialect.Name()
	if dialect != "postgres" && dialect != "mysql" {
		return fn(m)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	return m.db.WithContext(ctx).Connection(func(connDB *gorm.DB) (runErr error) {
		key, err := acquireSessionLock(ctx, connDB, dialect, m.historyTable)
		if err != nil {
			return fmt.Errorf("failed to acquire migration lock: %w", err)
		}
		defer func() {
			// The caller's Context may have been canceled; lock cleanup still
			// needs a short independent deadline on the SAME pinned connection.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := releaseSessionLock(connDB.WithContext(cleanupCtx), dialect, key); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("failed to release migration lock: %w", err))
			}
		}()

		scoped := *m
		scoped.db = connDB
		if history, ok := m.history.(*DefaultHistoryStore); ok {
			scoped.history = history.withDB(connDB)
		}
		return fn(&scoped)
	})
}

// acquireSessionLock is called on the fixed GORM Connection's physical session.
// PostgreSQL advisory locks are per database; MySQL named locks are server-wide,
// so MySQL includes the selected database in the lock identity.
func acquireSessionLock(ctx context.Context, db *gorm.DB, dialect, historyTable string) (string, error) {
	key := "storekit:migrator:" + historyTable
	if dialect == "mysql" {
		var selected sql.NullString
		if err := db.WithContext(ctx).Raw("SELECT DATABASE()").Scan(&selected).Error; err != nil {
			return "", fmt.Errorf("identify MySQL database: %w", err)
		}
		if !selected.Valid || selected.String == "" {
			return "", fmt.Errorf("MySQL migrator requires a selected database for lock scoping")
		}
		key += ":" + selected.String
	}

	if dialect == "postgres" {
		h := fnv.New64a()
		_, _ = h.Write([]byte(key))
		key = fmt.Sprintf("%d", int64(h.Sum64()))
	} else {
		sum := sha256.Sum256([]byte(key))
		key = fmt.Sprintf("storekit:migrator:%x", sum[:20])
	}

	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var acquired bool
		if dialect == "postgres" {
			var result sql.NullBool
			if err := db.WithContext(ctx).Raw("SELECT pg_try_advisory_lock(CAST(? AS bigint))", key).Scan(&result).Error; err != nil {
				return "", fmt.Errorf("try PostgreSQL migration advisory lock: %w", err)
			}
			if !result.Valid {
				return "", fmt.Errorf("PostgreSQL migration advisory lock returned NULL")
			}
			acquired = result.Bool
		} else {
			var result sql.NullInt64
			if err := db.WithContext(ctx).Raw("SELECT GET_LOCK(?, 0)", key).Scan(&result).Error; err != nil {
				return "", fmt.Errorf("try MySQL migration named lock: %w", err)
			}
			if !result.Valid {
				return "", fmt.Errorf("MySQL migration GET_LOCK returned NULL")
			}
			acquired = result.Int64 == 1
		}
		if acquired {
			return key, nil
		}
		timer := time.NewTimer(migrationLockPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

func releaseSessionLock(db *gorm.DB, dialect, key string) error {
	if dialect == "postgres" {
		var released sql.NullBool
		if err := db.Raw("SELECT pg_advisory_unlock(CAST(? AS bigint))", key).Scan(&released).Error; err != nil {
			return fmt.Errorf("PostgreSQL advisory unlock: %w", err)
		}
		if !released.Valid || !released.Bool {
			return fmt.Errorf("PostgreSQL migration advisory lock no longer held by session")
		}
		return nil
	}
	var released sql.NullInt64
	if err := db.Raw("SELECT RELEASE_LOCK(?)", key).Scan(&released).Error; err != nil {
		return fmt.Errorf("MySQL named lock release: %w", err)
	}
	if !released.Valid || released.Int64 != 1 {
		return fmt.Errorf("MySQL migration named lock no longer held by session")
	}
	return nil
}
