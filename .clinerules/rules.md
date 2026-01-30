# LLM Proxy Architecture Rules

## System Architecture

This is an **LLM proxy server** that provides an **Ollama-compatible API frontend** while supporting multiple backend types.

Currently we only support the openai back-end (for use with a llama.cpp server).

### Frontend (API)
- **Exposes Ollama-compatible API endpoints**:
  - `/api/chat` - Chat completions (Ollama format)
  - `/api/generate` - Text generation (Ollama format)
  - `/api/tags` - List models (Ollama format)
  - `/api/show` - Show model info (Ollama format)
- All requests and responses use **Ollama's JSON format**
- Clients connect to this proxy thinking it's an Ollama server

### Backend (Configurable)
- **Supports multiple backend types** (configured in `config.json`):
  - `"backend": { "type": "openai" }` - OpenAI-compatible API (default)
  - `"backend": { "type": "ollama" }` - Real Ollama server
- The proxy **translates between Ollama format (frontend) and backend format**

### Current Configuration
- **Frontend**: Ollama-compatible API on port 11434
- **Backend**: OpenAI-compatible API at `http://ai.lemon.com:8008`
- The proxy translates Ollama requests → OpenAI requests → OpenAI responses → Ollama responses

## Key Points for Development

1. **When fixing response format issues**, check:
   - The **backend type** in `config.json` (openai vs ollama)
   - Fix the corresponding backend file:
     - `backend/openai.go` for OpenAI backend
     - `backend/ollama.go` for Ollama backend

2. **The proxy performs translation**:
   - Incoming requests are in Ollama format
   - Backend requests are in the configured backend's format
   - Responses are converted back to Ollama format

3. **Response streaming**:
   - Both backends support streaming responses
   - OpenAI backend translates OpenAI SSE format → Ollama format
   - Ollama backend passes through Ollama format (may still need fixes)

4. **Always check config.json first** to understand which backend is active
