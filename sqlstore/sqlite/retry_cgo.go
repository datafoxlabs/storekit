//go:build cgo

package sqlite

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

func (d *dialector) IsRetryable(err error) bool {
	var sqliteErr sqlite3.Error
	// BUSY includes BUSY_SNAPSHOT: retry the transaction with a fresh snapshot.
	// LOCKED usually indicates a conflict on the same connection, not contention
	// that another attempt can resolve.
	return errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrBusy
}
