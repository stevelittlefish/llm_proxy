# Docker Setup for LLM Proxy

This guide explains how to run the LLM proxy server using Docker.

## Prerequisites

- Docker Engine 20.10+
- Docker Compose 1.29+ (optional, for orchestration)

## Using Pre-built Images (Recommended)

Pre-built Docker images are available from GitHub Container Registry. This is the easiest way to get started:

### Quick Start with Pre-built Image

```bash
# 1. Get the example config
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

### Using Docker Compose with Pre-built Image

Create a simple `docker-compose.yml`:

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
docker-compose up -d
```

### Available Image Tags

- `latest` - Latest stable release
- `v1.0.0` - Specific version (replace with desired version)
- `v1.0` - Latest patch version of 1.0.x
- `v1` - Latest minor version of 1.x.x

Examples:
```bash
# Use latest version
docker pull ghcr.io/stevelittlefish/llm_proxy:latest

# Use specific version
docker pull ghcr.io/stevelittlefish/llm_proxy:v1.0.0

# Use latest 1.x version
docker pull ghcr.io/stevelittlefish/llm_proxy:v1
```

## Building from Source

If you prefer to build the Docker image from source (for development or customization):

### Quick Start

### 1. Prepare Configuration

First, create your `config.json` from the **Docker-specific** example:

```bash
cp config.docker.json.example config.json
```

This example includes the correct paths for Docker containers:
- `database.path` is set to `/app/data/llm_proxy.db` (inside the container)
- `backend.endpoint` uses `host.docker.internal` to access services on the host

Edit `config.json` to match your backend settings if needed.

### 2. Create Docker Compose Override

The base `docker-compose.yml` has ports and volumes commented out. Create an override file:

```bash
cp docker-compose.override.yml.example docker-compose.override.yml
```

This file enables:
- Port mapping (default: `11435:11434` to avoid conflicts with local Ollama)
- Config file volume mount
- Database persistence directory

Edit `docker-compose.override.yml` if you need different port mappings or paths.

### 3. Create Data Directory

The database will be stored in the `data/` directory:

```bash
mkdir -p data
```

### 4. Build and Run

Using Docker Compose (recommended):

```bash
docker-compose up -d
```

Or manually with Docker:

```bash
# Build the image
docker build -t llm-proxy .

# Run the container
docker run -d \
  --name llm-proxy \
  -p 11435:11434 \
  -v $(pwd)/config.json:/app/config/config.json:ro \
  -v $(pwd)/data:/app/data \
  llm-proxy
```

**Note**: The manual Docker command uses port `11435` on the host to avoid conflicts with local Ollama instances.

### 5. Verify It's Running

```bash
# Check container status
docker-compose ps

# Check logs
docker-compose logs -f

# Test the endpoint (note: use port 11435 if using the override example)
curl http://localhost:11435/
# Should show the web UI HTML

# Health check
curl http://localhost:11435/health
# Should return: "OK"

# List models
curl http://localhost:11435/api/tags
```

## Configuration

### Docker Compose Structure

This project uses a layered Docker Compose approach:

1. **`docker-compose.yml`** (base configuration)
   - Defines the service build and health checks
   - Ports and volumes are commented out
   - Committed to version control

2. **`docker-compose.override.yml`** (your local configuration)
   - Extends the base configuration
   - Sets up ports and volume mounts
   - **Not committed** to version control (in `.gitignore`)
   - Create from `docker-compose.override.yml.example`

This approach allows each deployment to customize ports and paths without modifying tracked files.

### Volume Mounts

The `docker-compose.override.yml` sets up two volume mounts:

1. **Config file** (`./config.json` → `/app/config/config.json`)
   - Mounted as read-only (`:ro`)
   - Contains server and backend configuration
   - Edit this file to change proxy settings
   - Use `config.docker.json.example` as a template

2. **Database directory** (`./data` → `/app/data`)
   - Stores the SQLite database (`llm_proxy.db`)
   - Persists request/response logs across container restarts
   - The `config.docker.json.example` already has the correct path: `"/app/data/llm_proxy.db"`

### Environment Variables

You can customize the timezone or other settings in `docker-compose.yml`:

```yaml
environment:
  TZ: Europe/London  # Change to your timezone
```

## Managing the Container

### Start/Stop

```bash
# Start
docker-compose up -d

# Stop
docker-compose down

# Restart
docker-compose restart
```

### View Logs

```bash
# Follow logs
docker-compose logs -f

# View last 100 lines
docker-compose logs --tail=100
```

### Update Configuration

After editing `config.json`:

```bash
docker-compose restart
```

### Rebuild After Code Changes

```bash
docker-compose down
docker-compose build --no-cache
docker-compose up -d
```

## Troubleshooting

### Cannot connect to backend

If the backend is running on your host machine:
- Use `host.docker.internal` instead of `localhost` in your `backend.endpoint`
- Example: `"endpoint": "http://host.docker.internal:8008"`
- The `config.docker.json.example` already uses this format

On Linux, you may need to add this to your `docker-compose.override.yml`:

```yaml
services:
  llm-proxy:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

### Cannot access the proxy

If you can't reach the proxy from your browser or Home Assistant:
- Check that `docker-compose.override.yml` exists and has the ports section
- Verify the port mapping: `docker-compose ps`
- Ensure the config has `"host": "0.0.0.0"` (already set in `config.docker.json.example`)
- Check container logs: `docker-compose logs -f`

### Database permission errors

Ensure the `data/` directory is writable:

```bash
chmod 755 data/
```

### Port already in use

The default `docker-compose.override.yml.example` uses port `11435` to avoid conflicts with local Ollama.

If you need a different port, edit your `docker-compose.override.yml`:

```yaml
services:
  llm-proxy:
    ports:
      - "11436:11434"  # Use external port 11436
```

## Production Considerations

### Resource Limits

Add resource limits in docker-compose.yml:

```yaml
deploy:
  resources:
    limits:
      cpus: '2'
      memory: 1G
    reservations:
      cpus: '0.5'
      memory: 256M
```

### Logging

Configure log rotation in docker-compose.yml:

```yaml
logging:
  driver: "json-file"
  options:
    max-size: "10m"
    max-file: "3"
```

### Backup Database

```bash
# Backup
docker-compose exec llm-proxy cp /app/data/llm_proxy.db /app/data/llm_proxy.db.backup

# Or from host
cp data/llm_proxy.db data/llm_proxy.db.backup
```

## Advanced Usage

### Multiple Configurations

You can maintain multiple configuration files for different backends:

```bash
# For llama.cpp backend
cp config.docker.json.example config_llama_cpp.json

# For Ollama backend
cp config.docker.json.example config_ollama.json
# Edit config_ollama.json to set "type": "ollama"
```

Then reference the desired config in your `docker-compose.override.yml`:

```yaml
services:
  llm-proxy:
    volumes:
      - ./config_llama_cpp.json:/app/config/config.json:ro
      - ./data:/app/data
```

### Running with custom network

If you have other services (like a local LLM backend) in Docker, add a network configuration to `docker-compose.override.yml`:

```yaml
services:
  llm-proxy:
    networks:
      - llm-network

networks:
  llm-network:
    external: true
    name: my-existing-network
```

### Accessing the Web UI

The proxy includes a built-in web interface:

- **Home page**: `http://localhost:11435/` - Shows configuration overview
- **Logs**: `http://localhost:11435/logs` - Browse all requests
- **Details**: `http://localhost:11435/logs/details?id=<id>` - View specific request details

Replace `11435` with your configured port.

### Multi-stage debugging

Build without cache and see all output:

```bash
docker build --no-cache --progress=plain -t llm-proxy .
```
