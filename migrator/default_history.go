package migrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MigrationLog 迁移历史记录表结构
type MigrationLog struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement"`
	MigrationID string    `gorm:"column:migration_id;type:varchar(255);uniqueIndex;not null"`
	Description string    `gorm:"column:description;type:varchar(1024)"`
	AppliedAt   time.Time `gorm:"column:applied_at;type:timestamp;not null"`
	Success     bool      `gorm:"column:success;type:boolean;not null"`
	ErrorMsg    string    `gorm:"column:error_msg;type:text"`
}

func (MigrationLog) TableName() string {
	return DefaultHistoryTable
}

// DefaultHistoryStore 基于 GORM 的默认迁移历史存储实现
type DefaultHistoryStore struct {
	db     *gorm.DB
	logger *slog.Logger
	table  string
}

// 编译时验证接口实现
var _ HistoryStore = (*DefaultHistoryStore)(nil)

// NewDefaultHistoryStore 创建默认历史存储实例
func NewDefaultHistoryStore(db *gorm.DB, logger *slog.Logger) *DefaultHistoryStore {
	return newDefaultHistoryStore(db, logger, DefaultHistoryTable)
}

func newDefaultHistoryStore(db *gorm.DB, logger *slog.Logger, table string) *DefaultHistoryStore {
	return &DefaultHistoryStore{
		db:     db,
		table:  table,
		logger: logger.With("component", "migration_history"),
	}
}

// withDB binds history writes to the migration transaction. This keeps the
// schema change and its success marker atomic on transactional databases.
func (h *DefaultHistoryStore) withDB(db *gorm.DB) *DefaultHistoryStore {
	return &DefaultHistoryStore{db: db, logger: h.logger, table: h.table}
}

// EnsureTable 确保所选迁移历史表存在
func (h *DefaultHistoryStore) EnsureTable(ctx context.Context) error {
	if h.db.WithContext(ctx).Migrator().HasTable(h.table) {
		h.logger.DebugContext(ctx, "migration history table already exists", "table", h.table)
		return nil
	}

	h.logger.InfoContext(ctx, "creating migration history table", "table", h.table)
	if err := h.db.WithContext(ctx).Table(h.table).Migrator().CreateTable(&MigrationLog{}); err != nil {
		return fmt.Errorf("failed to create migration history table %s: %w", h.table, err)
	}

	h.logger.InfoContext(ctx, "migration history table created successfully", "table", h.table)
	return nil
}

// IsApplied 检查迁移是否已应用
func (h *DefaultHistoryStore) IsApplied(ctx context.Context, migrationID string) (bool, error) {
	var count int64
	err := h.db.WithContext(ctx).Table(h.table).
		Model(&MigrationLog{}).
		Where("migration_id = ? AND success = ?", migrationID, true).
		Count(&count).Error

	if err != nil {
		return false, fmt.Errorf("failed to check migration status: %w", err)
	}

	return count > 0, nil
}

// RecordSuccess 记录迁移成功
func (h *DefaultHistoryStore) RecordSuccess(ctx context.Context, migrationID, description string) error {
	log := &MigrationLog{
		MigrationID: migrationID,
		Description: description,
		AppliedAt:   time.Now(),
		Success:     true,
		ErrorMsg:    "",
	}

	// Upsert by migration_id so a previously recorded failure can be retried and
	// later marked as success without being blocked by the unique index.
	if err := h.db.WithContext(ctx).Table(h.table).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "migration_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"description",
				"applied_at",
				"success",
				"error_msg",
			}),
		}).
		Create(log).Error; err != nil {
		return fmt.Errorf("failed to record migration success: %w", err)
	}

	h.logger.InfoContext(ctx, "migration recorded successfully",
		"migration_id", migrationID,
		"description", description)
	return nil
}

// RecordFailure 记录迁移失败
func (h *DefaultHistoryStore) RecordFailure(ctx context.Context, migrationID, description string, err error) error {
	log := &MigrationLog{
		MigrationID: migrationID,
		Description: description,
		AppliedAt:   time.Now(),
		Success:     false,
		ErrorMsg:    err.Error(),
	}

	// Upsert by migration_id so repeated failures update the last error, and a
	// subsequent success can overwrite the failure record.
	if dbErr := h.db.WithContext(ctx).Table(h.table).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "migration_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"description",
				"applied_at",
				"success",
				"error_msg",
			}),
		}).
		Create(log).Error; dbErr != nil {
		h.logger.ErrorContext(ctx, "failed to record migration failure",
			"migration_id", migrationID,
			"error", dbErr)
		return fmt.Errorf("failed to record migration failure: %w", dbErr)
	}

	h.logger.WarnContext(ctx, "migration failure recorded",
		"migration_id", migrationID,
		"error", err.Error())
	return nil
}

// GetHistory 获取迁移历史
func (h *DefaultHistoryStore) GetHistory(ctx context.Context, limit int) ([]*MigrationLog, error) {
	var logs []*MigrationLog
	query := h.db.WithContext(ctx).Table(h.table).
		Model(&MigrationLog{}).
		Order("applied_at DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}

	if err := query.Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("failed to get migration history: %w", err)
	}

	return logs, nil
}

// GetFailedMigrations 获取失败的迁移列表
func (h *DefaultHistoryStore) GetFailedMigrations(ctx context.Context) ([]*MigrationLog, error) {
	var logs []*MigrationLog
	err := h.db.WithContext(ctx).Table(h.table).
		Model(&MigrationLog{}).
		Where("success = ?", false).
		Order("applied_at DESC").
		Find(&logs).Error

	if err != nil {
		return nil, fmt.Errorf("failed to get failed migrations: %w", err)
	}

	return logs, nil
}

// DeleteMigration 删除迁移记录
func (h *DefaultHistoryStore) DeleteMigration(ctx context.Context, migrationID string) error {
	result := h.db.WithContext(ctx).Table(h.table).
		Where("migration_id = ?", migrationID).
		Delete(&MigrationLog{})

	if result.Error != nil {
		return fmt.Errorf("failed to delete migration record: %w", result.Error)
	}

	h.logger.InfoContext(ctx, "migration record deleted",
		"migration_id", migrationID,
		"rows_affected", result.RowsAffected)
	return nil
}
