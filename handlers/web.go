package handlers

import (
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"llm_proxy/database"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var templates *template.Template

const pageSize = 25

func init() {
	// Load templates with custom functions
	funcMap := template.FuncMap{
		"truncate":    truncateString,
		"formatBytes": formatBytes,
	}

	var err error
	templates, err = template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		log.Fatalf("Failed to parse templates: %v", err)
	}
}

// WebHandler handles the web UI for viewing logs
type WebHandler struct {
	db     *database.DB
	config interface{} // Store config data for home page
}

// NewWebHandler creates a new web handler
func NewWebHandler(db *database.DB, config interface{}) *WebHandler {
	return &WebHandler{
		db:     db,
		config: config,
	}
}

// truncateString truncates a string to a maximum length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// formatBytes formats bytes in a human-readable way
func formatBytes(size int) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	} else if size < 1024*1024 {
		return fmt.Sprintf("%.2f KB", float64(size)/1024)
	} else {
		return fmt.Sprintf("%.2f MB", float64(size)/(1024*1024))
	}
}

// HomeHandler serves the home page with configuration info
func (h *WebHandler) HomeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "home.html", h.config); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

// IndexHandler serves the index page with paginated list
func (h *WebHandler) IndexHandler(w http.ResponseWriter, r *http.Request) {
	// Get page number from query params
	page := 1
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	offset := (page - 1) * pageSize

	// Get total count for pagination
	total, err := h.db.GetTotalCount()
	if err != nil {
		log.Printf("Error getting total count: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Get entries
	entries, err := h.db.GetRecentEntries(pageSize, offset)
	if err != nil {
		log.Printf("Error getting entries: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))

	// Prepare template data
	data := struct {
		Entries     []database.LogEntry
		CurrentPage int
		TotalPages  int
		TotalCount  int64
		HasPrev     bool
		HasNext     bool
		PrevPage    int
		NextPage    int
	}{
		Entries:     entries,
		CurrentPage: page,
		TotalPages:  totalPages,
		TotalCount:  total,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		PrevPage:    page - 1,
		NextPage:    page + 1,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "logs.html", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}

// FaviconHandler serves the favicon
func (h *WebHandler) FaviconHandler(w http.ResponseWriter, r *http.Request) {
	// Read the favicon from embedded FS
	data, err := staticFS.ReadFile("static/llama.ico")
	if err != nil {
		log.Printf("Error reading favicon: %v", err)
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=31536000") // Cache for 1 year
	w.Write(data)
}

// DetailsHandler serves the details page for a specific request
func (h *WebHandler) DetailsHandler(w http.ResponseWriter, r *http.Request) {
	// Get ID from query params
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Error(w, "Missing ID parameter", http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID parameter", http.StatusBadRequest)
		return
	}

	// Get entry
	entry, err := h.db.GetEntryByID(id)
	if err != nil {
		log.Printf("Error getting entry: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if entry == nil {
		http.NotFound(w, r)
		return
	}

	// Get next and previous entry IDs for navigation
	nextID, err := h.db.GetNextEntryID(id)
	if err != nil {
		log.Printf("Error getting next entry ID: %v", err)
	}

	prevID, err := h.db.GetPreviousEntryID(id)
	if err != nil {
		log.Printf("Error getting previous entry ID: %v", err)
	}

	// Prepare template data with navigation
	data := struct {
		*database.LogEntry
		NextID *int64
		PrevID *int64
	}{
		LogEntry: entry,
		NextID:   nextID,
		PrevID:   prevID,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "details.html", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
}
