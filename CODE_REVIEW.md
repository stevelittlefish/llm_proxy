# Codebase Review

Date: 2026-06-19

## Scope

Reviewed the Go proxy server, with emphasis on request translation, streaming behavior, log persistence, configuration validation, and the existing unit test surface.

## Summary

The codebase is small and generally cohesive. The separation between backend adapters, HTTP handlers, config loading, and SQLite persistence is clear. The most important behavior is concentrated in `backend/openai.go`, `backend/ollama.go`, and the chat handlers, and those paths now have stronger tests around translation and streaming edge cases.

No unresolved high-severity issue was found during this pass. One concrete streaming robustness defect was fixed and covered by a regression test.

## Findings

### Fixed: Ollama streaming scanner could drop large response lines

`backend/ollama.go` used `bufio.Scanner` with the default 64 KiB token limit in both generate and chat streaming paths. A single large JSON line from Ollama could cause scanning to stop, leaving the proxy with a partial or empty streamed response.

Resolution: increased the scanner buffer to 1 MiB in both paths:

- `backend/ollama.go:76`
- `backend/ollama.go:151`

Regression coverage:

- `backend/backend_http_test.go` verifies a chat stream with a 70 KiB content line is parsed and forwarded.

### Medium: malformed backend stream chunks are silently ignored

Both Ollama streaming paths skip JSON decode failures and continue:

- `backend/ollama.go:84`
- `backend/ollama.go:160`

This keeps the proxy tolerant of noisy streams, but if the backend emits only malformed chunks the client can receive a successful status with no useful error signal. Consider logging decode failures at least in verbose mode, and consider emitting a terminal error-shaped response if no valid chunk was ever parsed.

### Low: OpenAI model listing hides backend model endpoint failures

`OpenAIBackend.ListModels` returns a synthetic `default` model when `/v1/models` returns non-200 or malformed JSON:

- `backend/openai.go:792`
- `backend/openai.go:813`

This is useful compatibility behavior, but it can hide configuration problems in clients that rely on `/api/tags` or `/v1/models` for diagnostics. If this fallback is intentional, document it in `README.md`; otherwise return an error for non-200 responses and reserve the default for explicitly configured backends that do not support model listing.

## Test Coverage Added

Added meaningful unit tests for behavior that was previously easy to regress:

- OpenAI backend chat request translation: model, messages, `num_predict` to `max_tokens`, temperature, top-p, and prompt-cache forcing.
- OpenAI backend non-streaming response conversion: assistant message, finish reason, token counts, metadata capture.
- OpenAI backend streaming tool-call accumulation across SSE chunks.
- Ollama backend large streaming line parsing.
- Config defaults and validation for unknown keys and invalid chat text injection mode.
- Database cleanup retention and previous/next entry navigation.

All backend HTTP tests use an in-process fake `http.RoundTripper` rather than local network listeners, keeping the suite compatible with sandboxed environments.

## Existing Coverage That Looks Valuable

The pre-existing tests already cover several important surfaces:

- Tool-call conversion to OpenAI format, including generated IDs and tool-result ID propagation.
- Request sanitization for max-token policies.
- Text injection and tool blacklist parity across `/api/chat` and `/v1/chat/completions`.
- OpenAI frontend response formatting, including streaming and non-streaming tool calls.
- SQLite log round trips.
- LLM log markdown formatting.

## Recommended Next Tests

The next highest-value tests would be:

- `/api/generate` handler tests for frontend/backend payload logging and error logging.
- Backend non-200 responses for Ollama and OpenAI adapters, including captured raw response bodies.
- Web UI handler tests for details/download not-found and invalid ID cases.
- Middleware tests for CORS preflight and verbose request logging behavior.

## Verification

Ran:

```bash
GOCACHE=/tmp/llm_proxy_go_build CGO_ENABLED=0 go test ./...
```

Result: pass.
