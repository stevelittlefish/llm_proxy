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

type sanitizationSpyBackend struct {
	lastChatReq     models.ChatRequest
	lastGenerateReq models.GenerateRequest
}

func (s *sanitizationSpyBackend) Generate(ctx context.Context, req models.GenerateRequest) (<-chan models.GenerateResponse, *backend.BackendMetadata, error) {
	s.lastGenerateReq = req
	ch := make(chan models.GenerateResponse, 1)
	ch <- models.GenerateResponse{
		Model:     req.Model,
		CreatedAt: time.Now(),
		Done:      true,
	}
	close(ch)
	return ch, &backend.BackendMetadata{
		URL:         "http://backend/api/generate",
		RawRequest:  `{"backend_request":true}`,
		RawResponse: `{"backend_response":true}`,
	}, nil
}

func (s *sanitizationSpyBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, *backend.BackendMetadata, error) {
	s.lastChatReq = req
	ch := make(chan models.ChatResponse, 1)
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

func (s *sanitizationSpyBackend) ListModels(context.Context) (models.ModelsResponse, error) {
	return models.ModelsResponse{}, nil
}

func TestRequestSanitizationDropsExcessiveMaxTokens(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "openai chat", endpoint: "openai_chat"},
		{name: "ollama chat", endpoint: "ollama_chat"},
		{name: "ollama generate", endpoint: "ollama_generate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, db, cfg := newSanitizationTest(t)
			cfg.RequestSanitization.MaxTokensPolicy = "drop_above"
			cfg.RequestSanitization.MaxTokensLimit = 100

			rec := serveSanitizationRequest(t, tt.endpoint, backend, db, cfg, 101)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			assertNumPredictAbsent(t, forwardedOptions(tt.endpoint, backend))
		})
	}
}

func TestRequestSanitizationPreservesAllowedMaxTokens(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "openai chat", endpoint: "openai_chat"},
		{name: "ollama chat", endpoint: "ollama_chat"},
		{name: "ollama generate", endpoint: "ollama_generate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, db, cfg := newSanitizationTest(t)
			cfg.RequestSanitization.MaxTokensPolicy = "drop_above"
			cfg.RequestSanitization.MaxTokensLimit = 100

			rec := serveSanitizationRequest(t, tt.endpoint, backend, db, cfg, 100)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			assertNumPredict(t, forwardedOptions(tt.endpoint, backend), 100)
		})
	}
}

func TestRequestSanitizationAlwaysDropsMaxTokens(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "openai chat", endpoint: "openai_chat"},
		{name: "ollama chat", endpoint: "ollama_chat"},
		{name: "ollama generate", endpoint: "ollama_generate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, db, cfg := newSanitizationTest(t)
			cfg.RequestSanitization.MaxTokensPolicy = "drop"

			rec := serveSanitizationRequest(t, tt.endpoint, backend, db, cfg, 10)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			assertNumPredictAbsent(t, forwardedOptions(tt.endpoint, backend))
		})
	}
}

func newSanitizationTest(t *testing.T) (*sanitizationSpyBackend, *database.DB, *config.Config) {
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
	}

	return &sanitizationSpyBackend{}, db, cfg
}

func serveSanitizationRequest(t *testing.T, endpoint string, backend *sanitizationSpyBackend, db *database.DB, cfg *config.Config, maxTokens int) *httptest.ResponseRecorder {
	t.Helper()

	var (
		handler http.Handler
		path    string
		body    string
	)

	switch endpoint {
	case "openai_chat":
		handler = NewOpenAIChatCompletionsHandler(backend, db, cfg)
		path = "/v1/chat/completions"
		body = marshalSanitizationBody(t, models.OpenAIChatRequest{
			Model:     "test-model",
			Messages:  []models.Message{{Role: "user", Content: "hello"}},
			MaxTokens: maxTokens,
		})
	case "ollama_chat":
		handler = NewChatHandler(backend, db, cfg)
		path = "/api/chat"
		body = marshalSanitizationBody(t, models.ChatRequest{
			Model:    "test-model",
			Messages: []models.Message{{Role: "user", Content: "hello"}},
			Options: map[string]interface{}{
				"num_predict": maxTokens,
			},
		})
	case "ollama_generate":
		handler = NewGenerateHandler(backend, db, cfg)
		path = "/api/generate"
		body = marshalSanitizationBody(t, models.GenerateRequest{
			Model:  "test-model",
			Prompt: "hello",
			Options: map[string]interface{}{
				"num_predict": maxTokens,
			},
		})
	default:
		t.Fatalf("unknown endpoint %q", endpoint)
	}

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func marshalSanitizationBody(t *testing.T, body interface{}) string {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(data)
}

func forwardedOptions(endpoint string, backend *sanitizationSpyBackend) map[string]interface{} {
	if endpoint == "ollama_generate" {
		return backend.lastGenerateReq.Options
	}
	return backend.lastChatReq.Options
}

func assertNumPredict(t *testing.T, options map[string]interface{}, want int) {
	t.Helper()

	got, ok := options["num_predict"]
	if !ok {
		t.Fatal("num_predict is absent")
	}
	gotFloat, ok := got.(float64)
	if !ok {
		t.Fatalf("num_predict = %#v, want float64", got)
	}
	if gotFloat != float64(want) {
		t.Fatalf("num_predict = %v, want %d", gotFloat, want)
	}
}

func assertNumPredictAbsent(t *testing.T, options map[string]interface{}) {
	t.Helper()

	if _, ok := options["num_predict"]; ok {
		t.Fatalf("num_predict is present: %#v", options)
	}
}
