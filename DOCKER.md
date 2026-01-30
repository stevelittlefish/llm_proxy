# Docker Setup for LLM Proxy

This guide explains how to run the LLM proxy server using Docker.

## Prerequisites

- Docker Engine 20.10+
- Docker Compose 1.29+

## Quick Start

### 1. Prepare Configuration

First, create your `config.json` from the example:

```bash
cp config.json.example config.json
```

Edit `config.json` to match your backend settings. **Important**: When running in Docker, make sure to:
- Set `server.host` to `"0.0.0.0"` (to accept connections from outside the container)
- Update `backend.endpoint` to use the appropriate hostname
  - If your backend is on the host machine, use `host.docker.internal` instead of `localhost`
  - Example: `"endpoint": "http://host.docker.internal:8008"`

### 2. Create Data Directory

The database will be stored in the `data/` directory:

```bash
mkdir -p data
```

### 3. Build and Run

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
  -p 11434:11434 \
  -v $(pwd)/config.json:/app/config/config.json:ro \
  -v $(pwd)/data:/app/data \
  llm-proxy
```

### 4. Verify It's Running

```bash
# Check container status
docker-compose ps

# Check logs
docker-compose logs -f

# Test the endpoint
curl http://localhost:11434/
# Should return: "Ollama (proxy) is running"

# Health check
curl http://localhost:11434/health
# Should return: "OK"
```

## Configuration

### Volume Mounts

The docker-compose.yml sets up two volume mounts:

1. **Config file** (`./config.json` → `/app/config/config.json`)
   - Mounted as read-only (`:ro`)
   - Contains server and backend configuration
   - Edit this file to change proxy settings

2. **Database directory** (`./data` → `/app/data`)
   - Stores the SQLite database (`llm_proxy.db`)
   - Persists request/response logs across container restarts
   - Make sure to update `config.json` with: `"database": { "path": "/app/data/llm_proxy.db" }`

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

On Linux, you may need to add this to your docker-compose.yml:

```yaml
extra_hosts:
  - "host.docker.internal:host-gateway"
```

### Database permission errors

Ensure the `data/` directory is writable:

```bash
chmod 755 data/
```

### Port already in use

If port 11434 is already taken, change it in docker-compose.yml:

```yaml
ports:
  - "11435:11434"  # Use external port 11435
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

### Using a specific config file

Override the config path:

```bash
docker run -d \
  --name llm-proxy \
  -p 11434:11434 \
  -v /path/to/custom-config.json:/app/config/config.json:ro \
  -v $(pwd)/data:/app/data \
  llm-proxy \
  -config /app/config/config.json
```

### Running with custom network

If you have other services (like a local LLM backend) in Docker:

```yaml
networks:
  llm-proxy-network:
    external: true
    name: my-existing-network
```

### Multi-stage debugging

Build without cache and see all output:

```bash
docker build --no-cache --progress=plain -t llm-proxy .
```
