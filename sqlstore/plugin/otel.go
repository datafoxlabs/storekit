package plugin

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

const (
	// spanKey 用于在 GORM 的 Instance 上下文中传递 Span
	spanKey = "otel:span"
)

// OTelPlugin 是自定义的 Tracing 插件
type OTelPlugin struct {
	tracer trace.Tracer
}

// NewOTelPlugin 创建插件实例
func NewOTelPlugin(provider trace.TracerProvider) *OTelPlugin {
	// 使用调用方提供的 TracerProvider
	return &OTelPlugin{
		tracer: provider.Tracer("gorm"),
	}
}

// Name 返回插件名称
func (p *OTelPlugin) Name() string {
	return "OpenTelemetry"
}

// Initialize 注册 GORM 钩子
func (p *OTelPlugin) Initialize(db *gorm.DB) error {
	// Create
	db.Callback().Create().Before("gorm:create").Register("otel:before_create", p.before("INSERT"))
	db.Callback().Create().After("gorm:create").Register("otel:after_create", p.after)

	// Query
	db.Callback().Query().Before("gorm:query").Register("otel:before_query", p.before("SELECT"))
	db.Callback().Query().After("gorm:query").Register("otel:after_query", p.after)

	// Delete
	db.Callback().Delete().Before("gorm:delete").Register("otel:before_delete", p.before("DELETE"))
	db.Callback().Delete().After("gorm:delete").Register("otel:after_delete", p.after)

	// Update
	db.Callback().Update().Before("gorm:update").Register("otel:before_update", p.before("UPDATE"))
	db.Callback().Update().After("gorm:update").Register("otel:after_update", p.after)

	// Row (用于 Scan 等操作)
	db.Callback().Row().Before("gorm:row").Register("otel:before_row", p.before("ROW"))
	db.Callback().Row().After("gorm:row").Register("otel:after_row", p.after)

	// Raw (原生 SQL)
	db.Callback().Raw().Before("gorm:raw").Register("otel:before_raw", p.before("RAW"))
	db.Callback().Raw().After("gorm:raw").Register("otel:after_raw", p.after)

	return nil
}

// before 返回一个闭包，用于开始 Span
func (p *OTelPlugin) before(operation string) func(*gorm.DB) {
	return func(db *gorm.DB) {
		// 1. 获取当前 Context，如果没有则使用 Background
		ctx := db.Statement.Context
		if ctx == nil {
			ctx = context.Background()
		}

		// 2. 构造 Span 名称。推荐格式：<db.operation> <table_name>
		// 注意：此时 db.Statement.Table 可能为空（Raw SQL 场景），后续在 after 中可以补全
		spanName := fmt.Sprintf("%s %s", operation, db.Statement.Table)
		if db.Statement.Table == "" {
			spanName = operation
		}

		// 3. 开始 Span (Client 类型)
		ctx, span := p.tracer.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindClient))

		// 4. 记录基础属性 (Operation)
		span.SetAttributes(semconv.DBOperationName(operation))

		// 5. 将 Span 存入 GORM 实例上下文，以便在 after 中结束
		db.InstanceSet(spanKey, span)

		// 6. 更新 db 的 Context，确保后续流程能获取到 TraceID
		db.Statement.Context = ctx
	}
}

// after 钩子：结束 Span 并记录详细信息
func (p *OTelPlugin) after(db *gorm.DB) {
	// 1. 取出 Span
	v, ok := db.InstanceGet(spanKey)
	if !ok {
		return
	}
	span, ok := v.(trace.Span)
	if !ok {
		return
	}
	defer span.End()

	// 2. 准备属性
	attrs := []attribute.KeyValue{
		semconv.DBSystemKey.String(db.Dialector.Name()), // e.g. "postgres", "mysql"
	}

	// 3. 记录 SQL 语句
	// 注意：db.Statement.SQL.String() 返回的是参数化 SQL (含 ? 或 $1)，不含敏感数据，符合安全规范。
	if sql := db.Statement.SQL.String(); sql != "" {
		attrs = append(attrs, semconv.DBQueryText(sql))
	}

	// 4. 记录表名 (如果在 before 中没取到，这里再补一次)
	if db.Statement.Table != "" {
		attrs = append(attrs, attribute.String("sql.table", db.Statement.Table))
	}

	// 5. 记录受影响行数
	if db.RowsAffected != -1 {
		attrs = append(attrs, attribute.Int64("db.rows_affected", db.RowsAffected))
	}

	// 6. 处理错误
	if db.Error != nil && !errors.Is(db.Error, gorm.ErrRecordNotFound) {
		// 发生真实错误
		span.RecordError(db.Error)
		span.SetStatus(codes.Error, db.Error.Error())
	} else {
		// 正常或 RecordNotFound (通常业务上不算系统 Error)
		span.SetStatus(codes.Ok, "OK")
	}

	span.SetAttributes(attrs...)
}
