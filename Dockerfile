# Build stage
FROM golang:1.21-bookworm AS builder

# Install build dependencies for SQLite
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libc6-dev \
    libsqlite3-dev \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
# CGO is required for SQLite
RUN CGO_ENABLED=1 go build -o llm_proxy .

# Runtime stage
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libsqlite3-0 \
    wget \
    && rm -rf /var/lib/apt/lists/*

# Create app directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/llm_proxy .

# Create directories for data and config
RUN mkdir -p /app/data /app/config

# Expose the default Ollama port
EXPOSE 11434

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:11434/health || exit 1

# Run the application
ENTRYPOINT ["/app/llm_proxy"]
CMD ["-config", "/app/config/config.toml"]
