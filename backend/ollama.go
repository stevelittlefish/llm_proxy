package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/steveiliop56/llm_proxy/models"
)

// OllamaBackend implements the Backend interface for Ollama
type OllamaBackend struct {
	endpoint string
	client   *http.Client
}

// NewOllamaBackend creates a new Ollama backend
func NewOllamaBackend(endpoint string, timeout int) *OllamaBackend {
	return &OllamaBackend{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

// Generate handles text generation requests by forwarding to Ollama
func (o *OllamaBackend) Generate(ctx context.Context, req models.GenerateRequest) (<-chan models.GenerateResponse, error) {
	respChan := make(chan models.GenerateResponse, 10)

	data, err := json.Marshal(req)
	if err != nil {
		close(respChan)
		return respChan, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/api/generate", bytes.NewReader(data))
	if err != nil {
		close(respChan)
		return respChan, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		close(respChan)
		return respChan, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		close(respChan)
		return respChan, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Handle streaming response
	go func() {
		defer resp.Body.Close()
		defer close(respChan)

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			var genResp models.GenerateResponse
			if err := json.Unmarshal(scanner.Bytes(), &genResp); err != nil {
				// Log error but continue
				continue
			}

			select {
			case respChan <- genResp:
			case <-ctx.Done():
				return
			}

			if genResp.Done {
				return
			}
		}
	}()

	return respChan, nil
}

// Chat handles chat completion requests by forwarding to Ollama
func (o *OllamaBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, error) {
	respChan := make(chan models.ChatResponse, 10)

	data, err := json.Marshal(req)
	if err != nil {
		close(respChan)
		return respChan, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/api/chat", bytes.NewReader(data))
	if err != nil {
		close(respChan)
		return respChan, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		close(respChan)
		return respChan, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		close(respChan)
		return respChan, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Handle streaming response
	go func() {
		defer resp.Body.Close()
		defer close(respChan)

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			var chatResp models.ChatResponse
			if err := json.Unmarshal(scanner.Bytes(), &chatResp); err != nil {
				// Log error but continue
				continue
			}

			select {
			case respChan <- chatResp:
			case <-ctx.Done():
				return
			}

			if chatResp.Done {
				return
			}
		}
	}()

	return respChan, nil
}

// ListModels returns available models from Ollama
func (o *OllamaBackend) ListModels(ctx context.Context) (models.ModelsResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", o.endpoint+"/api/tags", nil)
	if err != nil {
		return models.ModelsResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return models.ModelsResponse{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return models.ModelsResponse{}, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var modelsResp models.ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&modelsResp); err != nil {
		return models.ModelsResponse{}, fmt.Errorf("failed to decode response: %w", err)
	}

	return modelsResp, nil
}
