package migrator

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"time"
)

const migrationLockPollInterval = 50 * time.Millisecond

// acquireMigrationLock serializes Run and Rollback across independent
// PostgreSQL/MySQL processes connecting to the same database and history table.
// The lock is session-scoped: its dedicated connection stays pinned until
// release. Migrations use other pooled connections, so a pool capped at one
// connection is rejected before locking.
//
// SQLite and ClickHouse require single-executor coordination by the host.
// SQLite has no equivalent portable session advisory lock, and ClickHouse DDL
// is non-transactional; do not mistake this function for distributed fencing.
func (m *Migrator) acquireMigrationLock(ctx context.Context) (func() error, error) {
	dialect := m.dialect.Name()
	if dialect != "postgres" && dialect != "mysql" {
		return func() error { return nil }, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sqlDB, err := m.db.DB()
	if err != nil {
		return nil, fmt.Errorf("get database connection pool: %w", err)
	}
	if sqlDB.Stats().MaxOpenConnections == 1 {
		return nil, fmt.Errorf("%s migration lock requires at least two pooled connections (one held by the lock); increase MaxOpenConns", dialect)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserve migration lock session: %w", err)
	}
	closeOnError := func(err error) (func() error, error) {
		return nil, errors.Join(err, conn.Close())
	}

	// PostgreSQL advisory locks are scoped to the connected database. MySQL
	// named locks are server-wide, so include the selected database in its key.
	key := "storekit:migrator:" + m.historyTable
	if dialect == "mysql" {
		var database sql.NullString
		if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
			return closeOnError(fmt.Errorf("identify MySQL database: %w", err))
		}
		if !database.Valid || database.String == "" {
			return closeOnError(fmt.Errorf("MySQL migrator requires a selected database for lock scoping"))
		}
		key += ":" + database.String
	}

	var postgresKey int64
	var mysqlKey string
	if dialect == "postgres" {
		h := fnv.New64a()
		_, _ = h.Write([]byte(key))
		postgresKey = int64(h.Sum64())
	} else {
		sum := sha256.Sum256([]byte(key))
		mysqlKey = fmt.Sprintf("storekit:migrator:%x", sum[:20])
	}

	for {
		if err := ctx.Err(); err != nil {
			return closeOnError(err)
		}
		var acquired bool
		if dialect == "postgres" {
			if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", postgresKey).Scan(&acquired); err != nil {
				return closeOnError(fmt.Errorf("try PostgreSQL migration advisory lock: %w", err))
			}
		} else {
			var result sql.NullInt64
			if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", mysqlKey).Scan(&result); err != nil {
				return closeOnError(fmt.Errorf("try MySQL migration named lock: %w", err))
			}
			if !result.Valid {
				return closeOnError(fmt.Errorf("MySQL migration GET_LOCK returned NULL"))
			}
			acquired = result.Int64 == 1
		}
		if acquired {
			break
		}
		timer := time.NewTimer(migrationLockPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return closeOnError(ctx.Err())
		case <-timer.C:
		}
	}

	return func() error {
		// Release even when the Run context was cancelled. The server also
		// releases this lock when its owning session is closed.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		var releaseErr error
		if dialect == "postgres" {
			releaseErr = conn.QueryRowContext(cleanupCtx, "SELECT pg_advisory_unlock($1)", postgresKey).Scan(&released)
		} else {
			var result sql.NullInt64
			releaseErr = conn.QueryRowContext(cleanupCtx, "SELECT RELEASE_LOCK(?)", mysqlKey).Scan(&result)
			released = result.Valid && result.Int64 == 1
		}
		if releaseErr == nil && !released {
			releaseErr = fmt.Errorf("%s migration lock no longer held by the session", dialect)
		}
		return errors.Join(releaseErr, conn.Close())
	}, nil
}
