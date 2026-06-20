package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"llm_proxy/models"
)

func TestOpenAIBackendChatTranslatesRequestAndNonStreamingResponse(t *testing.T) {
	var gotReq models.OpenAIChatRequest
	b := NewOpenAIBackend("http://backend.test", 10, true, false)
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		return jsonResponse(`{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"length"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`), nil
	})

	respChan, meta, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "test-model",
		Messages: []models.Message{{Role: "user", Content: "ping"}},
		Options: map[string]interface{}{
			"temperature": float64(0.25),
			"num_predict": float64(128),
			"top_p":       float64(0.9),
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var responses []models.ChatResponse
	for resp := range respChan {
		responses = append(responses, resp)
	}
	if len(responses) != 1 {
		t.Fatalf("len(responses) = %d, want 1", len(responses))
	}
	got := responses[0]
	if got.Message.Content != "pong" || got.DoneReason != "length" {
		t.Fatalf("response = %#v, want content pong and finish length", got)
	}
	if got.PromptEvalCount != 3 || got.EvalCount != 5 {
		t.Fatalf("token counts = (%d, %d), want (3, 5)", got.PromptEvalCount, got.EvalCount)
	}

	if gotReq.Model != "test-model" || gotReq.Messages[0].Content != "ping" {
		t.Fatalf("translated request = %#v", gotReq)
	}
	if !gotReq.CachePrompt {
		t.Fatal("CachePrompt = false, want true")
	}
	if gotReq.MaxTokens != 128 || gotReq.Temperature != 0.25 || gotReq.TopP != 0.9 {
		t.Fatalf("translated options = max=%d temp=%v top_p=%v", gotReq.MaxTokens, gotReq.Temperature, gotReq.TopP)
	}
	if meta.URL != "http://backend.test/v1/chat/completions" {
		t.Fatalf("metadata URL = %q", meta.URL)
	}
	if !strings.Contains(meta.RawResponse, `"pong"`) {
		t.Fatalf("RawResponse = %q, want response body", meta.RawResponse)
	}
}

func TestOpenAIBackendChatPreservesRawOpenAIFields(t *testing.T) {
	var gotReq map[string]json.RawMessage
	b := NewOpenAIBackend("http://backend.test", 10, true, false)
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		return jsonResponse(`{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`), nil
	})

	raw := map[string]json.RawMessage{
		"model":               json.RawMessage(`"old-model"`),
		"messages":            json.RawMessage(`[{"role":"user","content":"old"}]`),
		"stream_options":      json.RawMessage(`{"include_usage":true}`),
		"response_format":     json.RawMessage(`{"type":"json_object"}`),
		"tool_choice":         json.RawMessage(`"auto"`),
		"parallel_tool_calls": json.RawMessage(`false`),
	}

	respChan, _, err := b.Chat(context.Background(), models.ChatRequest{
		Model:     "test-model",
		Messages:  []models.Message{{Role: "user", Content: "ping"}},
		Stream:    true,
		OpenAIRaw: raw,
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	for range respChan {
	}

	var gotModel string
	if err := json.Unmarshal(gotReq["model"], &gotModel); err != nil {
		t.Fatalf("model decode error: %v", err)
	}
	if gotModel != "test-model" {
		t.Fatalf("model = %q, want test-model", gotModel)
	}
	if string(gotReq["stream_options"]) != `{"include_usage":true}` {
		t.Fatalf("stream_options = %s, want include_usage passthrough", gotReq["stream_options"])
	}
	if string(gotReq["response_format"]) != `{"type":"json_object"}` {
		t.Fatalf("response_format = %s, want passthrough", gotReq["response_format"])
	}
	if string(gotReq["tool_choice"]) != `"auto"` {
		t.Fatalf("tool_choice = %s, want passthrough", gotReq["tool_choice"])
	}
	if string(gotReq["parallel_tool_calls"]) != `false` {
		t.Fatalf("parallel_tool_calls = %s, want passthrough", gotReq["parallel_tool_calls"])
	}
	if string(gotReq["cache_prompt"]) != `true` {
		t.Fatalf("cache_prompt = %s, want forced true", gotReq["cache_prompt"])
	}
}

func TestOpenAIBackendStreamingChatAccumulatesToolCalls(t *testing.T) {
	b := NewOpenAIBackend("http://backend.test", 10, false, false)
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"lookup","arguments":"{\"ci"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":\"London\"}"}}]}}]}`,
			`data: [DONE]`,
			"",
		}, "\n")
		return textResponse("text/event-stream", body), nil
	})

	respChan, meta, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "test-model",
		Stream:   true,
		Messages: []models.Message{{Role: "user", Content: "weather"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var responses []models.ChatResponse
	for resp := range respChan {
		responses = append(responses, resp)
	}
	if len(responses) != 2 {
		t.Fatalf("len(responses) = %d, want tool call and done responses: %#v", len(responses), responses)
	}
	if responses[0].Done {
		t.Fatal("tool call response is Done=true, want false")
	}
	toolCalls := responses[0].Message.ToolCalls
	if len(toolCalls) != 1 {
		t.Fatalf("len(toolCalls) = %d, want 1", len(toolCalls))
	}
	tc := toolCalls[0].(map[string]interface{})
	fn := tc["function"].(map[string]interface{})
	args := fn["arguments"].(map[string]interface{})
	if tc["id"] != "call-1" || fn["name"] != "lookup" || args["city"] != "London" {
		t.Fatalf("tool call = %#v, want accumulated lookup London", tc)
	}
	if !responses[1].Done || responses[1].DoneReason != "stop" {
		t.Fatalf("final response = %#v, want done stop", responses[1])
	}
	if !strings.Contains(meta.RawResponse, "[DONE]") {
		t.Fatalf("RawResponse = %q, want captured SSE stream", meta.RawResponse)
	}
}

func TestOpenAIBackendStreamingChatCapturesUsageAfterFinish(t *testing.T) {
	b := NewOpenAIBackend("http://backend.test", 10, false, false)
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := strings.Join([]string{
			`data: {"choices":[{"delta":{"content":"pong"},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18}}`,
			`data: [DONE]`,
			"",
		}, "\n")
		return textResponse("text/event-stream", body), nil
	})

	respChan, _, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "test-model",
		Stream:   true,
		Messages: []models.Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var responses []models.ChatResponse
	for resp := range respChan {
		responses = append(responses, resp)
	}
	if len(responses) != 2 {
		t.Fatalf("len(responses) = %d, want content and done responses: %#v", len(responses), responses)
	}
	final := responses[1]
	if !final.Done {
		t.Fatalf("final response Done = false: %#v", final)
	}
	if final.PromptEvalCount != 7 || final.EvalCount != 11 {
		t.Fatalf("token counts = (%d, %d), want (7, 11)", final.PromptEvalCount, final.EvalCount)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %#v, want total_tokens 18", final.Usage)
	}
}

func TestOllamaBackendChatHandlesLargeStreamingLine(t *testing.T) {
	largeContent := strings.Repeat("x", 70*1024)
	b := NewOllamaBackend("http://backend.test", 10)
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data, err := json.Marshal(models.ChatResponse{
			Model: "test-model",
			Message: models.Message{
				Role:    "assistant",
				Content: largeContent,
			},
		})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		body := fmt.Sprintf("%s\n%s\n", string(data), `{"model":"test-model","done":true}`)
		return textResponse("application/x-ndjson", body), nil
	})

	respChan, meta, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "test-model",
		Messages: []models.Message{{Role: "user", Content: "large"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var responses []models.ChatResponse
	for resp := range respChan {
		responses = append(responses, resp)
	}
	if len(responses) != 2 {
		t.Fatalf("len(responses) = %d, want content and done responses", len(responses))
	}
	if responses[0].Message.Content != largeContent {
		t.Fatalf("content length = %d, want %d", len(responses[0].Message.Content), len(largeContent))
	}
	if !responses[1].Done {
		t.Fatalf("final response = %#v, want done", responses[1])
	}
	if !strings.Contains(meta.RawResponse, largeContent) {
		t.Fatal("RawResponse does not contain large streamed payload")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(body string) *http.Response {
	return textResponse("application/json", body)
}

func textResponse(contentType string, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
