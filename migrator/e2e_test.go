package migrator_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/datafoxlabs/storekit/migrator"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakeHistoryStore struct {
	ensureCalls        int
	isAppliedCalls     int
	recordSuccessCalls int
	recordFailureCalls int
}

// 编译时验证接口实现
var _ migrator.HistoryStore = (*fakeHistoryStore)(nil)

func (f *fakeHistoryStore) EnsureTable(context.Context) error { f.ensureCalls++; return nil }
func (f *fakeHistoryStore) IsApplied(context.Context, string) (bool, error) {
	f.isAppliedCalls++
	return false, nil
}
func (f *fakeHistoryStore) RecordSuccess(context.Context, string, string) error {
	f.recordSuccessCalls++
	return nil
}
func (f *fakeHistoryStore) RecordFailure(context.Context, string, string, error) error {
	f.recordFailureCalls++
	return nil
}
func (f *fakeHistoryStore) GetHistory(context.Context, int) ([]*migrator.MigrationLog, error) {
	return nil, nil
}
func (f *fakeHistoryStore) GetFailedMigrations(context.Context) ([]*migrator.MigrationLog, error) {
	return nil, nil
}
func (f *fakeHistoryStore) DeleteMigration(context.Context, string) error { return nil }

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	return db
}

func setupTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// TestEndToEnd 端到端测试：创建表、添加列、查询数据
func TestEndToEnd(t *testing.T) {
	db := setupTestDB(t)
	logger := setupTestLogger()
	ctx := context.Background()

	// 创建 migrator
	m := migrator.New(db, logger)

	// 定义迁移：创建 users 表
	createUsersMigration := &migrator.Migration{
		ID:          "e2e_create_users",
		Description: "Create users table for end-to-end test",
		Up: func(ctx context.Context, mg migrator.MigrationContext) error {
			if mg.TableExists("users") {
				return nil
			}
			return mg.CreateTable(&migrator.Table{
				Name: "users",
				Columns: []*migrator.Column{
					{Name: "id", Type: migrator.TypeBigInt, PrimaryKey: true, AutoIncrement: true},
					{Name: "username", Type: migrator.TypeVarchar, Size: 255, NotNull: true},
					{Name: "email", Type: migrator.TypeVarchar, Size: 255, NotNull: true},
				},
				Indexes: []*migrator.Index{
					{Name: "idx_users_username", Columns: []string{"username"}, Unique: true},
				},
			})
		},
	}

	// 执行迁移
	if err := m.Run(ctx, createUsersMigration); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// 验证表已创建
	if !db.Migrator().HasTable("users") {
		t.Fatal("users table was not created")
	}

	// 插入测试数据
	if err := db.Exec("INSERT INTO users (username, email) VALUES (?, ?)", "alice", "alice@example.com").Error; err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}

	// 查询数据
	var result struct {
		Username string
		Email    string
	}
	if err := db.Table("users").First(&result).Error; err != nil {
		t.Fatalf("failed to query data: %v", err)
	}

	if result.Username != "alice" {
		t.Errorf("expected username 'alice', got '%s'", result.Username)
	}

	// 再次运行相同的迁移（验证幂等性）
	if err := m.Run(ctx, createUsersMigration); err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}

	// 验证数据仍然存在
	var count int64
	db.Table("users").Count(&count)
	if count != 1 {
		t.Errorf("expected 1 user, got %d", count)
	}

	t.Log("✅ End-to-end test passed!")
}

// TestMigrationHistory 测试迁移历史记录
func TestMigrationHistory(t *testing.T) {
	db := setupTestDB(t)
	logger := setupTestLogger()
	ctx := context.Background()

	m := migrator.New(db, logger)

	migrations := []*migrator.Migration{
		{
			ID:          "history_test_1",
			Description: "First migration",
			Up: func(ctx context.Context, mg migrator.MigrationContext) error {
				return mg.CreateTable(&migrator.Table{
					Name: "test1",
					Columns: []*migrator.Column{
						{Name: "id", Type: migrator.TypeBigInt, PrimaryKey: true, AutoIncrement: true},
					},
				})
			},
		},
		{
			ID:          "history_test_2",
			Description: "Second migration",
			Up: func(ctx context.Context, mg migrator.MigrationContext) error {
				return mg.CreateTable(&migrator.Table{
					Name: "test2",
					Columns: []*migrator.Column{
						{Name: "id", Type: migrator.TypeBigInt, PrimaryKey: true, AutoIncrement: true},
					},
				})
			},
		},
	}

	if err := m.Run(ctx, migrations...); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	// 检查历史记录
	history, err := m.GetHistory(ctx, 0)
	if err != nil {
		t.Fatalf("failed to get history: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("expected 2 history records, got %d", len(history))
	}

	for _, record := range history {
		if !record.Success {
			t.Errorf("migration %s was marked as failed", record.MigrationID)
		}
	}

	t.Log("✅ Migration history test passed!")
}

func TestMigrationRetryAfterFailure(t *testing.T) {
	db := setupTestDB(t)
	logger := setupTestLogger()
	ctx := context.Background()

	m := migrator.New(db, logger)

	attempt := 0
	mig := &migrator.Migration{
		ID:          "retry_after_failure",
		Description: "A migration that fails once, then succeeds",
		Up: func(ctx context.Context, mg migrator.MigrationContext) error {
			attempt++
			if attempt == 1 {
				return errors.New("boom")
			}
			return mg.CreateTable(&migrator.Table{
				Name: "retry_table",
				Columns: []*migrator.Column{
					{Name: "id", Type: migrator.TypeBigInt, PrimaryKey: true, AutoIncrement: true},
				},
			})
		},
	}

	if err := m.Run(ctx, mig); err == nil {
		t.Fatal("expected first run to fail, got nil error")
	}

	if err := m.Run(ctx, mig); err != nil {
		t.Fatalf("expected second run to succeed, got error: %v", err)
	}

	history, err := m.GetHistory(ctx, 0)
	if err != nil {
		t.Fatalf("failed to get history: %v", err)
	}

	if len(history) != 1 {
		t.Fatalf("expected 1 history record (upsert), got %d", len(history))
	}

	if history[0].MigrationID != mig.ID {
		t.Fatalf("expected migration_id=%s, got %s", mig.ID, history[0].MigrationID)
	}
	if !history[0].Success {
		t.Fatalf("expected migration to be marked success after retry, got success=false (error=%q)", history[0].ErrorMsg)
	}
}

func TestHistoryFactoryInjection(t *testing.T) {
	db := setupTestDB(t)
	logger := setupTestLogger()
	ctx := context.Background()

	history := &fakeHistoryStore{}
	factoryCalls := 0

	m := migrator.New(db, logger,
		migrator.WithHistoryFactory(func(_ *gorm.DB, _ *slog.Logger, _ migrator.Dialect) migrator.HistoryStore {
			factoryCalls++
			return history
		}),
	)

	mig := &migrator.Migration{
		ID:          "history_factory_injection",
		Description: "Ensure injected history store is used",
		Up: func(ctx context.Context, mg migrator.MigrationContext) error {
			if mg.TableExists("factory_test") {
				return nil
			}
			return mg.CreateTable(&migrator.Table{
				Name: "factory_test",
				Columns: []*migrator.Column{
					{Name: "id", Type: migrator.TypeBigInt, PrimaryKey: true, AutoIncrement: true},
				},
			})
		},
	}

	if err := m.Run(ctx, mig); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	if factoryCalls != 1 {
		t.Fatalf("expected history factory to be called once, got %d", factoryCalls)
	}
	if history.ensureCalls != 1 {
		t.Fatalf("expected EnsureTable calls=1, got %d", history.ensureCalls)
	}
	if history.isAppliedCalls != 1 {
		t.Fatalf("expected IsApplied calls=1, got %d", history.isAppliedCalls)
	}
	if history.recordSuccessCalls != 1 {
		t.Fatalf("expected RecordSuccess calls=1, got %d", history.recordSuccessCalls)
	}
	if history.recordFailureCalls != 0 {
		t.Fatalf("expected RecordFailure calls=0, got %d", history.recordFailureCalls)
	}
}

// TestPostgreSQLDialect 测试 PostgreSQL 方言的 SQL 生成
func TestPostgreSQLDialect(t *testing.T) {
	dialect := &migrator.PostgresDialect{}

	table := &migrator.Table{
		Name: "products",
		Columns: []*migrator.Column{
			{Name: "id", Type: migrator.TypeBigInt, PrimaryKey: true, AutoIncrement: true},
			{Name: "name", Type: migrator.TypeVarchar, Size: 255, NotNull: true},
			{Name: "price", Type: migrator.TypeInt, NotNull: true},
			{Name: "in_stock", Type: migrator.TypeBoolean, NotNull: true, Default: "true"},
		},
		Indexes: []*migrator.Index{
			{Name: "idx_products_name", Columns: []string{"name"}},
		},
	}

	sqlStatements, err := dialect.CreateTableSQL(table)
	if err != nil {
		t.Fatalf("failed to generate SQL: %v", err)
	}

	if len(sqlStatements) != 2 {
		t.Errorf("expected 2 SQL statements, got %d", len(sqlStatements))
	}

	createTableSQL := sqlStatements[0]
	t.Logf("Generated SQL:\n%s", createTableSQL)

	// 验证关键字
	if !contains(createTableSQL, `CREATE TABLE "products"`) {
		t.Error("SQL missing CREATE TABLE")
	}
	if !contains(createTableSQL, "BIGSERIAL") {
		t.Error("SQL missing BIGSERIAL for auto-increment")
	}
	if !contains(createTableSQL, "PRIMARY KEY") {
		t.Error("SQL missing PRIMARY KEY")
	}
	if !contains(createTableSQL, `"id" BIGSERIAL`) {
		t.Error("SQL missing quoted id column")
	}

	t.Log("✅ PostgreSQL dialect test passed!")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsSubstring(s, substr)
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
