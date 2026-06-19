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

type recordingChatBackend struct {
	lastReq models.ChatRequest
}

func (b *recordingChatBackend) Generate(context.Context, models.GenerateRequest) (<-chan models.GenerateResponse, *backend.BackendMetadata, error) {
	ch := make(chan models.GenerateResponse)
	close(ch)
	return ch, &backend.BackendMetadata{URL: "http://backend/api/generate"}, nil
}

func (b *recordingChatBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, *backend.BackendMetadata, error) {
	b.lastReq = req
	ch := make(chan models.ChatResponse, 1)
	ch <- models.ChatResponse{
		Model:     req.Model,
		CreatedAt: time.Now(),
		Done:      true,
	}
	close(ch)
	return ch, &backend.BackendMetadata{URL: "http://backend/api/chat"}, nil
}

func (b *recordingChatBackend) ListModels(context.Context) (models.ModelsResponse, error) {
	return models.ModelsResponse{}, nil
}

type toolCallChatBackend struct {
	stream bool
}

func (b toolCallChatBackend) Generate(context.Context, models.GenerateRequest) (<-chan models.GenerateResponse, *backend.BackendMetadata, error) {
	ch := make(chan models.GenerateResponse)
	close(ch)
	return ch, &backend.BackendMetadata{URL: "http://backend/api/generate"}, nil
}

func (b toolCallChatBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, *backend.BackendMetadata, error) {
	ch := make(chan models.ChatResponse, 2)
	ch <- models.ChatResponse{
		Model:     req.Model,
		CreatedAt: time.Now(),
		Message: models.Message{
			Role:    "assistant",
			Content: "",
			ToolCalls: []interface{}{
				map[string]interface{}{
					"id": "call-terminal",
					"function": map[string]interface{}{
						"name":      "terminal",
						"arguments": map[string]interface{}{"command": "free -h"},
					},
				},
			},
		},
		Done: !b.stream,
	}
	if b.stream {
		ch <- models.ChatResponse{
			Model:      req.Model,
			CreatedAt:  time.Now(),
			Done:       true,
			DoneReason: "tool_calls",
		}
	}
	close(ch)
	return ch, &backend.BackendMetadata{URL: "http://backend/api/chat"}, nil
}

func (b toolCallChatBackend) ListModels(context.Context) (models.ModelsResponse, error) {
	return models.ModelsResponse{}, nil
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

func TestOpenAIChatCompletionsHandlerPreservesNonStreamingToolCalls(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "llm_proxy.db"))
	if err != nil {
		t.Fatalf("database.New() error = %v", err)
	}
	defer db.Close()

	cfg := &config.Config{}
	handler := NewOpenAIChatCompletionsHandler(toolCallChatBackend{}, db, cfg)

	body := `{"model":"test-model","messages":[{"role":"user","content":"check memory"}]}`
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
	if len(resp.Choices) != 1 || resp.Choices[0].Message == nil {
		t.Fatalf("choices = %#v, want one message choice", resp.Choices)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop", resp.Choices[0].FinishReason)
	}
	assertOpenAIToolCall(t, resp.Choices[0].Message.ToolCalls, false)
}

func TestOpenAIChatCompletionsHandlerPreservesStreamingToolCalls(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "llm_proxy.db"))
	if err != nil {
		t.Fatalf("database.New() error = %v", err)
	}
	defer db.Close()

	cfg := &config.Config{}
	handler := NewOpenAIChatCompletionsHandler(toolCallChatBackend{stream: true}, db, cfg)

	body := `{"model":"test-model","stream":true,"messages":[{"role":"user","content":"check memory"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	bodyText := rec.Body.String()
	if !strings.Contains(bodyText, `"tool_calls"`) {
		t.Fatalf("stream response missing tool_calls: %s", bodyText)
	}
	if !strings.Contains(bodyText, `"finish_reason":"tool_calls"`) {
		t.Fatalf("stream response missing tool_calls finish_reason: %s", bodyText)
	}
	if !strings.Contains(bodyText, `data: [DONE]`) {
		t.Fatalf("stream response missing [DONE]: %s", bodyText)
	}

	firstDataLine := strings.Split(strings.TrimPrefix(bodyText, "data: "), "\n\n")[0]
	var firstChunk models.OpenAIChatResponse
	if err := json.Unmarshal([]byte(firstDataLine), &firstChunk); err != nil {
		t.Fatalf("failed to decode first SSE chunk: %v\nchunk: %s", err, firstDataLine)
	}
	if len(firstChunk.Choices) != 1 || firstChunk.Choices[0].Delta == nil {
		t.Fatalf("choices = %#v, want one delta choice", firstChunk.Choices)
	}
	assertOpenAIToolCall(t, firstChunk.Choices[0].Delta.ToolCalls, true)
}

func assertOpenAIToolCall(t *testing.T, toolCalls []interface{}, wantIndex bool) {
	t.Helper()
	if len(toolCalls) != 1 {
		t.Fatalf("len(toolCalls) = %d, want 1", len(toolCalls))
	}
	tc, ok := toolCalls[0].(map[string]interface{})
	if !ok {
		t.Fatalf("tool call = %#v, want map", toolCalls[0])
	}
	if tc["type"] != "function" {
		t.Fatalf("tool call type = %#v, want function", tc["type"])
	}
	if wantIndex && tc["index"] != float64(0) {
		t.Fatalf("tool call index = %#v, want 0", tc["index"])
	}
	fn, ok := tc["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("function = %#v, want map", tc["function"])
	}
	if fn["name"] != "terminal" {
		t.Fatalf("function name = %#v, want terminal", fn["name"])
	}
	if fn["arguments"] != `{"command":"free -h"}` {
		t.Fatalf("function arguments = %#v, want JSON string", fn["arguments"])
	}
}

func TestOpenAIChatCompletionsHandlerPreservesMaxTokens(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "llm_proxy.db"))
	if err != nil {
		t.Fatalf("database.New() error = %v", err)
	}
	defer db.Close()

	cfg := &config.Config{}
	backend := &recordingChatBackend{}
	handler := NewOpenAIChatCompletionsHandler(backend, db, cfg)

	body := `{"model":"test-model","messages":[{"role":"user","content":"hello"}],"max_tokens":4}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if backend.lastReq.Options == nil {
		t.Fatal("Options = nil, want num_predict")
	}
	got, ok := backend.lastReq.Options["num_predict"].(float64)
	if !ok {
		t.Fatalf("num_predict = %#v, want float64", backend.lastReq.Options["num_predict"])
	}
	if got != 4 {
		t.Fatalf("num_predict = %v, want 4", got)
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
