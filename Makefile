.PHONY: help up down logs shell clean test build replay monitor

# ==============================================================================
# HELP
# ==============================================================================
# Shows a list of all available commands and their descriptions.
help:
	@echo "Usage: make [command]"
	@echo ""
	@echo "Available commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Grafana Dashboard:"
	@echo "  After running 'make monitor' or 'make up', access the PnL dashboard at:"
	@echo "  \033[32mhttp://localhost:3000\033[0m (login: admin/admin)"

# ==============================================================================
# DOCKER COMPOSE
# ==============================================================================
up: ## Start all services including the bot for live trading.
	@echo "Starting all services (including trading bot)..."
	docker-compose up -d --build

monitor: ## Start monitoring services (DB, Grafana) without the bot.
	@echo "Starting monitoring services (TimescaleDB, Grafana)..."
	docker-compose up -d timescaledb grafana

down: ## Stop and remove all application stack containers.
	@echo "Stopping application stack..."
	docker-compose down

logs: ## Follow logs from the bot service.
	@echo "Following logs for 'bot' service..."
	docker-compose logs -f bot

shell: ## Access a shell inside the running bot container.
	@echo "Accessing shell in 'bot' container..."
	docker-compose exec bot /bin/sh

clean: ## Stop, remove containers, and remove volumes.
	@echo "Stopping application stack and removing volumes..."
	docker-compose down -v --remove-orphans

replay: ## Run a backtest using historical data.
	@echo "Starting replay..."
	@echo "Ensuring monitoring services are running first..."
	@make monitor
	@echo "Running replay task..."
	docker-compose run --rm --entrypoint ./obi-scalp-bot bot -replay -config /app/config/config-replay.yaml

# ==============================================================================
# GO BUILDS & TESTS
# ==============================================================================
test: ## Run Go tests.
	@echo "Running Go tests..."
	go test ./... -v

build: ## Build the Go application binary locally.
	@echo "Building Go application binary..."
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-s -w" -o build/obi-scalp-bot cmd/bot/main.go
