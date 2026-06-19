package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

	var req models.OpenAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	chatReq := models.ChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   req.Stream,
		Tools:    req.Tools,
	}

	respChan, backendMeta, err := h.backend.Chat(r.Context(), chatReq)
	if err != nil {
		log.Printf("Backend error: %v", err)
		h.logRequest(startTime, chatReq, "", http.StatusInternalServerError, err.Error(), backendMeta.URL)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.Stream {
		h.streamResponse(w, r, req.Model, respChan, startTime, chatReq, backendMeta.URL)
		return
	}

	h.writeResponse(w, req.Model, respChan, startTime, chatReq, backendMeta.URL)
}

func (h *OpenAIChatCompletionsHandler) streamResponse(w http.ResponseWriter, r *http.Request, model string, respChan <-chan models.ChatResponse, startTime time.Time, req models.ChatRequest, backendURL string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, _ := w.(http.Flusher)
	created := time.Now().Unix()
	var fullResponse string

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
		fmt.Fprintf(w, "data: %s\n\n", data)
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
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}

	h.logRequest(startTime, req, fullResponse, http.StatusOK, "", backendURL)
}

func (h *OpenAIChatCompletionsHandler) writeResponse(w http.ResponseWriter, model string, respChan <-chan models.ChatResponse, startTime time.Time, req models.ChatRequest, backendURL string) {
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
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode OpenAI response: %v", err)
		return
	}

	h.logRequest(startTime, req, fullResponse, http.StatusOK, "", backendURL)
}

func (h *OpenAIChatCompletionsHandler) logRequest(startTime time.Time, req models.ChatRequest, response string, statusCode int, errMsg string, backendURL string) {
	lastMessage := "unknown"
	if len(req.Messages) > 0 {
		lastMessage = req.Messages[len(req.Messages)-1].Content
	}

	promptBytes, err := json.Marshal(req.Messages)
	prompt := ""
	if err == nil {
		prompt = string(promptBytes)
	}

	entry := database.LogEntry{
		Timestamp:   startTime,
		Endpoint:    "/v1/chat/completions",
		Method:      "POST",
		Model:       req.Model,
		Prompt:      prompt,
		Response:    response,
		StatusCode:  statusCode,
		LatencyMs:   time.Since(startTime).Milliseconds(),
		Stream:      req.Stream,
		BackendType: h.config.Backend.Type,
		Error:       errMsg,
		FrontendURL: fmt.Sprintf("http://%s:%d/v1/chat/completions",
			h.config.Server.Host,
			h.config.Server.Port,
		),
		BackendURL:  backendURL,
		LastMessage: lastMessage,
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
