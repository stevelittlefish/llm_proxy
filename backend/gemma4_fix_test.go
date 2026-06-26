package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

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

// TestGemma4ContentFilterDoesNotSplitUTF8Runes guards the holdback boundary: a
// multi-byte rune that lands on it must be held back whole, not byte-sliced. The
// filter forwards each safe chunk as its own SSE event (JSON-encoded), so a chunk
// ending or starting mid-rune becomes a U+FFFD replacement character downstream —
// the "Cinder␦␦␦s" corruption. Every forwarded chunk must therefore be valid
// UTF-8 on its own, and the stream must stay lossless.
func TestGemma4ContentFilterDoesNotSplitUTF8Runes(t *testing.T) {
	const sym = "✦" // U+2726 — 3 bytes: E2 9C A6
	// Size the content so the holdback cut (len - gemma4HoldbackLen) falls inside
	// the symbol: place gemma4HoldbackLen-1 trailing bytes after it.
	content := "Cinder " + sym + strings.Repeat("x", gemma4HoldbackLen-1)

	f := &gemma4ContentFilter{}
	forwarded := f.Feed(content)

	// The forwarded chunk is sent as a standalone SSE event; a partial trailing
	// rune in it gets mangled to U+FFFD by the next JSON encode/decode.
	if !utf8.ValidString(forwarded) {
		t.Fatalf("forwarded chunk is not valid UTF-8 (rune split across holdback): % x", forwarded)
	}

	// Nothing is dropped: Feed + Flush reconstruct the original exactly.
	whole := forwarded + f.Flush()
	if whole != content {
		t.Fatalf("filter was lossy: got %q, want %q", whole, content)
	}
	if f.Suspect() {
		t.Fatal("Suspect() = true, want false (no markers present)")
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
// forwarded yet, and the native-format parser cannot handle it (no args
// block), so the proxy falls through to a silent retry (with the same
// messages, but stream:false - the corruption is specific to vLLM's streaming
// gemma4 parser) and the client should see only the clean recovered output.
func TestOpenAIBackendGemma4FixRecoversCleanFailure(t *testing.T) {
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
			// Missing the {args} block — native parser cannot parse this, so the
			// fix falls through to the non-streaming retry path.
			sse := strings.Join([]string{
				`data: {"choices":[{"delta":{"role":"assistant","content":"<|tool_call>call:read<tool_call|>"},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
				"",
			}, "\n")
			return textResponse("text/event-stream", sse), nil
		}

		jsonResp := `{"choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"read","arguments":"{\"path\":\"x.html\"}"}}]},"finish_reason":"tool_calls"}]}`
		return textResponse("application/json", jsonResp), nil
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
	if !requestBodies[0].Stream {
		t.Fatal("original request stream = false, want true")
	}
	if requestBodies[1].Stream {
		t.Fatal("retry request stream = true, want false (retries must avoid vLLM's streaming gemma4 parser)")
	}
	if len(requestBodies[1].Messages) != len(requestBodies[0].Messages) {
		t.Fatalf("retry messages = %#v, want same messages as original (Case A: nothing forwarded yet)", requestBodies[1].Messages)
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
// legitimate precedes the corruption, so the native tool-call parser picks up
// the trapped bytes and synthesises a proper tool_calls response on the first
// attempt — no retry request is made.
func TestOpenAIBackendGemma4FixDetectsRealWorldCleanFailurePayload(t *testing.T) {
	b := NewOpenAIBackend("http://backend.test", 10, false, true)
	var requestBodies []models.OpenAIChatRequest
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var req models.OpenAIChatRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		requestBodies = append(requestBodies, req)
		return textResponse("text/event-stream", gemma4RealWorldCleanFailurePayload), nil
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

	if len(requestBodies) != 1 {
		t.Fatalf("request count = %d, want 1 (native parser recovers without a retry)", len(requestBodies))
	}
	if !requestBodies[0].Stream {
		t.Fatal("original request stream = false, want true")
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
			argsStr, ok := fn["arguments"].(string)
			if !ok {
				t.Fatalf("tool call arguments = %#v (%T), want JSON string", fn["arguments"], fn["arguments"])
			}
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
				t.Fatalf("Unmarshal(arguments) error = %v, args = %q", err, argsStr)
			}
			if args["command"] != "find . -type d -not -path '*/.*' -not -path '.' | sort -r" {
				t.Fatalf("tool call args = %#v, want recovered find command", args)
			}
		}
	}
	if !sawToolCall {
		t.Fatalf("no tool_calls in responses: %#v", responses)
	}
	if last := responses[len(responses)-1]; !last.Done {
		t.Fatalf("final response = %#v, want Done=true", last)
	}
}

// gemma4RealWorldExecuteCodePayload is a verbatim reproduction of the SSE
// stream observed in production for request #3077: Gemma 4 streaming a
// multi-line Python execute_code call via the hyphen-separated native format
// (call-execute-code), one token per chunk. The tool name in the native format
// is hyphenated ("execute-code"); the parser must convert it to the
// underscore form ("execute_code") that the Hermes client registered.
const gemma4RealWorldExecuteCodePayload = `data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"role":"assistant","content":""},"logprobs":null,"finish_reason":null}],"prompt_token_ids":null,"prompt_text":null}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"<|tool_call>"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"call-"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"execute"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"-"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"code"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"{"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"code"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":":"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"<|\"|>"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"from"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" hermes_tools"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" import"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":" read_file\n"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"print(read_file('/home/arthur/projects/agora/internal/store/messages.go')['content'])"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"\n"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"<|\"|>"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"}"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":"<tool_call|>"},"logprobs":null,"finish_reason":null,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[{"index":0,"delta":{"content":""},"logprobs":null,"finish_reason":"stop","stop_reason":50,"token_ids":null}]}

data: {"id":"chatcmpl-8df3d06daddb964c","object":"chat.completion.chunk","created":1781974989,"model":"gemma4-31b","choices":[],"usage":{"prompt_tokens":45947,"total_tokens":46035,"completion_tokens":88},"system_fingerprint":"vllm-0.23.0-tp2-39ad13f9"}

data: [DONE]
`

// TestParseGemma4NativeToolCalls exercises the parser directly with the two
// separator formats observed in production.
func TestParseGemma4NativeToolCalls(t *testing.T) {
	tests := []struct {
		name         string
		trapped      string
		wantOK       bool
		wantFuncName string
		wantArgKey   string
		wantArgVal   string
	}{
		{
			name:         "colon-separator simple",
			trapped:      `<|tool_call>call:bash{command:<|"|>ls -la<|"|>}<tool_call|>`,
			wantOK:       true,
			wantFuncName: "bash",
			wantArgKey:   "command",
			wantArgVal:   "ls -la",
		},
		{
			name:         "hyphen-separator converts to underscore",
			trapped:      `<|tool_call>call-execute-code{code:<|"|>print("hello")<|"|>}<tool_call|>`,
			wantOK:       true,
			wantFuncName: "execute_code",
			wantArgKey:   "code",
			wantArgVal:   `print("hello")`,
		},
		{
			name:         "value contains braces (Python f-string)",
			trapped:      "<|tool_call>call-execute-code{code:<|\"|>print(f\"{x}\")\n<|\"|>}<tool_call|>",
			wantOK:       true,
			wantFuncName: "execute_code",
			wantArgKey:   "code",
			wantArgVal:   "print(f\"{x}\")\n",
		},
		{
			name:         "edit with old_string/new_string/path (clean)",
			trapped:      `<|tool_call>call:edit{old_string:<|"|>foo<|"|>,new_string:<|"|>bar<|"|>,path:<|"|>cmd/imp/main.go<|"|>}<tool_call|>`,
			wantOK:       true,
			wantFuncName: "edit",
			wantArgKey:   "path",
			wantArgVal:   "cmd/imp/main.go",
		},
		{
			// Verbatim shape of production req #3963: Gemma dropped the
			// `old_string:` key label, dumping raw Go code straight after `{`,
			// and a stray delimiter landed mid-value (right after `TTY:`). The
			// lenient parser used to accept this — treating the code blob as a
			// property name and silently omitting `old_string` — and forward a
			// structurally-wrong tool call that the client rejected on its
			// schema, in an unrecoverable loop. It must now be rejected so the
			// caller falls through to the non-streaming retry.
			name:    "missing key label with stray delimiter (req 3963) is rejected",
			trapped: "<|tool_call>call:edit{\tfmt.Fprintln(w, \"     imp                         (no args on a TTY:<|\"|>open the interactive UI)\")\n\treturn nil\n`<|\"|>,new_string:<|\"|>\treturn nil\n<|\"|>,path:<|\"|>cmd/imp/main.go<|\"|>}<tool_call|>",
			wantOK:  false,
		},
		{
			name:    "missing args block",
			trapped: `<|tool_call>call:read<tool_call|>`,
			wantOK:  false,
		},
		{
			name:    "no call prefix",
			trapped: `<|tool_call>BADFORMAT{}<tool_call|>`,
			wantOK:  false,
		},
		{
			name:    "no open marker",
			trapped: `call:bash{command:<|"|>ls<|"|>}<tool_call|>`,
			wantOK:  false,
		},
		{
			name:    "trailing garbage",
			trapped: `<|tool_call>call:bash{command:<|"|>ls<|"|>}<tool_call|> extra`,
			wantOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toolCalls, ok := parseGemma4NativeToolCalls(tc.trapped)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if len(toolCalls) != 1 {
				t.Fatalf("len(toolCalls) = %d, want 1", len(toolCalls))
			}
			call := toolCalls[0].(map[string]interface{})
			fn := call["function"].(map[string]interface{})
			if fn["name"] != tc.wantFuncName {
				t.Fatalf("name = %q, want %q", fn["name"], tc.wantFuncName)
			}
			argsStr, ok := fn["arguments"].(string)
			if !ok {
				t.Fatalf("arguments type = %T, want string", fn["arguments"])
			}
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
				t.Fatalf("Unmarshal(arguments) error = %v", err)
			}
			if args[tc.wantArgKey] != tc.wantArgVal {
				t.Fatalf("args[%q] = %q, want %q", tc.wantArgKey, args[tc.wantArgKey], tc.wantArgVal)
			}
		})
	}
}

// TestOpenAIBackendGemma4FixParsesRealWorldExecuteCodePayload replays the
// verbatim SSE stream from production request #3077: Gemma 4 streaming a
// Python execute_code call in the hyphen-separated native format across many
// tiny deltas. The native parser must reconstruct the tool call from the
// accumulated trapped bytes and return it to the client on the first attempt,
// without any retry request.
func TestOpenAIBackendGemma4FixParsesRealWorldExecuteCodePayload(t *testing.T) {
	b := NewOpenAIBackend("http://backend.test", 10, false, true)
	var requestCount int
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestCount++
		return textResponse("text/event-stream", gemma4RealWorldExecuteCodePayload), nil
	})

	respChan, _, err := b.Chat(context.Background(), models.ChatRequest{
		Model:    "gemma4-31b",
		Stream:   true,
		Messages: []models.Message{{Role: "user", Content: "check messages.go"}},
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

	if requestCount != 1 {
		t.Fatalf("request count = %d, want 1 (native parser recovers without retry)", requestCount)
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
			call := resp.Message.ToolCalls[0].(map[string]interface{})
			fn := call["function"].(map[string]interface{})
			if fn["name"] != "execute_code" {
				t.Fatalf("function name = %q, want execute_code (hyphen-to-underscore conversion)", fn["name"])
			}
			argsStr, ok := fn["arguments"].(string)
			if !ok {
				t.Fatalf("arguments type = %T, want string", fn["arguments"])
			}
			var args map[string]interface{}
			if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
				t.Fatalf("Unmarshal(arguments) error = %v, args = %q", err, argsStr)
			}
			codeArg, ok := args["code"].(string)
			if !ok {
				t.Fatalf("args[code] type = %T, want string", args["code"])
			}
			if !strings.Contains(codeArg, "read_file") {
				t.Fatalf("args[code] = %q, want Python code containing read_file", codeArg)
			}
		}
	}
	if !sawToolCall {
		t.Fatalf("no tool_calls in responses: %#v", responses)
	}
	if last := responses[len(responses)-1]; !last.Done {
		t.Fatalf("final response = %#v, want Done=true", last)
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

		jsonResp := `{"choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"read","arguments":"{\"path\":\"x.html\"}"}}]},"finish_reason":"tool_calls"}]}`
		return textResponse("application/json", jsonResp), nil
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
	if !requestBodies[0].Stream {
		t.Fatal("original request stream = false, want true")
	}
	if requestBodies[1].Stream {
		t.Fatal("nudge request stream = true, want false (retries must avoid vLLM's streaming gemma4 parser)")
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
// persistently corrupted request shape where the native-format parser cannot
// help (missing args block): retries must be capped, and once exhausted the
// client must receive a clearly distinguishable error instead of any leaked
// control-token text.
func TestOpenAIBackendGemma4FixFailsSafeAfterExhaustingRetries(t *testing.T) {
	requestCount := 0
	b := NewOpenAIBackend("http://backend.test", 10, false, true)
	b.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requestCount++

		if requestCount == 1 {
			// Missing {args} block — native parser cannot parse this.
			sse := strings.Join([]string{
				`data: {"choices":[{"delta":{"role":"assistant","content":"<|tool_call>call:read<tool_call|>"},"finish_reason":null}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
				"",
			}, "\n")
			return textResponse("text/event-stream", sse), nil
		}

		// Subsequent attempts are non-streaming retries; this backend keeps
		// producing the same unparseable corrupted output, so recovery exhausts
		// its attempts and fails safe.
		jsonResp := `{"choices":[{"index":0,"message":{"role":"assistant","content":"<|tool_call>call:read<tool_call|>"},"finish_reason":"stop"}]}`
		return textResponse("application/json", jsonResp), nil
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
