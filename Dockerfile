# ---- Builder ----
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install git for fetching dependencies if needed, and ca-certificates for HTTPS
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod tidy
RUN go mod download

COPY . .

# Build a static binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-s -w" -o /go/bin/obi-scalp-bot cmd/bot/main.go

# ---- Final Image ----
FROM alpine:latest

WORKDIR /app

# Copy the entrypoint script with execute permissions
COPY --chmod=755 --from=builder /app/entrypoint.sh /app/entrypoint.sh

# Copy the static binary from the builder stage to a standard location
COPY --from=builder /go/bin/obi-scalp-bot /usr/local/bin/obi-scalp-bot

# Copy ca-certificates for TLS
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Set the entrypoint to our script
ENTRYPOINT ["/app/entrypoint.sh"]
