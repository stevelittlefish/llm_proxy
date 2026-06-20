package backend

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	gemma4FixEnabled bool
}

// NewOpenAIBackend creates a new OpenAI backend
func NewOpenAIBackend(endpoint string, timeout int, forcePromptCache bool, gemma4FixEnabled bool) *OpenAIBackend {
	return &OpenAIBackend{
		endpoint:         endpoint,
		forcePromptCache: forcePromptCache,
		gemma4FixEnabled: gemma4FixEnabled,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

// postChatCompletion POSTs an already-marshaled chat completion request body
// to the backend's /v1/chat/completions endpoint and validates the status
// code, recording the raw response on failure. Shared by the normal Chat()
// call path and, when gemma_4_fix is enabled, by its retry/nudge follow-up
// requests (see gemma4_fix.go).
func (o *OpenAIBackend) postChatCompletion(ctx context.Context, data []byte, metadata *BackendMetadata) (*http.Response, error) {
	url := o.endpoint + "/v1/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metadata.RawResponse = string(body)
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	return resp, nil
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
	// Increase buffer size to handle large SSE events (e.g., verbose responses with timings)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024) // 1MB max per line
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

	// Check for scanner errors
	if err := scanner.Err(); err != nil {
		fmt.Printf("Scanner error in handleStreamingCompletion: %v\n", err)
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
		if openaiResp.Usage != nil && openaiResp.Usage.PromptTokens > 0 {
			promptTokens = openaiResp.Usage.PromptTokens
		}
		if openaiResp.Usage != nil && openaiResp.Usage.CompletionTokens > 0 {
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

func generateToolCallID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "call-" + hex.EncodeToString(b)
}

// convertMessagesToOpenAI converts Ollama-format messages to OpenAI-format
// by adding the "type" field to tool_calls and converting arguments to JSON string.
// It also propagates generated tool call IDs to subsequent tool-result messages
// that lack a tool_call_id, matching them positionally.
func convertMessagesToOpenAI(messages []models.Message) []models.Message {
	converted := make([]models.Message, len(messages))

	// pendingIDs holds the ordered IDs of tool calls from the most recent
	// assistant message, to be assigned positionally to following tool messages.
	var pendingIDs []string

	for i, msg := range messages {
		converted[i] = msg

		switch msg.Role {
		case "assistant":
			pendingIDs = nil

			if len(msg.ToolCalls) == 0 {
				break
			}

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
							newFunction["arguments"] = argsTyped
						case map[string]interface{}, []interface{}:
							argsJSON, err := json.Marshal(argsTyped)
							if err == nil {
								newFunction["arguments"] = string(argsJSON)
							} else {
								newFunction["arguments"] = "{}"
							}
						default:
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
					newToolCall["function"] = tcMap["function"]
				}

				// Copy any other fields (like id)
				for k, v := range tcMap {
					if k != "function" && k != "type" {
						newToolCall[k] = v
					}
				}

				// vLLM requires id on every tool call; generate one if absent
				if _, hasID := newToolCall["id"]; !hasID {
					newToolCall["id"] = generateToolCallID()
				}

				convertedToolCalls[j] = newToolCall

				// Track this ID so we can assign it to the matching tool-result message
				if id, ok := newToolCall["id"].(string); ok {
					pendingIDs = append(pendingIDs, id)
				}
			}

			converted[i].ToolCalls = convertedToolCalls

		case "tool":
			// Assign tool_call_id positionally from the preceding tool call batch
			if converted[i].ToolCallID == "" && len(pendingIDs) > 0 {
				converted[i].ToolCallID = pendingIDs[0]
				pendingIDs = pendingIDs[1:]
			}

		default:
			pendingIDs = nil
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

	data, err := o.buildOpenAIChatRequest(req, convertedMessages)
	if err != nil {
		close(respChan)
		return respChan, metadata, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Store raw backend request
	metadata.RawRequest = string(data)
	metadata.URL = o.endpoint + "/v1/chat/completions"

	resp, err := o.postChatCompletion(ctx, data, metadata)
	if err != nil {
		close(respChan)
		return respChan, metadata, err
	}

	// Handle streaming response
	go func() {
		defer resp.Body.Close()
		defer close(respChan)

		switch {
		case req.Stream && o.gemma4FixEnabled:
			o.handleStreamingChatGemma4Fix(ctx, resp.Body, respChan, req.Model, metadata, req, convertedMessages)
		case req.Stream:
			o.handleStreamingChat(ctx, resp.Body, respChan, req.Model, metadata)
		default:
			o.handleNonStreamingChat(resp.Body, respChan, req.Model, metadata)
		}
	}()

	return respChan, metadata, nil
}

func (o *OpenAIBackend) buildOpenAIChatRequest(req models.ChatRequest, convertedMessages []models.Message) ([]byte, error) {
	if req.OpenAIRaw != nil {
		raw := cloneRawMessageMap(req.OpenAIRaw)
		setRawMessage(raw, "model", req.Model)
		setRawMessage(raw, "messages", convertedMessages)
		if req.Stream {
			setRawMessage(raw, "stream", req.Stream)
		} else if _, ok := raw["stream"]; ok {
			setRawMessage(raw, "stream", req.Stream)
		}
		if !req.Stream {
			// stream_options is only valid when stream=true; carrying it over
			// after forcing stream off (e.g. via stream_override) gets the
			// request rejected by OpenAI-compatible backends.
			delete(raw, "stream_options")
		}
		if len(req.Tools) > 0 {
			setRawMessage(raw, "tools", req.Tools)
		} else {
			delete(raw, "tools")
		}
		if req.Options != nil {
			if maxTokens, ok := req.Options["num_predict"].(float64); ok {
				setRawMessage(raw, "max_tokens", int(maxTokens))
			}
		}
		if o.forcePromptCache {
			setRawMessage(raw, "cache_prompt", true)
		}
		return json.Marshal(raw)
	}

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

	return json.Marshal(openaiReq)
}

func cloneRawMessageMap(raw map[string]json.RawMessage) map[string]json.RawMessage {
	cloned := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		clonedValue := make(json.RawMessage, len(value))
		copy(clonedValue, value)
		cloned[key] = clonedValue
	}
	return cloned
}

func setRawMessage(raw map[string]json.RawMessage, key string, value interface{}) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	raw[key] = data
}

// handleStreamingChat processes streaming OpenAI chat responses and converts to Ollama format
func (o *OpenAIBackend) handleStreamingChat(ctx context.Context, body io.Reader, respChan chan<- models.ChatResponse, model string, metadata *BackendMetadata) {
	scanner := bufio.NewScanner(body)
	// Increase buffer size to handle large SSE events (e.g., verbose responses with timings)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024) // 1MB max per line
	startTime := time.Now()
	tokenCount := 0
	var rawResponse strings.Builder
	doneReason := "stop"
	var finalUsage *models.OpenAIUsage

	// Tool call accumulation state
	// Map of tool call index -> accumulated data
	toolCallsState := make(map[int]struct {
		ID        string
		Name      string
		Arguments string
	})
	toolCallsSent := false
	sendToolCalls := func() {
		if len(toolCallsState) == 0 || toolCallsSent {
			return
		}
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
		toolCallsSent = true
	}
	finalResponse := func() models.ChatResponse {
		totalDuration := time.Since(startTime).Nanoseconds()
		promptTokens := 1
		evalTokens := tokenCount
		if finalUsage != nil {
			promptTokens = finalUsage.PromptTokens
			evalTokens = finalUsage.CompletionTokens
		}
		return models.ChatResponse{
			Model:              model,
			CreatedAt:          time.Now(),
			Message:            models.Message{Role: "assistant", Content: ""},
			Done:               true,
			DoneReason:         doneReason,
			TotalDuration:      totalDuration + 1,
			LoadDuration:       1,
			PromptEvalCount:    promptTokens,
			PromptEvalDuration: 1,
			EvalCount:          evalTokens,
			EvalDuration:       totalDuration,
			Usage:              finalUsage,
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		rawResponse.WriteString(line)
		rawResponse.WriteString("\n")

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			sendToolCalls()

			// Store raw response before sending final message
			metadata.RawResponse = rawResponse.String()
			// Send final response with done=true and performance metrics
			respChan <- finalResponse()
			return
		}

		var openaiResp models.OpenAIChatResponse
		if err := json.Unmarshal([]byte(data), &openaiResp); err != nil {
			continue
		}
		if openaiResp.Usage != nil {
			finalUsage = openaiResp.Usage
		}

		if len(openaiResp.Choices) > 0 {
			choice := openaiResp.Choices[0]

			// Check if this is the final chunk with finish_reason
			if choice.FinishReason != "" && choice.FinishReason != "null" {
				doneReason = choice.FinishReason
				sendToolCalls()
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
						return
					}
				}
			}
		}
	}

	sendToolCalls()

	// Check for scanner errors
	if err := scanner.Err(); err != nil {
		fmt.Printf("Scanner error in handleStreamingChat: %v\n", err)
	}

	// Send final done message if not already sent
	metadata.RawResponse = rawResponse.String()
	respChan <- finalResponse()
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

		toolCall := map[string]interface{}{
			"id": state.ID,
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
		if openaiResp.Usage != nil && openaiResp.Usage.PromptTokens > 0 {
			promptTokens = openaiResp.Usage.PromptTokens
		}
		if openaiResp.Usage != nil && openaiResp.Usage.CompletionTokens > 0 {
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
			Usage:              openaiResp.Usage,
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
