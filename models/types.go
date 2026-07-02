package models

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Ollama API types

// GenerateRequest represents an Ollama generate request
type GenerateRequest struct {
	Model     string                 `json:"model"`
	Prompt    string                 `json:"prompt"`
	Stream    bool                   `json:"stream,omitempty"`
	Options   map[string]interface{} `json:"options,omitempty"`
	Context   []int                  `json:"context,omitempty"`
	Format    string                 `json:"format,omitempty"`
	System    string                 `json:"system,omitempty"`
	Template  string                 `json:"template,omitempty"`
	Raw       bool                   `json:"raw,omitempty"`
	KeepAlive string                 `json:"keep_alive,omitempty"`
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
	Model     string                     `json:"model"`
	Messages  []Message                  `json:"messages"`
	Stream    bool                       `json:"stream,omitempty"`
	Options   map[string]interface{}     `json:"options,omitempty"`
	Format    string                     `json:"format,omitempty"`
	Template  string                     `json:"template,omitempty"`
	Tools     []interface{}              `json:"tools,omitempty"`
	KeepAlive string                     `json:"keep_alive,omitempty"`
	OpenAIRaw map[string]json.RawMessage `json:"-"`
}

// Message represents a chat message
type Message struct {
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	Thinking   string        `json:"thinking,omitempty"`
	ToolCalls  []interface{} `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`

	// RawContent preserves the original JSON value for OpenAI multimodal
	// content arrays. Content remains the flattened text view used by Ollama
	// compatibility, logging, and text-only features; MarshalJSON emits
	// RawContent when present so OpenAI-compatible backends receive images.
	RawContent json.RawMessage `json:"-"`
}

// SetContent replaces the text content and clears any preserved raw OpenAI
// content value, because the raw value no longer represents this message.
func (m *Message) SetContent(content string) {
	m.Content = content
	m.RawContent = nil
}

// UnmarshalJSON accepts content as either a plain string or an OpenAI-style
// content-parts array (e.g. [{"type":"text","text":"..."}]). For arrays,
// Content is a flattened text view while RawContent preserves the original
// value for lossless forwarding to OpenAI-compatible backends.
func (m *Message) UnmarshalJSON(data []byte) error {
	type messageAlias Message
	aux := struct {
		Content json.RawMessage `json:"content"`
		*messageAlias
	}{
		messageAlias: (*messageAlias)(m),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	m.RawContent = nil
	if len(aux.Content) == 0 || string(aux.Content) == "null" {
		m.Content = ""
		return nil
	}

	var asString string
	if err := json.Unmarshal(aux.Content, &asString); err == nil {
		m.Content = asString
		return nil
	}

	m.RawContent = append(m.RawContent[:0], aux.Content...)

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(aux.Content, &parts); err != nil {
		return fmt.Errorf("content: %w", err)
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type != "" && p.Type != "text" {
			continue
		}
		sb.WriteString(p.Text)
	}
	m.Content = sb.String()
	return nil
}

// MarshalJSON emits preserved raw content arrays when present, otherwise the
// normal string content. This keeps OpenAI multimodal messages lossless without
// changing the string-oriented internal API.
func (m Message) MarshalJSON() ([]byte, error) {
	content := json.RawMessage(nil)
	if len(m.RawContent) > 0 && json.Valid(m.RawContent) {
		content = m.RawContent
	} else {
		data, err := json.Marshal(m.Content)
		if err != nil {
			return nil, err
		}
		content = data
	}

	aux := struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		Thinking   string          `json:"thinking,omitempty"`
		ToolCalls  []interface{}   `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	}{
		Role:       m.Role,
		Content:    content,
		Thinking:   m.Thinking,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
	}
	return json.Marshal(aux)
}

// ChatResponse represents an Ollama chat response
type ChatResponse struct {
	Model              string       `json:"model"`
	CreatedAt          time.Time    `json:"created_at"`
	Message            Message      `json:"message"`
	Done               bool         `json:"done"`
	DoneReason         string       `json:"done_reason,omitempty"`
	TotalDuration      int64        `json:"total_duration,omitempty"`
	LoadDuration       int64        `json:"load_duration,omitempty"`
	PromptEvalCount    int          `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64        `json:"prompt_eval_duration,omitempty"`
	EvalCount          int          `json:"eval_count,omitempty"`
	EvalDuration       int64        `json:"eval_duration,omitempty"`
	Usage              *OpenAIUsage `json:"-"`
}

// ModelsResponse represents the response for listing models
type ModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ModelInfo represents information about a model
type ModelInfo struct {
	Name          string           `json:"name"`
	Model         string           `json:"model"` // Duplicate of Name for compatibility
	ModifiedAt    time.Time        `json:"modified_at"`
	Size          int64            `json:"size"`
	Digest        string           `json:"digest"`
	Details       ModelDetails     `json:"details,omitempty"`
	Capabilities  []string         `json:"capabilities,omitempty"`
	ContextLength int              `json:"-"`
	OpenAI        *OpenAIModelInfo `json:"-"`
}

// ModelDetails contains detailed model information
type ModelDetails struct {
	ParentModel       string   `json:"parent_model,omitempty"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
	ContextLength     int      `json:"context_length,omitempty"`
	EmbeddingLength   int      `json:"embedding_length,omitempty"`
}

// OpenAIModelInfo contains OpenAI/vLLM model-list metadata that should survive
// translation through the backend layer.
type OpenAIModelInfo struct {
	ID            string                 `json:"id"`
	Object        string                 `json:"object,omitempty"`
	Created       int64                  `json:"created,omitempty"`
	OwnedBy       string                 `json:"owned_by,omitempty"`
	MaxModelLen   int                    `json:"max_model_len,omitempty"`
	ContextLength int                    `json:"context_length,omitempty"`
	TopProvider   map[string]interface{} `json:"top_provider,omitempty"`
	Root          string                 `json:"root,omitempty"`
	Parent        interface{}            `json:"parent,omitempty"`
}

// ShowResponse represents an Ollama-compatible /api/show response. Extra holds
// upstream fields the proxy does not model so they can still be passed through.
type ShowResponse struct {
	Modelfile    string                 `json:"modelfile,omitempty"`
	Parameters   string                 `json:"parameters,omitempty"`
	Template     string                 `json:"template,omitempty"`
	Details      ModelDetails           `json:"details,omitempty"`
	ModelInfo    map[string]interface{} `json:"model_info,omitempty"`
	Tensors      []interface{}          `json:"tensors,omitempty"`
	Capabilities []string               `json:"capabilities,omitempty"`
	ModifiedAt   time.Time              `json:"modified_at,omitempty"`
	Extra        map[string]interface{} `json:"-"`
}

// MarshalJSON merges pass-through fields from Extra with the modeled fields.
func (s ShowResponse) MarshalJSON() ([]byte, error) {
	data := make(map[string]interface{}, len(s.Extra)+8)
	for k, v := range s.Extra {
		data[k] = v
	}

	if s.Modelfile != "" {
		data["modelfile"] = s.Modelfile
	}
	if s.Parameters != "" {
		data["parameters"] = s.Parameters
	}
	if s.Template != "" {
		data["template"] = s.Template
	}
	if !modelDetailsEmpty(s.Details) {
		data["details"] = s.Details
	}
	if len(s.ModelInfo) > 0 {
		data["model_info"] = s.ModelInfo
	}
	if len(s.Tensors) > 0 {
		data["tensors"] = s.Tensors
	}
	if len(s.Capabilities) > 0 {
		data["capabilities"] = s.Capabilities
	}
	if !s.ModifiedAt.IsZero() {
		data["modified_at"] = s.ModifiedAt
	}
	return json.Marshal(data)
}

// UnmarshalJSON captures modeled fields and keeps any additional upstream keys.
func (s *ShowResponse) UnmarshalJSON(data []byte) error {
	type showAlias ShowResponse
	var alias showAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for _, k := range []string{"modelfile", "parameters", "template", "details", "model_info", "tensors", "capabilities", "modified_at"} {
		delete(raw, k)
	}

	*s = ShowResponse(alias)
	s.Extra = raw
	return nil
}

func modelDetailsEmpty(d ModelDetails) bool {
	return d.ParentModel == "" &&
		d.Format == "" &&
		d.Family == "" &&
		len(d.Families) == 0 &&
		d.ParameterSize == "" &&
		d.QuantizationLevel == "" &&
		d.ContextLength == 0 &&
		d.EmbeddingLength == 0
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
	CachePrompt      bool        `json:"cache_prompt,omitempty"`
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
	CachePrompt      bool          `json:"cache_prompt,omitempty"`
}

// OpenAICompletionResponse represents an OpenAI completion response
type OpenAICompletionResponse struct {
	ID      string                   `json:"id"`
	Object  string                   `json:"object"`
	Created int64                    `json:"created"`
	Model   string                   `json:"model"`
	Choices []OpenAICompletionChoice `json:"choices"`
	Usage   *OpenAIUsage             `json:"usage,omitempty"`
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
	Usage   *OpenAIUsage       `json:"usage,omitempty"`
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
