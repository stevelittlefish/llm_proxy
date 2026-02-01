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

// ChatHandler handles /api/chat requests
type ChatHandler struct {
	backend backend.Backend
	db      *database.DB
	config  *config.Config
}

// NewChatHandler creates a new chat handler
func NewChatHandler(backend backend.Backend, db *database.DB, config *config.Config) *ChatHandler {
	return &ChatHandler{
		backend: backend,
		db:      db,
		config:  config,
	}
}

// ServeHTTP implements the http.Handler interface
func (h *ChatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	startTime := time.Now()

	// Read raw body bytes first for logging
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Parse into struct
	var req models.ChatRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Log raw request if enabled
	if h.config.Server.LogRawRequests {
		reqJSON, err := json.MarshalIndent(req, "", "  ")
		if err == nil {
			log.Printf("=== Raw Chat Request ===\n%s\n========================", string(reqJSON))
		}
	}

	// Capture original last message before injection for database logging
	originalLastMessage := "unknown"
	if len(req.Messages) > 0 {
		originalLastMessage = req.Messages[len(req.Messages)-1].Content
	}

	// Apply text injection if enabled
	if h.config.ChatTextInjection.Enabled && h.config.ChatTextInjection.Text != "" {
		h.applyTextInjection(&req)
	}

	// Log request messages if enabled
	if h.config.Server.LogMessages {
		log.Printf("=== Chat Request ===")
		log.Printf("Model: %s", req.Model)
		log.Printf("Messages:")
		for i, msg := range req.Messages {
			log.Printf("  [%d] %s: %s", i, msg.Role, msg.Content)
		}
		log.Printf("===================")
	}

	// Use raw body bytes for logging (truly raw JSON from the connection)
	frontendReqJSON := bodyBytes

	// Call backend
	respChan, backendMeta, err := h.backend.Chat(r.Context(), req)
	if err != nil {
		log.Printf("Backend error: %v", err)
		h.logRequest(startTime, req, "", http.StatusInternalServerError, err.Error(), string(frontendReqJSON), "", backendMeta.RawRequest, backendMeta.RawResponse, backendMeta.URL, originalLastMessage)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Set headers for streaming
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Transfer-Encoding", "chunked")

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Log when streaming starts if enabled
	if h.config.Server.LogMessages {
		log.Printf("=== Streaming Chat Response ===")
	}

	// Stream responses
	var fullResponse strings.Builder
	var responses []models.ChatResponse
	encoder := json.NewEncoder(w)

	for resp := range respChan {
		fullResponse.WriteString(resp.Message.Content)

		// Always store responses for database logging
		responses = append(responses, resp)

		if err := encoder.Encode(resp); err != nil {
			log.Printf("Error encoding response: %v", err)
			break
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		if resp.Done {
			break
		}
	}

	// Log complete response messages if enabled
	if h.config.Server.LogMessages {
		log.Printf("=== Chat Response Complete ===")
		log.Printf("Full Response: %s", fullResponse.String())
		log.Printf("==============================")
	}

	// Log raw responses if enabled
	if h.config.Server.LogRawResponses && len(responses) > 0 {
		respJSON, err := json.MarshalIndent(responses, "", "  ")
		if err == nil {
			log.Printf("=== Raw Chat Responses ===\n%s\n==========================", string(respJSON))
		}
	}

	// Capture frontend response as newline-delimited JSON (matching actual streamed format)
	var frontendRespBuilder strings.Builder
	for i, resp := range responses {
		respJSON, err := json.Marshal(resp)
		if err == nil {
			frontendRespBuilder.Write(respJSON)
			if i < len(responses)-1 {
				frontendRespBuilder.WriteString("\n")
			}
		}
	}

	// Log the request/response (use original last message, not injected version)
	h.logRequest(startTime, req, fullResponse.String(), http.StatusOK, "", string(frontendReqJSON), frontendRespBuilder.String(), backendMeta.RawRequest, backendMeta.RawResponse, backendMeta.URL, originalLastMessage)
}

// applyTextInjection injects text into the appropriate user message
func (h *ChatHandler) applyTextInjection(req *models.ChatRequest) {
	injectionText := h.config.ChatTextInjection.Text
	mode := h.config.ChatTextInjection.Mode

	// Find the target message index based on mode
	targetIndex := -1
	if mode == "first" {
		// Find first user message
		for i, msg := range req.Messages {
			if msg.Role == "user" {
				targetIndex = i
				break
			}
		}
	} else { // mode == "last"
		// Find last user message
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				targetIndex = i
				break
			}
		}
	}

	// If no user message found, nothing to inject
	if targetIndex == -1 {
		return
	}

	// Check if injection text already exists in the message
	if strings.Contains(req.Messages[targetIndex].Content, injectionText) {
		return
	}

	// Inject the text
	req.Messages[targetIndex].Content = req.Messages[targetIndex].Content + " " + injectionText
}

// logRequest logs the request and response to the database
func (h *ChatHandler) logRequest(startTime time.Time, req models.ChatRequest, response string, statusCode int, errMsg string, frontendReq string, frontendResp string, backendReq string, backendResp string, backendURL string, originalLastMessage string) {
	latency := time.Since(startTime).Milliseconds()

	// Extract prompt from messages (note: this may include injected text, but that's sent to backend)
	var prompt strings.Builder
	for _, msg := range req.Messages {
		prompt.WriteString(msg.Role)
		prompt.WriteString(": ")
		prompt.WriteString(msg.Content)
		prompt.WriteString("\n")
	}

	entry := database.LogEntry{
		Timestamp:        startTime,
		Endpoint:         "/api/chat",
		Method:           "POST",
		Model:            req.Model,
		Prompt:           prompt.String(),
		Response:         response,
		StatusCode:       statusCode,
		LatencyMs:        latency,
		Stream:           req.Stream,
		BackendType:      h.config.Backend.Type,
		Error:            errMsg,
		FrontendURL:      fmt.Sprintf("http://%s:%d/api/chat", h.config.Server.Host, h.config.Server.Port),
		BackendURL:       backendURL,
		FrontendRequest:  frontendReq,
		FrontendResponse: frontendResp,
		BackendRequest:   backendReq,
		BackendResponse:  backendResp,
		LastMessage:      originalLastMessage,
	}

	if err := h.db.Log(entry); err != nil {
		log.Printf("Failed to log request: %v", err)
	}
}
