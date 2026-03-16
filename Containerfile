# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /src

# Install build dependencies (git for private modules if needed)
RUN apk add --no-cache git ca-certificates

# Download dependencies first (better layer caching)
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w -extldflags '-static'" \
    -a -installsuffix cgo \
    -o /glitchgate .

# Final stage - minimal alpine image
FROM alpine:3.21

# OCI labels
LABEL org.opencontainers.image.title="glitchgate"
LABEL org.opencontainers.image.description="LLM API reverse proxy with format translation"
LABEL org.opencontainers.image.source="https://github.com/seckatie/glitchgate"
LABEL org.opencontainers.image.licenses="AGPL-3.0"

# Install ca-certificates for HTTPS requests to upstream providers
RUN apk add --no-cache ca-certificates && \
    addgroup -S -g 65532 glitchgate && \
    adduser -S -G glitchgate -u 65532 glitchgate

COPY --from=builder /glitchgate /usr/local/bin/glitchgate

# Default data directory — mount a volume here for persistent DB and config
WORKDIR /data
RUN chown -R glitchgate:glitchgate /data

USER glitchgate:glitchgate

EXPOSE 4000

# Health check using the built-in /health endpoint
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["wget", "--no-verbose", "--tries=1", "--spider", "http://localhost:4000/health"]

VOLUME ["/data"]

ENTRYPOINT ["glitchgate"]
CMD ["serve", "--config", "/data/config.yaml"]
