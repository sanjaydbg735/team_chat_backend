// Package repository provides data access objects (DAOs) for MySQL and Redis.
// Each struct wraps a single infrastructure dependency and exposes typed methods
// that the service layer calls — service code never writes raw SQL or Redis commands.
package repository

import (
	"database/sql"
	"time"

	_ "github.com/go-sql-driver/mysql" // registers the "mysql" driver with database/sql
)

// Connect opens a MySQL connection pool, verifies reachability with Ping, and
// configures sensible pool sizing to handle concurrent request spikes.
//
// The caller is responsible for calling db.Close() when the process exits.
func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}

	// Max open connections prevents exhausting MySQL's connection limit.
	// Rule of thumb: set to ~(CPU cores * 4) or your MySQL max_connections / num_pods.
	db.SetMaxOpenConns(25)

	// Idle connections are kept alive so subsequent requests don't pay TCP handshake
	// cost.  Too many idle connections waste MySQL memory.
	db.SetMaxIdleConns(5)

	// Recycle connections after 5 minutes to avoid hitting MySQL's wait_timeout,
	// which silently closes idle server-side connections.
	db.SetConnMaxLifetime(5 * time.Minute)

	// Ping validates the DSN and confirms the DB is reachable at startup.
	// Failing fast here surfaces misconfiguration before serving traffic.
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}
