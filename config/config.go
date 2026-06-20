package config

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// Config represents the application configuration
type Config struct {
	Server              ServerConfig              `toml:"server"`
	Backend             BackendConfig             `toml:"backend"`
	BackendOpenAI       BackendOpenAIConfig       `toml:"backend_openai"`
	Database            DatabaseConfig            `toml:"database"`
	RequestSanitization RequestSanitizationConfig `toml:"request_sanitization"`
	ChatTextInjection   ChatTextInjectionConfig   `toml:"chat_text_injection"`
	StreamOverride      StreamOverrideConfig      `toml:"stream_override"`
}

// ServerConfig holds the server settings
type ServerConfig struct {
	Host            string `toml:"host"`
	Port            int    `toml:"port"`
	EnableCORS      bool   `toml:"enable_cors"`
	LogMessages     bool   `toml:"log_messages"`
	LogRawRequests  bool   `toml:"log_raw_requests"`
	LogRawResponses bool   `toml:"log_raw_responses"`
	Verbose         bool   `toml:"verbose"`
}

// BackendConfig holds the backend service settings
type BackendConfig struct {
	Type          string   `toml:"type"` // "openai" or "ollama"
	Endpoint      string   `toml:"endpoint"`
	Timeout       int      `toml:"timeout"`        // in seconds
	ToolBlacklist []string `toml:"tool_blacklist"` // List of tool names to filter out
}

// DatabaseConfig holds the database settings
type DatabaseConfig struct {
	Path            string `toml:"path"`
	MaxRequests     int    `toml:"max_requests"`     // Maximum number of requests to keep (0 = unlimited)
	CleanupInterval int    `toml:"cleanup_interval"` // Cleanup interval in minutes (0 = disabled)
}

// BackendOpenAIConfig holds OpenAI-specific backend settings
type BackendOpenAIConfig struct {
	ForcePromptCache bool `toml:"force_prompt_cache"` // Force prompt caching on all requests
}

// RequestSanitizationConfig holds settings for removing problematic incoming request parameters.
type RequestSanitizationConfig struct {
	MaxTokensPolicy string `toml:"max_tokens_policy"` // "preserve", "drop", or "drop_above"
	MaxTokensLimit  int    `toml:"max_tokens_limit"`  // Used when max_tokens_policy is "drop_above"
}

// ChatTextInjectionConfig holds the chat text injection settings
type ChatTextInjectionConfig struct {
	Enabled bool   `toml:"enabled"` // Enable text injection
	Text    string `toml:"text"`    // Text to inject
	Mode    string `toml:"mode"`    // "first", "last", or "system" - which message to inject into
}

// StreamOverrideConfig holds settings for forcing the streaming behavior of
// requests regardless of what the client asked for.
type StreamOverrideConfig struct {
	Mode string `toml:"mode"` // "passthrough", "always", or "never"
}

// Load reads and parses the configuration file
func Load(path string) (*Config, error) {
	var config Config

	metadata, err := toml.DecodeFile(path, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to read/parse config file: %w", err)
	}

	// Fail on unknown keys
	if len(metadata.Undecoded()) > 0 {
		return nil, fmt.Errorf("unknown keys in config file: %v", metadata.Undecoded())
	}

	// Validate backend type
	if config.Backend.Type != "openai" && config.Backend.Type != "ollama" {
		return nil, fmt.Errorf("invalid backend type: %s (must be 'openai' or 'ollama')", config.Backend.Type)
	}

	// Validate chat text injection mode
	if config.ChatTextInjection.Mode != "" && config.ChatTextInjection.Mode != "first" && config.ChatTextInjection.Mode != "last" && config.ChatTextInjection.Mode != "system" {
		return nil, fmt.Errorf("invalid chat_text_injection.mode: %s (must be 'first', 'last', or 'system')", config.ChatTextInjection.Mode)
	}
	if config.RequestSanitization.MaxTokensPolicy != "" &&
		config.RequestSanitization.MaxTokensPolicy != "preserve" &&
		config.RequestSanitization.MaxTokensPolicy != "drop" &&
		config.RequestSanitization.MaxTokensPolicy != "drop_above" {
		return nil, fmt.Errorf("invalid request_sanitization.max_tokens_policy: %s (must be 'preserve', 'drop', or 'drop_above')", config.RequestSanitization.MaxTokensPolicy)
	}
	if config.RequestSanitization.MaxTokensLimit < 0 {
		return nil, fmt.Errorf("invalid request_sanitization.max_tokens_limit: %d (must be 0 or greater)", config.RequestSanitization.MaxTokensLimit)
	}

	// Validate stream override mode
	if config.StreamOverride.Mode != "" &&
		config.StreamOverride.Mode != "passthrough" &&
		config.StreamOverride.Mode != "always" &&
		config.StreamOverride.Mode != "never" {
		return nil, fmt.Errorf("invalid stream_override.mode: %s (must be 'passthrough', 'always', or 'never')", config.StreamOverride.Mode)
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
	if config.RequestSanitization.MaxTokensPolicy == "" {
		config.RequestSanitization.MaxTokensPolicy = "preserve"
	}
	if config.StreamOverride.Mode == "" {
		config.StreamOverride.Mode = "passthrough"
	}

	return &config, nil
}
