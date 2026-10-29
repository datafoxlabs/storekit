package sqlstore

import (
	"fmt"
	"reflect"

	"gorm.io/gorm"
)

// DefaultBatchSize is the default batch size for bulk operations.
const DefaultBatchSize = 1000

// BulkInsert inserts records in batches using GORM's CreateInBatches.
// batchSize controls how many records are inserted per batch.
// If batchSize <= 0, DefaultBatchSize is used.
//
// Example:
//
//	var users []User
//	affected, err := store.BulkInsert(db, &users, 500)
func BulkInsert(db *gorm.DB, records interface{}, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	result := db.CreateInBatches(records, batchSize)
	return result.RowsAffected, result.Error
}

// InBatches splits a slice into chunks and calls fn for each batch.
// This is a generic utility that works with any slice type.
//
// Example:
//
//	err := InBatches(allRecords, 500, func(batch interface{}) error {
//	    return db.Create(batch).Error
//	})
func InBatches(items interface{}, batchSize int, fn func(batch interface{}) error) error {
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	slice := reflect.Indirect(reflect.ValueOf(items))
	if slice.Kind() != reflect.Slice {
		return fmt.Errorf("InBatches: expected a slice, got %s", slice.Kind())
	}

	total := slice.Len()
	for i := 0; i < total; i += batchSize {
		end := i + batchSize
		if end > total {
			end = total
		}

		chunk := slice.Slice(i, end).Interface()
		if err := fn(chunk); err != nil {
			return err
		}
	}

	return nil
}
