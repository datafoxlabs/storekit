package sqlstore

import "errors"

var (
	// ErrPingFailed identifies a failed connection health check.
	ErrPingFailed = errors.New("database ping failed")
	// ErrMigrationFailed identifies a failed schema or registered migration.
	ErrMigrationFailed = errors.New("database migration failed")
)
