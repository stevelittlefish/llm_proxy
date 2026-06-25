package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"llm_proxy/backend"
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
		Name  string `json:"name"`
		Model string `json:"model"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	modelName := req.Model
	if modelName == "" {
		modelName = req.Name
	}
	if modelName == "" {
		http.Error(w, "Missing model", http.StatusBadRequest)
		return
	}

	response, err := h.backend.ShowModel(r.Context(), modelName)
	if err != nil {
		log.Printf("Failed to show model: %v", err)
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "status code: 404") || strings.Contains(err.Error(), "model not found") {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}
