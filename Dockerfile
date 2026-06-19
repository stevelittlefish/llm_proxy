# Build stage
FROM golang:1.25-bookworm AS builder

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 go build -o llm_proxy .

# Runtime stage
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
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
