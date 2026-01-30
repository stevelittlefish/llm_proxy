package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config represents the application configuration
type Config struct {
	Server   ServerConfig   `json:"server"`
	Backend  BackendConfig  `json:"backend"`
	Database DatabaseConfig `json:"database"`
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
	Path string `json:"path"`
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

	return &config, nil
}
