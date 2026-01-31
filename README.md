# LLM Proxy

A lightweight, Go-based proxy server that provides an Ollama-compatible API while forwarding requests to various LLM backends (OpenAI-compatible APIs like llama.cpp, or actual Ollama instances). All requests and responses are logged to an SQLite database for debugging and analysis.

The main motivation for creating this was to get the Home Assistant Ollama integration to work with llama.cpp.  I've also always wanted a better way to log requests and responses from other LLM based apps (i.e. Open Web UI).  This proxy has a web interface which shows all of the messages including the full context and the system prompt which is really useful for debugging.

## Features

- **Ollama-Compatible API** - Presents an Ollama API interface, compatible with Home Assistant and other Ollama clients
- **Multiple Backend Support** - Connect to OpenAI-compatible APIs (e.g., llama.cpp) or Ollama instances
- **Streaming Support** - Full support for streaming responses with minimal latency
- **Request/Response Logging** - All interactions logged to SQLite with timestamps, latency, and error tracking
- **Web UI** - Built-in interface for viewing logs, request/response details, and configuration
- **Text Injection** - Automatically inject text into user messages (disabled by default) for example "/nothink" to disable thinking
- **Docker Support** - Production-ready Docker images with health checks
- **Minimal Dependencies** - Only requires Go standard library + SQLite driver
- **Highly Configurable** - Fine-tune logging, timeouts, CORS, database cleanup, and more

## Quick Start with Docker

The easiest way to run LLM Proxy is using the pre-built Docker images:

### Using Docker Run

```bash
# 1. Create a config file
curl -O https://raw.githubusercontent.com/stevelittlefish/llm_proxy/master/config.json.example
mv config.json.example config.json
# Edit config.json to configure your backend

# 2. Create data directory
mkdir -p data

# 3. Run the container
docker run -d \
  --name llm-proxy \
  -p 11434:11434 \
  -v $(pwd)/config.json:/app/config/config.json:ro \
  -v $(pwd)/data:/app/data \
  ghcr.io/stevelittlefish/llm_proxy:latest
```

### Using Docker Compose

Create a `docker-compose.yml` file:

```yaml
version: '3.8'

services:
  llm-proxy:
    image: ghcr.io/stevelittlefish/llm_proxy:latest
    container_name: llm-proxy
    restart: unless-stopped
    ports:
      - "11434:11434"
    volumes:
      - ./config.json:/app/config/config.json:ro
      - ./data:/app/data
```

Then run:

```bash
# Get the example config
curl -O https://raw.githubusercontent.com/stevelittlefish/llm_proxy/master/config.json.example
mv config.json.example config.json
# Edit config.json to configure your backend

# Create data directory
mkdir -p data

# Start the service
docker-compose up -d
```

For advanced Docker setup (building from source, custom networks, etc.), see [DOCKER.md](DOCKER.md).

## Use Case

This proxy is designed to sit between Home Assistant (or any Ollama client) and llama.cpp (or other backends), allowing you to:
- Use Home Assistant's Ollama integration with llama.cpp for faster responses
- Log all LLM interactions for debugging and analysis
- Switch between different backends without reconfiguring clients
- Map model names between different systems

## Installation

### Download Pre-built Binaries

The easiest way to get started is to download a pre-built binary from the [releases page](https://github.com/stevelittlefish/llm_proxy/releases).

1. Download the archive for your platform:
   - Linux (x86_64): `llm_proxy_VERSION_linux_amd64.tar.gz`
   - macOS (Intel): `llm_proxy_VERSION_darwin_amd64.tar.gz`
   - Windows (x86_64): `llm_proxy_VERSION_windows_amd64.zip`

**Note:** Pre-built binaries are currently only available for x86_64/amd64 architectures. ARM64 users (including Apple Silicon Macs, Raspberry Pi 4/5, and ARM-based Linux systems) should [build from source](#build-from-source) as the SQLite dependency requires CGO which cannot be easily cross-compiled.

2. Extract the archive:
   ```bash
   # Linux/macOS
   tar -xzf llm_proxy_VERSION_linux_amd64.tar.gz
   cd llm_proxy_VERSION_linux_amd64
   
   # Windows: use your preferred extraction tool
   ```

3. Edit `config.json` to configure your backend

4. Run the proxy:
   ```bash
   # Linux/macOS
   ./llm_proxy
   
   # Windows
   llm_proxy.exe
   ```

### Build from Source

If you prefer to build from source:

#### Prerequisites

- Go 1.21 or later
- GCC (required for SQLite driver compilation)

#### Quickstart

Clone the repository and then do:

```bash
git clone https://github.com/stevelittlefish/llm_proxy
cd llm_proxy
cp config.json.example config.json
# Edit the file to change settings
go run .
```

#### Build Binary

```bash
git clone https://github.com/stevelittlefish/llm_proxy
cd llm_proxy
go mod download
go build -o llm_proxy
```

### Docker Installation

See [DOCKER.md](DOCKER.md) for Docker and Docker Compose installation instructions.

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
- `max_requests`: Maximum number of requests to keep in the database (default: `100`). Older requests are automatically deleted during cleanup.
- `cleanup_interval`: How often (in minutes) to run the cleanup task (default: `5`). Set to `0` to disable automatic cleanup.

**Database Cleanup:**
- The cleanup task runs automatically in the background based on the `cleanup_interval`
- When triggered, it removes the oldest requests, keeping only the most recent `max_requests` entries
- The first cleanup runs immediately on startup, then repeats at the configured interval
- Set `max_requests` to `0` or `cleanup_interval` to `0` to disable automatic cleanup
- All request/response data is permanently deleted when cleaned up

#### Chat Text Injection
- `enabled`: Enable text injection (default: `false`)
- `text`: The text string to inject (e.g., `"/nothink"`)
- `mode`: Which user message to inject into - either `"first"` or `"last"` (default: `"last"`)

**Text Injection Behavior:**
- **Disabled by default** - must be explicitly enabled in config.json
- **Only applies to `/api/chat` endpoint** (not `/api/generate`)
- When enabled, automatically appends the configured text to the specified user message
- **Smart injection** - checks if the text already exists and skips injection if present
- Text is added with a preceding space: `"hello"` becomes `"hello /nothink"`
- Injection happens after raw request logging but before the backend call
- Useful for adding special tokens or instructions to all user messages

**Example Configuration:**
```json
{
  "chat_text_injection": {
    "enabled": true,
    "text": "/nothink",
    "mode": "last"
  }
}
```

**Mode Options:**
- `"first"`: Injects text into the first message with `role == "user"` in the messages array
- `"last"`: Injects text into the last message with `role == "user"` in the messages array (typically the current user input)

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

## Releases

This project uses automated builds via GitHub Actions and GoReleaser. When you push a tag starting with `v`, it automatically builds binaries for all supported platforms and creates a GitHub release.

### Creating a Release

Use the included `make_release.sh` script to create and push a release:

```bash
./make_release.sh v1.0.0 "Initial release"
```

The script will:
1. Validate the version format (must start with `v`)
2. Check for uncommitted changes and warn if found
3. Verify you're on the `master` branch
4. Check that the tag doesn't already exist
5. Show a summary and ask for confirmation
6. Create an annotated tag with your message
7. Push the tag to GitHub

GitHub Actions will then automatically:
- Build binaries for Linux, macOS (Intel & ARM), and Windows
- Create release archives with config files and documentation
- Generate checksums
- Create a GitHub release with all artifacts

**Manual Release (alternative)**:
```bash
git tag -a v1.0.0 -m "Release v1.0.0"
git push origin v1.0.0
```

### Release Artifacts

Each release includes:
- Pre-built binaries for multiple platforms
- `config.json` (ready to edit)
- Documentation (README.md, LICENCE.md, DOCKER.md)
- Data directory with README
- SHA256 checksums for verification

## Contributing

I didn't write that much of the code, my good friend Claude did through the Cline plugin in Visual Studio Code.  It cost me $12.78 in tokens and took around half a day.  Not bad!

Contributions are welcome! Please feel free to submit issues and pull requests to the repository.
