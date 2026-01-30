package database

import (
	"database/sql"
	"fmt"
)

// GetRecentEntries returns the most recent log entries with pagination
func (db *DB) GetRecentEntries(limit, offset int) ([]LogEntry, error) {
	query := `
		SELECT id, timestamp, endpoint, method, model, prompt, response, status_code, latency_ms, stream, backend_type, error, frontend_url, backend_url, frontend_request, frontend_response, backend_request, backend_response
		FROM request
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?
	`

	rows, err := db.conn.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query entries: %w", err)
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var entry LogEntry
		err := rows.Scan(
			&entry.ID,
			&entry.Timestamp,
			&entry.Endpoint,
			&entry.Method,
			&entry.Model,
			&entry.Prompt,
			&entry.Response,
			&entry.StatusCode,
			&entry.LatencyMs,
			&entry.Stream,
			&entry.BackendType,
			&entry.Error,
			&entry.FrontendURL,
			&entry.BackendURL,
			&entry.FrontendRequest,
			&entry.FrontendResponse,
			&entry.BackendRequest,
			&entry.BackendResponse,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan entry: %w", err)
		}
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return entries, nil
}

// GetEntryByID returns a single log entry by ID
func (db *DB) GetEntryByID(id int64) (*LogEntry, error) {
	query := `
		SELECT id, timestamp, endpoint, method, model, prompt, response, status_code, latency_ms, stream, backend_type, error, frontend_url, backend_url, frontend_request, frontend_response, backend_request, backend_response
		FROM request
		WHERE id = ?
	`

	var entry LogEntry
	err := db.conn.QueryRow(query, id).Scan(
		&entry.ID,
		&entry.Timestamp,
		&entry.Endpoint,
		&entry.Method,
		&entry.Model,
		&entry.Prompt,
		&entry.Response,
		&entry.StatusCode,
		&entry.LatencyMs,
		&entry.Stream,
		&entry.BackendType,
		&entry.Error,
		&entry.FrontendURL,
		&entry.BackendURL,
		&entry.FrontendRequest,
		&entry.FrontendResponse,
		&entry.BackendRequest,
		&entry.BackendResponse,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query entry: %w", err)
	}

	return &entry, nil
}

// GetTotalCount returns the total number of log entries
func (db *DB) GetTotalCount() (int64, error) {
	var count int64
	err := db.conn.QueryRow("SELECT COUNT(*) FROM request").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count entries: %w", err)
	}
	return count, nil
}
