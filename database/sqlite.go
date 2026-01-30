package database

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite database connection
type DB struct {
	conn *sql.DB
}

// LogEntry represents a logged request/response
type LogEntry struct {
	ID               int64
	Timestamp        time.Time
	Endpoint         string
	Method           string
	Model            string
	Prompt           string
	Response         string
	StatusCode       int
	LatencyMs        int64
	Stream           bool
	BackendType      string
	Error            string
	FrontendRequest  string // Raw frontend request JSON
	FrontendResponse string // Raw frontend response JSON
	BackendRequest   string // Raw backend request JSON
	BackendResponse  string // Raw backend response data
}

// New creates a new database connection and initializes the schema
func New(path string) (*DB, error) {
	conn, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.initSchema(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

// initSchema creates the required tables if they don't exist
func (db *DB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL,
		endpoint TEXT NOT NULL,
		method TEXT NOT NULL,
		model TEXT,
		prompt TEXT,
		response TEXT,
		status_code INTEGER,
		latency_ms INTEGER,
		stream BOOLEAN,
		backend_type TEXT,
		error TEXT,
		frontend_request TEXT,
		frontend_response TEXT,
		backend_request TEXT,
		backend_response TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_timestamp ON logs(timestamp);
	CREATE INDEX IF NOT EXISTS idx_endpoint ON logs(endpoint);
	CREATE INDEX IF NOT EXISTS idx_model ON logs(model);
	`

	_, err := db.conn.Exec(schema)
	return err
}

// Log inserts a log entry into the database
func (db *DB) Log(entry LogEntry) error {
	query := `
		INSERT INTO logs (timestamp, endpoint, method, model, prompt, response, status_code, latency_ms, stream, backend_type, error, frontend_request, frontend_response, backend_request, backend_response)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := db.conn.Exec(
		query,
		entry.Timestamp,
		entry.Endpoint,
		entry.Method,
		entry.Model,
		entry.Prompt,
		entry.Response,
		entry.StatusCode,
		entry.LatencyMs,
		entry.Stream,
		entry.BackendType,
		entry.Error,
		entry.FrontendRequest,
		entry.FrontendResponse,
		entry.BackendRequest,
		entry.BackendResponse,
	)

	if err != nil {
		return fmt.Errorf("failed to insert log entry: %w", err)
	}

	return nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}
