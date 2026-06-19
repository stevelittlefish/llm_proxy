# AGENTS.md

## Project Overview

`llm_proxy` is a Go proxy server that exposes Ollama-compatible endpoints and forwards requests to either an OpenAI-compatible backend or an Ollama backend. It also exposes basic OpenAI-compatible frontend endpoints for simple clients.

The server logs requests and responses to SQLite and serves a small web UI for browsing logs. The repository also includes a dependency-free terminal chat client in `cmd/chatclient`.

## Important Commands

- Run all tests:
  ```bash
  CGO_ENABLED=0 go test ./...
  ```
- Run the proxy from source:
  ```bash
  ./run.sh
  ```
- Run the chat client from source:
  ```bash
  ./client.sh
  ```
- Run the chat client against OpenAI-compatible frontend endpoints:
  ```bash
  ./client.sh --openai
  ```
- Build the proxy binary:
  ```bash
  CGO_ENABLED=0 go build -o llm_proxy .
  ```

If the Go build cache is not writable in a sandbox, set it explicitly:

```bash
GOCACHE=/tmp/llm_proxy_go_build CGO_ENABLED=0 go test ./...
```

## Repository Layout

- `main.go` wires config, database, backend selection, HTTP routes, middleware, and shutdown.
- `config/` loads and validates `config.toml`.
- `backend/` contains the backend interface and the OpenAI/Ollama backend implementations.
- `handlers/` contains the Ollama frontend handlers, OpenAI frontend handlers, web UI handlers, templates, and static assets.
- `models/` contains shared request and response structs.
- `database/` owns SQLite initialization and log queries.
- `middleware/` contains CORS and verbose request logging middleware.
- `cmd/chatclient/` contains the stdlib-only terminal chat client.
- `run.sh` and `client.sh` are convenience wrappers for local manual testing.

## Development Notes

- Keep the chat client dependency-free. It should remain trivial to run with `go run ./cmd/chatclient`.
- The SQLite driver is `modernc.org/sqlite`, a pure-Go driver. Do not reintroduce `github.com/mattn/go-sqlite3` or CGO requirements unless explicitly requested.
- Use `database/sql` and the existing `database.DB` wrapper for database work.
- Preserve the two frontend API surfaces:
  - Ollama-compatible: `/api/generate`, `/api/chat`, `/api/tags`, `/api/show`
  - OpenAI-compatible: `/v1/chat/completions`, `/v1/models`
- OpenAI backend support lives in `backend/openai.go`; OpenAI-compatible frontend support lives in `handlers/openai_frontend.go`.
- Text injection and tool blacklist behavior apply to both chat frontend endpoints: `/api/chat` and `/v1/chat/completions`.
- Config parsing intentionally rejects unknown TOML keys.
- `config.toml` and `data/` are local runtime files and are ignored by git. Keep changes in `config.toml.example` when documenting default config shape.

## Testing Guidance

- Run `CGO_ENABLED=0 go test ./...` before finishing code changes.
- Add focused tests when changing request translation, handler response formats, or database behavior.
- Existing useful test areas:
  - `backend/convert_test.go` for tool-call conversion to OpenAI format.
  - `database/sqlite_test.go` for SQLite round trips.
  - `handlers/openai_frontend_test.go` for OpenAI-compatible frontend behavior.
  - `handlers/llmlog_test.go` for log formatting.

## Documentation Guidance

- Keep `README.md` aligned with `config.toml.example`, `go.mod`, `Dockerfile`, scripts, and exposed routes.
- If endpoints are added or changed, update both the API endpoint list and the project structure section in `README.md`.
- If release targets change, update the README release section and check `.goreleaser.yml` / `.github/workflows/release.yml`.
