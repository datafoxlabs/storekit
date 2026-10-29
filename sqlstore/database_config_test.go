package sqlstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDatabaseConfig(t *testing.T) {
	t.Run("default values", func(t *testing.T) {
		cfg, err := NewDatabaseConfig(func(cfg *DatabaseConfig) { cfg.Path = ":memory:" })
		require.NoError(t, err)

		assert.Equal(t, "sqlite", cfg.Type)
		assert.Equal(t, "private", cfg.CacheMode)
		assert.Equal(t, 2, cfg.MaxIdleConns)
		assert.Equal(t, 14400, cfg.ConnMaxLifetime)
		assert.Equal(t, 0, cfg.QueryRetries)
		assert.Equal(t, 5, cfg.TransactionRetries)
		assert.NotEmpty(t, cfg.ConnectionString) // auto-built
	})

	t.Run("ConnectionString takes priority", func(t *testing.T) {
		dsn := "host=myhost port=1234 user=me dbname=mydb sslmode=disable"
		cfg, err := NewDatabaseConfig(
			WithDBType("postgres"),
			WithHost("should-be-ignored"),
			WithConnectionString(dsn),
		)
		require.NoError(t, err)
		assert.Equal(t, dsn, cfg.ConnectionString)
	})

	t.Run("functional options override defaults", func(t *testing.T) {
		cfg, err := NewDatabaseConfig(
			WithDBType("postgres"),
			WithHost("db.example.com:5433"),
			WithDBName("testdb"),
			WithUser("admin"),
			WithPassword("secret"),
			WithConnPool(50, 10, 3600),
			WithRetries(3, 10),
		)
		require.NoError(t, err)
		assert.Equal(t, "postgres", cfg.Type)
		assert.Equal(t, 50, cfg.MaxOpenConns)
		assert.Equal(t, 3, cfg.QueryRetries)
		assert.Equal(t, 10, cfg.TransactionRetries)
		assert.Contains(t, cfg.ConnectionString, "host=db.example.com")
		assert.Contains(t, cfg.ConnectionString, "port=5433")
		assert.Contains(t, cfg.ConnectionString, "dbname=testdb")
		assert.Contains(t, cfg.ConnectionString, "password=secret")
	})
}

func TestParseURL(t *testing.T) {
	t.Run("postgres URL", func(t *testing.T) {
		cfg := &DatabaseConfig{}
		err := cfg.ParseURL("postgres://admin:pass123@db.host.com:5433/mydb")
		require.NoError(t, err)

		assert.Equal(t, "postgres", cfg.Type)
		assert.Equal(t, "db.host.com:5433", cfg.Host)
		assert.Equal(t, "mydb", cfg.Name)
		assert.Equal(t, "admin", cfg.User)
		assert.Equal(t, "pass123", cfg.Password)
	})

	t.Run("postgres URL with query params", func(t *testing.T) {
		cfg := &DatabaseConfig{}
		err := cfg.ParseURL("postgres://user@host/db?connect_timeout=10")
		require.NoError(t, err)

		assert.Equal(t, "postgres", cfg.Type)
		require.NotNil(t, cfg.UrlQueryParams)
		assert.Equal(t, "10", cfg.UrlQueryParams.Get("connect_timeout"))
	})

	t.Run("sqlite URL", func(t *testing.T) {
		cfg := &DatabaseConfig{}
		err := cfg.ParseURL("sqlite:///tmp/test.db")
		require.NoError(t, err)

		assert.Equal(t, "sqlite", cfg.Type)
		assert.Equal(t, "tmp/test.db", cfg.Path)
	})

	t.Run("sqlite URL with cache param", func(t *testing.T) {
		cfg := &DatabaseConfig{}
		err := cfg.ParseURL("sqlite:///data/app.db?cache=shared")
		require.NoError(t, err)

		assert.Equal(t, "shared", cfg.CacheMode)
	})

	t.Run("mysql URL", func(t *testing.T) {
		cfg := &DatabaseConfig{}
		err := cfg.ParseURL("mysql://root:secret@localhost:3306/app?charset=utf8mb4")
		require.NoError(t, err)

		assert.Equal(t, "mysql", cfg.Type)
		assert.Equal(t, "localhost:3306", cfg.Host)
		assert.Equal(t, "app", cfg.Name)
		assert.Equal(t, "root", cfg.User)
		assert.Equal(t, "secret", cfg.Password)
		require.NotNil(t, cfg.UrlQueryParams)
		assert.Equal(t, "utf8mb4", cfg.UrlQueryParams.Get("charset"))
	})

	t.Run("unsupported scheme", func(t *testing.T) {
		cfg := &DatabaseConfig{}
		err := cfg.ParseURL("oracle://root@localhost/db")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported database scheme")
	})

	t.Run("missing scheme", func(t *testing.T) {
		cfg := &DatabaseConfig{}
		err := cfg.ParseURL("no-scheme-here")
		require.Error(t, err)
	})
}

func TestBuildConnectionString(t *testing.T) {
	t.Run("postgres with defaults", func(t *testing.T) {
		cfg := &DatabaseConfig{
			Type: "postgres",
			User: "grafana",
			Name: "grafanadb",
		}
		err := cfg.BuildConnectionString()
		require.NoError(t, err)
		assert.Contains(t, cfg.ConnectionString, "host=127.0.0.1")
		assert.Contains(t, cfg.ConnectionString, "port=5432")
		assert.Contains(t, cfg.ConnectionString, "user=grafana")
		assert.Contains(t, cfg.ConnectionString, "dbname=grafanadb")
		assert.Contains(t, cfg.ConnectionString, "sslmode=disable")
	})

	t.Run("postgres with password and custom host", func(t *testing.T) {
		cfg := &DatabaseConfig{
			Type:     "postgres",
			Host:     "remotehost:5433",
			User:     "admin",
			Password: "secret",
			Name:     "appdb",
		}
		err := cfg.BuildConnectionString()
		require.NoError(t, err)
		assert.Contains(t, cfg.ConnectionString, "host=remotehost")
		assert.Contains(t, cfg.ConnectionString, "port=5433")
		assert.Contains(t, cfg.ConnectionString, "password=secret")
	})

	t.Run("postgres with isolation level", func(t *testing.T) {
		cfg := &DatabaseConfig{
			Type:           "postgres",
			User:           "user",
			Name:           "db",
			IsolationLevel: "read committed",
		}
		err := cfg.BuildConnectionString()
		require.NoError(t, err)
		assert.Contains(t, cfg.ConnectionString, "options=--transaction-isolation=read committed")
	})

	t.Run("sqlite requires explicit path", func(t *testing.T) {
		_, err := NewDatabaseConfig()
		require.ErrorContains(t, err, "explicit path")
	})

	t.Run("sqlite with custom path and cache", func(t *testing.T) {
		cfg := &DatabaseConfig{
			Type:      "sqlite",
			Path:      "/tmp/test.db",
			CacheMode: "shared",
		}
		err := cfg.BuildConnectionString()
		require.NoError(t, err)
		assert.Contains(t, cfg.ConnectionString, "file:/tmp/test.db")
		assert.Contains(t, cfg.ConnectionString, "cache=shared")
	})

	t.Run("mysql with defaults", func(t *testing.T) {
		cfg := &DatabaseConfig{
			Type: "mysql",
		}
		err := cfg.BuildConnectionString()
		require.NoError(t, err)
		assert.Contains(t, cfg.ConnectionString, "root:@tcp(127.0.0.1:3306)/mysql")
		assert.Contains(t, cfg.ConnectionString, "parseTime=true")
	})

	t.Run("mysql with custom host and params", func(t *testing.T) {
		cfg := &DatabaseConfig{
			Type: "mysql",
			Host: "mysql.internal:3307",
			Name: "appdb",
			User: "app",
			UrlQueryParams: map[string][]string{
				"charset": {"utf8mb4"},
			},
		}
		err := cfg.BuildConnectionString()
		require.NoError(t, err)
		assert.Contains(t, cfg.ConnectionString, "app:@tcp(mysql.internal:3307)/appdb")
		assert.Contains(t, cfg.ConnectionString, "charset=utf8mb4")
		assert.Contains(t, cfg.ConnectionString, "parseTime=true")
	})

	t.Run("mysql keeps raw password for driver dsn", func(t *testing.T) {
		cfg := &DatabaseConfig{
			Type:     "mysql",
			Host:     "mysql.internal:3307",
			Name:     "appdb",
			User:     "app",
			Password: "synthetic-only-dsn-password",
		}
		err := cfg.BuildConnectionString()
		require.NoError(t, err)
		assert.Contains(t, cfg.ConnectionString, "app:synthetic-only-dsn-password@tcp(mysql.internal:3307)/appdb")
		assert.Contains(t, cfg.ConnectionString, "parseTime=true")
	})

	t.Run("noop when ConnectionString already set", func(t *testing.T) {
		dsn := "already-set-dsn"
		cfg := &DatabaseConfig{
			Type:             "postgres",
			ConnectionString: dsn,
		}
		err := cfg.BuildConnectionString()
		require.NoError(t, err)
		assert.Equal(t, dsn, cfg.ConnectionString)
	})

	t.Run("unsupported type", func(t *testing.T) {
		cfg := &DatabaseConfig{Type: "oracle"}
		err := cfg.BuildConnectionString()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported database type")
	})
}

func TestSplitHostPort(t *testing.T) {
	tests := []struct {
		input    string
		wantHost string
		wantPort string
	}{
		{"", "127.0.0.1", "5432"},
		{"myhost", "myhost", "5432"},
		{"myhost:9999", "myhost", "9999"},
		{":9999", "127.0.0.1", "9999"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			host, port := splitHostPort(tt.input, "127.0.0.1", "5432")
			assert.Equal(t, tt.wantHost, host)
			assert.Equal(t, tt.wantPort, port)
		})
	}
}
