package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"llm_proxy/models"
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
func (o *OllamaBackend) Generate(ctx context.Context, req models.GenerateRequest) (<-chan models.GenerateResponse, *BackendMetadata, error) {
	respChan := make(chan models.GenerateResponse, 10)
	metadata := &BackendMetadata{}

	data, err := json.Marshal(req)
	if err != nil {
		close(respChan)
		return respChan, metadata, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Store raw backend request
	metadata.RawRequest = string(data)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/api/generate", bytes.NewReader(data))
	if err != nil {
		close(respChan)
		return respChan, metadata, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		close(respChan)
		return respChan, metadata, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metadata.RawResponse = string(body)
		close(respChan)
		return respChan, metadata, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Handle streaming response
	go func() {
		defer resp.Body.Close()
		defer close(respChan)

		var rawResponse strings.Builder
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			rawResponse.WriteString(line)
			rawResponse.WriteString("\n")

			var genResp models.GenerateResponse
			if err := json.Unmarshal(scanner.Bytes(), &genResp); err != nil {
				// Log error but continue
				continue
			}

			select {
			case respChan <- genResp:
			case <-ctx.Done():
				metadata.RawResponse = rawResponse.String()
				return
			}

			if genResp.Done {
				metadata.RawResponse = rawResponse.String()
				return
			}
		}
		metadata.RawResponse = rawResponse.String()
	}()

	return respChan, metadata, nil
}

// Chat handles chat completion requests by forwarding to Ollama
func (o *OllamaBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, *BackendMetadata, error) {
	respChan := make(chan models.ChatResponse, 10)
	metadata := &BackendMetadata{}

	data, err := json.Marshal(req)
	if err != nil {
		close(respChan)
		return respChan, metadata, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Store raw backend request
	metadata.RawRequest = string(data)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/api/chat", bytes.NewReader(data))
	if err != nil {
		close(respChan)
		return respChan, metadata, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		close(respChan)
		return respChan, metadata, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metadata.RawResponse = string(body)
		close(respChan)
		return respChan, metadata, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Handle streaming response
	go func() {
		defer resp.Body.Close()
		defer close(respChan)

		var rawResponse strings.Builder
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			// Log raw response from Ollama for debugging
			rawBytes := scanner.Bytes()
			rawResponse.WriteString(string(rawBytes))
			rawResponse.WriteString("\n")

			var chatResp models.ChatResponse
			if err := json.Unmarshal(rawBytes, &chatResp); err != nil {
				// Log error but continue
				continue
			}

			// Always ensure role is set to "assistant" if empty
			// This fixes Ollama's behavior of not including role in streaming chunks
			if chatResp.Message.Role == "" {
				chatResp.Message.Role = "assistant"
			}

			// Add load_duration to final response if missing
			// Ollama doesn't include this in streaming mode, so we add a placeholder
			if chatResp.Done && chatResp.LoadDuration == 0 {
				chatResp.LoadDuration = 1
			}

			select {
			case respChan <- chatResp:
			case <-ctx.Done():
				metadata.RawResponse = rawResponse.String()
				return
			}

			if chatResp.Done {
				metadata.RawResponse = rawResponse.String()
				return
			}
		}
		metadata.RawResponse = rawResponse.String()
	}()

	return respChan, metadata, nil
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
