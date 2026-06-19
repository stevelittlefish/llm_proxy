package handlers

import (
	"log"

	"llm_proxy/config"
	"llm_proxy/models"
)

const (
	maxTokensPolicyPreserve  = "preserve"
	maxTokensPolicyDrop      = "drop"
	maxTokensPolicyDropAbove = "drop_above"
)

func applyOpenAIChatRequestSanitization(req *models.OpenAIChatRequest, cfg *config.Config) {
	if shouldDropMaxTokens(req.MaxTokens, cfg) {
		if cfg.Server.Verbose {
			log.Printf("Dropping max_tokens: %d", req.MaxTokens)
		}
		req.MaxTokens = 0
	}
}

func applyChatRequestSanitization(req *models.ChatRequest, cfg *config.Config) {
	sanitizeOptionsMaxTokens(req.Options, cfg)
	if len(req.Options) == 0 {
		req.Options = nil
	}
}

func applyGenerateRequestSanitization(req *models.GenerateRequest, cfg *config.Config) {
	sanitizeOptionsMaxTokens(req.Options, cfg)
	if len(req.Options) == 0 {
		req.Options = nil
	}
}

func sanitizeOptionsMaxTokens(options map[string]interface{}, cfg *config.Config) {
	if options == nil {
		return
	}

	value, ok := options["num_predict"]
	if !ok {
		return
	}

	maxTokens, ok := numericOptionValue(value)
	if !ok {
		return
	}

	if shouldDropMaxTokens(maxTokens, cfg) {
		if cfg.Server.Verbose {
			log.Printf("Dropping options.num_predict: %d", maxTokens)
		}
		delete(options, "num_predict")
	}
}

func shouldDropMaxTokens(maxTokens int, cfg *config.Config) bool {
	if maxTokens <= 0 {
		return false
	}

	switch cfg.RequestSanitization.MaxTokensPolicy {
	case maxTokensPolicyDrop:
		return true
	case maxTokensPolicyDropAbove:
		return maxTokens > cfg.RequestSanitization.MaxTokensLimit
	default:
		return false
	}
}

func numericOptionValue(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	default:
		return 0, false
	}
}
