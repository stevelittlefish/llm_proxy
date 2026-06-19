package database

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLogRoundTrip(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "llm_proxy.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer db.Close()

	entry := LogEntry{
		Timestamp:        time.Date(2026, 6, 19, 12, 30, 0, 0, time.UTC),
		Endpoint:         "/api/generate",
		Method:           "POST",
		Model:            "test-model",
		Prompt:           "hello",
		Response:         "world",
		StatusCode:       200,
		LatencyMs:        42,
		Stream:           true,
		BackendType:      "ollama",
		FrontendURL:      "http://frontend",
		BackendURL:       "http://backend",
		FrontendRequest:  `{"prompt":"hello"}`,
		FrontendResponse: `{"response":"world"}`,
		BackendRequest:   `{"prompt":"hello"}`,
		BackendResponse:  `{"response":"world"}`,
		LastMessage:      "hello",
	}

	if err := db.Log(entry); err != nil {
		t.Fatalf("Log() error = %v", err)
	}

	entries, err := db.GetRecentEntries(1, 0)
	if err != nil {
		t.Fatalf("GetRecentEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}

	got := entries[0]
	if got.Timestamp.IsZero() {
		t.Fatal("Timestamp was not scanned")
	}
	if got.Endpoint != entry.Endpoint || got.Method != entry.Method || got.Model != entry.Model {
		t.Fatalf("entry metadata = (%q, %q, %q), want (%q, %q, %q)",
			got.Endpoint, got.Method, got.Model,
			entry.Endpoint, entry.Method, entry.Model,
		)
	}
	if !got.Stream {
		t.Fatal("Stream = false, want true")
	}
}
