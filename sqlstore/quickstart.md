# sqlstore 快速上手

本文档通过实例演示 `sqlstore` 模块的常见用法。完整 API 参考请查阅 [README.md](./README.md)。

## 1. 创建 Store

### 通过 Open（生产用法）

```go
import (
    "log/slog"
    "go.opentelemetry.io/otel/trace/noop"
    "github.com/datafoxlabs/storekit/sqlstore"
    "github.com/datafoxlabs/storekit/sqlstore/postgres"
)

cfg := &sqlstore.DatabaseConfig{
    Type:               "postgres",
    Host:               "localhost:5432",
    User:               "myuser",
    Password:           "mypass",
    Name:               "mydb",
    MaxOpenConns:        100,
    MaxIdleConns:        10,
    ConnMaxLifetime:     3600,
    QueryRetries:        3,
    TransactionRetries:  5,
}

if err := cfg.BuildConnectionString(); err != nil {
    panic(err)
}

logger := slog.Default()
store, err := sqlstore.Open(cfg, postgres.Open, logger, noop.NewTracerProvider())
if err != nil {
    panic(err)
}
defer store.Close()
```

### 通过函数式选项

```go
cfg, err := sqlstore.NewDatabaseConfig(
    sqlstore.WithDBType("postgres"),
    sqlstore.WithHost("db.example.com:5432"),
    sqlstore.WithDBName("appdb"),
    sqlstore.WithUser("admin"),
    sqlstore.WithPassword("secret"),
    sqlstore.WithConnPool(100, 10, 3600),
    sqlstore.WithRetries(3, 5),
)
if err != nil {
    panic(err)
}

store, err := sqlstore.Open(cfg, postgres.Open, logger, noop.NewTracerProvider())
```

### 通过 URL 解析

```go
cfg := &sqlstore.DatabaseConfig{}
if err := cfg.ParseURL("postgres://admin:secret@db.example.com:5432/appdb?sslmode=disable"); err != nil {
    panic(err)
}
if err := cfg.BuildConnectionString(); err != nil {
    panic(err)
}

store, err := sqlstore.Open(cfg, postgres.Open, logger, noop.NewTracerProvider())
```

## 2. CRUD 操作

### 通过 Exec（推荐）

`Exec` 内置重试逻辑，是执行数据库操作的首选方式：

```go
// 创建
err := store.Exec(ctx, func(db *gorm.DB) error {
    return db.Create(&User{Name: "张三", Email: "zhangsan@example.com"}).Error
})

// 查询
var user User
err := store.Exec(ctx, func(db *gorm.DB) error {
    return db.Where("name = ?", "张三").First(&user).Error
})

// 更新
err := store.Exec(ctx, func(db *gorm.DB) error {
    return db.Model(&User{}).Where("id = ?", 1).Update("name", "李四").Error
})

// 删除
err := store.Exec(ctx, func(db *gorm.DB) error {
    return db.Delete(&User{}, 1).Error
})
```

## 3. 事务

### 简单事务（Tx）

适合单个函数内的原子操作：

```go
err := store.Tx(ctx, func(tx *gorm.DB) error {
    if err := tx.Create(&Order{UserID: 1, Total: 100}).Error; err != nil {
        return err // 触发回滚
    }
    if err := tx.Model(&User{}).Where("id = ?", 1).
        Update("balance", gorm.Expr("balance - ?", 100)).Error; err != nil {
        return err // 触发回滚
    }
    return nil // 提交
})
```

### 跨服务事务（InTransaction）

适合需要在多个服务方法间共享事务的场景：

```go
err := store.InTransaction(ctx, func(txCtx context.Context) error {
    // 所有通过 txCtx 调用的 Exec 自动复用同一事务
    if err := store.Exec(txCtx, func(db *gorm.DB) error {
        return db.Create(&Order{UserID: 1, Total: 100}).Error
    }); err != nil {
        return err
    }

    // 可以传递 txCtx 给其他服务方法
    if err := userService.DeductBalance(txCtx, 1, 100); err != nil {
        return err // 整体回滚
    }

    return nil
})

// userService 内部：
func (s *UserService) DeductBalance(ctx context.Context, userID int, amount int) error {
    // 自动复用外层事务
    return s.store.Exec(ctx, func(db *gorm.DB) error {
        return db.Model(&User{}).Where("id = ?", userID).
            Update("balance", gorm.Expr("balance - ?", amount)).Error
    })
}
```

嵌套 `InTransaction` 调用不会创建新事务，而是复用外层事务：

```go
store.InTransaction(ctx, func(outerCtx context.Context) error {
    // 嵌套调用 — 复用外层事务，不创建嵌套事务
    return store.InTransaction(outerCtx, func(innerCtx context.Context) error {
        return store.Exec(innerCtx, func(db *gorm.DB) error {
            return db.Create(&record).Error
        })
    })
})
```

## 4. 批量操作

### BulkInsert

```go
records := make([]User, 1000)
for i := range records {
    records[i] = User{Name: fmt.Sprintf("user-%d", i)}
}

affected, err := sqlstore.BulkInsert(store.GetDB(), &records, 200) // 每批 200 条
fmt.Printf("插入 %d 条记录\n", affected)
```

### InBatches（通用分批）

```go
allIDs := []int{1, 2, 3, ..., 10000}

err := sqlstore.InBatches(allIDs, 500, func(batch interface{}) error {
    ids := batch.([]int)
    return store.GetDB().Where("id IN ?", ids).Delete(&User{}).Error
})
```

### 事务内批量插入

```go
err := store.InTransaction(ctx, func(txCtx context.Context) error {
    return store.Exec(txCtx, func(db *gorm.DB) error {
        records := make([]Item, 500)
        // ... 填充 records
        _, err := sqlstore.BulkInsert(db, &records, 100)
        return err
    })
})
```

## 5. 编写测试

### 基础用法

测试文件导入 `github.com/datafoxlabs/storekit/sqlstore/sqlstoretest`。

```go
func TestUserCreate(t *testing.T) {
    store := sqlstoretest.New(t, sqlstoretest.WithModels(&User{}))

    ctx := context.Background()
    err := store.Exec(ctx, func(db *gorm.DB) error {
        return db.Create(&User{Name: "test"}).Error
    })
    require.NoError(t, err)

    var user User
    err = store.GetDB().First(&user, "name = ?", "test").Error
    require.NoError(t, err)
    assert.Equal(t, "test", user.Name)
}
```

### 测试事务回滚

```go
func TestOrderRollback(t *testing.T) {
    store := sqlstoretest.New(t, sqlstoretest.WithModels(&Order{}))

    err := store.Tx(context.Background(), func(tx *gorm.DB) error {
        tx.Create(&Order{Total: 100})
        return fmt.Errorf("模拟失败")
    })
    require.Error(t, err)

    var count int64
    store.GetDB().Model(&Order{}).Count(&count)
    assert.Equal(t, int64(0), count) // 已回滚
}
```

### 测试隔离性

每个 `sqlstoretest.New` 返回独立数据库，互不影响：

```go
func TestIsolation(t *testing.T) {
    store1 := sqlstoretest.New(t, sqlstoretest.WithModels(&User{}))
    store2 := sqlstoretest.New(t, sqlstoretest.WithModels(&User{}))

    store1.GetDB().Create(&User{Name: "only-in-store1"})

    var count int64
    store2.GetDB().Model(&User{}).Count(&count)
    assert.Equal(t, int64(0), count) // store2 看不到 store1 的数据
}
```

### 显式选择测试数据库

```go
store := sqlstoretest.New(t, sqlstoretest.WithDBType("postgres"))
```

连接参数通过 `POSTGRES_HOST`、`POSTGRES_PORT`、`POSTGRES_USER`、`POSTGRES_PASSWORD`、
`POSTGRES_DB` 和 `POSTGRES_SSL` 设置；账号需要创建和删除临时数据库的权限。MySQL 使用
`WithDBType("mysql")` 和对应的 `MYSQL_*` 环境变量。详情见 [测试辅助](README.md#测试辅助)。

SQLite 内存模式通过选项选择，每个实例保持隔离：

```go
store := sqlstoretest.New(t, sqlstoretest.WithInMemory())
```

## 6. 健康检查与监控

```go
// 健康检查
if err := store.Ping(ctx); err != nil {
    log.Fatal("数据库不可达", "error", err)
}

// 连接池统计
stats := store.Stats()
fmt.Printf("打开连接: %d, 使用中: %d, 空闲: %d\n",
    stats.OpenConnections, stats.InUse, stats.Idle)
```

## 7. Schema 迁移

```go
err := store.AutoMigrate(
    &User{},
    &Order{},
    &Product{},
)
if err != nil {
    log.Fatal("迁移失败", "error", err)
}
```
