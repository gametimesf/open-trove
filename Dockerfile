# syntax=docker/dockerfile:1.7

# ─── Builder ─────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder
WORKDIR /app

# System deps first (layer stays cached regardless of GOPRIVATE value)
RUN apk add --no-cache git

# Private modules (if needed)
# No private modules needed

# Deps first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/server ./cmd/server

# ─── Runtime ─────────────────────────────────────────────────────
FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
COPY --from=builder /bin/server /bin/server
COPY --from=builder /app/docs /app/docs
COPY --from=builder /app/trove*.yaml /app/
WORKDIR /app
USER appuser

EXPOSE 8080
ENTRYPOINT ["/bin/server"]
