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
		log.Printf("Chat request: failed to read request body: %v", err)
		h.logInvalidRequest(startTime, "", fmt.Sprintf("failed to read request body: %v", err))
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Parse into struct
	var req models.ChatRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		log.Printf("Chat request: invalid request body: %v\nBody: %s", err, string(bodyBytes))
		h.logInvalidRequest(startTime, string(bodyBytes), fmt.Sprintf("invalid request body: %v", err))
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

	originalLastMessage := lastMessageContent(req.Messages)
	originalMessages := cloneMessages(req.Messages)

	applyChatRequestSanitization(&req, h.config)
	applyChatFeatures(&req, h.config)
	clientWantsStream := req.Stream
	req.Stream = resolveStream(clientWantsStream, h.config)

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
		h.logRequest(startTime, req.Model, clientWantsStream, originalMessages, "", http.StatusInternalServerError, err.Error(), string(frontendReqJSON), "", backendMeta.RawRequest, backendMeta.RawResponse, backendMeta.URL, originalLastMessage)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Set headers for streaming
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Transfer-Encoding", "chunked")

	// Log when streaming starts if enabled
	if h.config.Server.LogMessages {
		log.Printf("=== Streaming Chat Response ===")
	}

	// Stream responses
	var fullResponse strings.Builder
	var responses []models.ChatResponse
	var combined models.ChatResponse
	encoder := json.NewEncoder(w)

	for resp := range respChan {
		fullResponse.WriteString(resp.Message.Content)

		// Always store responses for database logging
		responses = append(responses, resp)

		// Accumulate into a single response in case the client needs the
		// non-streamed shape even though the backend call streamed (or
		// vice versa) due to stream_override.
		combined.Model = resp.Model
		combined.CreatedAt = resp.CreatedAt
		if resp.Message.Role != "" {
			combined.Message.Role = resp.Message.Role
		}
		combined.Message.Content += resp.Message.Content
		combined.Message.Thinking += resp.Message.Thinking
		if len(resp.Message.ToolCalls) > 0 {
			combined.Message.ToolCalls = resp.Message.ToolCalls
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
			combined.Usage = resp.Usage
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
		if combined.Message.Role == "" {
			combined.Message.Role = "assistant"
		}
		if err := encoder.Encode(combined); err != nil {
			log.Printf("Error encoding response: %v", err)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
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

	// Log the request/response (use original messages, not injected version)
	h.logRequest(startTime, req.Model, clientWantsStream, originalMessages, fullResponse.String(), http.StatusOK, "", string(frontendReqJSON), frontendRespBuilder.String(), backendMeta.RawRequest, backendMeta.RawResponse, backendMeta.URL, originalLastMessage)
}

// logRequest logs the request and response to the database
func (h *ChatHandler) logRequest(startTime time.Time, model string, stream bool, originalMessages []models.Message, response string, statusCode int, errMsg string, frontendReq string, frontendResp string, backendReq string, backendResp string, backendURL string, originalLastMessage string) {
	latency := time.Since(startTime).Milliseconds()

	// Extract prompt from original messages (before injection)
	var prompt strings.Builder
	for _, msg := range originalMessages {
		prompt.WriteString(msg.Role)
		prompt.WriteString(": ")
		prompt.WriteString(msg.Content)
		prompt.WriteString("\n")
	}

	entry := database.LogEntry{
		Timestamp:        startTime,
		Endpoint:         "/api/chat",
		Method:           "POST",
		Model:            model,
		Prompt:           prompt.String(),
		Response:         response,
		StatusCode:       statusCode,
		LatencyMs:        latency,
		Stream:           stream,
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

// logInvalidRequest persists a request that was rejected before it could be parsed
// into a ChatRequest (unreadable body or malformed JSON), so it's still visible in
// the request log instead of vanishing silently.
func (h *ChatHandler) logInvalidRequest(startTime time.Time, frontendReq string, errMsg string) {
	entry := database.LogEntry{
		Timestamp:       startTime,
		Endpoint:        "/api/chat",
		Method:          "POST",
		StatusCode:      http.StatusBadRequest,
		LatencyMs:       time.Since(startTime).Milliseconds(),
		BackendType:     h.config.Backend.Type,
		Error:           errMsg,
		FrontendURL:     fmt.Sprintf("http://%s:%d/api/chat", h.config.Server.Host, h.config.Server.Port),
		FrontendRequest: frontendReq,
	}

	if err := h.db.Log(entry); err != nil {
		log.Printf("Failed to log invalid request: %v", err)
	}
}
