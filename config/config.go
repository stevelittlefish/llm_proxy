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
	Models   ModelsConfig   `json:"models"`
}

// ServerConfig holds the server settings
type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
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

// ModelsConfig holds model-related settings
type ModelsConfig struct {
	Default  string            `json:"default"`
	Mappings map[string]string `json:"mappings"`
}

// Load reads and parses the configuration file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
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

// GetModelMapping returns the mapped model name, or the original if no mapping exists
func (c *Config) GetModelMapping(model string) string {
	if mapped, ok := c.Models.Mappings[model]; ok {
		return mapped
	}
	return model
}
