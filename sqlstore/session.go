package sqlstore

import (
	"context"

	"gorm.io/gorm"
)

// contextTxKey is the context key for storing the current transaction.
type contextTxKey struct{}

// txFromContext retrieves the GORM transaction from the context, if present.
func txFromContext(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(contextTxKey{}).(*gorm.DB)
	return tx, ok
}

// contextWithTx stores a GORM transaction in the context.
func contextWithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, contextTxKey{}, tx)
}

// InTransaction starts a new transaction and stores it in the context.
// Any Exec call within fn will automatically reuse the transaction.
// If a transaction already exists in the context, fn runs within it without
// creating a nested transaction (session reuse).
// When tracing is enabled, automatically creates a span for the outer transaction.
func (s *Store) InTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	// If there's already a transaction in context, reuse it (no new span)
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	// Create span only for outer transaction
	ctx, span := s.startSpan(ctx, "sqlstore.transaction")
	defer span.End()

	// Create a new transaction with retry
	return s.execWithRetry(ctx, s.config.TransactionRetries, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			txCtx := contextWithTx(ctx, tx)
			return fn(txCtx)
		})
	})
}
