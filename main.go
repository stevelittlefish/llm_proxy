package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"llm_proxy/backend"
	"llm_proxy/config"
	"llm_proxy/database"
	"llm_proxy/handlers"
	"llm_proxy/middleware"
	"time"
)

// startCleanupTask runs a periodic cleanup task to remove old database entries
func startCleanupTask(db *database.DB, maxRequests int, intervalMinutes int, done chan struct{}) {
	ticker := time.NewTicker(time.Duration(intervalMinutes) * time.Minute)
	defer ticker.Stop()
	defer close(done)

	// Run cleanup immediately on startup
	if deleted, err := db.CleanupOldRequests(maxRequests); err != nil {
		log.Printf("Error during database cleanup: %v", err)
	} else if deleted > 0 {
		log.Printf("Database cleanup: removed %d old request(s)", deleted)
	}

	for {
		select {
		case <-ticker.C:
			if deleted, err := db.CleanupOldRequests(maxRequests); err != nil {
				log.Printf("Error during database cleanup: %v", err)
			} else if deleted > 0 {
				log.Printf("Database cleanup: removed %d old request(s)", deleted)
			}
		case <-done:
			log.Println("Stopping database cleanup task...")
			return
		}
	}
}

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.toml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	log.Printf("Loading configuration from %s", *configPath)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize database
	log.Printf("Initializing database at %s", cfg.Database.Path)
	db, err := database.New(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Start background cleanup task
	cleanupDone := make(chan struct{})
	if cfg.Database.CleanupInterval > 0 && cfg.Database.MaxRequests > 0 {
		log.Printf("Starting database cleanup task: keeping max %d requests, running every %d minutes",
			cfg.Database.MaxRequests, cfg.Database.CleanupInterval)
		go startCleanupTask(db, cfg.Database.MaxRequests, cfg.Database.CleanupInterval, cleanupDone)
	} else {
		log.Printf("Database cleanup task disabled")
		close(cleanupDone)
	}

	// Create backend based on configuration
	var backendInstance backend.Backend
	log.Printf("Initializing %s backend at %s", cfg.Backend.Type, cfg.Backend.Endpoint)

	switch cfg.Backend.Type {
	case "openai":
		backendInstance = backend.NewOpenAIBackend(cfg.Backend.Endpoint, cfg.Backend.Timeout, cfg.BackendOpenAI.ForcePromptCache)
		if cfg.BackendOpenAI.ForcePromptCache {
			log.Printf("OpenAI backend: prompt caching enabled")
		}
	case "ollama":
		backendInstance = backend.NewOllamaBackend(cfg.Backend.Endpoint, cfg.Backend.Timeout)
	default:
		log.Fatalf("Invalid backend type: %s", cfg.Backend.Type)
	}

	// Set up HTTP handlers
	mux := http.NewServeMux()

	generateHandler := handlers.NewGenerateHandler(backendInstance, db, cfg)
	chatHandler := handlers.NewChatHandler(backendInstance, db, cfg)
	modelsHandler := handlers.NewModelsHandler(backendInstance)
	showHandler := handlers.NewShowHandler(backendInstance)

	// Prepare config data for web UI
	homeData := map[string]interface{}{
		"BackendType":     cfg.Backend.Type,
		"BackendEndpoint": cfg.Backend.Endpoint,
		"ServerHost":      cfg.Server.Host,
		"ServerPort":      cfg.Server.Port,
		"Timeout":         cfg.Backend.Timeout,
		"DatabasePath":    cfg.Database.Path,
		"EnableCORS":      cfg.Server.EnableCORS,
	}

	webHandler := handlers.NewWebHandler(db, homeData)

	mux.Handle("/api/generate", generateHandler)
	mux.Handle("/api/chat", chatHandler)
	mux.Handle("/api/tags", modelsHandler)
	mux.Handle("/api/show", showHandler)

	// Web UI endpoints
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		webHandler.HomeHandler(w, r)
	})
	mux.HandleFunc("/logs", webHandler.IndexHandler)
	mux.HandleFunc("/logs/details", webHandler.DetailsHandler)
	mux.HandleFunc("/favicon.ico", webHandler.FaviconHandler)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})

	// Start HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	// Apply middlewares
	var handler http.Handler = mux

	// Apply request logging middleware if verbose is enabled
	handler = middleware.RequestLogging(cfg.Server.Verbose)(handler)

	// Apply CORS middleware if enabled
	if cfg.Server.EnableCORS {
		handler = middleware.CORS(handler)
		log.Printf("CORS enabled")
	}

	if cfg.Server.Verbose {
		log.Printf("Verbose logging enabled")
	}
	if cfg.Server.LogMessages {
		log.Printf("Message logging enabled - message content will be logged to stdout")
	}
	if cfg.Server.LogRawRequests {
		log.Printf("Raw request logging enabled - raw JSON requests will be logged to stdout")
	}
	if cfg.Server.LogRawResponses {
		log.Printf("Raw response logging enabled - raw JSON responses will be logged to stdout")
	}

	server := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Starting LLM proxy server on %s", addr)
		log.Printf("Backend: %s (%s)", cfg.Backend.Type, cfg.Backend.Endpoint)
		log.Printf("Database: %s", cfg.Database.Path)

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-sigChan
	log.Println("Shutting down server...")

	// Stop cleanup task
	if cfg.Database.CleanupInterval > 0 && cfg.Database.MaxRequests > 0 {
		cleanupDone <- struct{}{}
		<-cleanupDone
	}

	if err := server.Close(); err != nil {
		log.Printf("Error closing server: %v", err)
	}

	log.Println("Server stopped")
}
