package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"llm_proxy/backend"
	"llm_proxy/config"
	"llm_proxy/database"
	"llm_proxy/models"
)

type spyChatBackend struct {
	lastReq models.ChatRequest
}

func (s *spyChatBackend) Generate(context.Context, models.GenerateRequest) (<-chan models.GenerateResponse, *backend.BackendMetadata, error) {
	ch := make(chan models.GenerateResponse)
	close(ch)
	return ch, &backend.BackendMetadata{URL: "http://backend/api/generate"}, nil
}

func (s *spyChatBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, *backend.BackendMetadata, error) {
	s.lastReq = req

	ch := make(chan models.ChatResponse, 2)
	ch <- models.ChatResponse{
		Model:     req.Model,
		CreatedAt: time.Now(),
		Message: models.Message{
			Role:    "assistant",
			Content: "ok",
		},
	}
	ch <- models.ChatResponse{
		Model:     req.Model,
		CreatedAt: time.Now(),
		Done:      true,
	}
	close(ch)

	return ch, &backend.BackendMetadata{
		URL:         "http://backend/api/chat",
		RawRequest:  `{"backend_request":true}`,
		RawResponse: `{"backend_response":true}`,
	}, nil
}

func (s *spyChatBackend) ListModels(context.Context) (models.ModelsResponse, error) {
	return models.ModelsResponse{}, nil
}

func (s *spyChatBackend) ShowModel(context.Context, string) (models.ShowResponse, error) {
	return models.ShowResponse{}, nil
}

func TestChatFeatureParity(t *testing.T) {
	tests := []struct {
		name             string
		mode             string
		messages         []models.Message
		wantMessages     []models.Message
		wantOriginalLast string
	}{
		{
			name: "injects last user message",
			mode: "last",
			messages: []models.Message{
				{Role: "user", Content: "first"},
				{Role: "assistant", Content: "middle"},
				{Role: "user", Content: "last"},
			},
			wantMessages: []models.Message{
				{Role: "user", Content: "first"},
				{Role: "assistant", Content: "middle"},
				{Role: "user", Content: "last /nothink"},
			},
			wantOriginalLast: "last",
		},
		{
			name: "injects first user message",
			mode: "first",
			messages: []models.Message{
				{Role: "user", Content: "first"},
				{Role: "assistant", Content: "middle"},
				{Role: "user", Content: "last"},
			},
			wantMessages: []models.Message{
				{Role: "user", Content: "first /nothink"},
				{Role: "assistant", Content: "middle"},
				{Role: "user", Content: "last"},
			},
			wantOriginalLast: "last",
		},
		{
			name: "injects existing system message",
			mode: "system",
			messages: []models.Message{
				{Role: "system", Content: "be brief"},
				{Role: "user", Content: "hello"},
			},
			wantMessages: []models.Message{
				{Role: "system", Content: "be brief /nothink"},
				{Role: "user", Content: "hello"},
			},
			wantOriginalLast: "hello",
		},
		{
			name: "creates system message",
			mode: "system",
			messages: []models.Message{
				{Role: "user", Content: "hello"},
			},
			wantMessages: []models.Message{
				{Role: "system", Content: "/nothink"},
				{Role: "user", Content: "hello"},
			},
			wantOriginalLast: "hello",
		},
		{
			name: "skips duplicate injection",
			mode: "last",
			messages: []models.Message{
				{Role: "user", Content: "hello /nothink"},
			},
			wantMessages: []models.Message{
				{Role: "user", Content: "hello /nothink"},
			},
			wantOriginalLast: "hello /nothink",
		},
	}

	for _, api := range []string{"ollama", "openai"} {
		for _, tt := range tests {
			t.Run(api+"/"+tt.name, func(t *testing.T) {
				backend := &spyChatBackend{}
				db, cfg := newChatFeatureTestDB(t, tt.mode)

				body := chatRequestBody(t, api, tt.messages, nil)
				rec := serveChatFeatureRequest(t, api, backend, db, cfg, body)
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
				}

				assertMessages(t, backend.lastReq.Messages, tt.wantMessages)
				assertOriginalLog(t, db, tt.wantOriginalLast, "/nothink")
			})
		}
	}
}

func TestChatToolBlacklistParity(t *testing.T) {
	tools := []interface{}{
		map[string]interface{}{
			"function": map[string]interface{}{
				"name": "blocked_tool",
			},
		},
		map[string]interface{}{
			"function": map[string]interface{}{
				"name": "allowed_tool",
			},
		},
		map[string]interface{}{
			"type": "unparseable",
		},
	}

	for _, api := range []string{"ollama", "openai"} {
		t.Run(api, func(t *testing.T) {
			backend := &spyChatBackend{}
			db, cfg := newChatFeatureTestDB(t, "last")
			cfg.ChatTextInjection.Enabled = false
			cfg.Backend.ToolBlacklist = []string{"blocked_tool"}

			body := chatRequestBody(t, api, []models.Message{{Role: "user", Content: "hello"}}, tools)
			rec := serveChatFeatureRequest(t, api, backend, db, cfg, body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			if len(backend.lastReq.Tools) != 2 {
				t.Fatalf("len(Tools) = %d, want 2: %#v", len(backend.lastReq.Tools), backend.lastReq.Tools)
			}
			if toolName(backend.lastReq.Tools[0]) != "allowed_tool" {
				t.Fatalf("first remaining tool = %q, want allowed_tool", toolName(backend.lastReq.Tools[0]))
			}
			if toolName(backend.lastReq.Tools[1]) != "" {
				t.Fatalf("second remaining tool name = %q, want empty/unparseable", toolName(backend.lastReq.Tools[1]))
			}
		})
	}
}

func newChatFeatureTestDB(t *testing.T, mode string) (*database.DB, *config.Config) {
	t.Helper()

	db, err := database.New(filepath.Join(t.TempDir(), "llm_proxy.db"))
	if err != nil {
		t.Fatalf("database.New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("db.Close() error = %v", err)
		}
	})

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host: "127.0.0.1",
			Port: 11434,
		},
		Backend: config.BackendConfig{
			Type: "ollama",
		},
		ChatTextInjection: config.ChatTextInjectionConfig{
			Enabled: true,
			Text:    "/nothink",
			Mode:    mode,
		},
	}

	return db, cfg
}

func serveChatFeatureRequest(t *testing.T, api string, backend *spyChatBackend, db *database.DB, cfg *config.Config, body string) *httptest.ResponseRecorder {
	t.Helper()

	var handler http.Handler
	path := "/api/chat"
	if api == "openai" {
		handler = NewOpenAIChatCompletionsHandler(backend, db, cfg)
		path = "/v1/chat/completions"
	} else {
		handler = NewChatHandler(backend, db, cfg)
	}

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func chatRequestBody(t *testing.T, api string, messages []models.Message, tools []interface{}) string {
	t.Helper()

	if api == "openai" {
		body := models.OpenAIChatRequest{
			Model:    "test-model",
			Messages: messages,
			Tools:    tools,
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return string(data)
	}

	body := models.ChatRequest{
		Model:    "test-model",
		Messages: messages,
		Tools:    tools,
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(data)
}

func assertMessages(t *testing.T, got []models.Message, want []models.Message) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("len(messages) = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Fatalf("message[%d] = (%q, %q), want (%q, %q)",
				i,
				got[i].Role,
				got[i].Content,
				want[i].Role,
				want[i].Content,
			)
		}
	}
}

func assertOriginalLog(t *testing.T, db *database.DB, wantLast string, injected string) {
	t.Helper()

	entries, err := db.GetRecentEntries(1, 0)
	if err != nil {
		t.Fatalf("GetRecentEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].LastMessage != wantLast {
		t.Fatalf("LastMessage = %q, want %q", entries[0].LastMessage, wantLast)
	}
	if wantLast != "" && !strings.Contains(wantLast, injected) && strings.Contains(entries[0].Prompt, injected) {
		t.Fatalf("Prompt contains injected text %q, want original prompt: %q", injected, entries[0].Prompt)
	}
	if entries[0].FrontendRequest == "" {
		t.Fatal("FrontendRequest is empty")
	}
	if entries[0].FrontendResponse == "" {
		t.Fatal("FrontendResponse is empty")
	}
	if entries[0].BackendRequest == "" {
		t.Fatal("BackendRequest is empty")
	}
	if entries[0].BackendResponse == "" {
		t.Fatal("BackendResponse is empty")
	}
}

func toolName(tool interface{}) string {
	toolMap, ok := tool.(map[string]interface{})
	if !ok {
		return ""
	}
	funcMap, ok := toolMap["function"].(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := funcMap["name"].(string)
	return name
}
