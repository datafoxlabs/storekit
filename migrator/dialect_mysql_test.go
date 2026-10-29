package migrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMySQLDialect_NameAndTxSupport(t *testing.T) {
	d := &MySQLDialect{}
	assert.Equal(t, "mysql", d.Name())
	assert.False(t, d.SupportsTransactions())
}

func TestMySQLDialect_CreateTableSQL(t *testing.T) {
	d := &MySQLDialect{}

	sqls, err := d.CreateTableSQL(&Table{
		Name:           "users",
		PrimaryKeyName: "pk_users",
		Comment:        "user table",
		Columns: []*Column{
			{Name: "id", Type: TypeBigInt, PrimaryKey: true, AutoIncrement: true, Unsigned: true, Comment: "pk_id"},
			{Name: "name", Type: TypeVarchar, Size: 64, NotNull: true, Comment: "user name"},
			{Name: "enabled", Type: TypeBoolean, NotNull: true, Default: "true", Comment: "enabled flag"},
		},
		Indexes: []*Index{
			{Name: "idx_users_name", Columns: []string{"name"}},
		},
	})
	require.NoError(t, err)
	require.Len(t, sqls, 2)
	assert.Contains(t, sqls[0], "CREATE TABLE `users`")
	assert.Contains(t, sqls[0], "`id` BIGINT UNSIGNED AUTO_INCREMENT COMMENT 'pk_id'")
	assert.Contains(t, sqls[0], "`enabled` TINYINT(1) NOT NULL DEFAULT 1 COMMENT 'enabled flag'")
	assert.Contains(t, sqls[0], "CONSTRAINT `pk_users` PRIMARY KEY (`id`)")
	assert.Contains(t, sqls[0], "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='user table'")
	assert.Equal(t, "CREATE INDEX `idx_users_name` USING BTREE ON `users` (`name`)", sqls[1])
}

func TestMySQLDialect_AlterAndDropIndexSQL(t *testing.T) {
	d := &MySQLDialect{}

	alterSQL, err := d.AlterColumnSQL("users", &Column{Name: "name", Type: TypeVarchar, Size: 128, NotNull: true})
	require.NoError(t, err)
	require.Len(t, alterSQL, 1)
	assert.Equal(t, "ALTER TABLE `users` MODIFY COLUMN `name` VARCHAR(128) NOT NULL", alterSQL[0])

	dropSQL, err := d.DropIndexSQL("users", "idx_users_name")
	require.NoError(t, err)
	require.Len(t, dropSQL, 1)
	assert.Equal(t, "DROP INDEX `idx_users_name` ON `users`", dropSQL[0])
}
