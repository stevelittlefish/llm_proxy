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

// OpenAIBackend implements the Backend interface for OpenAI-compatible APIs
type OpenAIBackend struct {
	endpoint string
	client   *http.Client
}

// NewOpenAIBackend creates a new OpenAI backend
func NewOpenAIBackend(endpoint string, timeout int) *OpenAIBackend {
	return &OpenAIBackend{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

// Generate handles text generation requests by translating to OpenAI format
func (o *OpenAIBackend) Generate(ctx context.Context, req models.GenerateRequest) (<-chan models.GenerateResponse, error) {
	respChan := make(chan models.GenerateResponse, 10)

	// Translate Ollama request to OpenAI completion request
	openaiReq := models.OpenAICompletionRequest{
		Model:  req.Model,
		Prompt: req.Prompt,
		Stream: req.Stream,
	}

	// Map Ollama options to OpenAI parameters
	if req.Options != nil {
		if temp, ok := req.Options["temperature"].(float64); ok {
			openaiReq.Temperature = temp
		}
		if maxTokens, ok := req.Options["num_predict"].(float64); ok {
			openaiReq.MaxTokens = int(maxTokens)
		}
		if topP, ok := req.Options["top_p"].(float64); ok {
			openaiReq.TopP = topP
		}
	}

	data, err := json.Marshal(openaiReq)
	if err != nil {
		close(respChan)
		return respChan, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/v1/completions", bytes.NewReader(data))
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
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		close(respChan)
		return respChan, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	// Handle streaming response
	go func() {
		defer resp.Body.Close()
		defer close(respChan)

		if req.Stream {
			o.handleStreamingCompletion(ctx, resp.Body, respChan, req.Model)
		} else {
			o.handleNonStreamingCompletion(resp.Body, respChan, req.Model)
		}
	}()

	return respChan, nil
}

// handleStreamingCompletion processes streaming OpenAI responses and converts to Ollama format
func (o *OpenAIBackend) handleStreamingCompletion(ctx context.Context, body io.Reader, respChan chan<- models.GenerateResponse, model string) {
	scanner := bufio.NewScanner(body)
	fullResponse := ""

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Send final response with done=true
			respChan <- models.GenerateResponse{
				Model:     model,
				CreatedAt: time.Now(),
				Response:  "",
				Done:      true,
			}
			return
		}

		var openaiResp models.OpenAICompletionResponse
		if err := json.Unmarshal([]byte(data), &openaiResp); err != nil {
			continue
		}

		if len(openaiResp.Choices) > 0 {
			text := openaiResp.Choices[0].Text
			fullResponse += text

			ollamaResp := models.GenerateResponse{
				Model:     model,
				CreatedAt: time.Now(),
				Response:  text,
				Done:      false,
			}

			select {
			case respChan <- ollamaResp:
			case <-ctx.Done():
				return
			}
		}
	}

	// Send final done message if not already sent
	respChan <- models.GenerateResponse{
		Model:     model,
		CreatedAt: time.Now(),
		Response:  "",
		Done:      true,
	}
}

// handleNonStreamingCompletion processes non-streaming OpenAI responses
func (o *OpenAIBackend) handleNonStreamingCompletion(body io.Reader, respChan chan<- models.GenerateResponse, model string) {
	var openaiResp models.OpenAICompletionResponse
	if err := json.NewDecoder(body).Decode(&openaiResp); err != nil {
		return
	}

	if len(openaiResp.Choices) > 0 {
		respChan <- models.GenerateResponse{
			Model:     model,
			CreatedAt: time.Now(),
			Response:  openaiResp.Choices[0].Text,
			Done:      true,
		}
	}
}

// Chat handles chat completion requests by translating to OpenAI format
func (o *OpenAIBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, error) {
	respChan := make(chan models.ChatResponse, 10)

	// Translate Ollama request to OpenAI chat request
	openaiReq := models.OpenAIChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   req.Stream,
	}

	// Map Ollama options to OpenAI parameters
	if req.Options != nil {
		if temp, ok := req.Options["temperature"].(float64); ok {
			openaiReq.Temperature = temp
		}
		if maxTokens, ok := req.Options["num_predict"].(float64); ok {
			openaiReq.MaxTokens = int(maxTokens)
		}
		if topP, ok := req.Options["top_p"].(float64); ok {
			openaiReq.TopP = topP
		}
	}

	data, err := json.Marshal(openaiReq)
	if err != nil {
		close(respChan)
		return respChan, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.endpoint+"/v1/chat/completions", bytes.NewReader(data))
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
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		close(respChan)
		return respChan, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	// Handle streaming response
	go func() {
		defer resp.Body.Close()
		defer close(respChan)

		if req.Stream {
			o.handleStreamingChat(ctx, resp.Body, respChan, req.Model)
		} else {
			o.handleNonStreamingChat(resp.Body, respChan, req.Model)
		}
	}()

	return respChan, nil
}

// handleStreamingChat processes streaming OpenAI chat responses and converts to Ollama format
func (o *OpenAIBackend) handleStreamingChat(ctx context.Context, body io.Reader, respChan chan<- models.ChatResponse, model string) {
	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Send final response with done=true
			respChan <- models.ChatResponse{
				Model:     model,
				CreatedAt: time.Now(),
				Message:   models.Message{Role: "assistant", Content: ""},
				Done:      true,
			}
			return
		}

		var openaiResp models.OpenAIChatResponse
		if err := json.Unmarshal([]byte(data), &openaiResp); err != nil {
			continue
		}

		if len(openaiResp.Choices) > 0 && openaiResp.Choices[0].Delta != nil {
			ollamaResp := models.ChatResponse{
				Model:     model,
				CreatedAt: time.Now(),
				Message: models.Message{
					Role:    openaiResp.Choices[0].Delta.Role,
					Content: openaiResp.Choices[0].Delta.Content,
				},
				Done: false,
			}

			select {
			case respChan <- ollamaResp:
			case <-ctx.Done():
				return
			}
		}
	}

	// Send final done message if not already sent
	respChan <- models.ChatResponse{
		Model:     model,
		CreatedAt: time.Now(),
		Message:   models.Message{Role: "assistant", Content: ""},
		Done:      true,
	}
}

// handleNonStreamingChat processes non-streaming OpenAI chat responses
func (o *OpenAIBackend) handleNonStreamingChat(body io.Reader, respChan chan<- models.ChatResponse, model string) {
	var openaiResp models.OpenAIChatResponse
	if err := json.NewDecoder(body).Decode(&openaiResp); err != nil {
		return
	}

	if len(openaiResp.Choices) > 0 && openaiResp.Choices[0].Message != nil {
		respChan <- models.ChatResponse{
			Model:     model,
			CreatedAt: time.Now(),
			Message:   *openaiResp.Choices[0].Message,
			Done:      true,
		}
	}
}

// ListModels returns available models from OpenAI-compatible API
func (o *OpenAIBackend) ListModels(ctx context.Context) (models.ModelsResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", o.endpoint+"/v1/models", nil)
	if err != nil {
		return models.ModelsResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return models.ModelsResponse{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// If /v1/models doesn't work, return a default model
		return models.ModelsResponse{
			Models: []models.ModelInfo{
				{
					Name:       "default",
					ModifiedAt: time.Now(),
					Size:       0,
					Digest:     "",
				},
			},
		}, nil
	}

	// Try to parse OpenAI models response and convert to Ollama format
	var openaiModels struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openaiModels); err != nil {
		// Return default model on error
		return models.ModelsResponse{
			Models: []models.ModelInfo{
				{
					Name:       "default",
					ModifiedAt: time.Now(),
					Size:       0,
					Digest:     "",
				},
			},
		}, nil
	}

	// Convert to Ollama format
	var modelInfos []models.ModelInfo
	for _, m := range openaiModels.Data {
		modelInfos = append(modelInfos, models.ModelInfo{
			Name:       m.ID,
			ModifiedAt: time.Now(),
			Size:       0,
			Digest:     "",
		})
	}

	return models.ModelsResponse{Models: modelInfos}, nil
}
