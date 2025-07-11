.PHONY: up down logs shell replay clean test build

# Default action: display help (optional, but good practice)
help:
	@echo "Available commands:"
	@echo "  make up        - Start the bot application in detached mode (builds if necessary)"
	@echo "  make down      - Stop and remove the bot application containers"
	@echo "  make logs      - Follow logs from the bot container"
	@echo "  make shell     - Access a shell inside the running bot container"
	@echo "  make clean     - Stop and remove containers, and remove volumes"
	@echo "  make test      - Run Go tests (TODO)"
	@echo "  make build     - Build the Go application binary locally (TODO)"
	@echo "  make replay    - Replay historical tick data for backtesting (TODO)"

# Start application stack in detached mode
up:
	@echo "Starting OBI Scalp Bot..."
	docker-compose up -d --build

# Stop and remove application stack
down:
	@echo "Stopping OBI Scalp Bot..."
	docker-compose down

# Follow logs from the bot service
logs:
	@echo "Following logs for 'bot' service..."
	docker-compose logs -f bot

# Access a shell inside the running bot container
shell:
	@echo "Accessing shell in 'bot' container..."
	docker-compose exec bot /bin/sh

# Stop, remove containers, and remove volumes (useful for a clean restart)
clean:
	@echo "Stopping OBI Scalp Bot and removing volumes..."
	docker-compose down -v --remove-orphans

# TODO: Run Go tests
test:
	@echo "Running Go tests..."
	# go test ./... -v

# TODO: Build the Go application binary locally
build:
	@echo "Building Go application binary..."
	# CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-s -w" -o build/obi-scalp-bot cmd/bot/main.go

# TODO: Replay historical tick data for backtesting
replay:
	@echo "Replay function is not yet implemented."
	# docker-compose run --rm bot ./obi-scalp-bot -replay -config config/config-replay.yaml
	# Or some other mechanism to trigger replay mode

# Ensure .env file exists, copy from .env.sample if not
# This can be added to targets like 'up' if needed, or run manually.
# Example:
# up: .env
# .env:
#	@echo "Creating .env from .env.sample..."
#	@cp -n .env.sample .env || true
# The `-n` flag to cp prevents overwriting if .env already exists.
# `|| true` ensures the command doesn't fail if .env exists and cp -n does nothing.
# For now, we assume the user manages .env manually as per README instructions.
