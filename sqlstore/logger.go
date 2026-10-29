package sqlstore

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm/logger"
)

type GormLogger struct {
	logger   *slog.Logger
	logLevel logger.LogLevel
}

func newGormLogger(slogLogger *slog.Logger, level logger.LogLevel) logger.Interface {
	return &GormLogger{
		logger:   slogLogger,
		logLevel: level,
	}
}

func (l *GormLogger) LogMode(level logger.LogLevel) logger.Interface {
	return &GormLogger{
		logger:   l.logger,
		logLevel: level,
	}
}

func (l *GormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= logger.Info {
		l.logger.InfoContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (l *GormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= logger.Warn {
		l.logger.WarnContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (l *GormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.logLevel >= logger.Error {
		l.logger.ErrorContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (l *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.logLevel <= logger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()

	// Add SQL details to existing span if present (enriches spans created by WithTracing)
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(
			attribute.String("db.statement", sql),
			attribute.Int64("db.rows_affected", rows),
			attribute.Float64("db.duration_ms", float64(elapsed.Milliseconds())),
		)
	}

	// Original logging logic unchanged
	if err != nil {
		l.logger.ErrorContext(ctx, "database query failed",
			"elapsed", elapsed,
			"rows", rows,
			"sql", sql,
			"error", err,
		)
	} else if elapsed > 200*time.Millisecond {
		l.logger.WarnContext(ctx, "slow query",
			"elapsed", elapsed,
			"rows", rows,
			"sql", sql,
		)
	} else if l.logLevel >= logger.Info {
		l.logger.DebugContext(ctx, "database query",
			"elapsed", elapsed,
			"rows", rows,
			"sql", sql,
		)
	}
}
