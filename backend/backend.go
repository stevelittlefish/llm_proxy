package backend

import (
	"context"

	"llm_proxy/models"
)

// BackendMetadata contains raw request/response data from backend calls
type BackendMetadata struct {
	RawRequest  string // Raw JSON sent to backend
	RawResponse string // Raw response data received from backend
}

// Backend defines the interface for different LLM backends
type Backend interface {
	// Generate handles text generation requests
	// Returns response channel, metadata (with raw request/response), and error
	Generate(ctx context.Context, req models.GenerateRequest) (<-chan models.GenerateResponse, *BackendMetadata, error)

	// Chat handles chat completion requests
	// Returns response channel, metadata (with raw request/response), and error
	Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, *BackendMetadata, error)

	// ListModels returns available models
	ListModels(ctx context.Context) (models.ModelsResponse, error)
}
