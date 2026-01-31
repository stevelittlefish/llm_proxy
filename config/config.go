package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config represents the application configuration
type Config struct {
	Server            ServerConfig            `json:"server"`
	Backend           BackendConfig           `json:"backend"`
	Database          DatabaseConfig          `json:"database"`
	ChatTextInjection ChatTextInjectionConfig `json:"chat_text_injection"`
}

// ServerConfig holds the server settings
type ServerConfig struct {
	Host            string `json:"host"`
	Port            int    `json:"port"`
	EnableCORS      bool   `json:"enable_cors"`
	LogMessages     bool   `json:"log_messages"`
	LogRawRequests  bool   `json:"log_raw_requests"`
	LogRawResponses bool   `json:"log_raw_responses"`
}

// BackendConfig holds the backend service settings
type BackendConfig struct {
	Type     string `json:"type"` // "openai" or "ollama"
	Endpoint string `json:"endpoint"`
	Timeout  int    `json:"timeout"` // in seconds
}

// DatabaseConfig holds the database settings
type DatabaseConfig struct {
	Path            string `json:"path"`
	MaxRequests     int    `json:"max_requests"`      // Maximum number of requests to keep (0 = unlimited)
	CleanupInterval int    `json:"cleanup_interval"`  // Cleanup interval in minutes (0 = disabled)
}

// ChatTextInjectionConfig holds the chat text injection settings
type ChatTextInjectionConfig struct {
	Enabled bool   `json:"enabled"` // Enable text injection
	Text    string `json:"text"`    // Text to inject
	Mode    string `json:"mode"`    // "first" or "last" - which user message to inject into
}

// Load reads and parses the configuration file
func Load(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	defer file.Close()

	var config Config
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields() // Fail on unknown keys
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate backend type
	if config.Backend.Type != "openai" && config.Backend.Type != "ollama" {
		return nil, fmt.Errorf("invalid backend type: %s (must be 'openai' or 'ollama')", config.Backend.Type)
	}

	// Validate chat text injection mode
	if config.ChatTextInjection.Mode != "" && config.ChatTextInjection.Mode != "first" && config.ChatTextInjection.Mode != "last" {
		return nil, fmt.Errorf("invalid chat_text_injection.mode: %s (must be 'first' or 'last')", config.ChatTextInjection.Mode)
	}

	// Set defaults
	if config.Server.Host == "" {
		config.Server.Host = "0.0.0.0"
	}
	if config.Server.Port == 0 {
		config.Server.Port = 11434
	}
	if config.Backend.Timeout == 0 {
		config.Backend.Timeout = 300
	}
	if config.Database.Path == "" {
		config.Database.Path = "./llm_proxy.db"
	}
	if config.Database.MaxRequests == 0 {
		config.Database.MaxRequests = 100
	}
	if config.Database.CleanupInterval == 0 {
		config.Database.CleanupInterval = 5
	}
	if config.ChatTextInjection.Mode == "" {
		config.ChatTextInjection.Mode = "last"
	}

	return &config, nil
}
