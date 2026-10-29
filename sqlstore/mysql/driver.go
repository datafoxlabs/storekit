// Package mysql provides the opt-in mysql driver for sqlstore.
package mysql

import (
	"errors"

	mysqldriver "github.com/go-sql-driver/mysql"
	gormdriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Open constructs a dialector for sqlstore.Open. It does not open a connection.
func Open(dsn string) gorm.Dialector {
	return &dialector{gormdriver.Open(dsn).(*gormdriver.Dialector)}
}

// Embed the concrete dialect to preserve GORM's optional interfaces, including
// ErrorTranslator and savepoints, while keeping driver errors out of sqlstore.
type dialector struct{ *gormdriver.Dialector }

func (d *dialector) IsRetryable(err error) bool {
	var mysqlErr *mysqldriver.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	// InnoDB deadlock and lock-wait timeout. Tx rolls back before retrying.
	return mysqlErr.Number == 1213 || mysqlErr.Number == 1205
}
