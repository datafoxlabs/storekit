package sqlstoretest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestModel is a simple model used for testing.
type testModel struct {
	ID    int    `gorm:"primaryKey;autoIncrement"`
	Name  string `gorm:"index"`
	Value int
}

func (testModel) TableName() string {
	return "test_models"
}

func TestNew_Connect(t *testing.T) {
	store := New(t)
	require.NotNil(t, store)

	ctx := context.Background()
	err := store.Ping(ctx)
	require.NoError(t, err)
}

func TestNew_WithModels(t *testing.T) {
	store := New(t, WithModels(&testModel{}))

	ctx := context.Background()

	// Verify table exists by inserting a record via Exec (with built-in retry)
	err := store.Exec(ctx, func(db *gorm.DB) error {
		return db.Create(&testModel{Name: "test", Value: 42}).Error
	})
	require.NoError(t, err)

	// Verify data was created
	var result testModel
	err = store.GetDB().First(&result, "name = ?", "test").Error
	require.NoError(t, err)
	assert.Equal(t, "test", result.Name)
	assert.Equal(t, 42, result.Value)
}

func TestNew_Isolation(t *testing.T) {
	store1 := New(t, WithModels(&testModel{}))
	store2 := New(t, WithModels(&testModel{}))

	ctx := context.Background()

	// Write to store1
	err := store1.Exec(ctx, func(db *gorm.DB) error {
		return db.Create(&testModel{Name: "store1_only", Value: 1}).Error
	})
	require.NoError(t, err)

	// Verify store1 has the data
	var count1 int64
	store1.GetDB().Model(&testModel{}).Count(&count1)
	assert.Equal(t, int64(1), count1)

	// Verify store2 does NOT have the data (isolation)
	var count2 int64
	store2.GetDB().Model(&testModel{}).Count(&count2)
	assert.Equal(t, int64(0), count2, "store2 should be isolated from store1")
}

func TestNew_Tx_Commit(t *testing.T) {
	store := New(t, WithModels(&testModel{}))

	ctx := context.Background()

	// Tx with built-in retry should work
	err := store.Tx(ctx, func(tx *gorm.DB) error {
		if err := tx.Create(&testModel{Name: "tx1", Value: 1}).Error; err != nil {
			return err
		}
		return tx.Create(&testModel{Name: "tx2", Value: 2}).Error
	})
	require.NoError(t, err)

	var count int64
	store.GetDB().Model(&testModel{}).Count(&count)
	assert.Equal(t, int64(2), count)
}

func TestNew_Tx_Rollback(t *testing.T) {
	store := New(t, WithModels(&testModel{}))

	ctx := context.Background()

	// Transaction that fails should rollback
	err := store.Tx(ctx, func(tx *gorm.DB) error {
		if err := tx.Create(&testModel{Name: "should_rollback", Value: 1}).Error; err != nil {
			return err
		}
		return assert.AnError // force rollback
	})
	require.Error(t, err)

	// Verify nothing was committed
	var count int64
	store.GetDB().Model(&testModel{}).Count(&count)
	assert.Equal(t, int64(0), count, "rolled back transaction should leave no records")
}

func TestNew_WithTruncation(t *testing.T) {
	store := New(t, WithModels(&testModel{}))

	ctx := context.Background()

	// Insert some data
	err := store.Exec(ctx, func(db *gorm.DB) error {
		return db.Create(&testModel{Name: "pre_truncate", Value: 1}).Error
	})
	require.NoError(t, err)

	// Create another store with truncation (simulating fresh state)
	store2 := New(t, WithModels(&testModel{}), WithTruncation())
	var count int64
	store2.GetDB().Model(&testModel{}).Count(&count)
	assert.Equal(t, int64(0), count, "truncated store should have no records")
}

func TestNewInMemoryIsolation(t *testing.T) {
	first := New(t, WithDBType("sqlite3"), WithInMemory(), WithModels(&testModel{}))
	second := New(t, WithInMemory(), WithModels(&testModel{}))
	require.NoError(t, first.GetDB().Create(&testModel{Name: "first"}).Error)
	var count int64
	require.NoError(t, second.GetDB().Model(&testModel{}).Count(&count).Error)
	require.Zero(t, count)
}
