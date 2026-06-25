package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"llm_proxy/backend"
	"llm_proxy/models"
)

type modelMetadataBackend struct {
	lastShowModel string
}

func (b *modelMetadataBackend) Generate(context.Context, models.GenerateRequest) (<-chan models.GenerateResponse, *backend.BackendMetadata, error) {
	ch := make(chan models.GenerateResponse)
	close(ch)
	return ch, &backend.BackendMetadata{}, nil
}

func (b *modelMetadataBackend) Chat(context.Context, models.ChatRequest) (<-chan models.ChatResponse, *backend.BackendMetadata, error) {
	ch := make(chan models.ChatResponse)
	close(ch)
	return ch, &backend.BackendMetadata{}, nil
}

func (b *modelMetadataBackend) ListModels(context.Context) (models.ModelsResponse, error) {
	return models.ModelsResponse{
		Models: []models.ModelInfo{
			{
				Name:         "test-model",
				Model:        "test-model",
				ModifiedAt:   time.Unix(100, 0),
				Capabilities: []string{"completion"},
				Details: models.ModelDetails{
					ContextLength:   2048,
					EmbeddingLength: 5120,
				},
			},
		},
	}, nil
}

func (b *modelMetadataBackend) ShowModel(_ context.Context, model string) (models.ShowResponse, error) {
	b.lastShowModel = model
	return models.ShowResponse{
		ModelInfo: map[string]interface{}{"context_length": 2048},
		Details:   models.ModelDetails{ContextLength: 2048},
	}, nil
}

func TestModelsHandlerPreservesOllamaMetadata(t *testing.T) {
	handler := NewModelsHandler(&modelMetadataBackend{})
	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		`"context_length":2048`,
		`"embedding_length":5120`,
		`"capabilities":["completion"]`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("response missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestShowHandlerAcceptsModelOrName(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{body: `{"model":"from-model"}`, want: "from-model"},
		{body: `{"name":"from-name"}`, want: "from-name"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			backend := &modelMetadataBackend{}
			handler := NewShowHandler(backend)
			req := httptest.NewRequest(http.MethodPost, "/api/show", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if backend.lastShowModel != tt.want {
				t.Fatalf("ShowModel called with %q, want %q", backend.lastShowModel, tt.want)
			}
			if !strings.Contains(rec.Body.String(), `"context_length":2048`) {
				t.Fatalf("response missing context length: %s", rec.Body.String())
			}
		})
	}
}
