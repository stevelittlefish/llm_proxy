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

type streamOverrideSpyBackend struct {
	lastChatStream     bool
	lastGenerateStream bool
	lastChatReq        models.ChatRequest
}

// Generate returns multiple chunks when the backend-facing req.Stream is
// true (mimicking a real streaming backend) and a single chunk otherwise.
func (s *streamOverrideSpyBackend) Generate(ctx context.Context, req models.GenerateRequest) (<-chan models.GenerateResponse, *backend.BackendMetadata, error) {
	s.lastGenerateStream = req.Stream

	if !req.Stream {
		ch := make(chan models.GenerateResponse, 1)
		ch <- models.GenerateResponse{
			Model:      req.Model,
			CreatedAt:  time.Now(),
			Response:   "ok there",
			Done:       true,
			DoneReason: "stop",
		}
		close(ch)
		return ch, &backend.BackendMetadata{URL: "http://backend/api/generate"}, nil
	}

	ch := make(chan models.GenerateResponse, 3)
	ch <- models.GenerateResponse{Model: req.Model, CreatedAt: time.Now(), Response: "ok "}
	ch <- models.GenerateResponse{Model: req.Model, CreatedAt: time.Now(), Response: "there"}
	ch <- models.GenerateResponse{Model: req.Model, CreatedAt: time.Now(), Done: true, DoneReason: "stop"}
	close(ch)
	return ch, &backend.BackendMetadata{URL: "http://backend/api/generate"}, nil
}

// Chat returns multiple chunks when the backend-facing req.Stream is true
// (mimicking a real streaming backend) and a single chunk otherwise, so
// tests can verify the handler reshapes whatever the backend produced into
// what the client actually asked for.
func (s *streamOverrideSpyBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, *backend.BackendMetadata, error) {
	s.lastChatStream = req.Stream
	s.lastChatReq = req

	if !req.Stream {
		ch := make(chan models.ChatResponse, 1)
		ch <- models.ChatResponse{
			Model:      req.Model,
			CreatedAt:  time.Now(),
			Message:    models.Message{Role: "assistant", Content: "ok there"},
			Done:       true,
			DoneReason: "stop",
		}
		close(ch)
		return ch, &backend.BackendMetadata{URL: "http://backend/api/chat"}, nil
	}

	ch := make(chan models.ChatResponse, 3)
	ch <- models.ChatResponse{Model: req.Model, CreatedAt: time.Now(), Message: models.Message{Role: "assistant", Content: "ok "}}
	ch <- models.ChatResponse{Model: req.Model, CreatedAt: time.Now(), Message: models.Message{Content: "there"}}
	ch <- models.ChatResponse{Model: req.Model, CreatedAt: time.Now(), Done: true, DoneReason: "stop"}
	close(ch)
	return ch, &backend.BackendMetadata{URL: "http://backend/api/chat"}, nil
}

func (s *streamOverrideSpyBackend) ListModels(context.Context) (models.ModelsResponse, error) {
	return models.ModelsResponse{}, nil
}

func TestStreamOverrideForcesBackendRequestStream(t *testing.T) {
	tests := []struct {
		name            string
		endpoint        string
		mode            string
		requestedStream bool
		wantStream      bool
	}{
		{name: "openai chat forces on", endpoint: "openai_chat", mode: "always", requestedStream: false, wantStream: true},
		{name: "openai chat forces off", endpoint: "openai_chat", mode: "never", requestedStream: true, wantStream: false},
		{name: "openai chat passthrough", endpoint: "openai_chat", mode: "passthrough", requestedStream: true, wantStream: true},
		{name: "ollama chat forces on", endpoint: "ollama_chat", mode: "always", requestedStream: false, wantStream: true},
		{name: "ollama chat forces off", endpoint: "ollama_chat", mode: "never", requestedStream: true, wantStream: false},
		{name: "ollama generate forces on", endpoint: "ollama_generate", mode: "always", requestedStream: false, wantStream: true},
		{name: "ollama generate forces off", endpoint: "ollama_generate", mode: "never", requestedStream: true, wantStream: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy, db, cfg := newStreamOverrideTest(t)
			cfg.StreamOverride.Mode = tt.mode

			rec := serveStreamOverrideRequest(t, tt.endpoint, spy, db, cfg, tt.requestedStream)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			got := spy.lastChatStream
			if tt.endpoint == "ollama_generate" {
				got = spy.lastGenerateStream
			}
			if got != tt.wantStream {
				t.Fatalf("backend stream = %v, want %v", got, tt.wantStream)
			}
		})
	}
}

// TestStreamOverridePreservesClientResponseFormat guards against a
// regression where stream_override changed the wire format sent back to the
// client (SSE vs. single JSON). The override must only affect how the proxy
// talks to the backend; a client that asked for streaming must still get an
// SSE stream even if the backend call itself was forced non-streaming (and
// vice versa), since clients are coded against the format they requested.
func TestStreamOverridePreservesClientResponseFormat(t *testing.T) {
	tests := []struct {
		name            string
		mode            string
		requestedStream bool
	}{
		{name: "client streaming request stays SSE under always", mode: "always", requestedStream: true},
		{name: "client streaming request stays SSE under never", mode: "never", requestedStream: true},
		{name: "client non-streaming request stays single JSON under always", mode: "always", requestedStream: false},
		{name: "client non-streaming request stays single JSON under never", mode: "never", requestedStream: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy, db, cfg := newStreamOverrideTest(t)
			cfg.StreamOverride.Mode = tt.mode

			rec := serveStreamOverrideRequest(t, "openai_chat", spy, db, cfg, tt.requestedStream)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			isSSE := strings.HasPrefix(rec.Body.String(), "data: ")
			if isSSE != tt.requestedStream {
				t.Fatalf("response body = %q, isSSE = %v, want %v (what the client requested)", rec.Body.String(), isSSE, tt.requestedStream)
			}
			// Guards against a regression where forcing the backend call to
			// be non-streaming delivered content and Done:true in the same
			// chunk, and the SSE writer discarded the content when it saw Done.
			got := extractOpenAIContent(t, rec.Body.String(), isSSE)
			if got != "ok there" {
				t.Fatalf("content = %q, want %q (response body = %q)", got, "ok there", rec.Body.String())
			}
		})
	}
}

// extractOpenAIContent reconstructs the assistant message content from an
// OpenAI-compatible response body, whether it's a single JSON object or an
// SSE stream of "chat.completion.chunk" events.
func extractOpenAIContent(t *testing.T, body string, isSSE bool) string {
	t.Helper()

	if !isSSE {
		var resp models.OpenAIChatResponse
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("json.Unmarshal() error = %v, body = %q", err, body)
		}
		if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
			return ""
		}
		return resp.Choices[0].Message.Content
	}

	var content strings.Builder
	for _, line := range strings.Split(body, "\n") {
		data := strings.TrimPrefix(line, "data: ")
		if data == line || data == "" || data == "[DONE]" {
			continue
		}
		var resp models.OpenAIChatResponse
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			t.Fatalf("json.Unmarshal() error = %v, line = %q", err, line)
		}
		if len(resp.Choices) > 0 && resp.Choices[0].Delta != nil {
			content.WriteString(resp.Choices[0].Delta.Content)
		}
	}
	return content.String()
}

// TestStreamOverrideReshapesOllamaNativeResponse guards against the same
// regression for /api/chat and /api/generate: when stream_override forces
// the backend call to actually stream (mode=always) but the client asked
// for a single non-streamed response, the proxy must aggregate the backend
// chunks into exactly one JSON object instead of forwarding each chunk.
func TestStreamOverrideReshapesOllamaNativeResponse(t *testing.T) {
	for _, endpoint := range []string{"ollama_chat", "ollama_generate"} {
		t.Run(endpoint, func(t *testing.T) {
			spy, db, cfg := newStreamOverrideTest(t)
			cfg.StreamOverride.Mode = "always"

			rec := serveStreamOverrideRequest(t, endpoint, spy, db, cfg, false)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}

			lines := nonEmptyLines(rec.Body.String())
			if len(lines) != 1 {
				t.Fatalf("got %d response line(s), want 1 (client asked for non-streaming): %q", len(lines), rec.Body.String())
			}
			if !strings.Contains(lines[0], "ok there") {
				t.Fatalf("response = %q, want aggregated content %q", lines[0], "ok there")
			}
		})
	}
}

func nonEmptyLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestStreamOverrideStripsStreamOptionsWhenForcedOff guards against a
// regression where forcing stream off left a passed-through stream_options
// field in the backend request; OpenAI-compatible backends (e.g. vLLM)
// reject stream_options unless stream=true.
func TestStreamOverrideStripsStreamOptionsWhenForcedOff(t *testing.T) {
	spy, db, cfg := newStreamOverrideTest(t)
	cfg.StreamOverride.Mode = "never"

	handler := NewOpenAIChatCompletionsHandler(spy, db, cfg)
	body := `{
		"model":"test-model",
		"messages":[{"role":"user","content":"hello"}],
		"stream":true,
		"stream_options":{"include_usage":true}
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if spy.lastChatStream {
		t.Fatal("backend stream = true, want false")
	}
	raw := spy.lastChatReq.OpenAIRaw
	if _, ok := raw["stream_options"]; ok {
		t.Fatalf("stream_options present = %s, want stripped when stream forced off", raw["stream_options"])
	}
}

func newStreamOverrideTest(t *testing.T) (*streamOverrideSpyBackend, *database.DB, *config.Config) {
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

	return &streamOverrideSpyBackend{}, db, cfg
}

func serveStreamOverrideRequest(t *testing.T, endpoint string, spy *streamOverrideSpyBackend, db *database.DB, cfg *config.Config, requestedStream bool) *httptest.ResponseRecorder {
	t.Helper()

	var (
		handler http.Handler
		path    string
		body    string
	)

	switch endpoint {
	case "openai_chat":
		handler = NewOpenAIChatCompletionsHandler(spy, db, cfg)
		path = "/v1/chat/completions"
		body = marshalStreamOverrideBody(t, models.OpenAIChatRequest{
			Model:    "test-model",
			Messages: []models.Message{{Role: "user", Content: "hello"}},
			Stream:   requestedStream,
		})
	case "ollama_chat":
		handler = NewChatHandler(spy, db, cfg)
		path = "/api/chat"
		body = marshalStreamOverrideBody(t, models.ChatRequest{
			Model:    "test-model",
			Messages: []models.Message{{Role: "user", Content: "hello"}},
			Stream:   requestedStream,
		})
	case "ollama_generate":
		handler = NewGenerateHandler(spy, db, cfg)
		path = "/api/generate"
		body = marshalStreamOverrideBody(t, models.GenerateRequest{
			Model:  "test-model",
			Prompt: "hello",
			Stream: requestedStream,
		})
	default:
		t.Fatalf("unknown endpoint %q", endpoint)
	}

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func marshalStreamOverrideBody(t *testing.T, body interface{}) string {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(data)
}
