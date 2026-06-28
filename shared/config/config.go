// Package config centralises all runtime configuration for the TeamChat backend.
//
// Every value is read from an environment variable with a sensible default so
// the binary can run locally without any setup while still being configurable
// in Docker / Kubernetes via environment injection.
//
// Usage:
//
//	cfg := config.Load()
//	db, err := sql.Open("mysql", cfg.MySQLDSN)
package config

import "os"

// Config holds all runtime settings for the application.
// Populate it once in main() and pass it down to constructors.
type Config struct {
	// MySQLDSN is the Data Source Name used by database/sql to connect to MySQL.
	// Format: user:password@tcp(host:port)/dbname?parseTime=true
	MySQLDSN string

	// RedisAddr is the host:port of the Redis server used for Pub/Sub and
	// idempotency key storage.
	RedisAddr string

	// APIPort is the :port the HTTP API server will bind to.
	APIPort string

	// WSPort is the :port the WebSocket worker will bind to.
	WSPort string

	// WorkerID is the Snowflake generator node identifier (0–1023).
	// In a multi-node deployment each instance must have a unique WorkerID.
	// Typically derived from a Kubernetes StatefulSet pod ordinal.
	WorkerID int64
}

// Load reads configuration from environment variables, falling back to
// hard-coded defaults that target the local Docker Compose stack.
//
// Environment variables:
//
//	MYSQL_DSN   — full MySQL DSN
//	REDIS_ADDR  — Redis address (host:port)
//	API_PORT    — HTTP API listen port (default :8080)
//	WS_PORT     — WebSocket worker listen port (default :8081)
//	WORKER_ID   — Snowflake worker node ID 0–1023 (default 1)
func Load() *Config {
	return &Config{
		MySQLDSN:  getEnv("MYSQL_DSN", "root:rootpassword@tcp(127.0.0.1:3307)/teamchat?parseTime=true"),
		RedisAddr: getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		APIPort:   getEnv("API_PORT", ":8080"),
		WSPort:    getEnv("WS_PORT", ":8081"),
		WorkerID:  getEnvInt64("WORKER_ID", 1),
	}
}

// getEnv returns the environment variable for key, or fallback if not set.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvInt64 parses an integer environment variable, returning fallback on
// missing or invalid values.
func getEnvInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	var n int64
	for _, c := range v {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
