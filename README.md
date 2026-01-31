# LLM Proxy

A lightweight, Go-based proxy server that provides an Ollama-compatible API while forwarding requests to various LLM backends (OpenAI-compatible APIs like llama.cpp, or actual Ollama instances). All requests and responses are logged to an SQLite database for debugging and analysis.

The main motivation for creating this was to get the Home Assistant Ollama integration to work with llama.cpp.  I've also always wanted a better way to log requests and responses from other LLM based apps (i.e. Open Web UI).  This proxy has a web interface which shows all of the messages including the full context and the system prompt which is really useful for debugging.

## Features

- **Ollama-Compatible API**: Presents an Ollama API interface, making it compatible with Home Assistant and other Ollama clients
- **Multiple Backend Support**: 
  - OpenAI-compatible APIs (e.g., llama.cpp)
  - Ollama instances (for pass-through with logging)
- **Streaming Support**: Full support for streaming responses
- **Request/Response Logging**: All interactions logged to SQLite database with timestamps, latency, and error tracking
- **Web UI**: Built-in web interface for viewing logs, request/response details, and configuration
- **Docker Support**: Production-ready Docker images with health checks
- **Minimal Dependencies**: Only requires Go standard library + SQLite driver

## Use Case

This proxy is designed to sit between Home Assistant (or any Ollama client) and llama.cpp (or other backends), allowing you to:
- Use Home Assistant's Ollama integration with llama.cpp for faster responses
- Log all LLM interactions for debugging and analysis
- Switch between different backends without reconfiguring clients
- Map model names between different systems

## Installation

### Prerequisites

- Go 1.21 or later
- GCC (required for SQLite driver compilation)

### Quickstart

Clone the repository and then do:

```bash
cp config.json.example config.json
# Edit the file to change settings
go run .
```

### Build from Source

```bash
git clone <REPOSITORY>
cd llm_proxy
go mod download
go build -o llm_proxy
```

Or see [DOCKER.md](DOCKER.md) for Docker installation instructions.

## Configuration

Create a `config.json` file based on the provided example:

```json
{
  "server": {
    "host": "0.0.0.0",
    "port": 11434,
    "enable_cors": false,
    "log_messages": false,
    "log_raw_requests": false,
    "log_raw_responses": false
  },
  "backend": {
    "type": "openai",
    "endpoint": "http://localhost:8080",
    "timeout": 300
  },
  "database": {
    "path": "./data/llm_proxy.db"
  }
}
```

### Configuration Options

#### Server
- `host`: IP address to bind to (default: `0.0.0.0`)
- `port`: Port to listen on (default: `11434` - Ollama's default port)
- `enable_cors`: Enable CORS middleware (default: `false`) - this will allow any web page to directly access the server via javascript
- `log_messages`: Log message content in human-readable format to stdout (default: `false`)
- `log_raw_requests`: Log raw JSON request payloads (pretty-printed) to stdout (default: `false`)
- `log_raw_responses`: Log raw JSON response payloads (pretty-printed) to stdout (default: `false`)

**Logging Options:**
- All three logging options are independent and can be enabled together
- `log_messages` provides human-readable summaries (e.g., "Model: llama2, Prompt: Why is the sky blue?")
- `log_raw_requests` shows the exact JSON payloads received by the proxy
- `log_raw_responses` shows the complete JSON responses (including all streaming chunks)
- All logs go to stdout and can be redirected to files if needed
- Note: These are stdout logs only; database logging is always enabled regardless of these settings

#### Backend
- `type`: Backend type - either `"openai"` or `"ollama"`
- `endpoint`: URL of the backend service
  - For llama.cpp: typically `http://localhost:8080`
  - For Ollama: typically `http://localhost:11434`
- `timeout`: Request timeout in seconds (default: `300`)

#### Database
- `path`: Path to SQLite database file (default: `./data/llm_proxy.db`)

## Usage

### Start the Server

```bash
./llm_proxy -config config.json
```

Or use the default config file location:

```bash
./llm_proxy
```

### Test with curl

#### Generate Endpoint (Streaming)
```bash
curl -X POST http://localhost:11434/api/generate \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama2",
    "prompt": "Why is the sky blue?",
    "stream": true
  }'
```

#### Chat Endpoint (Streaming)
```bash
curl -X POST http://localhost:11434/api/chat \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama2",
    "messages": [
      {"role": "user", "content": "Hello, how are you?"}
    ],
    "stream": true
  }'
```

#### List Models
```bash
curl http://localhost:11434/api/tags
```

### Configure Home Assistant

In Home Assistant, configure the Ollama integration to point to your proxy:

```yaml
# configuration.yaml
ollama:
  base_url: "http://your-proxy-ip:11434"
  model: "llama2"
```

## API Endpoints

### Ollama-Compatible Endpoints

The proxy implements the following Ollama API endpoints:

- `POST /api/generate` - Text completion
- `POST /api/chat` - Chat completion
- `GET /api/tags` - List available models
- `POST /api/show` - Show model information

### Web UI Endpoints

- `GET /` - Home page with configuration overview
- `GET /logs` - Paginated list of all requests/responses
- `GET /logs/details?id=<id>` - Detailed view of a specific request
- `GET /health` - Health check endpoint (returns "OK")

The web interface provides an easy way to browse logs, inspect request/response details, and monitor the proxy's configuration without needing direct database access.

## Backend Types

### OpenAI Backend

Use `"type": "openai"` to connect to OpenAI-compatible APIs like llama.cpp:

- Translates Ollama requests to OpenAI format
- Converts streaming SSE responses to Ollama's newline-delimited JSON
- Maps parameters (temperature, max_tokens, etc.)

Example llama.cpp command:
```bash
./server -m model.gguf --port 8080 --host 0.0.0.0
```

### Ollama Backend

Use `"type": "ollama"` to wrap an existing Ollama instance:

- Simple pass-through with logging
- Useful for debugging and monitoring Ollama usage
- No translation required

Example Ollama configuration:
```json
{
  "backend": {
    "type": "ollama",
    "endpoint": "http://localhost:11435"
  }
}
```

## Architecture

```
┌─────────────────┐
│ Home Assistant  │
│  (Ollama API)   │
└────────┬────────┘
         │
         v
┌─────────────────┐
│   LLM Proxy     │
│ - Logging       │
│ - Translation   │
└────────┬────────┘
         │
    ┌────┴────┐
    v         v
┌────────┐  ┌────────┐
│ llama  │  │ Ollama │
│  .cpp  │  │        │
└────────┘  └────────┘
```

## Project Structure

```
llm_proxy/
├── main.go                      # Entry point and server setup
├── config/
│   └── config.go                # Configuration loading
├── backend/
│   ├── backend.go               # Backend interface
│   ├── openai.go                # OpenAI backend implementation
│   └── ollama.go                # Ollama backend implementation
├── handlers/
│   ├── generate.go              # /api/generate handler
│   ├── chat.go                  # /api/chat handler
│   ├── models.go                # /api/tags and /api/show handlers
│   ├── web.go                   # Web UI handlers
│   └── templates/               # HTML templates for web UI
│       ├── home.html            # Configuration overview
│       ├── logs.html            # Request logs list
│       └── details.html         # Request details view
├── models/
│   └── types.go                 # Request/response types
├── database/
│   ├── sqlite.go                # SQLite connection and initialization
│   └── queries.go               # Database queries
├── middleware/
│   └── cors.go                  # CORS middleware
├── Dockerfile                   # Docker build configuration
├── docker-compose.yml           # Docker compose setup
├── config.json.example          # Example configuration
├── config.docker.json.example   # Example Docker configuration
└── README.md                    # This file
```

## Performance

The proxy adds minimal latency (typically <10ms) as it streams responses directly from the backend without buffering. All logging is done asynchronously after the response is sent.

## Docker Deployment

See [DOCKER.md](DOCKER.md) for detailed Docker and Docker Compose instructions.

## Contributing

I didn't write that much of the code, my good friend Claude did through the Cline plugin in Visual Studio Code.  It cost me $12.78 in tokens and took around half a day.  Not bad!

Contributions are welcome! Please feel free to submit issues and pull requests to the repository.
