.PHONY: help up down logs shell clean test build replay

# ==============================================================================
# HELP
# ==============================================================================
# Shows a list of all available commands and their descriptions.
# Descriptions are extracted from comments following each target.
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ==============================================================================
# DOCKER COMPOSE
# ==============================================================================
up: ## Start the application stack in detached mode.
	@echo "Starting OBI Scalp Bot..."
	docker-compose up -d --build

down: ## Stop and remove the application stack.
	@echo "Stopping OBI Scalp Bot..."
	docker-compose down

logs: ## Follow logs from the bot service.
	@echo "Following logs for 'bot' service..."
	docker-compose logs -f bot

shell: ## Access a shell inside the running bot container.
	@echo "Accessing shell in 'bot' container..."
	docker-compose exec bot /bin/sh

clean: ## Stop and remove containers, and remove volumes.
	@echo "Stopping OBI Scalp Bot and removing volumes..."
	docker-compose down -v --remove-orphans

replay: ## Replay historical tick data for backtesting.
	@echo "Starting replay..."
	docker-compose run --rm bot ./obi-scalp-bot -replay -config config/config-replay.yaml

# ==============================================================================
# GO BUILDS & TESTS
# ==============================================================================
test: ## Run Go tests.
	@echo "Running Go tests..."
	go test ./... -v

build: ## Build the Go application binary locally.
	@echo "Building Go application binary..."
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-s -w" -o build/obi-scalp-bot cmd/bot/main.go
