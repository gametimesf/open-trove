# syntax=docker/dockerfile:1.7

# ─── Builder ─────────────────────────────────────────────────────
FROM golang:1.26.8-alpine AS builder
WORKDIR /app

RUN apk add --no-cache git

# Deps first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . .
# Optional files are relative to the build context and never fetched remotely.
ARG LLMS_TXT_APPEND=""
ARG LLMS_TXT_OVERRIDE=""
RUN go run ./cmd/build-llms -append "$LLMS_TXT_APPEND" -override "$LLMS_TXT_OVERRIDE"
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
