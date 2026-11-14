// Package models provides data structures and database operations for Reign.
package models

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// InitDB initializes the SQLite database with the required schema.
func InitDB(dbPath string) (*sql.DB, error) {
	// Ensure the directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open database. busy_timeout makes concurrent writers (API handlers,
	// background operations, health checks) wait for the lock instead of
	// failing with SQLITE_BUSY; WAL lets readers proceed during a write.
	// foreign_keys enables the ON DELETE CASCADE the schema relies on.
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Create schema
	if err := createSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	return db, nil
}

// createSchema creates all required tables if they don't exist.
func createSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS services (
		id              TEXT PRIMARY KEY,
		name            TEXT NOT NULL,
		type            TEXT NOT NULL CHECK (type IN ('compose', 'binary')),
		path            TEXT NOT NULL,
		command         TEXT,
		work_dir        TEXT,
		env_file        TEXT,
		pull_policy     TEXT,
		enabled         INTEGER NOT NULL DEFAULT 1,
		infrastructure  INTEGER NOT NULL DEFAULT 0,
		created_at      TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS service_state (
		service_id      TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
		status          TEXT NOT NULL DEFAULT 'stopped',
		pid             INTEGER,
		started_at      TEXT,
		stopped_at      TEXT,
		restart_count   INTEGER NOT NULL DEFAULT 0,
		last_error      TEXT,
		updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS service_events (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		service_id      TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
		event_type      TEXT NOT NULL,
		message         TEXT,
		requester       TEXT,
		created_at      TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX IF NOT EXISTS idx_service_events_service_id ON service_events(service_id);
	CREATE INDEX IF NOT EXISTS idx_service_events_created_at ON service_events(created_at);
	`

	_, err := db.Exec(schema)
	if err != nil {
		return err
	}

	// Migrate existing databases: add columns introduced after the first release.
	migrations := []string{
		"ALTER TABLE services ADD COLUMN work_dir TEXT",
		"ALTER TABLE services ADD COLUMN env_file TEXT",
		"ALTER TABLE services ADD COLUMN pull_policy TEXT",
		"ALTER TABLE service_events ADD COLUMN requester TEXT",
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("migration %q: %w", m, err)
		}
	}

	// Disabled services that are not running read "disabled". Older versions
	// left some as "stopped" (or "failed"), so reign ls showed both.
	if _, err := db.Exec(`
		UPDATE service_state SET status = 'disabled'
		WHERE status IN ('stopped', 'failed')
		  AND service_id IN (SELECT id FROM services WHERE enabled = 0)
	`); err != nil {
		return fmt.Errorf("normalize disabled status: %w", err)
	}

	return nil
}
