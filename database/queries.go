package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const logEntryColumns = "id, timestamp, endpoint, method, model, prompt, response, status_code, latency_ms, stream, backend_type, error, frontend_url, backend_url, frontend_request, frontend_response, backend_request, backend_response, last_message"

// LogFilter contains filters for querying request logs.
type LogFilter struct {
	Model       string
	Endpoint    string
	BackendType string
	Query       string
	Order       string
	Status      *int
	ErrorsOnly  bool
	Since       *time.Time
	Until       *time.Time
	Limit       int
	Offset      int
}

// GetRecentEntries returns the most recent log entries with pagination
func (db *DB) GetRecentEntries(limit, offset int) ([]LogEntry, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM request
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?
	`, logEntryColumns)

	rows, err := db.conn.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query entries: %w", err)
	}
	defer rows.Close()

	return scanLogEntries(rows)
}

// GetEntryByID returns a single log entry by ID
func (db *DB) GetEntryByID(id int64) (*LogEntry, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM request
		WHERE id = ?
	`, logEntryColumns)

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

// GetEntries returns filtered log entries.
func (db *DB) GetEntries(filter LogFilter) ([]LogEntry, error) {
	where, args := buildLogWhere(filter)
	order := "DESC"
	if strings.EqualFold(filter.Order, "asc") {
		order = "ASC"
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM request
		%s
		ORDER BY timestamp %s, id %s
		LIMIT ? OFFSET ?
	`, logEntryColumns, where, order, order)
	args = append(args, limit, offset)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query entries: %w", err)
	}
	defer rows.Close()

	return scanLogEntries(rows)
}

// CountEntries returns the number of log entries matching the filter.
func (db *DB) CountEntries(filter LogFilter) (int64, error) {
	where, args := buildLogWhere(filter)
	query := fmt.Sprintf("SELECT COUNT(*) FROM request %s", where)

	var count int64
	if err := db.conn.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count entries: %w", err)
	}
	return count, nil
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

func buildLogWhere(filter LogFilter) (string, []interface{}) {
	var clauses []string
	var args []interface{}
	if filter.Model != "" {
		clauses = append(clauses, "model = ?")
		args = append(args, filter.Model)
	}
	if filter.Endpoint != "" {
		clauses = append(clauses, "endpoint = ?")
		args = append(args, filter.Endpoint)
	}
	if filter.BackendType != "" {
		clauses = append(clauses, "backend_type = ?")
		args = append(args, filter.BackendType)
	}
	if filter.Status != nil {
		clauses = append(clauses, "status_code = ?")
		args = append(args, *filter.Status)
	}
	if filter.ErrorsOnly {
		clauses = append(clauses, "(COALESCE(error, '') != '' OR status_code >= 400)")
	}
	if filter.Since != nil {
		clauses = append(clauses, "timestamp >= ?")
		args = append(args, *filter.Since)
	}
	if filter.Until != nil {
		clauses = append(clauses, "timestamp <= ?")
		args = append(args, *filter.Until)
	}
	if filter.Query != "" {
		clauses = append(clauses, "(LOWER(COALESCE(model, '')) LIKE ? OR LOWER(COALESCE(last_message, '')) LIKE ? OR LOWER(COALESCE(error, '')) LIKE ?)")
		q := "%" + strings.ToLower(filter.Query) + "%"
		args = append(args, q, q, q)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func scanLogEntries(rows *sql.Rows) ([]LogEntry, error) {
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
