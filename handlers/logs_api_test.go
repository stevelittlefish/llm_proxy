package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llm_proxy/database"
)

func TestLogsAPIListBodyToggleAndFilters(t *testing.T) {
	db := newLogsAPITestDB(t)
	handler := NewLogsAPIHandler(db)

	req := httptest.NewRequest(http.MethodGet, "/api/logs?limit=10", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Total   int64                    `json:"total"`
		Entries []map[string]interface{} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 2 || len(list.Entries) != 2 {
		t.Fatalf("total,len = %d,%d; want 2,2", list.Total, len(list.Entries))
	}
	if _, ok := list.Entries[0]["frontend_request"]; ok {
		t.Fatalf("frontend_request present by default: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/logs?bodies=true&errors_only=true", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode filtered list: %v", err)
	}
	if list.Total != 1 || len(list.Entries) != 1 {
		t.Fatalf("filtered total,len = %d,%d; want 1,1", list.Total, len(list.Entries))
	}
	if list.Entries[0]["status_code"].(float64) != 500 {
		t.Fatalf("status_code = %#v, want 500", list.Entries[0]["status_code"])
	}
	if list.Entries[0]["frontend_request"] != `{"bad":true}` {
		t.Fatalf("frontend_request = %#v, want body", list.Entries[0]["frontend_request"])
	}
}

func TestLogsAPIEntryByID(t *testing.T) {
	db := newLogsAPITestDB(t)
	handler := NewLogsAPIHandler(db)

	req := httptest.NewRequest(http.MethodGet, "/api/logs/1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"frontend_request":"{\"ok\":true}"`) {
		t.Fatalf("single-entry response missing frontend body: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/logs/999", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestLogsAPIBadParams(t *testing.T) {
	db := newLogsAPITestDB(t)
	handler := NewLogsAPIHandler(db)

	req := httptest.NewRequest(http.MethodGet, "/api/logs?until=not-a-time", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func newLogsAPITestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "llm_proxy.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	base := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	entries := []database.LogEntry{
		{
			Timestamp:        base,
			Endpoint:         "/v1/chat/completions",
			Method:           "POST",
			Model:            "gemma4-31b",
			StatusCode:       200,
			LatencyMs:        100,
			Stream:           true,
			BackendType:      "openai",
			FrontendRequest:  `{"ok":true}`,
			FrontendResponse: `{"done":true}`,
			BackendRequest:   `{"backend":true}`,
			BackendResponse:  `data: [DONE]`,
			LastMessage:      "hello",
		},
		{
			Timestamp:        base.Add(time.Minute),
			Endpoint:         "/api/chat",
			Method:           "POST",
			Model:            "other-model",
			StatusCode:       500,
			LatencyMs:        25,
			BackendType:      "ollama",
			Error:            "backend failed",
			FrontendRequest:  `{"bad":true}`,
			FrontendResponse: `{"error":true}`,
			BackendRequest:   `{"backend":false}`,
			BackendResponse:  `failed`,
			LastMessage:      "bad",
		},
	}
	for _, entry := range entries {
		if err := db.Log(entry); err != nil {
			t.Fatalf("Log() error = %v", err)
		}
	}
	return db
}
