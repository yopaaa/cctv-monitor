# ============================
# Stage 1: Build Go Binary
# ============================
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build static binary with embedded web UI
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /cctv-monitor .

# ============================
# Stage 2: Runtime Image
# ============================
FROM alpine:3.20

RUN apk add --no-cache \
    ffmpeg \
    ca-certificates \
    tzdata

# Create non-root user
RUN adduser -D -h /app cctv
WORKDIR /app

# Copy binary from builder
COPY --from=builder /cctv-monitor /app/cctv-monitor

# Default config (user should mount their own)
COPY config.yaml /app/config.yaml

# Create streams directory
RUN mkdir -p /app/streams && chown -R cctv:cctv /app

USER cctv

EXPOSE 8080

VOLUME ["/app/streams"]

ENTRYPOINT ["/app/cctv-monitor"]
CMD ["-config", "/app/config.yaml"]
