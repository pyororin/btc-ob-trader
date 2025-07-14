-include .env
export

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
	sudo -E docker compose up -d --build

monitor: ## Start monitoring services (DB, Grafana) without the bot.
	@echo "Starting monitoring services (TimescaleDB, Grafana)..."
	sudo -E docker compose up -d timescaledb grafana

down: ## Stop and remove all application stack containers.
	@echo "Stopping application stack..."
	sudo -E docker compose down

logs: ## Follow logs from the bot service.
	@echo "Following logs for 'bot' service..."
	sudo -E docker compose logs -f bot

shell: ## Access a shell inside the running bot container.
	@echo "Accessing shell in 'bot' container..."
	sudo -E docker compose exec bot /bin/sh

clean: ## Stop, remove containers, and remove volumes.
	@echo "Stopping application stack and removing volumes..."
	sudo -E docker compose down -v --remove-orphans

replay: ## Run a backtest using historical data from the database.
	@echo "Running replay task..."
	sudo -E docker compose run --build --rm bot-replay

simulate: ## Run a backtest using trade data from a local CSV file.
	@echo "Running simulation task..."
	@if [ -z "$(CSV_PATH)" ]; then \
		echo "Error: CSV_PATH environment variable is not set."; \
		echo "Usage: make simulate CSV_PATH=/path/to/your/trades.csv"; \
		exit 1; \
	fi
	sudo -E docker compose run --build --rm --entrypoint "go" bot-replay run cmd/bot/main.go --simulate --csv=$(CSV_PATH) --config=config/config.yaml

export-sim-data: ## Export order book data from the database to a CSV file for simulation.
	@echo "Exporting simulation data..."
	@mkdir -p ./simulation
	@if [ -z "$(START_TIME)" ] || [ -z "$(END_TIME)" ]; then \
		echo "Error: START_TIME and END_TIME environment variables must be set."; \
		echo "Usage: make export-sim-data START_TIME='YYYY-MM-DD HH:MM:SS' END_TIME='YYYY-MM-DD HH:MM:SS'"; \
		exit 1; \
	fi
	@FILENAME="simulation/order_book_updates_$$(date +%Y%m%d-%H%M%S).csv"; \
	echo "Exporting data to $$FILENAME..."; \
	sudo -E docker compose run --rm \
		-e DB_USER=$(DB_USER) \
		-e DB_PASSWORD=$(DB_PASSWORD) \
		-e DB_NAME=$(DB_NAME) \
		builder go run cmd/export/main.go --start "$(START_TIME)" --end "$(END_TIME)" > $$FILENAME
	@echo "Export complete. See $$FILENAME";

# ==============================================================================
# GO BUILDS & TESTS
# ==============================================================================
# Define a helper to run commands inside a temporary Go builder container
DOCKER_RUN_GO = sudo -E docker compose run --rm --service-ports --entrypoint "" builder

test: ## Run Go tests inside the container.
	@echo "Running Go tests..."
	$(DOCKER_RUN_GO) go test ./... -v

build: ## Build the Go application binary inside the container.
	@echo "Building Go application binary..."
	@mkdir -p build
	$(DOCKER_RUN_GO) go build -a -ldflags="-s -w" -o build/obi-scalp-bot cmd/bot/main.go

report: ## Generate and display the PnL report.
	@echo "Starting report generator service..."
	sudo -E docker compose up -d --build report-generator
	@echo "Generating PnL report..."
	sudo -E docker compose exec report-generator go build -o build/report cmd/report/main.go
	@echo "Running PnL report..."
	sudo -E docker compose exec report-generator ./build/report
