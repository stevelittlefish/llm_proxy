package database

import (
	"database/sql"
	"fmt"
)

// GetRecentEntries returns the most recent log entries with pagination
func (db *DB) GetRecentEntries(limit, offset int) ([]LogEntry, error) {
	query := `
		SELECT id, timestamp, endpoint, method, model, prompt, response, status_code, latency_ms, stream, backend_type, error, frontend_url, backend_url, frontend_request, frontend_response, backend_request, backend_response, last_message
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
			&entry.LastMessage,
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
		SELECT id, timestamp, endpoint, method, model, prompt, response, status_code, latency_ms, stream, backend_type, error, frontend_url, backend_url, frontend_request, frontend_response, backend_request, backend_response, last_message
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
		&entry.LastMessage,
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

// GetNextEntryID returns the ID of the next entry (chronologically newer, higher ID)
func (db *DB) GetNextEntryID(currentID int64) (*int64, error) {
	query := `
		SELECT id
		FROM request
		WHERE id > ?
		ORDER BY id ASC
		LIMIT 1
	`

	var nextID int64
	err := db.conn.QueryRow(query, currentID).Scan(&nextID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query next entry: %w", err)
	}

	return &nextID, nil
}

// GetPreviousEntryID returns the ID of the previous entry (chronologically older, lower ID)
func (db *DB) GetPreviousEntryID(currentID int64) (*int64, error) {
	query := `
		SELECT id
		FROM request
		WHERE id < ?
		ORDER BY id DESC
		LIMIT 1
	`

	var prevID int64
	err := db.conn.QueryRow(query, currentID).Scan(&prevID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query previous entry: %w", err)
	}

	return &prevID, nil
}

// CleanupOldRequests removes the oldest requests, keeping only the most recent maxRequests
// Returns the number of deleted rows
func (db *DB) CleanupOldRequests(maxRequests int) (int64, error) {
	// First, get the total count
	var totalCount int64
	err := db.conn.QueryRow("SELECT COUNT(*) FROM request").Scan(&totalCount)
	if err != nil {
		return 0, fmt.Errorf("failed to count entries: %w", err)
	}

	// If we're under the limit, nothing to do
	if totalCount <= int64(maxRequests) {
		return 0, nil
	}

	// Delete all but the most recent maxRequests entries
	// We do this by deleting entries with IDs less than the ID of the Nth newest entry
	query := `
		DELETE FROM request
		WHERE id NOT IN (
			SELECT id
			FROM request
			ORDER BY timestamp DESC, id DESC
			LIMIT ?
		)
	`

	result, err := db.conn.Exec(query, maxRequests)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old requests: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}
