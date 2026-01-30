package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/steveiliop56/llm_proxy/backend"
	"github.com/steveiliop56/llm_proxy/config"
	"github.com/steveiliop56/llm_proxy/database"
	"github.com/steveiliop56/llm_proxy/models"
)

// GenerateHandler handles /api/generate requests
type GenerateHandler struct {
	backend backend.Backend
	db      *database.DB
	config  *config.Config
}

// NewGenerateHandler creates a new generate handler
func NewGenerateHandler(backend backend.Backend, db *database.DB, config *config.Config) *GenerateHandler {
	return &GenerateHandler{
		backend: backend,
		db:      db,
		config:  config,
	}
}

// ServeHTTP implements the http.Handler interface
func (h *GenerateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	startTime := time.Now()

	var req models.GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Apply model mapping
	req.Model = h.config.GetModelMapping(req.Model)

	// Call backend
	respChan, err := h.backend.Generate(r.Context(), req)
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
		fullResponse.WriteString(resp.Response)

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
func (h *GenerateHandler) logRequest(startTime time.Time, req models.GenerateRequest, response string, statusCode int, errMsg string) {
	latency := time.Since(startTime).Milliseconds()

	entry := database.LogEntry{
		Timestamp:   startTime,
		Endpoint:    "/api/generate",
		Method:      "POST",
		Model:       req.Model,
		Prompt:      req.Prompt,
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
