// Package sqlite provides the opt-in sqlite driver for sqlstore.
package sqlite

import (
	gormdriver "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Open constructs a dialector for sqlstore.Open. It does not open a connection.
func Open(dsn string) gorm.Dialector {
	return &dialector{gormdriver.Open(dsn).(*gormdriver.Dialector)}
}

type dialector struct{ *gormdriver.Dialector }
