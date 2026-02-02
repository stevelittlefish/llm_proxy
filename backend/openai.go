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
	endpoint         string
	client           *http.Client
	forcePromptCache bool
}

// NewOpenAIBackend creates a new OpenAI backend
func NewOpenAIBackend(endpoint string, timeout int, forcePromptCache bool) *OpenAIBackend {
	return &OpenAIBackend{
		endpoint:         endpoint,
		forcePromptCache: forcePromptCache,
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
		Model:       req.Model,
		Prompt:      req.Prompt,
		Stream:      req.Stream,
		CachePrompt: o.forcePromptCache,
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
	sentFinalMessage := false
	var finalDoneReason string

	for scanner.Scan() {
		line := scanner.Text()
		rawResponse.WriteString(line)
		rawResponse.WriteString("\n")

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Don't return here - continue reading to capture full response
			continue
		}

		var openaiResp models.OpenAICompletionResponse
		if err := json.Unmarshal([]byte(data), &openaiResp); err != nil {
			continue
		}

		if len(openaiResp.Choices) > 0 {
			choice := openaiResp.Choices[0]

			// Check if this is the final chunk with finish_reason
			if choice.FinishReason != "" && choice.FinishReason != "null" && !sentFinalMessage {
				finalDoneReason = choice.FinishReason
				totalDuration := time.Since(startTime).Nanoseconds()
				respChan <- models.GenerateResponse{
					Model:              model,
					CreatedAt:          time.Now(),
					Response:           "",
					Done:               true,
					DoneReason:         finalDoneReason,
					TotalDuration:      totalDuration + 1,
					PromptEvalCount:    1,
					PromptEvalDuration: 1,
					EvalCount:          tokenCount,
					EvalDuration:       totalDuration,
				}
				sentFinalMessage = true
				// Don't return - continue reading to capture full response
				continue
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
				// Store response even when cancelled
				metadata.RawResponse = rawResponse.String()
				return
			}
		}
	}

	// Store complete raw response after reading entire stream
	metadata.RawResponse = rawResponse.String()

	// Send final done message if not already sent
	if !sentFinalMessage {
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

// convertMessagesToOpenAI converts Ollama-format messages to OpenAI-format
// by adding the "type" field to tool_calls and converting arguments to JSON string
func convertMessagesToOpenAI(messages []models.Message) []models.Message {
	converted := make([]models.Message, len(messages))

	for i, msg := range messages {
		converted[i] = msg

		// If this message has tool_calls, convert them to OpenAI format
		if len(msg.ToolCalls) > 0 {
			convertedToolCalls := make([]interface{}, len(msg.ToolCalls))

			for j, tc := range msg.ToolCalls {
				tcMap, ok := tc.(map[string]interface{})
				if !ok {
					convertedToolCalls[j] = tc
					continue
				}

				// Build OpenAI-format tool call
				newToolCall := make(map[string]interface{})
				newToolCall["type"] = "function"

				// Handle the function field
				if fnField, ok := tcMap["function"].(map[string]interface{}); ok {
					newFunction := make(map[string]interface{})

					// Copy name
					if name, ok := fnField["name"].(string); ok {
						newFunction["name"] = name
					}

					// Convert arguments to JSON string if it's an object
					if args, ok := fnField["arguments"]; ok {
						switch argsTyped := args.(type) {
						case string:
							// Already a string, use as-is
							newFunction["arguments"] = argsTyped
						case map[string]interface{}, []interface{}:
							// It's an object/array, convert to JSON string
							argsJSON, err := json.Marshal(argsTyped)
							if err == nil {
								newFunction["arguments"] = string(argsJSON)
							} else {
								newFunction["arguments"] = "{}"
							}
						default:
							// Try to marshal whatever it is
							argsJSON, err := json.Marshal(argsTyped)
							if err == nil {
								newFunction["arguments"] = string(argsJSON)
							} else {
								newFunction["arguments"] = "{}"
							}
						}
					} else {
						newFunction["arguments"] = "{}"
					}

					newToolCall["function"] = newFunction
				} else {
					// Copy function field as-is if not a map
					newToolCall["function"] = tcMap["function"]
				}

				// Copy any other fields (like id)
				for k, v := range tcMap {
					if k != "function" && k != "type" {
						newToolCall[k] = v
					}
				}

				convertedToolCalls[j] = newToolCall
			}

			converted[i].ToolCalls = convertedToolCalls
		}
	}

	return converted
}

// Chat handles chat completion requests by translating to OpenAI format
func (o *OpenAIBackend) Chat(ctx context.Context, req models.ChatRequest) (<-chan models.ChatResponse, *BackendMetadata, error) {
	respChan := make(chan models.ChatResponse, 10)
	metadata := &BackendMetadata{}

	// Convert messages from Ollama format to OpenAI format (add type field to tool_calls)
	convertedMessages := convertMessagesToOpenAI(req.Messages)

	// Translate Ollama request to OpenAI chat request
	openaiReq := models.OpenAIChatRequest{
		Model:       req.Model,
		Messages:    convertedMessages,
		Stream:      req.Stream,
		Tools:       req.Tools,
		CachePrompt: o.forcePromptCache,
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
	sentFinalMessage := false
	var finalDoneReason string

	// Tool call accumulation state
	// Map of tool call index -> accumulated data
	toolCallsState := make(map[int]struct {
		ID        string
		Name      string
		Arguments string
	})

	for scanner.Scan() {
		line := scanner.Text()
		rawResponse.WriteString(line)
		rawResponse.WriteString("\n")

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Don't return here - continue reading to capture full response
			continue
		}

		var openaiResp models.OpenAIChatResponse
		if err := json.Unmarshal([]byte(data), &openaiResp); err != nil {
			continue
		}

		if len(openaiResp.Choices) > 0 {
			choice := openaiResp.Choices[0]

			// Check if this is the final chunk with finish_reason
			if choice.FinishReason != "" && choice.FinishReason != "null" && !sentFinalMessage {
				// Send accumulated tool calls if any exist
				if len(toolCallsState) > 0 {
					toolCalls := buildToolCallsArray(toolCallsState)
					respChan <- models.ChatResponse{
						Model:     model,
						CreatedAt: time.Now(),
						Message: models.Message{
							Role:      "assistant",
							Content:   "",
							ToolCalls: toolCalls,
						},
						Done: false,
					}
				}

				finalDoneReason = choice.FinishReason
				totalDuration := time.Since(startTime).Nanoseconds()
				respChan <- models.ChatResponse{
					Model:              model,
					CreatedAt:          time.Now(),
					Message:            models.Message{Role: "assistant", Content: ""},
					Done:               true,
					DoneReason:         finalDoneReason,
					TotalDuration:      totalDuration + 1,
					LoadDuration:       1,
					PromptEvalCount:    1,
					PromptEvalDuration: 1,
					EvalCount:          tokenCount,
					EvalDuration:       totalDuration,
				}
				sentFinalMessage = true
				// Don't return - continue reading to capture full response
				continue
			}

			if choice.Delta != nil {
				// Handle tool calls by accumulating them
				if choice.Delta.ToolCalls != nil && len(choice.Delta.ToolCalls) > 0 {
					for _, tc := range choice.Delta.ToolCalls {
						tcMap, ok := tc.(map[string]interface{})
						if !ok {
							continue
						}

						// Get the index to track which tool call this chunk belongs to
						index := 0
						if idx, ok := tcMap["index"].(float64); ok {
							index = int(idx)
						}

						// Initialize state for this tool call if needed
						if _, exists := toolCallsState[index]; !exists {
							toolCallsState[index] = struct {
								ID        string
								Name      string
								Arguments string
							}{}
						}

						state := toolCallsState[index]

						// Accumulate ID
						if id, ok := tcMap["id"].(string); ok && id != "" {
							state.ID = id
						}

						// Accumulate function name and arguments
						if fn, ok := tcMap["function"].(map[string]interface{}); ok {
							if name, ok := fn["name"].(string); ok && name != "" {
								state.Name = name
							}
							if args, ok := fn["arguments"].(string); ok {
								state.Arguments += args
							}
						}

						toolCallsState[index] = state
					}
					// Don't send tool call chunks immediately, continue accumulating
					continue
				}

				// Handle regular content
				if choice.Delta.Content != "" {
					tokenCount++

					// Set role to "assistant" if empty
					role := choice.Delta.Role
					if role == "" {
						role = "assistant"
					}

					ollamaResp := models.ChatResponse{
						Model:     model,
						CreatedAt: time.Now(),
						Message: models.Message{
							Role:     role,
							Content:  choice.Delta.Content,
							Thinking: choice.Delta.Thinking,
						},
						Done: false,
					}

					select {
					case respChan <- ollamaResp:
					case <-ctx.Done():
						// Store response even when cancelled
						metadata.RawResponse = rawResponse.String()
						return
					}
				}
			}
		}
	}

	// Store complete raw response after reading entire stream
	metadata.RawResponse = rawResponse.String()

	// Send accumulated tool calls if any exist (only if final message not already sent)
	if len(toolCallsState) > 0 && !sentFinalMessage {
		toolCalls := buildToolCallsArray(toolCallsState)
		respChan <- models.ChatResponse{
			Model:     model,
			CreatedAt: time.Now(),
			Message: models.Message{
				Role:      "assistant",
				Content:   "",
				ToolCalls: toolCalls,
			},
			Done: false,
		}
	}

	// Send final done message if not already sent
	if !sentFinalMessage {
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
	}
}

// buildToolCallsArray converts accumulated tool call state into Ollama format
func buildToolCallsArray(toolCallsState map[int]struct {
	ID        string
	Name      string
	Arguments string
}) []interface{} {
	var toolCalls []interface{}

	// Process tool calls in order by index
	maxIndex := -1
	for idx := range toolCallsState {
		if idx > maxIndex {
			maxIndex = idx
		}
	}

	for i := 0; i <= maxIndex; i++ {
		state, exists := toolCallsState[i]
		if !exists {
			continue
		}

		// Parse arguments JSON string into an object
		var argsObj interface{}
		if state.Arguments != "" {
			if err := json.Unmarshal([]byte(state.Arguments), &argsObj); err != nil {
				// If parsing fails, use the string as-is
				argsObj = state.Arguments
			}
		} else {
			argsObj = map[string]interface{}{}
		}

		// Build Ollama-format tool call (simpler structure than OpenAI)
		toolCall := map[string]interface{}{
			"function": map[string]interface{}{
				"name":      state.Name,
				"arguments": argsObj,
			},
		}

		toolCalls = append(toolCalls, toolCall)
	}

	return toolCalls
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
