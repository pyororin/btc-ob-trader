# --- Builder Stage ---
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download all dependencies.
RUN go mod download

# Copy the source code
COPY . .

# Build the application
# CGO_ENABLED=0 is important for creating a static binary
# -o /app/report-generator specifies the output file name
RUN CGO_ENABLED=0 go build -o /app/report-generator ./cmd/report

# --- Final Stage ---
FROM alpine:latest

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/report-generator .

# Command to run the application
CMD ["./report-generator"]
