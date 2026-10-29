package sqlstore_test

import (
	"context"
	"testing"

	"github.com/datafoxlabs/storekit/sqlstore/sqlstoretest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type testSessionModel struct {
	ID   int64  `gorm:"primaryKey;autoIncrement"`
	Name string `gorm:"size:100"`
}

func (*testSessionModel) TableName() string { return "test_session_models" }

func TestInTransaction(t *testing.T) {
	t.Run("commits on success", func(t *testing.T) {
		store := sqlstoretest.New(t, sqlstoretest.WithModels(&testSessionModel{}))

		err := store.InTransaction(context.Background(), func(ctx context.Context) error {
			return store.Exec(ctx, func(db *gorm.DB) error {
				return db.Create(&testSessionModel{Name: "committed"}).Error
			})
		})
		require.NoError(t, err)

		// Verify data was committed
		var count int64
		store.GetDB().Model(&testSessionModel{}).Count(&count)
		assert.Equal(t, int64(1), count)
	})

	t.Run("rolls back on error", func(t *testing.T) {
		store := sqlstoretest.New(t, sqlstoretest.WithModels(&testSessionModel{}))

		err := store.InTransaction(context.Background(), func(ctx context.Context) error {
			if err := store.Exec(ctx, func(db *gorm.DB) error {
				return db.Create(&testSessionModel{Name: "should-rollback"}).Error
			}); err != nil {
				return err
			}
			return assert.AnError // force rollback
		})
		require.Error(t, err)

		// Verify data was NOT committed
		var count int64
		store.GetDB().Model(&testSessionModel{}).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("nested InTransaction reuses session", func(t *testing.T) {
		store := sqlstoretest.New(t, sqlstoretest.WithModels(&testSessionModel{}))

		err := store.InTransaction(context.Background(), func(outerCtx context.Context) error {
			// First write in outer tx
			if err := store.Exec(outerCtx, func(db *gorm.DB) error {
				return db.Create(&testSessionModel{Name: "outer"}).Error
			}); err != nil {
				return err
			}

			// Nested InTransaction should reuse outer tx
			return store.InTransaction(outerCtx, func(innerCtx context.Context) error {
				return store.Exec(innerCtx, func(db *gorm.DB) error {
					return db.Create(&testSessionModel{Name: "inner"}).Error
				})
			})
		})
		require.NoError(t, err)

		var count int64
		store.GetDB().Model(&testSessionModel{}).Count(&count)
		assert.Equal(t, int64(2), count)
	})

	t.Run("nested rollback rolls back entire transaction", func(t *testing.T) {
		store := sqlstoretest.New(t, sqlstoretest.WithModels(&testSessionModel{}))

		err := store.InTransaction(context.Background(), func(outerCtx context.Context) error {
			if err := store.Exec(outerCtx, func(db *gorm.DB) error {
				return db.Create(&testSessionModel{Name: "outer"}).Error
			}); err != nil {
				return err
			}

			// Inner returns error → should rollback everything
			return store.InTransaction(outerCtx, func(innerCtx context.Context) error {
				_ = store.Exec(innerCtx, func(db *gorm.DB) error {
					return db.Create(&testSessionModel{Name: "inner"}).Error
				})
				return assert.AnError
			})
		})
		require.Error(t, err)

		var count int64
		store.GetDB().Model(&testSessionModel{}).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("Exec without InTransaction works normally", func(t *testing.T) {
		store := sqlstoretest.New(t, sqlstoretest.WithModels(&testSessionModel{}))

		err := store.Exec(context.Background(), func(db *gorm.DB) error {
			return db.Create(&testSessionModel{Name: "direct"}).Error
		})
		require.NoError(t, err)

		var count int64
		store.GetDB().Model(&testSessionModel{}).Count(&count)
		assert.Equal(t, int64(1), count)
	})
}
