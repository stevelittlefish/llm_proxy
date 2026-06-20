package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRequestSanitizationConfig(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "openai"
endpoint = "http://localhost:8008"

[request_sanitization]
max_tokens_policy = "drop_above"
max_tokens_limit = 8192
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.RequestSanitization.MaxTokensPolicy != "drop_above" {
		t.Fatalf("MaxTokensPolicy = %q, want drop_above", cfg.RequestSanitization.MaxTokensPolicy)
	}
	if cfg.RequestSanitization.MaxTokensLimit != 8192 {
		t.Fatalf("MaxTokensLimit = %d, want 8192", cfg.RequestSanitization.MaxTokensLimit)
	}
}

func TestLoadDefaultsRequestSanitizationPolicy(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "openai"
endpoint = "http://localhost:8008"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.RequestSanitization.MaxTokensPolicy != "preserve" {
		t.Fatalf("MaxTokensPolicy = %q, want preserve", cfg.RequestSanitization.MaxTokensPolicy)
	}
}

func TestLoadRejectsInvalidMaxTokensPolicy(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "openai"
endpoint = "http://localhost:8008"

[request_sanitization]
max_tokens_policy = "clamp"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "request_sanitization.max_tokens_policy") {
		t.Fatalf("Load() error = %v, want max_tokens_policy error", err)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "openai"
endpoint = "http://localhost:8008"
unexpected = true
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unknown keys") {
		t.Fatalf("Load() error = %v, want unknown keys error", err)
	}
}

func TestLoadRejectsInvalidChatTextInjectionMode(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "openai"
endpoint = "http://localhost:8008"

[chat_text_injection]
mode = "middle"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "chat_text_injection.mode") {
		t.Fatalf("Load() error = %v, want chat_text_injection.mode error", err)
	}
}

func TestLoadStreamOverrideConfig(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "openai"
endpoint = "http://localhost:8008"

[stream_override]
mode = "always"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.StreamOverride.Mode != "always" {
		t.Fatalf("StreamOverride.Mode = %q, want always", cfg.StreamOverride.Mode)
	}
}

func TestLoadDefaultsStreamOverrideMode(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "openai"
endpoint = "http://localhost:8008"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.StreamOverride.Mode != "passthrough" {
		t.Fatalf("StreamOverride.Mode = %q, want passthrough", cfg.StreamOverride.Mode)
	}
}

func TestLoadRejectsInvalidStreamOverrideMode(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "openai"
endpoint = "http://localhost:8008"

[stream_override]
mode = "sometimes"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "stream_override.mode") {
		t.Fatalf("Load() error = %v, want stream_override.mode error", err)
	}
}

func TestLoadAppliesOperationalDefaults(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "ollama"
endpoint = "http://localhost:11434"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Fatalf("Server.Host = %q, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Server.Port != 11434 {
		t.Fatalf("Server.Port = %d, want 11434", cfg.Server.Port)
	}
	if cfg.Backend.Timeout != 300 {
		t.Fatalf("Backend.Timeout = %d, want 300", cfg.Backend.Timeout)
	}
	if cfg.Database.Path != "./llm_proxy.db" {
		t.Fatalf("Database.Path = %q, want ./llm_proxy.db", cfg.Database.Path)
	}
	if cfg.Database.MaxRequests != 100 || cfg.Database.CleanupInterval != 5 {
		t.Fatalf("database cleanup defaults = (%d, %d), want (100, 5)", cfg.Database.MaxRequests, cfg.Database.CleanupInterval)
	}
	if cfg.ChatTextInjection.Mode != "last" {
		t.Fatalf("ChatTextInjection.Mode = %q, want last", cfg.ChatTextInjection.Mode)
	}
}

func TestLoadGemma4FixConfig(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "openai"
endpoint = "http://localhost:8008"

[gemma_4_fix]
enabled = true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Gemma4Fix.Enabled {
		t.Fatal("Gemma4Fix.Enabled = false, want true")
	}
}

func TestLoadDefaultsGemma4FixDisabled(t *testing.T) {
	path := writeTestConfig(t, `
[backend]
type = "openai"
endpoint = "http://localhost:8008"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Gemma4Fix.Enabled {
		t.Fatal("Gemma4Fix.Enabled = true, want false (default off)")
	}
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	return path
}
