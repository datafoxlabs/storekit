package migrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClickHouseDialectPreservesExplicitTimezoneSQL(t *testing.T) {
	dialect := &ClickHouseDialect{Timezone: "Asia/Shanghai"}
	statements, err := dialect.CreateTableSQL(&Table{
		Name: "history_test",
		Columns: []*Column{
			{
				Name:       "timestamp",
				Type:       TypeTimestamp,
				PrimaryKey: true,
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, statements, 1)
	assert.Contains(t, statements[0], "CREATE TABLE IF NOT EXISTS `history_test`")
	assert.Contains(t, statements[0], "`timestamp` DateTime('Asia/Shanghai')")
	statements, err = dialect.AddColumnSQL("history_test", &Column{
		Name: "created_at",
		Type: TypeTimestamp,
	})
	require.NoError(t, err)
	require.Len(t, statements, 1)
	assert.Equal(t, "ALTER TABLE history_test ADD COLUMN IF NOT EXISTS `created_at` DateTime('Asia/Shanghai') CODEC(DoubleDelta)", statements[0])
}

func TestClickHouseDialectTimezoneIsExplicit(t *testing.T) {
	for _, tc := range []struct{ timezone, column string }{
		{"", "`created_at` DateTime CODEC(DoubleDelta)"},
		{"UTC", "`created_at` DateTime('UTC') CODEC(DoubleDelta)"},
	} {
		t.Run(tc.timezone, func(t *testing.T) {
			dialect := &ClickHouseDialect{Timezone: tc.timezone}
			statements, err := dialect.AddColumnSQL("events", &Column{Name: "created_at", Type: TypeTimestamp})
			require.NoError(t, err)
			require.Equal(t, []string{"ALTER TABLE events ADD COLUMN IF NOT EXISTS " + tc.column}, statements)
			statements, err = dialect.CreateTableSQL(&Table{Name: "events", Columns: []*Column{{Name: "created_at", Type: TypeTimestamp, PrimaryKey: true}}})
			require.NoError(t, err)
			require.Contains(t, statements[0], tc.column)
		})
	}
}
