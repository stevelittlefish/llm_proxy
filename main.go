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
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.json", "Path to configuration file")
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

	// Create backend based on configuration
	var backendInstance backend.Backend
	log.Printf("Initializing %s backend at %s", cfg.Backend.Type, cfg.Backend.Endpoint)

	switch cfg.Backend.Type {
	case "openai":
		backendInstance = backend.NewOpenAIBackend(cfg.Backend.Endpoint, cfg.Backend.Timeout)
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
	webHandler := handlers.NewWebHandler(db)

	mux.Handle("/api/generate", generateHandler)
	mux.Handle("/api/chat", chatHandler)
	mux.Handle("/api/tags", modelsHandler)
	mux.Handle("/api/show", showHandler)

	// Web UI endpoints
	mux.HandleFunc("/logs", webHandler.IndexHandler)
	mux.HandleFunc("/logs/details", webHandler.DetailsHandler)

	// Root endpoint - mimics Ollama's behavior
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Ollama (proxy) is running")
	})

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})

	// Start HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	// Apply CORS middleware if enabled
	var handler http.Handler = mux
	if cfg.Server.EnableCORS {
		handler = middleware.CORS(handler)
		log.Printf("CORS enabled")
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

	if err := server.Close(); err != nil {
		log.Printf("Error closing server: %v", err)
	}

	log.Println("Server stopped")
}
