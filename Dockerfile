FROM golang:1.24-alpine AS builder

ARG CI=false
WORKDIR /build

# Dependencies first (cache layer)
COPY go.mod go.sum ./
RUN go mod download

# Source
COPY . .

# Build — pure-Go, no CGO needed (glebarez/sqlite for tests, postgres for prod)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /app/server ./cmd/server

# ── Runtime ──────────────────────────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates for HTTPS to Copilot API
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app

WORKDIR /app

COPY --from=builder /app/server ./server
COPY migrations/ ./migrations/

USER app
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["./server"]
