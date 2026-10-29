package sqlstore

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm/logger"
)

// DatabaseConfig holds the full database configuration.
// Modeled after Grafana's DatabaseConfig with support for structured
// connection parameters, URL parsing, and automatic DSN building.
type DatabaseConfig struct {
	// --- 基础连接 ---
	// Type is the database type: "sqlite", "sqlite3", "postgres", "mysql", or "clickhouse".
	Type string
	// Host is the database server address, e.g. "localhost:5432".
	Host string
	// Name is the database name.
	Name string
	// User is the database user.
	User string
	// Password is the database password.
	Password string
	// Path is the SQLite file path (only for sqlite type).
	Path string
	// ConnectionString overrides all other fields if set.
	// When non-empty, BuildConnectionString is a no-op.
	ConnectionString string
	// UrlQueryParams holds additional query parameters appended to the connection string.
	UrlQueryParams url.Values

	// --- SQLite ---
	// CacheMode is the SQLite cache mode: "private" or "shared".
	CacheMode string

	// --- 连接池 ---
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime int // seconds

	// --- 重试 (分离查询 vs 事务) ---
	// QueryRetries is the number of retries for non-transactional operations (Exec).
	QueryRetries int
	// TransactionRetries is the number of retries for transactional operations (Tx).
	TransactionRetries int
	// RetryInterval is the base interval between retries.
	RetryInterval time.Duration

	// --- 其他 ---
	// IsolationLevel sets the transaction isolation level (Postgres only).
	IsolationLevel string
	// LogQueries enables query logging.
	LogQueries bool
	// LogLevel is the GORM logger level.
	LogLevel logger.LogLevel
	// QueryTimeout is the per-query timeout.
	QueryTimeout time.Duration
}

// ConfigOption is a functional option for DatabaseConfig.
type ConfigOption func(*DatabaseConfig)

// WithDBType sets the database type.
func WithDBType(dbType string) ConfigOption {
	return func(c *DatabaseConfig) { c.Type = dbType }
}

// WithHost sets the database host.
func WithHost(host string) ConfigOption {
	return func(c *DatabaseConfig) { c.Host = host }
}

// WithDBName sets the database name.
func WithDBName(name string) ConfigOption {
	return func(c *DatabaseConfig) { c.Name = name }
}

// WithUser sets the database user.
func WithUser(user string) ConfigOption {
	return func(c *DatabaseConfig) { c.User = user }
}

// WithPassword sets the database password.
func WithPassword(password string) ConfigOption {
	return func(c *DatabaseConfig) { c.Password = password }
}

// WithConnectionString directly sets the DSN, bypassing BuildConnectionString.
func WithConnectionString(dsn string) ConfigOption {
	return func(c *DatabaseConfig) { c.ConnectionString = dsn }
}

// WithConnPool configures connection pool parameters.
func WithConnPool(maxOpen, maxIdle, maxLifetimeSec int) ConfigOption {
	return func(c *DatabaseConfig) {
		c.MaxOpenConns = maxOpen
		c.MaxIdleConns = maxIdle
		c.ConnMaxLifetime = maxLifetimeSec
	}
}

// WithRetries sets retry counts separately for queries and transactions.
func WithRetries(queryRetries, txRetries int) ConfigOption {
	return func(c *DatabaseConfig) {
		c.QueryRetries = queryRetries
		c.TransactionRetries = txRetries
	}
}

// NewDatabaseConfig creates a DatabaseConfig with defaults and applies options.
func NewDatabaseConfig(opts ...ConfigOption) (*DatabaseConfig, error) {
	cfg := &DatabaseConfig{
		Type:               "sqlite",
		CacheMode:          "private",
		MaxOpenConns:       0, // unlimited
		MaxIdleConns:       2,
		ConnMaxLifetime:    14400, // 4 hours
		QueryRetries:       0,
		TransactionRetries: 5,
		RetryInterval:      10 * time.Millisecond,
		LogLevel:           logger.Warn,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	// If no ConnectionString, build it
	if cfg.ConnectionString == "" {
		if err := cfg.BuildConnectionString(); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// ParseURL parses a database URL and populates the config fields.
// Supported formats:
//   - postgres://user:password@host:port/dbname?sslmode=disable
//   - sqlite:///path/to/db.sqlite
func (cfg *DatabaseConfig) ParseURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("failed to parse database URL: %w", err)
	}

	cfg.Type = u.Scheme
	if cfg.Type == "" {
		return fmt.Errorf("missing scheme in database URL: %s", rawURL)
	}

	switch cfg.Type {
	case "postgres":
		cfg.Host = u.Host
		if u.Path != "" {
			cfg.Name = strings.TrimPrefix(u.Path, "/")
		}
		if u.User != nil {
			cfg.User = u.User.Username()
			cfg.Password, _ = u.User.Password()
		}
		// Extract extra query params
		params := u.Query()
		if len(params) > 0 {
			cfg.UrlQueryParams = params
		}

	case "mysql":
		cfg.Host = u.Host
		if u.Path != "" {
			cfg.Name = strings.TrimPrefix(u.Path, "/")
		}
		if u.User != nil {
			cfg.User = u.User.Username()
			cfg.Password, _ = u.User.Password()
		}
		params := u.Query()
		if len(params) > 0 {
			cfg.UrlQueryParams = params
		}

	case "sqlite", "sqlite3":
		// For sqlite, the path is the file path
		if u.Host != "" {
			cfg.Path = filepath.Join(u.Host, u.Path)
		} else {
			cfg.Path = strings.TrimPrefix(u.Path, "/")
		}
		params := u.Query()
		if v := params.Get("cache"); v != "" {
			cfg.CacheMode = v
			params.Del("cache")
		}
		if len(params) > 0 {
			cfg.UrlQueryParams = params
		}

	default:
		return fmt.Errorf("unsupported database scheme: %s", cfg.Type)
	}

	return nil
}

// BuildConnectionString constructs a DSN from the structured config fields.
// If ConnectionString is already set, this is a no-op.
func (cfg *DatabaseConfig) BuildConnectionString() error {
	if cfg.ConnectionString != "" {
		return nil
	}

	switch cfg.Type {
	case "postgres":
		return cfg.buildPostgresConnectionString()
	case "mysql":
		return cfg.buildMySQLConnectionString()
	case "sqlite", "sqlite3":
		return cfg.buildSQLiteConnectionString()
	case "clickhouse":
		return cfg.buildClickHouseConnectionString()
	default:
		return fmt.Errorf("unsupported database type for connection string building: %s", cfg.Type)
	}
}

func (cfg *DatabaseConfig) buildPostgresConnectionString() error {
	host, port := splitHostPort(cfg.Host, "127.0.0.1", "5432")

	parts := []string{
		fmt.Sprintf("host=%s", host),
		fmt.Sprintf("port=%s", port),
		fmt.Sprintf("user=%s", cfg.User),
		fmt.Sprintf("dbname=%s", cfg.Name),
		"sslmode=disable",
	}

	if cfg.Password != "" {
		parts = append(parts, fmt.Sprintf("password=%s", cfg.Password))
	}

	if cfg.IsolationLevel != "" {
		parts = append(parts, fmt.Sprintf("options=--transaction-isolation=%s", cfg.IsolationLevel))
	}

	// Append extra query params
	extra := buildExtraParams(' ', cfg.UrlQueryParams)
	if extra != "" {
		parts = append(parts, extra)
	}

	cfg.ConnectionString = strings.Join(parts, " ")
	return nil
}

func (cfg *DatabaseConfig) buildSQLiteConnectionString() error {
	dbPath := cfg.Path
	if dbPath == "" {
		return fmt.Errorf("sqlite requires an explicit path or connection string")
	}

	// Ensure parent directory exists
	if !strings.HasPrefix(dbPath, ":memory:") && !strings.HasPrefix(dbPath, "file::memory:") {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
			return fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	cacheMode := cfg.CacheMode
	if cacheMode == "" {
		cacheMode = "private"
	}

	dsn := fmt.Sprintf("file:%s?cache=%s&mode=rwc", dbPath, cacheMode)

	dsn += buildExtraParams('&', cfg.UrlQueryParams)

	cfg.ConnectionString = dsn
	return nil
}

func (cfg *DatabaseConfig) buildMySQLConnectionString() error {
	host, port := splitHostPort(cfg.Host, "127.0.0.1", "3306")
	user := cfg.User
	if user == "" {
		user = "root"
	}

	dbName := cfg.Name
	if dbName == "" {
		dbName = "mysql"
	}

	base := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", user, cfg.Password, host, port, dbName)

	params := cloneURLValues(cfg.UrlQueryParams)
	if params == nil {
		params = url.Values{}
	}
	if _, ok := params["parseTime"]; !ok {
		params.Set("parseTime", "true")
	}

	cfg.ConnectionString = base
	if len(params) > 0 {
		cfg.ConnectionString += "?" + params.Encode()
	}

	return nil
}

func (cfg *DatabaseConfig) buildClickHouseConnectionString() error {
	// ClickHouse DSN format:
	// - For HTTP protocol: http://username:password@host:port/database?param=value
	// - For Native protocol: clickhouse://username:password@host:port/database?param=value
	//
	// We determine the protocol based on the port:
	// - Port 8123 -> HTTP
	// - Port 8443 -> HTTPS
	// - Port 9000, 9440, 9004 -> Native (clickhouse://)
	// - Default: Native (clickhouse://)
	if cfg.ConnectionString != "" {
		return nil
	}

	host := cfg.Host
	if host == "" {
		host = "localhost:9000"
	}

	user := cfg.User
	if user == "" {
		user = "default"
	}

	password := cfg.Password
	name := cfg.Name
	if name == "" {
		name = "default"
	}

	// URL-encode password to handle special characters like @, :, etc.
	encodedPassword := url.QueryEscape(password)

	// Determine scheme based on port
	scheme := "clickhouse"
	if strings.Contains(host, ":8123") {
		scheme = "http"
	} else if strings.Contains(host, ":8443") {
		scheme = "https"
	}

	var dsn string
	if password != "" {
		dsn = fmt.Sprintf("%s://%s:%s@%s/%s", scheme, user, encodedPassword, host, name)
	} else {
		dsn = fmt.Sprintf("%s://%s@%s/%s", scheme, user, host, name)
	}

	// Add query parameters
	if len(cfg.UrlQueryParams) > 0 {
		dsn += "?" + cfg.UrlQueryParams.Encode()
	}

	cfg.ConnectionString = dsn
	return nil
}

// splitHostPort splits a host:port string with defaults.
func splitHostPort(hostPort, defaultHost, defaultPort string) (string, string) {
	if hostPort == "" {
		return defaultHost, defaultPort
	}

	parts := strings.SplitN(hostPort, ":", 2)
	host := parts[0]
	port := defaultPort

	if host == "" {
		host = defaultHost
	}
	if len(parts) == 2 && parts[1] != "" {
		port = parts[1]
	}

	return host, port
}

// buildExtraParams formats URL query parameters as a connection string suffix.
func buildExtraParams(sep rune, params map[string][]string) string {
	if len(params) == 0 {
		return ""
	}

	var sb strings.Builder
	for key, values := range params {
		for _, value := range values {
			sb.WriteRune(sep)
			sb.WriteString(key)
			sb.WriteRune('=')
			sb.WriteString(value)
		}
	}
	return sb.String()
}

func cloneURLValues(v url.Values) url.Values {
	if len(v) == 0 {
		return nil
	}

	out := make(url.Values, len(v))
	for k, values := range v {
		copied := make([]string, len(values))
		copy(copied, values)
		out[k] = copied
	}
	return out
}
