package sqlstore

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// execWithRetry is the core retry engine used by Exec and Tx.
// It wraps any func() error with exponential backoff retry logic.
// maxRetries is passed explicitly so callers can use QueryRetries or TransactionRetries.
func (s *Store) execWithRetry(ctx context.Context, maxRetries int, fn func() error) error {
	delay := s.config.RetryInterval
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Keep the existing attempt budget: non-positive values execute once.
		if attempt >= max(1, maxRetries) || !s.isRetryable(err) {
			return err
		}

		s.logger.WarnContext(ctx, "retrying database operation", "error", err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, s.config.RetryInterval*10)
	}
}

// WithTimeout applies a timeout to a database operation
func (s *Store) WithTimeout(ctx context.Context, fn func(context.Context, *gorm.DB) error) error {
	// If no timeout configured, execute directly
	if s.config.QueryTimeout <= 0 {
		return fn(ctx, s.db.WithContext(ctx))
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, s.config.QueryTimeout)
	defer cancel()

	return fn(timeoutCtx, s.db.WithContext(timeoutCtx))
}

// The selected driver owns its error codes. Unknown drivers and errors fail
// closed; network failures can leave a write's commit outcome unknown.
func (s *Store) isRetryable(err error) bool {
	driver, ok := s.db.Dialector.(interface{ IsRetryable(error) bool })
	return ok && driver.IsRetryable(err)
}
