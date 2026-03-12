# Multi-stage build for netscan
# Stage 1: Build the Go binary
FROM golang:1.26-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Set working directory
WORKDIR /build

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Version build argument
ARG VERSION=dev

# Build the binary with optimizations for linux/amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=${VERSION}" \
    -o netscan \
    ./cmd/netscan

# Stage 2: Create minimal runtime image
FROM alpine:latest

# Install runtime dependencies (including wget for healthcheck)
RUN apk add --no-cache ca-certificates libcap wget \
    && apk upgrade --no-cache zlib

# Create non-root user for running the service
RUN addgroup -S netscan && adduser -S netscan -G netscan

# Set working directory
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /build/netscan /app/netscan

# Copy config template
COPY config.yml.example /app/config.yml.example

# Set CAP_NET_RAW capability for ICMP access
RUN setcap cap_net_raw+ep /app/netscan

# Change ownership to non-root user
RUN chown -R netscan:netscan /app

# NOTE: Running as root is required for ICMP raw socket access in Docker
# Even with CAP_NET_RAW capability, non-root users cannot create raw ICMP sockets
# This is a limitation of the Linux kernel's security model in containerized environments
# USER netscan  # Commented out - must run as root for ICMP to work

# Set default config path (can be overridden with -config flag)
ENV CONFIG_PATH=/app/config.yml

# Expose health check port
EXPOSE 8080

# Add health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=40s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health/live || exit 1

# Run netscan
ENTRYPOINT ["/app/netscan"]
CMD ["-config", "/app/config.yml"]
