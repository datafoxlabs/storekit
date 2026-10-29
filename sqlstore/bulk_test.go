package sqlstore_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/datafoxlabs/storekit/sqlstore"
	"github.com/datafoxlabs/storekit/sqlstore/sqlstoretest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type testBulkModel struct {
	ID   int64  `gorm:"primaryKey;autoIncrement"`
	Name string `gorm:"size:100"`
}

func (*testBulkModel) TableName() string { return "test_bulk_models" }

func TestBulkInsert(t *testing.T) {
	t.Run("inserts all records in batches", func(t *testing.T) {
		store := sqlstoretest.New(t, sqlstoretest.WithModels(&testBulkModel{}))

		records := make([]testBulkModel, 25)
		for i := range records {
			records[i] = testBulkModel{Name: fmt.Sprintf("item-%d", i)}
		}

		affected, err := sqlstore.BulkInsert(store.GetDB(), &records, 10)
		require.NoError(t, err)
		assert.Equal(t, int64(25), affected)

		var count int64
		store.GetDB().Model(&testBulkModel{}).Count(&count)
		assert.Equal(t, int64(25), count)
	})

	t.Run("handles empty slice", func(t *testing.T) {
		store := sqlstoretest.New(t, sqlstoretest.WithModels(&testBulkModel{}))

		records := []testBulkModel{}
		affected, err := sqlstore.BulkInsert(store.GetDB(), &records, 10)
		require.NoError(t, err)
		assert.Equal(t, int64(0), affected)
	})

	t.Run("uses default batch size when <= 0", func(t *testing.T) {
		store := sqlstoretest.New(t, sqlstoretest.WithModels(&testBulkModel{}))

		records := []testBulkModel{{Name: "one"}, {Name: "two"}}
		affected, err := sqlstore.BulkInsert(store.GetDB(), &records, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(2), affected)
	})
}

func TestInBatches(t *testing.T) {
	t.Run("splits evenly", func(t *testing.T) {
		items := make([]int, 10)
		for i := range items {
			items[i] = i
		}

		var batchCount int
		var totalItems int
		err := sqlstore.InBatches(items, 5, func(batch interface{}) error {
			batchCount++
			totalItems += len(batch.([]int))
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 2, batchCount)
		assert.Equal(t, 10, totalItems)
	})

	t.Run("handles remainder batch", func(t *testing.T) {
		items := make([]string, 7)
		var batchSizes []int
		err := sqlstore.InBatches(items, 3, func(batch interface{}) error {
			batchSizes = append(batchSizes, len(batch.([]string)))
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, []int{3, 3, 1}, batchSizes)
	})

	t.Run("stops on error", func(t *testing.T) {
		items := make([]int, 10)
		var batchCount int
		err := sqlstore.InBatches(items, 3, func(batch interface{}) error {
			batchCount++
			if batchCount == 2 {
				return fmt.Errorf("stop")
			}
			return nil
		})
		require.Error(t, err)
		assert.Equal(t, 2, batchCount) // stopped at 2nd batch
	})

	t.Run("rejects non-slice", func(t *testing.T) {
		err := sqlstore.InBatches("not-a-slice", 5, func(batch interface{}) error {
			return nil
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected a slice")
	})

	t.Run("with GORM integration", func(t *testing.T) {
		store := sqlstoretest.New(t, sqlstoretest.WithModels(&testBulkModel{}))

		items := make([]testBulkModel, 15)
		for i := range items {
			items[i] = testBulkModel{Name: fmt.Sprintf("bulk-%d", i)}
		}

		err := sqlstore.InBatches(items, 5, func(batch interface{}) error {
			return store.GetDB().Create(batch).Error
		})
		require.NoError(t, err)

		var count int64
		store.GetDB().Model(&testBulkModel{}).Count(&count)
		assert.Equal(t, int64(15), count)
	})

	t.Run("uses default batch size", func(t *testing.T) {
		items := make([]int, 3)
		var batchCount int
		err := sqlstore.InBatches(items, 0, func(batch interface{}) error {
			batchCount++
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 1, batchCount) // all in one batch (<= sqlstore.DefaultBatchSize)
	})
}

func TestBulkInsertInTransaction(t *testing.T) {
	t.Run("bulk insert within InTransaction", func(t *testing.T) {
		store := sqlstoretest.New(t, sqlstoretest.WithModels(&testBulkModel{}))

		err := store.InTransaction(context.Background(), func(ctx context.Context) error {
			return store.Exec(ctx, func(db *gorm.DB) error {
				records := make([]testBulkModel, 20)
				for i := range records {
					records[i] = testBulkModel{Name: fmt.Sprintf("tx-bulk-%d", i)}
				}
				_, err := sqlstore.BulkInsert(db, &records, 5)
				return err
			})
		})
		require.NoError(t, err)

		var count int64
		store.GetDB().Model(&testBulkModel{}).Count(&count)
		assert.Equal(t, int64(20), count)
	})
}
