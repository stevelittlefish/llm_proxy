# LLM Proxy

A lightweight, Go-based proxy server that provides an Ollama-compatible API while forwarding requests to various LLM backends (OpenAI-compatible APIs like llama.cpp, or actual Ollama instances). All requests and responses are logged to an SQLite database for debugging and analysis.

## Features

- **Ollama-Compatible API**: Presents an Ollama API interface, making it compatible with Home Assistant and other Ollama clients
- **Multiple Backend Support**: 
  - OpenAI-compatible APIs (e.g., llama.cpp)
  - Ollama instances (for pass-through with logging)
- **Streaming Support**: Full support for streaming responses
- **Request/Response Logging**: All interactions logged to SQLite database with timestamps, latency, and error tracking
- **Model Mapping**: Configure model name translations between frontend and backend
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

### Build from Source

```bash
git clone <repository-url>
cd llm_proxy
go mod download
go build -o llm_proxy
```

## Configuration

Create a `config.json` file based on the provided example:

```json
{
  "server": {
    "host": "0.0.0.0",
    "port": 11434,
    "enable_cors": true,
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
    "path": "./llm_proxy.db"
  },
  "models": {
    "default": "llama2",
    "mappings": {
      "llama2": "llama-2-7b-chat",
      "codellama": "codellama-7b"
    }
  }
}
```

### Configuration Options

#### Server
- `host`: IP address to bind to (default: `0.0.0.0`)
- `port`: Port to listen on (default: `11434` - Ollama's default port)
- `enable_cors`: Enable CORS middleware (default: `false`)
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
- `path`: Path to SQLite database file (default: `./llm_proxy.db`)

#### Models
- `default`: Default model name to use if none specified
- `mappings`: Map Ollama model names to backend model names

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

The proxy implements the following Ollama API endpoints:

- `POST /api/generate` - Text completion
- `POST /api/chat` - Chat completion
- `GET /api/tags` - List available models
- `POST /api/show` - Show model information
- `GET /health` - Health check endpoint

## Database Schema

Logs are stored in SQLite with the following schema:

```sql
CREATE TABLE logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp DATETIME NOT NULL,
  endpoint TEXT NOT NULL,
  method TEXT NOT NULL,
  model TEXT,
  prompt TEXT,
  response TEXT,
  status_code INTEGER,
  latency_ms INTEGER,
  stream BOOLEAN,
  backend_type TEXT,
  error TEXT
);
```

### Query Examples

View recent requests:
```sql
SELECT timestamp, endpoint, model, latency_ms 
FROM logs 
ORDER BY timestamp DESC 
LIMIT 10;
```

Calculate average latency by model:
```sql
SELECT model, AVG(latency_ms) as avg_latency_ms, COUNT(*) as request_count
FROM logs 
WHERE error IS NULL
GROUP BY model;
```

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
├── main.go              # Entry point and server setup
├── config/
│   └── config.go        # Configuration loading
├── backend/
│   ├── backend.go       # Backend interface
│   ├── openai.go        # OpenAI backend implementation
│   └── ollama.go        # Ollama backend implementation
├── handlers/
│   ├── generate.go      # /api/generate handler
│   ├── chat.go          # /api/chat handler
│   └── models.go        # /api/tags and /api/show handlers
├── models/
│   └── types.go         # Request/response types
├── database/
│   └── sqlite.go        # SQLite logging
├── config.json.example  # Example configuration
└── README.md           # This file
```

## Troubleshooting

### Connection Refused
- Ensure the backend service (llama.cpp/Ollama) is running
- Check the endpoint URL in config.json
- Verify firewall settings

### Streaming Not Working
- The proxy uses chunked transfer encoding for streaming
- Ensure your client supports streaming responses
- Check that the backend has streaming enabled

### Model Not Found
- Check model mappings in config.json
- Verify the model exists on the backend
- Use `/api/tags` to list available models

### Database Locked
- Only one proxy instance can access the database at a time
- Ensure no other processes are using the database file

## Performance

The proxy adds minimal latency (typically <10ms) as it streams responses directly from the backend without buffering. All logging is done asynchronously after the response is sent.

## License

[Your License Here]

## Contributing

Contributions are welcome! Please feel free to submit issues and pull requests.
