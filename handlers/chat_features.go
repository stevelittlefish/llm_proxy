package handlers

import (
	"log"
	"strings"

	"llm_proxy/config"
	"llm_proxy/models"
)

func applyChatFeatures(req *models.ChatRequest, cfg *config.Config) {
	if cfg.ChatTextInjection.Enabled && cfg.ChatTextInjection.Text != "" {
		applyChatTextInjection(req, cfg)
	}
	if len(cfg.Backend.ToolBlacklist) > 0 {
		filterChatTools(req, cfg)
	}
}

func filterChatTools(req *models.ChatRequest, cfg *config.Config) {
	if len(req.Tools) == 0 {
		return
	}

	blacklist := make(map[string]bool)
	for _, toolName := range cfg.Backend.ToolBlacklist {
		blacklist[toolName] = true
	}

	var filteredTools []interface{}
	for _, tool := range req.Tools {
		toolMap, ok := tool.(map[string]interface{})
		if !ok {
			filteredTools = append(filteredTools, tool)
			continue
		}

		var toolName string
		if funcField, ok := toolMap["function"].(map[string]interface{}); ok {
			if name, ok := funcField["name"].(string); ok {
				toolName = name
			}
		}

		if toolName == "" || !blacklist[toolName] {
			filteredTools = append(filteredTools, tool)
			continue
		}

		if cfg.Server.Verbose {
			log.Printf("[VERBOSE] Filtering out blacklisted tool: %s", toolName)
		}
	}

	req.Tools = filteredTools
}

func applyChatTextInjection(req *models.ChatRequest, cfg *config.Config) {
	injectionText := cfg.ChatTextInjection.Text
	mode := cfg.ChatTextInjection.Mode

	if mode == "system" {
		systemIndex := -1
		for i, msg := range req.Messages {
			if msg.Role == "system" {
				systemIndex = i
				break
			}
		}

		if systemIndex != -1 {
			if strings.Contains(req.Messages[systemIndex].Content, injectionText) {
				return
			}
			if cfg.Server.Verbose {
				log.Printf("[VERBOSE] Injecting text into existing system message: %q", injectionText)
			}
			req.Messages[systemIndex].SetContent(req.Messages[systemIndex].Content + " " + injectionText)
			return
		}

		if cfg.Server.Verbose {
			log.Printf("[VERBOSE] Creating new system message with injected text: %q", injectionText)
		}
		systemMsg := models.Message{
			Role:    "system",
			Content: injectionText,
		}
		req.Messages = append([]models.Message{systemMsg}, req.Messages...)
		return
	}

	targetIndex := -1
	if mode == "first" {
		for i, msg := range req.Messages {
			if msg.Role == "user" {
				targetIndex = i
				break
			}
		}
	} else {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				targetIndex = i
				break
			}
		}
	}

	if targetIndex == -1 {
		return
	}
	if strings.Contains(req.Messages[targetIndex].Content, injectionText) {
		return
	}

	if cfg.Server.Verbose {
		log.Printf("[VERBOSE] Injecting text into %s user message (index %d): %q", mode, targetIndex, injectionText)
	}
	req.Messages[targetIndex].SetContent(req.Messages[targetIndex].Content + " " + injectionText)
}

func cloneMessages(messages []models.Message) []models.Message {
	cloned := make([]models.Message, len(messages))
	copy(cloned, messages)
	return cloned
}

func lastMessageContent(messages []models.Message) string {
	if len(messages) == 0 {
		return "unknown"
	}
	return messages[len(messages)-1].Content
}
