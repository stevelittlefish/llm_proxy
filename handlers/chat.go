package handlers

import (
	"encoding/json"
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

	var req models.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Apply model mapping
	req.Model = h.config.GetModelMapping(req.Model)

	// Call backend
	respChan, err := h.backend.Chat(r.Context(), req)
	if err != nil {
		log.Printf("Backend error: %v", err)
		h.logRequest(startTime, req, "", http.StatusInternalServerError, err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Set headers for streaming
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Transfer-Encoding", "chunked")

	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Stream responses
	var fullResponse strings.Builder
	encoder := json.NewEncoder(w)

	for resp := range respChan {
		fullResponse.WriteString(resp.Message.Content)

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

	// Log the request/response
	h.logRequest(startTime, req, fullResponse.String(), http.StatusOK, "")
}

// logRequest logs the request and response to the database
func (h *ChatHandler) logRequest(startTime time.Time, req models.ChatRequest, response string, statusCode int, errMsg string) {
	latency := time.Since(startTime).Milliseconds()

	// Extract prompt from messages
	var prompt strings.Builder
	for _, msg := range req.Messages {
		prompt.WriteString(msg.Role)
		prompt.WriteString(": ")
		prompt.WriteString(msg.Content)
		prompt.WriteString("\n")
	}

	entry := database.LogEntry{
		Timestamp:   startTime,
		Endpoint:    "/api/chat",
		Method:      "POST",
		Model:       req.Model,
		Prompt:      prompt.String(),
		Response:    response,
		StatusCode:  statusCode,
		LatencyMs:   latency,
		Stream:      req.Stream,
		BackendType: h.config.Backend.Type,
		Error:       errMsg,
	}

	if err := h.db.Log(entry); err != nil {
		log.Printf("Failed to log request: %v", err)
	}
}
