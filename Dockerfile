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
FROM scratch

# Copy the static binary from the builder stage
COPY --from=builder /go/bin/obi-scalp-bot /obi-scalp-bot
# Copy ca-certificates for TLS
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Command to run the application
ENTRYPOINT ["/obi-scalp-bot"]
