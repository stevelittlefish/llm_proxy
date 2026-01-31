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
func (o *OpenAIBackend) Generate(ctx context.Context, req models.GenerateRequest) (<-chan models.GenerateResponse, *BackendMetadata, error) {
	respChan := make(chan models.GenerateResponse, 10)
	metadata := &BackendMetadata{}

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
		return respChan, metadata, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Store raw backend request
	metadata.RawRequest = string(data)
	metadata.URL = o.endpoint + "/v1/completions"

	httpReq, err := http.NewRequestWithContext(ctx, "POST", metadata.URL, bytes.NewReader(data))
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
		return respChan, metadata, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	// Handle streaming response
	go func() {
		defer resp.Body.Close()
		defer close(respChan)

		if req.Stream {
			o.handleStreamingCompletion(ctx, resp.Body, respChan, req.Model, metadata)
		} else {
			o.handleNonStreamingCompletion(resp.Body, respChan, req.Model, metadata)
		}
	}()

	return respChan, metadata, nil
}

// handleStreamingCompletion processes streaming OpenAI responses and converts to Ollama format
func (o *OpenAIBackend) handleStreamingCompletion(ctx context.Context, body io.Reader, respChan chan<- models.GenerateResponse, model string, metadata *BackendMetadata) {
	scanner := bufio.NewScanner(body)
	startTime := time.Now()
	tokenCount := 0
	var rawResponse strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		rawResponse.WriteString(line)
		rawResponse.WriteString("\n")

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Store raw response before sending final message
			metadata.RawResponse = rawResponse.String()
			// Send final response with done=true and performance metrics
			totalDuration := time.Since(startTime).Nanoseconds()
			respChan <- models.GenerateResponse{
				Model:              model,
				CreatedAt:          time.Now(),
				Response:           "",
				Done:               true,
				DoneReason:         "stop",
				TotalDuration:      totalDuration + 1,
				PromptEvalCount:    1,
				PromptEvalDuration: 1,
				EvalCount:          tokenCount,
				EvalDuration:       totalDuration,
			}
			return
		}

		var openaiResp models.OpenAICompletionResponse
		if err := json.Unmarshal([]byte(data), &openaiResp); err != nil {
			continue
		}

		if len(openaiResp.Choices) > 0 {
			choice := openaiResp.Choices[0]

			// Check if this is the final chunk with finish_reason
			if choice.FinishReason != "" && choice.FinishReason != "null" {
				// Store raw response before sending final message
				metadata.RawResponse = rawResponse.String()
				totalDuration := time.Since(startTime).Nanoseconds()
				respChan <- models.GenerateResponse{
					Model:              model,
					CreatedAt:          time.Now(),
					Response:           "",
					Done:               true,
					DoneReason:         choice.FinishReason,
					TotalDuration:      totalDuration + 1,
					PromptEvalCount:    1,
					PromptEvalDuration: 1,
					EvalCount:          tokenCount,
					EvalDuration:       totalDuration,
				}
				return
			}

			text := choice.Text
			if text != "" {
				tokenCount++
			}

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
	totalDuration := time.Since(startTime).Nanoseconds()
	metadata.RawResponse = rawResponse.String()
	respChan <- models.GenerateResponse{
		Model:              model,
		CreatedAt:          time.Now(),
		Response:           "",
		Done:               true,
		DoneReason:         "stop",
		TotalDuration:      totalDuration + 1,
		PromptEvalCount:    1,
		PromptEvalDuration: 1,
		EvalCount:          tokenCount,
		EvalDuration:       totalDuration,
	}
}

// handleNonStreamingCompletion processes non-streaming OpenAI responses
func (o *OpenAIBackend) handleNonStreamingCompletion(body io.Reader, respChan chan<- models.GenerateResponse, model string, metadata *BackendMetadata) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return
	}
	metadata.RawResponse = string(bodyBytes)

	var openaiResp models.OpenAICompletionResponse
	if err := json.Unmarshal(bodyBytes, &openaiResp); err != nil {
		return
	}

	if len(openaiResp.Choices) > 0 {
		choice := openaiResp.Choices[0]
		doneReason := "stop"
		if choice.FinishReason != "" {
			doneReason = choice.FinishReason
		}

		// Extract token counts from usage if available, default to 1
		promptTokens := 1
		evalTokens := 1
		if openaiResp.Usage.PromptTokens > 0 {
			promptTokens = openaiResp.Usage.PromptTokens
		}
		if openaiResp.Usage.CompletionTokens > 0 {
			evalTokens = openaiResp.Usage.CompletionTokens
		}

		respChan <- models.GenerateResponse{
			Model:           model,
			CreatedAt:       time.Now(),
			Response:        choice.Text,
			Done:            true,
			DoneReason:      doneReason,
			PromptEvalCount: promptTokens,
			EvalCount:       evalTokens,
		}
	}
}

// Chat handles chat completion requests by translating to OpenAI format
func (o *OpenAIBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, *BackendMetadata, error) {
	respChan := make(chan models.ChatResponse, 10)
	metadata := &BackendMetadata{}

	// Translate Ollama request to OpenAI chat request
	openaiReq := models.OpenAIChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   req.Stream,
		Tools:    req.Tools,
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
		return respChan, metadata, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Store raw backend request
	metadata.RawRequest = string(data)
	metadata.URL = o.endpoint + "/v1/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, "POST", metadata.URL, bytes.NewReader(data))
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
		return respChan, metadata, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	// Handle streaming response
	go func() {
		defer resp.Body.Close()
		defer close(respChan)

		if req.Stream {
			o.handleStreamingChat(ctx, resp.Body, respChan, req.Model, metadata)
		} else {
			o.handleNonStreamingChat(resp.Body, respChan, req.Model, metadata)
		}
	}()

	return respChan, metadata, nil
}

// handleStreamingChat processes streaming OpenAI chat responses and converts to Ollama format
func (o *OpenAIBackend) handleStreamingChat(ctx context.Context, body io.Reader, respChan chan<- models.ChatResponse, model string, metadata *BackendMetadata) {
	scanner := bufio.NewScanner(body)
	startTime := time.Now()
	tokenCount := 0
	var rawResponse strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		rawResponse.WriteString(line)
		rawResponse.WriteString("\n")

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Store raw response before sending final message
			metadata.RawResponse = rawResponse.String()
			// Send final response with done=true and performance metrics
			totalDuration := time.Since(startTime).Nanoseconds()
			respChan <- models.ChatResponse{
				Model:              model,
				CreatedAt:          time.Now(),
				Message:            models.Message{Role: "assistant", Content: ""},
				Done:               true,
				DoneReason:         "stop",
				TotalDuration:      totalDuration + 1,
				LoadDuration:       1,
				PromptEvalCount:    1,
				PromptEvalDuration: 1,
				EvalCount:          tokenCount,
				EvalDuration:       totalDuration,
			}
			return
		}

		var openaiResp models.OpenAIChatResponse
		if err := json.Unmarshal([]byte(data), &openaiResp); err != nil {
			continue
		}

		if len(openaiResp.Choices) > 0 {
			choice := openaiResp.Choices[0]

			// Check if this is the final chunk with finish_reason
			if choice.FinishReason != "" && choice.FinishReason != "null" {
				// Store raw response before sending final message
				metadata.RawResponse = rawResponse.String()
				totalDuration := time.Since(startTime).Nanoseconds()
				respChan <- models.ChatResponse{
					Model:              model,
					CreatedAt:          time.Now(),
					Message:            models.Message{Role: "assistant", Content: ""},
					Done:               true,
					DoneReason:         choice.FinishReason,
					TotalDuration:      totalDuration + 1,
					LoadDuration:       1,
					PromptEvalCount:    1,
					PromptEvalDuration: 1,
					EvalCount:          tokenCount,
					EvalDuration:       totalDuration,
				}
				return
			}

			if choice.Delta != nil {
				if choice.Delta.Content != "" {
					tokenCount++
				}

				// Set role to "assistant" if empty (OpenAI often doesn't send role in streaming chunks)
				role := choice.Delta.Role
				if role == "" {
					role = "assistant"
				}

				ollamaResp := models.ChatResponse{
					Model:     model,
					CreatedAt: time.Now(),
					Message: models.Message{
						Role:      role,
						Content:   choice.Delta.Content,
						ToolCalls: choice.Delta.ToolCalls,
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
	}

	// Send final done message if not already sent
	totalDuration := time.Since(startTime).Nanoseconds()
	metadata.RawResponse = rawResponse.String()
	respChan <- models.ChatResponse{
		Model:              model,
		CreatedAt:          time.Now(),
		Message:            models.Message{Role: "assistant", Content: ""},
		Done:               true,
		DoneReason:         "stop",
		TotalDuration:      totalDuration + 1,
		LoadDuration:       1,
		PromptEvalCount:    1,
		PromptEvalDuration: 1,
		EvalCount:          tokenCount,
		EvalDuration:       totalDuration,
	}
}

// handleNonStreamingChat processes non-streaming OpenAI chat responses
func (o *OpenAIBackend) handleNonStreamingChat(body io.Reader, respChan chan<- models.ChatResponse, model string, metadata *BackendMetadata) {
	startTime := time.Now()

	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return
	}
	metadata.RawResponse = string(bodyBytes)

	var openaiResp models.OpenAIChatResponse
	if err := json.Unmarshal(bodyBytes, &openaiResp); err != nil {
		return
	}

	if len(openaiResp.Choices) > 0 && openaiResp.Choices[0].Message != nil {
		choice := openaiResp.Choices[0]
		doneReason := "stop"
		if choice.FinishReason != "" {
			doneReason = choice.FinishReason
		}

		// Extract token counts from usage if available, default to 1
		promptTokens := 1
		evalTokens := 1
		if openaiResp.Usage.PromptTokens > 0 {
			promptTokens = openaiResp.Usage.PromptTokens
		}
		if openaiResp.Usage.CompletionTokens > 0 {
			evalTokens = openaiResp.Usage.CompletionTokens
		}

		// Calculate durations
		totalDuration := time.Since(startTime).Nanoseconds()

		// Message already includes ToolCalls field, so it passes through automatically
		respChan <- models.ChatResponse{
			Model:              model,
			CreatedAt:          time.Now(),
			Message:            *choice.Message,
			Done:               true,
			DoneReason:         doneReason,
			TotalDuration:      totalDuration + 1,
			LoadDuration:       1,
			PromptEvalCount:    promptTokens,
			PromptEvalDuration: 1,
			EvalCount:          evalTokens,
			EvalDuration:       totalDuration,
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
					Model:      "default",
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
					Model:      "default",
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
			Model:      m.ID,
			ModifiedAt: time.Now(),
			Size:       0,
			Digest:     "",
		})
	}

	return models.ModelsResponse{Models: modelInfos}, nil
}
