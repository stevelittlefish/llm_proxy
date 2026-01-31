package models

import "time"

// Ollama API types

// GenerateRequest represents an Ollama generate request
type GenerateRequest struct {
	Model    string                 `json:"model"`
	Prompt   string                 `json:"prompt"`
	Stream   bool                   `json:"stream,omitempty"`
	Options  map[string]interface{} `json:"options,omitempty"`
	Context  []int                  `json:"context,omitempty"`
	Format   string                 `json:"format,omitempty"`
	System   string                 `json:"system,omitempty"`
	Template string                 `json:"template,omitempty"`
	Raw      bool                   `json:"raw,omitempty"`
}

// GenerateResponse represents an Ollama generate response
type GenerateResponse struct {
	Model              string    `json:"model"`
	CreatedAt          time.Time `json:"created_at"`
	Response           string    `json:"response"`
	Done               bool      `json:"done"`
	DoneReason         string    `json:"done_reason,omitempty"`
	Context            []int     `json:"context,omitempty"`
	TotalDuration      int64     `json:"total_duration,omitempty"`
	LoadDuration       int64     `json:"load_duration,omitempty"`
	PromptEvalCount    int       `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64     `json:"prompt_eval_duration,omitempty"`
	EvalCount          int       `json:"eval_count,omitempty"`
	EvalDuration       int64     `json:"eval_duration,omitempty"`
}

// ChatRequest represents an Ollama chat request
type ChatRequest struct {
	Model    string                 `json:"model"`
	Messages []Message              `json:"messages"`
	Stream   bool                   `json:"stream,omitempty"`
	Options  map[string]interface{} `json:"options,omitempty"`
	Format   string                 `json:"format,omitempty"`
	Template string                 `json:"template,omitempty"`
	Tools    []interface{}          `json:"tools,omitempty"`
}

// Message represents a chat message
type Message struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []interface{} `json:"tool_calls,omitempty"`
}

// ChatResponse represents an Ollama chat response
type ChatResponse struct {
	Model              string    `json:"model"`
	CreatedAt          time.Time `json:"created_at"`
	Message            Message   `json:"message"`
	Done               bool      `json:"done"`
	DoneReason         string    `json:"done_reason,omitempty"`
	TotalDuration      int64     `json:"total_duration,omitempty"`
	LoadDuration       int64     `json:"load_duration,omitempty"`
	PromptEvalCount    int       `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64     `json:"prompt_eval_duration,omitempty"`
	EvalCount          int       `json:"eval_count,omitempty"`
	EvalDuration       int64     `json:"eval_duration,omitempty"`
}

// ModelsResponse represents the response for listing models
type ModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ModelInfo represents information about a model
type ModelInfo struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"` // Duplicate of Name for compatibility
	ModifiedAt time.Time    `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    ModelDetails `json:"details,omitempty"`
}

// ModelDetails contains detailed model information
type ModelDetails struct {
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

// OpenAI API types

// OpenAICompletionRequest represents an OpenAI completion request
type OpenAICompletionRequest struct {
	Model            string      `json:"model"`
	Prompt           interface{} `json:"prompt"` // can be string or array
	Stream           bool        `json:"stream,omitempty"`
	MaxTokens        int         `json:"max_tokens,omitempty"`
	Temperature      float64     `json:"temperature,omitempty"`
	TopP             float64     `json:"top_p,omitempty"`
	Stop             interface{} `json:"stop,omitempty"`
	FrequencyPenalty float64     `json:"frequency_penalty,omitempty"`
	PresencePenalty  float64     `json:"presence_penalty,omitempty"`
}

// OpenAIChatRequest represents an OpenAI chat request
type OpenAIChatRequest struct {
	Model            string        `json:"model"`
	Messages         []Message     `json:"messages"`
	Stream           bool          `json:"stream,omitempty"`
	MaxTokens        int           `json:"max_tokens,omitempty"`
	Temperature      float64       `json:"temperature,omitempty"`
	TopP             float64       `json:"top_p,omitempty"`
	Stop             interface{}   `json:"stop,omitempty"`
	FrequencyPenalty float64       `json:"frequency_penalty,omitempty"`
	PresencePenalty  float64       `json:"presence_penalty,omitempty"`
	Tools            []interface{} `json:"tools,omitempty"`
}

// OpenAICompletionResponse represents an OpenAI completion response
type OpenAICompletionResponse struct {
	ID      string                   `json:"id"`
	Object  string                   `json:"object"`
	Created int64                    `json:"created"`
	Model   string                   `json:"model"`
	Choices []OpenAICompletionChoice `json:"choices"`
	Usage   OpenAIUsage              `json:"usage,omitempty"`
}

// OpenAICompletionChoice represents a completion choice
type OpenAICompletionChoice struct {
	Text         string `json:"text"`
	Index        int    `json:"index"`
	FinishReason string `json:"finish_reason,omitempty"`
}

// OpenAIChatResponse represents an OpenAI chat response
type OpenAIChatResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []OpenAIChatChoice `json:"choices"`
	Usage   OpenAIUsage        `json:"usage,omitempty"`
}

// OpenAIChatChoice represents a chat choice
type OpenAIChatChoice struct {
	Delta        *Message `json:"delta,omitempty"`
	Message      *Message `json:"message,omitempty"`
	Index        int      `json:"index"`
	FinishReason string   `json:"finish_reason,omitempty"`
}

// OpenAIUsage represents token usage information
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error"`
}
