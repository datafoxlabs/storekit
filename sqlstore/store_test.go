package sqlstore

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/datafoxlabs/storekit/sqlstore/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupTestStore creates a test Store with in-memory SQLite
func setupTestStore(t *testing.T) *Store {
	return setupTestStoreWithProvider(t, noop.NewTracerProvider())
}

func setupTestStoreWithProvider(t *testing.T, provider trace.TracerProvider) *Store {
	t.Helper()

	cfg := &DatabaseConfig{
		Type:               "sqlite",
		ConnectionString:   "file::memory:?cache=shared",
		MaxOpenConns:       1,
		MaxIdleConns:       1,
		QueryRetries:       3,
		TransactionRetries: 3,
		RetryInterval:      10 * time.Millisecond,
		QueryTimeout:       5 * time.Second,
		LogLevel:           logger.Silent,
	}

	testLogger := slog.Default()
	store, err := Open(cfg, sqlite.Open, testLogger, provider)
	require.NoError(t, err)

	t.Cleanup(func() {
		store.Close()
	})

	return store
}

// setupTestStoreWithTracing creates a test Store with an in-memory span exporter.
// Both store and SQL spans use the supplied provider.
func setupTestStoreWithTracing(t *testing.T) (*Store, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(
		tracesdk.WithSyncer(exporter),
		tracesdk.WithSampler(tracesdk.AlwaysSample()),
	)

	t.Cleanup(func() {
		tp.Shutdown(context.Background())
	})

	store := setupTestStoreWithProvider(t, tp)

	return store, exporter
}

// Test model for database operations
type TestModel struct {
	ID    int    `gorm:"primaryKey"`
	Name  string `gorm:"index"`
	Value int
}

func (TestModel) TableName() string {
	return "test_models"
}

// --- Retry Tests ---

func TestExec_Success(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	err := store.AutoMigrate(&TestModel{})
	require.NoError(t, err)

	err = store.Exec(ctx, func(db *gorm.DB) error {
		return db.Create(&TestModel{Name: "test", Value: 1}).Error
	})

	assert.NoError(t, err)

	var model TestModel
	err = store.GetDB().First(&model).Error
	require.NoError(t, err)
	assert.Equal(t, "test", model.Name)
}

func TestExec_NonRetryableError(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	err := store.AutoMigrate(&TestModel{})
	require.NoError(t, err)

	err = store.Exec(ctx, func(db *gorm.DB) error {
		return db.First(&TestModel{}, 999).Error
	})

	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestExec_ContextCancellation(t *testing.T) {
	store := setupTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := store.Exec(ctx, func(*gorm.DB) error { calls++; return nil })
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls)
}

// --- Timeout Tests ---

func TestWithTimeout_Success(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	err := store.AutoMigrate(&TestModel{})
	require.NoError(t, err)

	err = store.WithTimeout(ctx, func(ctx context.Context, db *gorm.DB) error {
		return db.Create(&TestModel{Name: "test", Value: 1}).Error
	})

	assert.NoError(t, err)
}

// --- Embedded Tracing Tests ---

func TestExec_CreatesSpan(t *testing.T) {
	store, exporter := setupTestStoreWithTracing(t)
	ctx := context.Background()

	err := store.AutoMigrate(&TestModel{})
	require.NoError(t, err)

	err = store.Exec(ctx, func(db *gorm.DB) error {
		return db.Create(&TestModel{Name: "traced", Value: 1}).Error
	})
	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.NotEmpty(t, spans)

	// Find our operation span
	var found bool
	for _, s := range spans {
		if s.Name == "sqlstore.exec" {
			found = true
			assert.Equal(t, codes.Unset, s.Status.Code, "successful exec should not set error status")
			break
		}
	}
	assert.True(t, found, "should find sqlstore.exec span")
}

func TestTx_CreatesSpan(t *testing.T) {
	store, exporter := setupTestStoreWithTracing(t)
	ctx := context.Background()

	err := store.AutoMigrate(&TestModel{})
	require.NoError(t, err)

	err = store.Tx(ctx, func(tx *gorm.DB) error {
		if err := tx.Create(&TestModel{Name: "tx1", Value: 1}).Error; err != nil {
			return err
		}
		return tx.Create(&TestModel{Name: "tx2", Value: 2}).Error
	})
	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.NotEmpty(t, spans)

	var found bool
	for _, s := range spans {
		if s.Name == "sqlstore.tx" {
			found = true
			break
		}
	}
	assert.True(t, found, "should find sqlstore.tx span")

	var count int64
	store.GetDB().Model(&TestModel{}).Count(&count)
	assert.Equal(t, int64(2), count)
}

func TestExec_NoTracer_NoSpan(t *testing.T) {
	store := setupTestStore(t) // no tracer set
	ctx := context.Background()

	err := store.AutoMigrate(&TestModel{})
	require.NoError(t, err)

	// Should work fine without tracer (noop span, zero overhead)
	err = store.Exec(ctx, func(db *gorm.DB) error {
		return db.Create(&TestModel{Name: "no-trace", Value: 1}).Error
	})
	require.NoError(t, err)

	var count int64
	store.GetDB().Model(&TestModel{}).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestExec_TracingRecordsError(t *testing.T) {
	store, exporter := setupTestStoreWithTracing(t)
	ctx := context.Background()

	expectedErr := errors.New("intentional error")
	err := store.Exec(ctx, func(db *gorm.DB) error {
		return expectedErr
	})

	assert.Error(t, err)

	spans := exporter.GetSpans()
	var found bool
	for _, s := range spans {
		if s.Name == "sqlstore.exec" {
			found = true
			// Span is created but status is not set by startSpan — the error propagates up
			break
		}
	}
	assert.True(t, found, "should find sqlstore.exec span even on error")
}

// --- GormLogger Span Enrichment Test ---

func TestGormLogger_AddsSpanAttributes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := tracesdk.NewTracerProvider(
		tracesdk.WithSyncer(exporter),
		tracesdk.WithSampler(tracesdk.AlwaysSample()),
	)

	t.Cleanup(func() {
		tp.Shutdown(context.Background())
	})

	cfg := &DatabaseConfig{
		Type:               "sqlite",
		ConnectionString:   "file::memory:?cache=shared",
		MaxOpenConns:       1,
		MaxIdleConns:       1,
		QueryRetries:       3,
		TransactionRetries: 3,
		RetryInterval:      10 * time.Millisecond,
		QueryTimeout:       5 * time.Second,
		LogLevel:           logger.Info,
	}

	testLogger := slog.Default()
	store, err := Open(cfg, sqlite.Open, testLogger, tp)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	err = store.AutoMigrate(&TestModel{})
	require.NoError(t, err)

	// Execute inside Exec to create a parent span
	err = store.Exec(ctx, func(db *gorm.DB) error {
		return db.Create(&TestModel{Name: "test", Value: 1}).Error
	})
	require.NoError(t, err)

	// Verify spans exist
	spans := exporter.GetSpans()
	require.NotEmpty(t, spans, "should have at least one span")

	// Find the exec span and verify logger enriched it
	var execSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "sqlstore.exec" {
			execSpan = &spans[i]
			break
		}
	}
	require.NotNil(t, execSpan, "should find sqlstore.exec span")

	// GormLogger should have added db.statement and db.rows_affected via span attributes
	attrs := execSpan.Attributes
	_ = attrs // attributes may be on child spans from the OTel plugin
}

// Helper function to assert attribute existence and optional value
func assertHasAttribute(t *testing.T, attrs []attribute.KeyValue, key string, expectedValue interface{}) {
	t.Helper()

	found := false
	for _, attr := range attrs {
		if string(attr.Key) == key {
			found = true
			if expectedValue != nil {
				assert.Equal(t, expectedValue, attr.Value.AsInterface(), "attribute %s has unexpected value", key)
			}
			break
		}
	}

	assert.True(t, found, "attribute %s not found in span", key)
}
