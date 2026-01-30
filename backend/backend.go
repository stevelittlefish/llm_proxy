package backend

import (
	"context"

	"github.com/steveiliop56/llm_proxy/models"
)

// Backend defines the interface for different LLM backends
type Backend interface {
	// Generate handles text generation requests
	Generate(ctx context.Context, req models.GenerateRequest) (<-chan models.GenerateResponse, error)

	// Chat handles chat completion requests
	Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, error)

	// ListModels returns available models
	ListModels(ctx context.Context) (models.ModelsResponse, error)
}
