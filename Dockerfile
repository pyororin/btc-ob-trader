# Stage 1: Build the Go application
FROM golang:1.24.3-alpine AS builder

WORKDIR /app

# Install git for fetching dependencies if needed, and ca-certificates for HTTPS
RUN apk add --no-cache git ca-certificates

# Copy go.mod and go.sum files
COPY go.mod go.sum ./
# Download dependencies
RUN go mod download

# Copy the entire source code
COPY . .

# Build the application
# CGO_ENABLED=0 produces a statically linked binary
# -ldflags="-s -w" strips debug information, reducing binary size
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-s -w" -o /go/bin/obi-scalp-bot cmd/bot/main.go

# Stage 2: Create the final lightweight image
FROM alpine:latest

WORKDIR /app

# Install ca-certificates for HTTPS and curl for healthchecks
RUN apk add --no-cache ca-certificates curl

# Copy the config directory and .env.sample
# Note: .env.sample is for reference; actual .env or environment variables should be used for secrets
COPY --from=builder /app/config ./config
COPY --from=builder /app/.env.sample ./.env.sample
# It's good practice to also copy the .env file if it's present and intended for the container
# For production, secrets should be handled via environment variables or secret management systems
# COPY .env ./.env

# Copy the built binary from the builder stage
COPY --from=builder /go/bin/obi-scalp-bot /usr/local/bin/obi-scalp-bot

# Create a non-root user and group for security
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Switch to the non-root user
USER appuser

# Expose the port the bot might use for health checks (if any, specified in config later)
# EXPOSE 8080

# Command to run the application
# The default config path is "config/config.yaml", which will be relative to WORKDIR /app
ENTRYPOINT ["/usr/local/bin/obi-scalp-bot"]
# Optionally, allow overriding the config path via docker run arguments
# CMD ["-config", "config/config.yaml"]
# Using ENTRYPOINT makes the container behave like an executable.
# If CMD is also specified with ENTRYPOINT ["executable", "param1"], CMD provides default arguments to ENTRYPOINT.
# If the user provides arguments to `docker run`, those will override CMD.
# Example: docker run my-bot -config some/other/config.yaml
