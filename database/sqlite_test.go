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

func TestCleanupOldRequestsKeepsNewestEntries(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "llm_proxy.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer db.Close()

	base := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if err := db.Log(LogEntry{
			Timestamp:   base.Add(time.Duration(i) * time.Minute),
			Endpoint:    "/api/chat",
			Method:      "POST",
			Model:       "test-model",
			StatusCode:  200,
			BackendType: "ollama",
			LastMessage: "message",
		}); err != nil {
			t.Fatalf("Log(%d) error = %v", i, err)
		}
	}

	deleted, err := db.CleanupOldRequests(2)
	if err != nil {
		t.Fatalf("CleanupOldRequests() error = %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}

	entries, err := db.GetRecentEntries(10, 0)
	if err != nil {
		t.Fatalf("GetRecentEntries() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if !entries[0].Timestamp.Equal(base.Add(4*time.Minute)) || !entries[1].Timestamp.Equal(base.Add(3*time.Minute)) {
		t.Fatalf("kept timestamps = %v, %v; want newest two", entries[0].Timestamp, entries[1].Timestamp)
	}
}

func TestEntryNavigationUsesAdjacentIDs(t *testing.T) {
	db, err := New(filepath.Join(t.TempDir(), "llm_proxy.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer db.Close()

	for i := 0; i < 3; i++ {
		if err := db.Log(LogEntry{
			Timestamp:   time.Date(2026, 6, 19, 12, i, 0, 0, time.UTC),
			Endpoint:    "/api/generate",
			Method:      "POST",
			Model:       "test-model",
			StatusCode:  200,
			BackendType: "openai",
			LastMessage: "message",
		}); err != nil {
			t.Fatalf("Log(%d) error = %v", i, err)
		}
	}

	next, err := db.GetNextEntryID(2)
	if err != nil {
		t.Fatalf("GetNextEntryID() error = %v", err)
	}
	if next == nil || *next != 3 {
		t.Fatalf("next = %v, want 3", next)
	}

	prev, err := db.GetPreviousEntryID(2)
	if err != nil {
		t.Fatalf("GetPreviousEntryID() error = %v", err)
	}
	if prev == nil || *prev != 1 {
		t.Fatalf("prev = %v, want 1", prev)
	}

	noNext, err := db.GetNextEntryID(3)
	if err != nil {
		t.Fatalf("GetNextEntryID(last) error = %v", err)
	}
	if noNext != nil {
		t.Fatalf("next after last = %v, want nil", *noNext)
	}
}
