package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRetryableErrors(t *testing.T) {
	d := Open("").(*dialector)
	for _, tc := range []struct {
		code  string
		retry bool
	}{
		{"40001", true}, {"40P01", true}, {"23505", false}, {"23503", false}, {"42601", false}, {"08006", false}, {"57014", false},
	} {
		err := fmt.Errorf("query: %w", &pgconn.PgError{Code: tc.code})
		require.Equal(t, tc.retry, d.IsRetryable(err), "code=%s", tc.code)
	}
	require.False(t, d.IsRetryable(errors.New("serialization failure")))
	require.ErrorIs(t, d.Translate(&pgconn.PgError{Code: "23505"}), gorm.ErrDuplicatedKey)
	var _ gorm.SavePointerDialectorInterface = d
	var _ gorm.ErrorTranslator = d
}
