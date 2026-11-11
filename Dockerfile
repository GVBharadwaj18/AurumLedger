# Multi-stage Dockerfile for AurumLedger
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Copy dependency specifications
COPY go.mod go.sum ./
COPY vendor ./vendor

# Copy source code
COPY . .

# Build statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -ldflags="-w -s" -o /aurumledger-server ./cmd/server

# Final lightweight runtime image
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata curl

WORKDIR /root/

COPY --from=builder /aurumledger-server .
COPY .env.example .env

EXPOSE 8080

HEALTHCHECK --interval=5s --timeout=3s --retries=3 \
  CMD curl -f http://localhost:8080/health || exit 1

ENTRYPOINT ["./aurumledger-server"]

