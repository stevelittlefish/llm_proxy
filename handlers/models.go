package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/steveiliop56/llm_proxy/backend"
	"github.com/steveiliop56/llm_proxy/models"
)

// ModelsHandler handles /api/tags requests
type ModelsHandler struct {
	backend backend.Backend
}

// NewModelsHandler creates a new models handler
func NewModelsHandler(backend backend.Backend) *ModelsHandler {
	return &ModelsHandler{
		backend: backend,
	}
}

// ServeHTTP implements the http.Handler interface
func (h *ModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(modelsResp); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}

// ShowHandler handles /api/show requests (returns basic model info)
type ShowHandler struct {
	backend backend.Backend
}

// NewShowHandler creates a new show handler
func NewShowHandler(backend backend.Backend) *ShowHandler {
	return &ShowHandler{
		backend: backend,
	}
}

// ServeHTTP implements the http.Handler interface
func (h *ShowHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Return basic model info
	response := models.ModelInfo{
		Name:   req.Name,
		Size:   0,
		Digest: "",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}
