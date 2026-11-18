# syntax=docker/dockerfile:1

# ---- Build stage: full Go toolchain, never shipped -------------------------
FROM golang:1.24-alpine AS builder
WORKDIR /src

# Dependencies are downloaded in their own layer, so they are only fetched
# again when go.mod or go.sum changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is disabled to get a static binary; -s -w strips debug information.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---- Runtime stage: only the binary on a minimal base ----------------------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app && adduser -S app -G app

WORKDIR /app
COPY --from=builder /out/api /app/api

# Run as an unprivileged user.
USER app
EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${APP_PORT:-8080}/health" >/dev/null || exit 1

ENTRYPOINT ["/app/api"]
