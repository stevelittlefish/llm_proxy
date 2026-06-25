package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"llm_proxy/database"
)

// LogsAPIHandler serves JSON request logs.
type LogsAPIHandler struct {
	db *database.DB
}

func NewLogsAPIHandler(db *database.DB) *LogsAPIHandler {
	return &LogsAPIHandler{db: db}
}

type logsAPIListResponse struct {
	Total   int64             `json:"total"`
	Limit   int               `json:"limit"`
	Offset  int               `json:"offset"`
	Entries []logsAPILogEntry `json:"entries"`
}

type logsAPILogEntry struct {
	ID               int64     `json:"id"`
	Timestamp        time.Time `json:"timestamp"`
	Endpoint         string    `json:"endpoint"`
	Method           string    `json:"method"`
	Model            string    `json:"model"`
	StatusCode       int       `json:"status_code"`
	LatencyMs        int64     `json:"latency_ms"`
	Stream           bool      `json:"stream"`
	BackendType      string    `json:"backend_type"`
	Error            string    `json:"error"`
	FrontendURL      string    `json:"frontend_url"`
	BackendURL       string    `json:"backend_url"`
	LastMessage      string    `json:"last_message"`
	FrontendRequest  string    `json:"frontend_request,omitempty"`
	FrontendResponse string    `json:"frontend_response,omitempty"`
	BackendRequest   string    `json:"backend_request,omitempty"`
	BackendResponse  string    `json:"backend_response,omitempty"`
}

func (h *LogsAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeLogsAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if strings.HasPrefix(r.URL.Path, "/api/logs/") {
		idText := strings.TrimPrefix(r.URL.Path, "/api/logs/")
		if idText == "" {
			writeLogsAPIError(w, http.StatusNotFound, "not found")
			return
		}
		h.serveEntryByID(w, idText)
		return
	}

	if idText := r.URL.Query().Get("id"); idText != "" {
		h.serveEntryByID(w, idText)
		return
	}

	h.serveList(w, r)
}

func (h *LogsAPIHandler) serveList(w http.ResponseWriter, r *http.Request) {
	filter, includeBodies, err := parseLogsFilter(r)
	if err != nil {
		writeLogsAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	total, err := h.db.CountEntries(filter)
	if err != nil {
		writeLogsAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries, err := h.db.GetEntries(filter)
	if err != nil {
		writeLogsAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := logsAPIListResponse{
		Total:   total,
		Limit:   filter.Limit,
		Offset:  filter.Offset,
		Entries: make([]logsAPILogEntry, 0, len(entries)),
	}
	for _, entry := range entries {
		resp.Entries = append(resp.Entries, logEntryToAPI(entry, includeBodies))
	}
	writeLogsAPIJSON(w, http.StatusOK, resp)
}

func (h *LogsAPIHandler) serveEntryByID(w http.ResponseWriter, idText string) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeLogsAPIError(w, http.StatusBadRequest, "invalid id")
		return
	}

	entry, err := h.db.GetEntryByID(id)
	if err != nil {
		writeLogsAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if entry == nil {
		writeLogsAPIError(w, http.StatusNotFound, "not found")
		return
	}

	writeLogsAPIJSON(w, http.StatusOK, logEntryToAPI(*entry, true))
}

func parseLogsFilter(r *http.Request) (database.LogFilter, bool, error) {
	q := r.URL.Query()
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return database.LogFilter{}, false, err
		}
		limit = parsed
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	offset := 0
	if raw := q.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return database.LogFilter{}, false, err
		}
		offset = parsed
	}
	if offset < 0 {
		offset = 0
	}

	order := q.Get("order")
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		return database.LogFilter{}, false, &parseParamError{"invalid order"}
	}

	var status *int
	if raw := q.Get("status"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return database.LogFilter{}, false, err
		}
		status = &parsed
	}

	var since *time.Time
	if raw := q.Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return database.LogFilter{}, false, err
		}
		since = &parsed
	}
	var until *time.Time
	if raw := q.Get("until"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return database.LogFilter{}, false, err
		}
		until = &parsed
	}

	errorsOnly := false
	if raw := q.Get("errors_only"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return database.LogFilter{}, false, err
		}
		errorsOnly = parsed
	}

	includeBodies := false
	if raw := q.Get("bodies"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return database.LogFilter{}, false, err
		}
		includeBodies = parsed
	}

	return database.LogFilter{
		Model:       q.Get("model"),
		Endpoint:    q.Get("endpoint"),
		BackendType: q.Get("backend_type"),
		Query:       q.Get("q"),
		Order:       order,
		Status:      status,
		ErrorsOnly:  errorsOnly,
		Since:       since,
		Until:       until,
		Limit:       limit,
		Offset:      offset,
	}, includeBodies, nil
}

type parseParamError struct {
	msg string
}

func (e *parseParamError) Error() string {
	return e.msg
}

func logEntryToAPI(entry database.LogEntry, includeBodies bool) logsAPILogEntry {
	apiEntry := logsAPILogEntry{
		ID:          entry.ID,
		Timestamp:   entry.Timestamp.UTC(),
		Endpoint:    entry.Endpoint,
		Method:      entry.Method,
		Model:       entry.Model,
		StatusCode:  entry.StatusCode,
		LatencyMs:   entry.LatencyMs,
		Stream:      entry.Stream,
		BackendType: entry.BackendType,
		Error:       entry.Error,
		FrontendURL: entry.FrontendURL,
		BackendURL:  entry.BackendURL,
		LastMessage: entry.LastMessage,
	}
	if includeBodies {
		apiEntry.FrontendRequest = entry.FrontendRequest
		apiEntry.FrontendResponse = entry.FrontendResponse
		apiEntry.BackendRequest = entry.BackendRequest
		apiEntry.BackendResponse = entry.BackendResponse
	}
	return apiEntry
}

func writeLogsAPIJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeLogsAPIError(w http.ResponseWriter, status int, message string) {
	writeLogsAPIJSON(w, status, map[string]string{"error": message})
}
