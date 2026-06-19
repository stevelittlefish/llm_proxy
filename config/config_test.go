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

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	return path
}
