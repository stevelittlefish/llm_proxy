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

	// Log raw request if enabled
	if h.config.Server.LogRawRequests {
		reqJSON, err := json.MarshalIndent(req, "", "  ")
		if err == nil {
			log.Printf("=== Raw Generate Request ===\n%s\n============================", string(reqJSON))
		}
	}

	// Apply model mapping
	req.Model = h.config.GetModelMapping(req.Model)

	// Log request messages if enabled
	if h.config.Server.LogMessages {
		log.Printf("=== Generate Request ===")
		log.Printf("Model: %s", req.Model)
		log.Printf("Prompt: %s", req.Prompt)
		log.Printf("=======================")
	}

	// Capture frontend request as raw JSON
	frontendReqJSON, _ := json.Marshal(req)

	// Call backend
	respChan, backendMeta, err := h.backend.Generate(r.Context(), req)
	if err != nil {
		log.Printf("Backend error: %v", err)
		h.logRequest(startTime, req, "", http.StatusInternalServerError, err.Error(), string(frontendReqJSON), "", backendMeta.RawRequest, backendMeta.RawResponse)
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
		log.Printf("=== Streaming Generate Response ===")
	}

	// Stream responses
	var fullResponse strings.Builder
	var responses []models.GenerateResponse
	encoder := json.NewEncoder(w)

	for resp := range respChan {
		fullResponse.WriteString(resp.Response)

		// Store response for raw logging if enabled
		if h.config.Server.LogRawResponses {
			responses = append(responses, resp)
		}

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
		log.Printf("=== Generate Response Complete ===")
		log.Printf("Full Response: %s", fullResponse.String())
		log.Printf("==================================")
	}

	// Log raw responses if enabled
	if h.config.Server.LogRawResponses && len(responses) > 0 {
		respJSON, err := json.MarshalIndent(responses, "", "  ")
		if err == nil {
			log.Printf("=== Raw Generate Responses ===\n%s\n==============================", string(respJSON))
		}
	}

	// Capture frontend response as raw JSON
	frontendRespJSON, _ := json.Marshal(responses)

	// Log the request/response
	h.logRequest(startTime, req, fullResponse.String(), http.StatusOK, "", string(frontendReqJSON), string(frontendRespJSON), backendMeta.RawRequest, backendMeta.RawResponse)
}

// logRequest logs the request and response to the database
func (h *GenerateHandler) logRequest(startTime time.Time, req models.GenerateRequest, response string, statusCode int, errMsg string, frontendReq string, frontendResp string, backendReq string, backendResp string) {
	latency := time.Since(startTime).Milliseconds()

	entry := database.LogEntry{
		Timestamp:        startTime,
		Endpoint:         "/api/generate",
		Method:           "POST",
		Model:            req.Model,
		Prompt:           req.Prompt,
		Response:         response,
		StatusCode:       statusCode,
		LatencyMs:        latency,
		Stream:           req.Stream,
		BackendType:      h.config.Backend.Type,
		Error:            errMsg,
		FrontendRequest:  frontendReq,
		FrontendResponse: frontendResp,
		BackendRequest:   backendReq,
		BackendResponse:  backendResp,
	}

	if err := h.db.Log(entry); err != nil {
		log.Printf("Failed to log request: %v", err)
	}
}
