package mysql

import (
	"errors"
	"fmt"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetryableErrors(t *testing.T) {
	d := Open("").(*dialector)
	for _, tc := range []struct {
		code  uint16
		retry bool
	}{
		{1205, true}, {1213, true}, {1062, false}, {1045, false}, {1064, false}, {2006, false}, {2013, false},
	} {
		err := fmt.Errorf("query: %w", &mysqldriver.MySQLError{Number: tc.code})
		require.Equal(t, tc.retry, d.IsRetryable(err), "code=%d", tc.code)
	}
	require.False(t, d.IsRetryable(errors.New("deadlock")))
	require.ErrorIs(t, d.Translate(&mysqldriver.MySQLError{Number: 1062}), gorm.ErrDuplicatedKey)
	var _ gorm.SavePointerDialectorInterface = d
	var _ gorm.ErrorTranslator = d
}
