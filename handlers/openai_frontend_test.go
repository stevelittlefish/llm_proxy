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

type fakeChatBackend struct{}

func (fakeChatBackend) Generate(context.Context, models.GenerateRequest) (<-chan models.GenerateResponse, *backend.BackendMetadata, error) {
	ch := make(chan models.GenerateResponse)
	close(ch)
	return ch, &backend.BackendMetadata{URL: "http://backend/api/generate"}, nil
}

func (fakeChatBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, *backend.BackendMetadata, error) {
	ch := make(chan models.ChatResponse, 2)
	ch <- models.ChatResponse{
		Model:     req.Model,
		CreatedAt: time.Now(),
		Message: models.Message{
			Role:    "assistant",
			Content: "hello from backend",
		},
	}
	ch <- models.ChatResponse{
		Model:     req.Model,
		CreatedAt: time.Now(),
		Done:      true,
	}
	close(ch)
	return ch, &backend.BackendMetadata{URL: "http://backend/api/chat"}, nil
}

func (fakeChatBackend) ListModels(context.Context) (models.ModelsResponse, error) {
	return models.ModelsResponse{
		Models: []models.ModelInfo{
			{
				Name:       "test-model",
				Model:      "test-model",
				ModifiedAt: time.Unix(100, 0),
			},
		},
	}, nil
}

func TestOpenAIChatCompletionsHandler(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "llm_proxy.db"))
	if err != nil {
		t.Fatalf("database.New() error = %v", err)
	}
	defer db.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Host: "127.0.0.1",
			Port: 11434,
		},
		Backend: config.BackendConfig{
			Type: "ollama",
		},
	}
	handler := NewOpenAIChatCompletionsHandler(fakeChatBackend{}, db, cfg)

	body := `{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp models.OpenAIChatResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("len(resp.Choices) = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != "hello from backend" {
		t.Fatalf("message = %#v, want hello from backend", resp.Choices[0].Message)
	}

	count, err := db.GetTotalCount()
	if err != nil {
		t.Fatalf("GetTotalCount() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("logged count = %d, want 1", count)
	}
}

func TestOpenAIModelsHandler(t *testing.T) {
	handler := NewOpenAIModelsHandler(fakeChatBackend{})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"test-model"`) {
		t.Fatalf("response does not include test-model: %s", rec.Body.String())
	}
}
