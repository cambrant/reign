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

	// Open database
	db, err := sql.Open("sqlite", dbPath+"?_foreign_keys=on")
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
		created_at      TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE INDEX IF NOT EXISTS idx_service_events_service_id ON service_events(service_id);
	CREATE INDEX IF NOT EXISTS idx_service_events_created_at ON service_events(created_at);
	`

	_, err := db.Exec(schema)
	if err != nil {
		return err
	}

	// Migrate existing databases: add work_dir column if missing
	_, err = db.Exec("ALTER TABLE services ADD COLUMN work_dir TEXT")
	if err != nil {
		// Ignore "duplicate column" error — column already exists
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}

	// Migrate existing databases: add env_file column if missing
	_, err = db.Exec("ALTER TABLE services ADD COLUMN env_file TEXT")
	if err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}

	return nil
}
