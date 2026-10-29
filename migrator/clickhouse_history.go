package migrator

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ClickHouseHistoryStore 管理 ClickHouse 的迁移历史（使用 GORM）
type ClickHouseHistoryStore struct {
	db    *gorm.DB
	table string
}

// 编译时验证接口实现
var _ HistoryStore = (*ClickHouseHistoryStore)(nil)

// NewClickHouseHistoryStore 创建 ClickHouse 历史存储
func NewClickHouseHistoryStore(db *gorm.DB) *ClickHouseHistoryStore {
	return &ClickHouseHistoryStore{db: db, table: DefaultHistoryTable}
}

// EnsureTable 确保迁移历史表存在（使用 GORM 执行原始 SQL）
func (h *ClickHouseHistoryStore) EnsureTable(ctx context.Context) error {
	createSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			migration_id String NOT NULL,
			description String NOT NULL,
			applied_at DateTime NOT NULL,
			success UInt8 NOT NULL,
			error_msg String,
			execution_time_ms Int64 NOT NULL
		) ENGINE = MergeTree()
		ORDER BY (applied_at, migration_id)
	`, h.table)

	if err := h.db.WithContext(ctx).Exec(createSQL).Error; err != nil {
		return fmt.Errorf("failed to create migration history table %s: %w", h.table, err)
	}

	return nil
}

// IsApplied 检查迁移是否已应用（使用 GORM）
func (h *ClickHouseHistoryStore) IsApplied(ctx context.Context, migrationID string) (bool, error) {
	var count int64
	err := h.db.WithContext(ctx).
		Raw("SELECT COUNT(*) FROM "+h.table+" WHERE migration_id = ? AND success = 1", migrationID).
		Scan(&count).Error

	if err != nil {
		return false, fmt.Errorf("failed to check migration status: %w", err)
	}

	return count > 0, nil
}

// RecordSuccess 记录迁移成功（使用 GORM）
func (h *ClickHouseHistoryStore) RecordSuccess(ctx context.Context, migrationID, description string) error {
	err := h.db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s (migration_id, description, applied_at, success, error_msg, execution_time_ms)
		 VALUES (?, ?, ?, ?, ?, ?)`, h.table),
		migrationID,
		description,
		time.Now(),
		1, // success = true
		"",
		0,
	).Error

	if err != nil {
		return fmt.Errorf("failed to record migration success: %w", err)
	}

	return nil
}

// RecordFailure 记录迁移失败（使用 GORM）
func (h *ClickHouseHistoryStore) RecordFailure(ctx context.Context, migrationID, description string, err error) error {
	execErr := h.db.WithContext(ctx).Exec(
		fmt.Sprintf(`INSERT INTO %s (migration_id, description, applied_at, success, error_msg, execution_time_ms)
		 VALUES (?, ?, ?, ?, ?, ?)`, h.table),
		migrationID,
		description,
		time.Now(),
		0, // success = false
		err.Error(),
		0,
	).Error

	if execErr != nil {
		return fmt.Errorf("failed to record migration failure: %w", execErr)
	}

	return nil
}

// GetHistory 获取迁移历史（使用 GORM，limit=0 表示全部）
func (h *ClickHouseHistoryStore) GetHistory(ctx context.Context, limit int) ([]*MigrationLog, error) {
	query := h.db.WithContext(ctx).
		Table(h.table).
		Select("migration_id, description, applied_at, success, error_msg").
		Order("applied_at DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}

	var history []*MigrationLog
	var rawResults []struct {
		MigrationID string
		Description string
		AppliedAt   time.Time
		Success     uint8
		ErrorMsg    string
	}

	if err := query.Find(&rawResults).Error; err != nil {
		return nil, fmt.Errorf("failed to query migration history: %w", err)
	}

	for _, raw := range rawResults {
		history = append(history, &MigrationLog{
			MigrationID: raw.MigrationID,
			Description: raw.Description,
			AppliedAt:   raw.AppliedAt,
			Success:     raw.Success == 1,
			ErrorMsg:    raw.ErrorMsg,
		})
	}

	return history, nil
}

// GetFailedMigrations 获取失败的迁移（使用 GORM）
func (h *ClickHouseHistoryStore) GetFailedMigrations(ctx context.Context) ([]*MigrationLog, error) {
	var rawResults []struct {
		MigrationID string
		Description string
		AppliedAt   time.Time
		Success     uint8
		ErrorMsg    string
	}

	err := h.db.WithContext(ctx).
		Table(h.table).
		Select("migration_id, description, applied_at, success, error_msg").
		Where("success = 0").
		Order("applied_at DESC").
		Find(&rawResults).Error

	if err != nil {
		return nil, fmt.Errorf("failed to query failed migrations: %w", err)
	}

	var history []*MigrationLog
	for _, raw := range rawResults {
		history = append(history, &MigrationLog{
			MigrationID: raw.MigrationID,
			Description: raw.Description,
			AppliedAt:   raw.AppliedAt,
			Success:     raw.Success == 1,
			ErrorMsg:    raw.ErrorMsg,
		})
	}

	return history, nil
}

// DeleteMigration 删除迁移记录（使用 GORM）
func (h *ClickHouseHistoryStore) DeleteMigration(ctx context.Context, migrationID string) error {
	err := h.db.WithContext(ctx).
		Exec("ALTER TABLE "+h.table+" DELETE WHERE migration_id = ?", migrationID).Error

	if err != nil {
		return fmt.Errorf("failed to delete migration record: %w", err)
	}

	return nil
}
