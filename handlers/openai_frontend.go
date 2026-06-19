package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"llm_proxy/backend"
	"llm_proxy/config"
	"llm_proxy/database"
	"llm_proxy/models"
)

// OpenAIChatCompletionsHandler handles OpenAI-compatible /v1/chat/completions requests.
type OpenAIChatCompletionsHandler struct {
	backend backend.Backend
	db      *database.DB
	config  *config.Config
}

// NewOpenAIChatCompletionsHandler creates a new OpenAI-compatible chat handler.
func NewOpenAIChatCompletionsHandler(backend backend.Backend, db *database.DB, config *config.Config) *OpenAIChatCompletionsHandler {
	return &OpenAIChatCompletionsHandler{
		backend: backend,
		db:      db,
		config:  config,
	}
}

// ServeHTTP implements the http.Handler interface.
func (h *OpenAIChatCompletionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	startTime := time.Now()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var req models.OpenAIChatRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if h.config.Server.LogRawRequests {
		reqJSON, err := json.MarshalIndent(req, "", "  ")
		if err == nil {
			log.Printf("=== Raw OpenAI Chat Request ===\n%s\n================================", string(reqJSON))
		}
	}

	chatReq := models.ChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   req.Stream,
		Tools:    req.Tools,
	}

	originalLastMessage := lastMessageContent(chatReq.Messages)
	originalMessages := cloneMessages(chatReq.Messages)

	applyChatFeatures(&chatReq, h.config)

	if h.config.Server.LogMessages {
		log.Printf("=== OpenAI Chat Request ===")
		log.Printf("Model: %s", chatReq.Model)
		log.Printf("Messages:")
		for i, msg := range chatReq.Messages {
			log.Printf("  [%d] %s: %s", i, msg.Role, msg.Content)
		}
		log.Printf("===========================")
	}

	respChan, backendMeta, err := h.backend.Chat(r.Context(), chatReq)
	if err != nil {
		log.Printf("Backend error: %v", err)
		h.logRequest(startTime, chatReq.Model, chatReq.Stream, originalMessages, "", http.StatusInternalServerError, err.Error(), string(bodyBytes), "", backendMeta.RawRequest, backendMeta.RawResponse, backendMeta.URL, originalLastMessage)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.Stream {
		h.streamResponse(w, req.Model, respChan, startTime, chatReq, string(bodyBytes), backendMeta, originalMessages, originalLastMessage)
		return
	}

	h.writeResponse(w, req.Model, respChan, startTime, chatReq, string(bodyBytes), backendMeta, originalMessages, originalLastMessage)
}

func (h *OpenAIChatCompletionsHandler) streamResponse(w http.ResponseWriter, model string, respChan <-chan models.ChatResponse, startTime time.Time, req models.ChatRequest, frontendReq string, backendMeta *backend.BackendMetadata, originalMessages []models.Message, originalLastMessage string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, _ := w.(http.Flusher)
	created := time.Now().Unix()
	var fullResponse string
	var frontendResp strings.Builder

	for resp := range respChan {
		if resp.Done {
			break
		}
		content := resp.Message.Content
		if content == "" {
			continue
		}
		fullResponse += content
		chunk := models.OpenAIChatResponse{
			ID:      fmt.Sprintf("chatcmpl-%d", startTime.UnixNano()),
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []models.OpenAIChatChoice{
				{
					Index: 0,
					Delta: &models.Message{
						Role:    "assistant",
						Content: content,
					},
				},
			},
		}
		data, err := json.Marshal(chunk)
		if err != nil {
			log.Printf("Failed to marshal OpenAI stream chunk: %v", err)
			continue
		}
		writeSSE(w, &frontendResp, string(data))
		if flusher != nil {
			flusher.Flush()
		}
	}

	finalChunk := models.OpenAIChatResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", startTime.UnixNano()),
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []models.OpenAIChatChoice{
			{
				Index:        0,
				Delta:        &models.Message{},
				FinishReason: "stop",
			},
		},
	}
	data, err := json.Marshal(finalChunk)
	if err == nil {
		writeSSE(w, &frontendResp, string(data))
	}
	writeSSE(w, &frontendResp, "[DONE]")
	if flusher != nil {
		flusher.Flush()
	}

	if h.config.Server.LogMessages {
		log.Printf("=== OpenAI Chat Response Complete ===")
		log.Printf("Full Response: %s", fullResponse)
		log.Printf("=====================================")
	}
	if h.config.Server.LogRawResponses {
		log.Printf("=== Raw OpenAI Chat Response ===\n%s\n================================", frontendResp.String())
	}

	h.logRequest(startTime, req.Model, req.Stream, originalMessages, fullResponse, http.StatusOK, "", frontendReq, strings.TrimRight(frontendResp.String(), "\n"), backendMeta.RawRequest, backendMeta.RawResponse, backendMeta.URL, originalLastMessage)
}

func (h *OpenAIChatCompletionsHandler) writeResponse(w http.ResponseWriter, model string, respChan <-chan models.ChatResponse, startTime time.Time, req models.ChatRequest, frontendReq string, backendMeta *backend.BackendMetadata, originalMessages []models.Message, originalLastMessage string) {
	var fullResponse string
	for resp := range respChan {
		fullResponse += resp.Message.Content
	}

	response := models.OpenAIChatResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", startTime.UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []models.OpenAIChatChoice{
			{
				Index: 0,
				Message: &models.Message{
					Role:    "assistant",
					Content: fullResponse,
				},
				FinishReason: "stop",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	var frontendResp strings.Builder
	if err := json.NewEncoder(io.MultiWriter(w, &frontendResp)).Encode(response); err != nil {
		log.Printf("Failed to encode OpenAI response: %v", err)
		return
	}

	if h.config.Server.LogMessages {
		log.Printf("=== OpenAI Chat Response Complete ===")
		log.Printf("Full Response: %s", fullResponse)
		log.Printf("=====================================")
	}
	if h.config.Server.LogRawResponses {
		log.Printf("=== Raw OpenAI Chat Response ===\n%s\n================================", frontendResp.String())
	}

	h.logRequest(startTime, req.Model, req.Stream, originalMessages, fullResponse, http.StatusOK, "", frontendReq, strings.TrimRight(frontendResp.String(), "\n"), backendMeta.RawRequest, backendMeta.RawResponse, backendMeta.URL, originalLastMessage)
}

func writeSSE(w io.Writer, capture *strings.Builder, data string) {
	line := fmt.Sprintf("data: %s\n\n", data)
	fmt.Fprint(w, line)
	capture.WriteString(line)
}

func (h *OpenAIChatCompletionsHandler) logRequest(startTime time.Time, model string, stream bool, originalMessages []models.Message, response string, statusCode int, errMsg string, frontendReq string, frontendResp string, backendReq string, backendResp string, backendURL string, originalLastMessage string) {
	var prompt strings.Builder
	for _, msg := range originalMessages {
		prompt.WriteString(msg.Role)
		prompt.WriteString(": ")
		prompt.WriteString(msg.Content)
		prompt.WriteString("\n")
	}

	entry := database.LogEntry{
		Timestamp:   startTime,
		Endpoint:    "/v1/chat/completions",
		Method:      "POST",
		Model:       model,
		Prompt:      prompt.String(),
		Response:    response,
		StatusCode:  statusCode,
		LatencyMs:   time.Since(startTime).Milliseconds(),
		Stream:      stream,
		BackendType: h.config.Backend.Type,
		Error:       errMsg,
		FrontendURL: fmt.Sprintf("http://%s:%d/v1/chat/completions",
			h.config.Server.Host,
			h.config.Server.Port,
		),
		BackendURL:       backendURL,
		FrontendRequest:  frontendReq,
		FrontendResponse: frontendResp,
		BackendRequest:   backendReq,
		BackendResponse:  backendResp,
		LastMessage:      originalLastMessage,
	}

	if err := h.db.Log(entry); err != nil {
		log.Printf("Failed to log OpenAI request: %v", err)
	}
}

// OpenAIModelsHandler handles OpenAI-compatible /v1/models requests.
type OpenAIModelsHandler struct {
	backend backend.Backend
}

// NewOpenAIModelsHandler creates a new OpenAI-compatible models handler.
func NewOpenAIModelsHandler(backend backend.Backend) *OpenAIModelsHandler {
	return &OpenAIModelsHandler{backend: backend}
}

// ServeHTTP implements the http.Handler interface.
func (h *OpenAIModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	modelsResp, err := h.backend.ListModels(r.Context())
	if err != nil {
		log.Printf("Failed to list models: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type openAIModel struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	response := struct {
		Object string        `json:"object"`
		Data   []openAIModel `json:"data"`
	}{
		Object: "list",
	}

	for _, model := range modelsResp.Models {
		name := model.Name
		if name == "" {
			name = model.Model
		}
		response.Data = append(response.Data, openAIModel{
			ID:      name,
			Object:  "model",
			Created: model.ModifiedAt.Unix(),
			OwnedBy: "llm_proxy",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode OpenAI models response: %v", err)
	}
}
