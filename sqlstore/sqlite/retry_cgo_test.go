//go:build cgo

package sqlite

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetryableErrors(t *testing.T) {
	d := Open("").(*dialector)
	for _, tc := range []struct {
		code  sqlite3.ErrNo
		retry bool
	}{
		{sqlite3.ErrBusy, true}, {sqlite3.ErrLocked, false}, {sqlite3.ErrConstraint, false}, {sqlite3.ErrIoErr, false},
	} {
		err := fmt.Errorf("query: %w", sqlite3.Error{Code: tc.code})
		require.Equal(t, tc.retry, d.IsRetryable(err))
	}
	require.False(t, d.IsRetryable(errors.New("database is locked")))
	var _ gorm.SavePointerDialectorInterface = d
	var _ gorm.ErrorTranslator = d
}
