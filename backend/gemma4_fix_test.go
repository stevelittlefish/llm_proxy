package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"llm_proxy/models"
)

func TestGemma4ContentFilterStripsReasoningChannelAcrossChunks(t *testing.T) {
	f := &gemma4ContentFilter{}
	var got strings.Builder
	got.WriteString(f.Feed("Before. <|chan"))
	got.WriteString(f.Feed("nel>thought\nhidden\n<channel|> After."))
	got.WriteString(f.Flush())

	if f.Suspect() {
		t.Fatal("Suspect() = true, want false (no tool-call marker present)")
	}
	result := got.String()
	if strings.Contains(result, "hidden") || strings.Contains(result, "<|channel>") || strings.Contains(result, "<channel|>") {
		t.Fatalf("result = %q, want channel markers and wrapped content stripped", result)
	}
	if stripped, content := f.ChannelLeakStripped(); !stripped || !strings.Contains(content, "hidden") {
		t.Fatalf("ChannelLeakStripped() = (%v, %q), want stripped=true and wrapped content containing %q", stripped, content, "hidden")
	}
	if !strings.Contains(result, "Before.") || !strings.Contains(result, "After.") {
		t.Fatalf("result = %q, want legitimate prose preserved", result)
	}
}

func TestGemma4ContentFilterDetectsToolCallMarkerAcrossChunks(t *testing.T) {
	f := &gemma4ContentFilter{}
	var got strings.Builder
	got.WriteString(f.Feed("Sure, reading now. <|tool_c"))
	got.WriteString(f.Feed(`all>call:read{path:<|"|>x.html<|"|>}<tool_call|>`))
	got.WriteString(f.Flush())

	if !f.Suspect() {
		t.Fatal("Suspect() = false, want true (tool-call marker present)")
	}
	result := got.String()
	if strings.Contains(result, "tool_call") {
		t.Fatalf("result = %q, want no leaked control tokens", result)
	}
	if !strings.Contains(result, "Sure, reading now.") {
		t.Fatalf("result = %q, want legitimate prefix preserved", result)
	}
	if !strings.Contains(f.Trapped(), "<|tool_call>") {
		t.Fatalf("Trapped() = %q, want suppressed marker text captured for logging", f.Trapped())
	}
}

// TestOpenAIBackendGemma4FixDisabledPassesThroughUnchanged guards against any
// behavior change for users who haven't opted into gemma_4_fix: a corrupted
// stream must pass straight through exactly as it does today, with no
// buffering or retry logic engaged at all.
func TestOpenAIBackendGemma4FixDisabledPassesThroughUnchanged(t *testing.T) {
	requestCount := 0
	b := NewOpenAIBackend("http://backend.test", 10, false, false)
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestCount++
		sse := strings.Join([]string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"<|tool_call>call:read{path:<|\"|>x.html<|\"|>}<tool_call|>"},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
			"",
		}, "\n")
		return textResponse("text/event-stream", sse), nil
	})

	respChan, _, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "gemma-4",
		Stream:   true,
		Messages: []models.Message{{Role: "user", Content: "please read x.html"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var fullContent strings.Builder
	for resp := range respChan {
		fullContent.WriteString(resp.Message.Content)
	}

	if requestCount != 1 {
		t.Fatalf("request count = %d, want 1 (flag off: no retry logic engaged)", requestCount)
	}
	if !strings.Contains(fullContent.String(), "<|tool_call>") {
		t.Fatalf("content = %q, want unmitigated leaked markers when gemma_4_fix is disabled", fullContent.String())
	}
}

// TestOpenAIBackendGemma4FixStripsReasoningChannelLeak covers the addendum
// bug end-to-end through Chat(): a leaked <|channel>thought...<channel|>
// wrapper, split across SSE chunks, must never reach the client and must not
// trigger any retry (this leak is pure filtering, unlike the tool-call leak).
func TestOpenAIBackendGemma4FixStripsReasoningChannelLeak(t *testing.T) {
	requestCount := 0
	b := NewOpenAIBackend("http://backend.test", 10, false, true)
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestCount++
		sse := strings.Join([]string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"Before. <|chan"},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"content":"nel>thought\nhidden\n<channel|> After."},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
			"",
		}, "\n")
		return textResponse("text/event-stream", sse), nil
	})

	respChan, _, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "gemma-4",
		Stream:   true,
		Messages: []models.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var fullContent strings.Builder
	for resp := range respChan {
		fullContent.WriteString(resp.Message.Content)
	}

	if requestCount != 1 {
		t.Fatalf("request count = %d, want 1 (reasoning-channel leak never triggers a retry)", requestCount)
	}
	result := fullContent.String()
	if strings.Contains(result, "hidden") || strings.Contains(result, "<|channel>") || strings.Contains(result, "<channel|>") {
		t.Fatalf("content = %q, want reasoning-channel wrapper and its content stripped", result)
	}
	if !strings.Contains(result, "Before.") || !strings.Contains(result, "After.") {
		t.Fatalf("content = %q, want legitimate prose preserved", result)
	}
}

// TestOpenAIBackendGemma4FixRecoversCleanFailure covers the "clean failure"
// shape from the spec: the entire turn is corrupted with nothing legitimate
// forwarded yet, so the proxy should silently retry the byte-identical
// request and the client should see only the clean recovered output.
func TestOpenAIBackendGemma4FixRecoversCleanFailure(t *testing.T) {
	b := NewOpenAIBackend("http://backend.test", 10, false, true)
	var requestBodies []string
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, string(body))

		if len(requestBodies) == 1 {
			sse := strings.Join([]string{
				`data: {"choices":[{"delta":{"role":"assistant","content":"<|tool_call>call:read{path:<|\"|>x.html<|\"|>}<tool_call|>"},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
				"",
			}, "\n")
			return textResponse("text/event-stream", sse), nil
		}

		sse := strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"read","arguments":"{\"path\":\"x.html\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
			"",
		}, "\n")
		return textResponse("text/event-stream", sse), nil
	})

	respChan, _, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "gemma-4",
		Stream:   true,
		Messages: []models.Message{{Role: "user", Content: "please read x.html"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var responses []models.ChatResponse
	for resp := range respChan {
		responses = append(responses, resp)
	}

	if len(requestBodies) != 2 {
		t.Fatalf("request count = %d, want 2 (original + 1 retry)", len(requestBodies))
	}
	if requestBodies[0] != requestBodies[1] {
		t.Fatalf("retry body differs from original:\n%s\nvs\n%s", requestBodies[0], requestBodies[1])
	}

	for _, resp := range responses {
		if strings.Contains(resp.Message.Content, "tool_call") {
			t.Fatalf("leaked control tokens reached client: %#v", resp)
		}
	}

	var sawToolCall bool
	for _, resp := range responses {
		if len(resp.Message.ToolCalls) > 0 {
			sawToolCall = true
			tc := resp.Message.ToolCalls[0].(map[string]interface{})
			fn := tc["function"].(map[string]interface{})
			if fn["name"] != "read" {
				t.Fatalf("tool call = %#v, want name read", tc)
			}
		}
	}
	if !sawToolCall {
		t.Fatalf("no tool_calls in responses: %#v", responses)
	}
	if last := responses[len(responses)-1]; !last.Done || last.DoneReason != "tool_calls" {
		t.Fatalf("final response = %#v, want done tool_calls", last)
	}
}

// gemma4RealWorldCleanFailurePayload is a verbatim capture of an actual
// corrupted vLLM stream for a Gemma 4 `bash` tool call: the model streams
// the leaked tool-call syntax token-by-token in many tiny deltas (including
// the open/close sentinels and both string delimiters as their own
// standalone chunks), finishes with a plain "stop" (not "tool_calls"), and
// is followed by a choices-less usage-only chunk before [DONE] - exercising
// real vLLM SSE shapes the synthetic tests above don't.
const gemma4RealWorldCleanFailurePayload = `data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}],"prompt_token_ids":null,"prompt_text":null}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"<|tool_call>"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"call"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":":"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"bash"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"{"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"command"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":":"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"<|\"|>"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"find"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" ."},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" -"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"type"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" d"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" -"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"not"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" -"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"path"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" '"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"*/"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":".*"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"'"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" -"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"not"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" -"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"path"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" '.'"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" |"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" sort"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" -"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"r"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"<|\"|>"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"}"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"<tool_call|>"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":""},"logprobs":null,"finish_reason":"stop","stop_reason":50,"token_ids":null}]}

data: {"id":"chatcmpl-84fcf3946abda724","object":"chat.completion.chunk","created":1781964066,"model":"gemma4-31b","choices":[],"usage":{"prompt_tokens":5268,"total_tokens":5302,"completion_tokens":34},"system_fingerprint":"vllm-0.23.0-tp2-39ad13f9"}

data: [DONE]
`

// TestOpenAIBackendGemma4FixDetectsRealWorldCleanFailurePayload replays an
// actual corrupted vLLM stream captured in production (see
// gemma4RealWorldCleanFailurePayload) to confirm detection isn't an artifact
// of the synthetic, larger-chunk fixtures used elsewhere in this file: the
// real bug streams the leaked syntax one token at a time, and the markers
// themselves still always arrive as complete, undivided chunks. Nothing
// legitimate precedes the corruption, so recovery should silently retry and
// the client should see only the clean, recovered tool call.
func TestOpenAIBackendGemma4FixDetectsRealWorldCleanFailurePayload(t *testing.T) {
	b := NewOpenAIBackend("http://backend.test", 10, false, true)
	var requestBodies []string
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, string(body))

		if len(requestBodies) == 1 {
			return textResponse("text/event-stream", gemma4RealWorldCleanFailurePayload), nil
		}

		sse := strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"bash","arguments":"{\"command\":\"find . -type d -not -path '*/.*' -not -path '.' | sort -r\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
			"",
		}, "\n")
		return textResponse("text/event-stream", sse), nil
	})

	respChan, _, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "gemma4-31b",
		Stream:   true,
		Messages: []models.Message{{Role: "user", Content: "list directories, deepest first"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var responses []models.ChatResponse
	var fullContent strings.Builder
	for resp := range respChan {
		fullContent.WriteString(resp.Message.Content)
		responses = append(responses, resp)
	}

	if len(requestBodies) != 2 {
		t.Fatalf("request count = %d, want 2 (original + 1 retry)", len(requestBodies))
	}
	if requestBodies[0] != requestBodies[1] {
		t.Fatalf("retry body differs from original:\n%s\nvs\n%s", requestBodies[0], requestBodies[1])
	}

	if fullContent.Len() != 0 {
		t.Fatalf("content = %q, want empty (nothing legitimate preceded the corruption)", fullContent.String())
	}
	for _, resp := range responses {
		if strings.Contains(resp.Message.Content, "tool_call") || strings.Contains(resp.Message.Content, `<|"|>`) {
			t.Fatalf("leaked control tokens reached client: %#v", resp)
		}
	}

	var sawToolCall bool
	for _, resp := range responses {
		if len(resp.Message.ToolCalls) > 0 {
			sawToolCall = true
			tc := resp.Message.ToolCalls[0].(map[string]interface{})
			fn := tc["function"].(map[string]interface{})
			if fn["name"] != "bash" {
				t.Fatalf("tool call = %#v, want name bash", tc)
			}
			args := fn["arguments"].(map[string]interface{})
			if args["command"] != "find . -type d -not -path '*/.*' -not -path '.' | sort -r" {
				t.Fatalf("tool call args = %#v, want recovered find command", args)
			}
		}
	}
	if !sawToolCall {
		t.Fatalf("no tool_calls in responses: %#v", responses)
	}
	if last := responses[len(responses)-1]; !last.Done || last.DoneReason != "tool_calls" {
		t.Fatalf("final response = %#v, want done tool_calls", last)
	}
}

// TestOpenAIBackendGemma4FixNudgesAfterTrailingFailure covers the "trailing
// failure" shape: legitimate prose streams first, then a corrupted tail.
// Real content has already reached the client, so recovery must suppress
// only the corrupted tail and send an internal nudge rather than retrying
// verbatim (which would risk duplicated/re-explained prose).
func TestOpenAIBackendGemma4FixNudgesAfterTrailingFailure(t *testing.T) {
	b := NewOpenAIBackend("http://backend.test", 10, false, true)
	var requestBodies []models.OpenAIChatRequest
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var req models.OpenAIChatRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		requestBodies = append(requestBodies, req)

		if len(requestBodies) == 1 {
			sse := strings.Join([]string{
				`data: {"choices":[{"delta":{"role":"assistant","content":"I'll read the file now. "},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{"content":"<|tool_call>call:read{path:<|\"|>x.html<|\"|>}<tool_call|>"},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
				"",
			}, "\n")
			return textResponse("text/event-stream", sse), nil
		}

		sse := strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"read","arguments":"{\"path\":\"x.html\"}"}}]}}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
			"",
		}, "\n")
		return textResponse("text/event-stream", sse), nil
	})

	respChan, _, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "gemma-4",
		Stream:   true,
		Messages: []models.Message{{Role: "user", Content: "please read x.html"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var responses []models.ChatResponse
	var fullContent strings.Builder
	for resp := range respChan {
		fullContent.WriteString(resp.Message.Content)
		responses = append(responses, resp)
	}

	if len(requestBodies) != 2 {
		t.Fatalf("request count = %d, want 2 (original + 1 nudge)", len(requestBodies))
	}
	if strings.Contains(fullContent.String(), "tool_call") {
		t.Fatalf("content = %q, leaked control tokens reached client", fullContent.String())
	}
	if !strings.Contains(fullContent.String(), "I'll read the file now.") {
		t.Fatalf("content = %q, want legitimate prose preserved", fullContent.String())
	}

	nudgeMessages := requestBodies[1].Messages
	if len(nudgeMessages) != 3 {
		t.Fatalf("nudge request messages = %#v, want original user message + assistant prose + user nudge", nudgeMessages)
	}
	assistantMsg, nudgeMsg := nudgeMessages[1], nudgeMessages[2]
	if assistantMsg.Role != "assistant" || !strings.Contains(assistantMsg.Content, "I'll read the file now.") {
		t.Fatalf("second nudge message = %#v, want assistant prose-so-far", assistantMsg)
	}
	if nudgeMsg.Role != "user" || nudgeMsg.Content == "" {
		t.Fatalf("third nudge message = %#v, want non-empty internal user nudge", nudgeMsg)
	}

	var sawToolCall bool
	for _, resp := range responses {
		if len(resp.Message.ToolCalls) > 0 {
			sawToolCall = true
		}
	}
	if !sawToolCall {
		t.Fatalf("no tool_calls in responses after nudge: %#v", responses)
	}
}

// TestOpenAIBackendGemma4FixFailsSafeAfterExhaustingRetries covers a
// persistently corrupted request shape: retries must be capped, and once
// exhausted the client must receive a clearly distinguishable error instead
// of any leaked control-token text.
func TestOpenAIBackendGemma4FixFailsSafeAfterExhaustingRetries(t *testing.T) {
	requestCount := 0
	b := NewOpenAIBackend("http://backend.test", 10, false, true)
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestCount++
		sse := strings.Join([]string{
			`data: {"choices":[{"delta":{"role":"assistant","content":"<|tool_call>call:read{path:<|\"|>x.html<|\"|>}<tool_call|>"},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
			"",
		}, "\n")
		return textResponse("text/event-stream", sse), nil
	})

	respChan, _, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "gemma-4",
		Stream:   true,
		Messages: []models.Message{{Role: "user", Content: "please read x.html"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	var responses []models.ChatResponse
	for resp := range respChan {
		responses = append(responses, resp)
	}

	if requestCount != gemma4MaxAttempts {
		t.Fatalf("request count = %d, want %d (capped retries)", requestCount, gemma4MaxAttempts)
	}
	if len(responses) != 1 {
		t.Fatalf("responses = %#v, want exactly one fail-safe response (nothing was ever forwarded)", responses)
	}
	final := responses[0]
	if !final.Done || final.DoneReason != "error" {
		t.Fatalf("final response = %#v, want Done=true DoneReason=error", final)
	}
	if strings.Contains(final.Message.Content, "tool_call") {
		t.Fatalf("final message = %q, leaked control tokens in fail-safe response", final.Message.Content)
	}
}
