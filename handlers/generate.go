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

	// Read raw body bytes first for logging
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Generate request: failed to read request body: %v", err)
		h.logInvalidRequest(startTime, "", fmt.Sprintf("failed to read request body: %v", err))
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Parse into struct
	var req models.GenerateRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		log.Printf("Generate request: invalid request body: %v\nBody: %s", err, string(bodyBytes))
		h.logInvalidRequest(startTime, string(bodyBytes), fmt.Sprintf("invalid request body: %v", err))
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

	applyGenerateRequestSanitization(&req, h.config)
	clientWantsStream := req.Stream
	req.Stream = resolveStream(clientWantsStream, h.config)

	// Log request messages if enabled
	if h.config.Server.LogMessages {
		log.Printf("=== Generate Request ===")
		log.Printf("Model: %s", req.Model)
		log.Printf("Prompt: %s", req.Prompt)
		log.Printf("=======================")
	}

	// Use raw body bytes for logging (truly raw JSON from the connection)
	frontendReqJSON := bodyBytes

	// Call backend
	respChan, backendMeta, err := h.backend.Generate(r.Context(), req)
	if err != nil {
		log.Printf("Backend error: %v", err)
		h.logRequest(startTime, req, clientWantsStream, "", http.StatusInternalServerError, err.Error(), string(frontendReqJSON), "", backendMeta.RawRequest, backendMeta.RawResponse, backendMeta.URL)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Set headers for streaming
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Transfer-Encoding", "chunked")

	// Log when streaming starts if enabled
	if h.config.Server.LogMessages {
		log.Printf("=== Streaming Generate Response ===")
	}

	// Stream responses
	var fullResponse strings.Builder
	var responses []models.GenerateResponse
	var combined models.GenerateResponse
	encoder := json.NewEncoder(w)

	for resp := range respChan {
		fullResponse.WriteString(resp.Response)

		// Always store responses for database logging
		responses = append(responses, resp)

		// Accumulate into a single response in case the client needs the
		// non-streamed shape even though the backend call streamed (or
		// vice versa) due to stream_override.
		combined.Model = resp.Model
		combined.CreatedAt = resp.CreatedAt
		combined.Response += resp.Response
		if len(resp.Context) > 0 {
			combined.Context = resp.Context
		}
		if resp.Done {
			combined.Done = true
			combined.DoneReason = resp.DoneReason
			combined.TotalDuration = resp.TotalDuration
			combined.LoadDuration = resp.LoadDuration
			combined.PromptEvalCount = resp.PromptEvalCount
			combined.PromptEvalDuration = resp.PromptEvalDuration
			combined.EvalCount = resp.EvalCount
			combined.EvalDuration = resp.EvalDuration
		}

		// Only forward chunks as they arrive if the client actually asked
		// to stream; otherwise wait and send the aggregated response once.
		if clientWantsStream {
			if err := encoder.Encode(resp); err != nil {
				log.Printf("Error encoding response: %v", err)
				break
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}

		if resp.Done {
			break
		}
	}

	if !clientWantsStream {
		if err := encoder.Encode(combined); err != nil {
			log.Printf("Error encoding response: %v", err)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
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

	// Capture frontend response as newline-delimited JSON (matching what was
	// actually written to the client: per-chunk if streamed, one object otherwise)
	var frontendRespBuilder strings.Builder
	if clientWantsStream {
		for i, resp := range responses {
			respJSON, err := json.Marshal(resp)
			if err == nil {
				frontendRespBuilder.Write(respJSON)
				if i < len(responses)-1 {
					frontendRespBuilder.WriteString("\n")
				}
			}
		}
	} else if respJSON, err := json.Marshal(combined); err == nil {
		frontendRespBuilder.Write(respJSON)
	}

	// Log the request/response
	h.logRequest(startTime, req, clientWantsStream, fullResponse.String(), http.StatusOK, "", string(frontendReqJSON), frontendRespBuilder.String(), backendMeta.RawRequest, backendMeta.RawResponse, backendMeta.URL)
}

// logRequest logs the request and response to the database
func (h *GenerateHandler) logRequest(startTime time.Time, req models.GenerateRequest, stream bool, response string, statusCode int, errMsg string, frontendReq string, frontendResp string, backendReq string, backendResp string, backendURL string) {
	latency := time.Since(startTime).Milliseconds()

	// For generate endpoint, the prompt is the last message
	lastMessage := req.Prompt
	if lastMessage == "" {
		lastMessage = "unknown"
	}

	entry := database.LogEntry{
		Timestamp:        startTime,
		Endpoint:         "/api/generate",
		Method:           "POST",
		Model:            req.Model,
		Prompt:           req.Prompt,
		Response:         response,
		StatusCode:       statusCode,
		LatencyMs:        latency,
		Stream:           stream,
		BackendType:      h.config.Backend.Type,
		Error:            errMsg,
		FrontendURL:      fmt.Sprintf("http://%s:%d/api/generate", h.config.Server.Host, h.config.Server.Port),
		BackendURL:       backendURL,
		FrontendRequest:  frontendReq,
		FrontendResponse: frontendResp,
		BackendRequest:   backendReq,
		BackendResponse:  backendResp,
		LastMessage:      lastMessage,
	}

	if err := h.db.Log(entry); err != nil {
		log.Printf("Failed to log request: %v", err)
	}
}

// logInvalidRequest persists a request that was rejected before it could be parsed
// into a GenerateRequest (unreadable body or malformed JSON), so it's still visible
// in the request log instead of vanishing silently.
func (h *GenerateHandler) logInvalidRequest(startTime time.Time, frontendReq string, errMsg string) {
	entry := database.LogEntry{
		Timestamp:       startTime,
		Endpoint:        "/api/generate",
		Method:          "POST",
		StatusCode:      http.StatusBadRequest,
		LatencyMs:       time.Since(startTime).Milliseconds(),
		BackendType:     h.config.Backend.Type,
		Error:           errMsg,
		FrontendURL:     fmt.Sprintf("http://%s:%d/api/generate", h.config.Server.Host, h.config.Server.Port),
		FrontendRequest: frontendReq,
	}

	if err := h.db.Log(entry); err != nil {
		log.Printf("Failed to log invalid request: %v", err)
	}
}
