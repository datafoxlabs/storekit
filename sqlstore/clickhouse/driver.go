// Package clickhouse provides the opt-in clickhouse driver for sqlstore.
package clickhouse

import (
	gormdriver "gorm.io/driver/clickhouse"
	"gorm.io/gorm"
)

// Open constructs a dialector for sqlstore.Open. It does not open a connection.
func Open(dsn string) gorm.Dialector {
	return gormdriver.Open(dsn)
}
