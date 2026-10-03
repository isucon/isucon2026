package isureal

import (
	"net"
	"net/url"
	"os"
)

type Config struct {
	Region     string
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	// DBReadHost and DBReadPort name a synchronous hot standby in this region.
	// Read-only requests use it; empty means the primary serves reads too.
	DBReadHost string
	DBReadPort string
}

func ConfigFromEnv() *Config {
	return &Config{
		Region:     envOrDefault("ISUREAL_REGION", "asia"),
		DBHost:     envOrDefault("ISUREAL_DB_HOST", "127.0.0.1"),
		DBPort:     envOrDefault("ISUREAL_DB_PORT", "5432"),
		DBUser:     envOrDefault("ISUREAL_DB_USER", "isureal"),
		DBPassword: envOrDefault("ISUREAL_DB_PASSWORD", "isureal"),
		DBName:     envOrDefault("ISUREAL_DB_NAME", "isureal"),
		DBReadHost: os.Getenv("ISUREAL_DB_READ_HOST"),
		DBReadPort: envOrDefault("ISUREAL_DB_READ_PORT", "5432"),
	}
}

func (c *Config) DatabaseDSN() string {
	return c.dsn(c.DBHost, c.DBPort)
}

// ReadDatabaseDSN is the DSN for read-only requests.
func (c *Config) ReadDatabaseDSN() string {
	if c.DBReadHost == "" {
		return c.DatabaseDSN()
	}
	return c.dsn(c.DBReadHost, c.DBReadPort)
}

func (c *Config) dsn(host, port string) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUser, c.DBPassword),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + c.DBName,
	}
	// Prepared statements are cached per connection, so each query text pays the
	// Describe/Prepare exchange once per connection. Generic plans avoid
	// re-planning the large visibility queries on every execution.
	q := u.Query()
	q.Set("default_query_exec_mode", "cache_statement")
	q.Set("plan_cache_mode", "force_generic_plan")
	u.RawQuery = q.Encode()
	return u.String()
}

func envOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
