// Package postgres provides the opt-in postgres driver for sqlstore.
package postgres

import (
	"errors"

	gormdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Open constructs a dialector for sqlstore.Open. It does not open a connection.
func Open(dsn string) gorm.Dialector {
	return &dialector{gormdriver.Open(dsn).(*gormdriver.Dialector)}
}

type dialector struct{ *gormdriver.Dialector }

func (d *dialector) IsRetryable(err error) bool {
	var pgErr interface{ SQLState() string }
	if !errors.As(err, &pgErr) {
		return false
	}
	// Serialization failure and deadlock: PostgreSQL aborts the transaction.
	return pgErr.SQLState() == "40001" || pgErr.SQLState() == "40P01"
}
