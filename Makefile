-include .env
export

.PHONY: help up down logs shell clean test build monitor report

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
	$(MAKE) migrate

migrate: ## Run database migrations
	@echo "Running database migrations..."
	@sudo -E docker compose exec -T timescaledb sh -c '\
	for dir in /docker-entrypoint-initdb.d/01_schema /docker-entrypoint-initdb.d/02_migrations; do \
		for f in $$dir/*.sql; do \
			if [ -f "$$f" ]; then \
				echo "Applying $$f..."; \
				psql -v ON_ERROR_STOP=1 --username="bot" --dbname="coincheck_data" -f "$$f"; \
			fi; \
		done; \
	done'

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

simulate: build-image ## Run a backtest using trade data from a local CSV file.
	@echo "Running simulation task..."
	@if [ -z "$(CSV_PATH)" ]; then \
		echo "Error: CSV_PATH environment variable is not set."; \
		echo "Usage: make simulate CSV_PATH=/path/to/your/trades.csv"; \
		exit 1; \
	fi
	@mkdir -p ./simulation
	@if echo "$(CSV_PATH)" | grep -q ".zip$$"; then \
		echo "Unzipping $(CSV_PATH) to ./simulation..."; \
		unzip -o $(CSV_PATH) -d ./simulation; \
		UNZIPPED_CSV_PATH=/simulation/$$(basename $(CSV_PATH) .zip).csv; \
		echo "Using unzipped file: $$UNZIPPED_CSV_PATH"; \
		sudo -E docker compose run --rm --no-deps \
			-v $$(pwd)/simulation:/simulation \
			bot-simulate \
			--simulate --config=config/app_config.yaml \
			--csv=$$UNZIPPED_CSV_PATH; \
	else \
		HOST_CSV_PATH=$$(realpath $(CSV_PATH)); \
		CONTAINER_CSV_PATH=/simulation/$$(basename $(CSV_PATH)); \
		sudo -E docker compose run --rm --no-deps \
			-v $$HOST_CSV_PATH:$$CONTAINER_CSV_PATH \
			-v $$(pwd)/simulation:/simulation \
			bot-simulate \
			--simulate --config=config/app_config.yaml \
			--csv=$$CONTAINER_CSV_PATH; \
	fi

export-sim-data: ## Export order book data. Use HOURS_BEFORE or START_TIME/END_TIME.
	@echo "Exporting simulation data..."
	@mkdir -p ./simulation
	@FLAGS=""; \
	if [ -n "$(HOURS_BEFORE)" ]; then \
		FLAGS="--hours-before=$(HOURS_BEFORE)"; \
	elif [ -n "$(START_TIME)" ] && [ -n "$(END_TIME)" ]; then \
		FLAGS="--start='$(START_TIME)' --end='$(END_TIME)'"; \
	else \
		echo "Error: Please set either HOURS_BEFORE or both START_TIME and END_TIME."; \
		echo "Usage: make export-sim-data HOURS_BEFORE=24"; \
		echo "   or: make export-sim-data START_TIME='YYYY-MM-DD HH:MM:SS' END_TIME='YYYY-MM-DD HH:MM:SS'"; \
		exit 1; \
	fi; \
	if [ "$(NO_ZIP)" = "true" ]; then \
		FLAGS="$$FLAGS --no-zip"; \
	fi; \
	echo "Running export with flags: $$FLAGS"; \
	sudo -E docker compose run --rm \
		-v $$(pwd)/simulation:/app/simulation \
		-e DB_USER=$(DB_USER) \
		-e DB_PASSWORD=$(DB_PASSWORD) \
		-e DB_NAME=$(DB_NAME) \
		builder sh -c "cd /app && go run cmd/export/main.go $$FLAGS"
	@echo "Export complete. Check the 'simulation' directory."

# ==============================================================================
# GO BUILDS & TESTS
# ==============================================================================
# Define a helper to run commands inside a temporary Go builder container
DOCKER_RUN_GO = sudo -E docker compose run --rm --service-ports --entrypoint "" builder

test: ## Run standard Go tests (excluding DB-dependent tests).
	@echo "Running standard Go tests..."
	$(DOCKER_RUN_GO) go test -tags="" -v ./...

sqltest: ## Run tests that require a database connection.
	@echo "Running database integration tests..."
	@echo "Make sure TimescaleDB is running ('make monitor')."
	$(DOCKER_RUN_GO) go test -tags=sqltest -v ./db/schema/...

local-test: ## Run Go tests locally without Docker.
	@echo "Running Go tests locally..."
	@if [ -f .env ]; then \
		export $$(cat .env | grep -v '#' | xargs); \
	fi
	go test ./... -v

build: ## Build the Go application binary inside the container.
	@echo "Building Go application binary..."
	@mkdir -p build
	$(DOCKER_RUN_GO) go build -a -ldflags="-s -w" -o build/obi-scalp-bot cmd/bot/main.go

build-image: ## Build the Docker image for the bot.
	@echo "Building Docker image..."
	sudo -E docker build -t obi-scalp-bot-image:latest .

# ==============================================================================
# GRAFANA DASHBOARDS
# ==============================================================================
.PHONY: grafana-render grafana-lint vendor tools

GRAFANA_DIR = ./grafana
DASHBOARDS_DIR = $(GRAFANA_DIR)/dashboards
JSONNET_DIR = $(GRAFANA_DIR)/jsonnet
JSONNET_FILES = $(wildcard $(JSONNET_DIR)/*.jsonnet)
JSON_FILES = $(patsubst $(JSONNET_DIR)/%.jsonnet,$(DASHBOARDS_DIR)/%.json,$(JSONNET_FILES))
JB_EXE = $(shell pwd)/bin/jb
JSONNET_EXE = $(shell pwd)/bin/jsonnet

tools: $(JB_EXE) $(JSONNET_EXE) ## Install required local tools (jb, jsonnet).

$(JB_EXE):
	@echo "Installing jsonnet-bundler (jb)..."
	@mkdir -p ./bin
	@go install github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb@latest
	@mv $$(go env GOPATH)/bin/jb ./bin/

$(JSONNET_EXE):
	@echo "Installing jsonnet..."
	@mkdir -p ./bin
	@go install github.com/google/go-jsonnet/cmd/jsonnet@latest
	@mv $$(go env GOPATH)/bin/jsonnet ./bin/

vendor: tools ## Install jsonnet dependencies into the vendor directory.
	@echo "Installing jsonnet dependencies..."
	@cd $(JSONNET_DIR) && $(JB_EXE) install

grafana-render: vendor ## Render Grafana dashboards from Jsonnet to JSON.
	@echo "Rendering Grafana dashboards..."
	@mkdir -p $(DASHBOARDS_DIR)
	@for file in $(JSONNET_FILES); do \
		output_file="$(DASHBOARDS_DIR)/$$(basename $${file%.jsonnet}.json)"; \
		echo "Rendering $$file -> $$output_file"; \
		$(JSONNET_EXE) -J $(JSONNET_DIR)/vendor $$file > $$output_file; \
	done

grafana-lint: vendor ## Lint and validate Grafana dashboards.
	@echo "Linting and validating Grafana dashboards..."
	$(DOCKER_RUN_GO) go test -v ./grafana/jsonnet/...

report: ## Generate and display the PnL report.
	@echo "Starting report generator service..."
	sudo -E docker compose up -d --build report-generator
	@echo "Generating PnL report..."
	sudo -E docker compose exec report-generator go build -o build/report cmd/report/main.go
	@echo "Running PnL report..."
	sudo -E docker compose exec report-generator ./build/report

optimize: ## Run hyperparameter optimization using Goptuna.
	@echo "Running hyperparameter optimization..."
	@if [ -z "$(CSV_PATH)" ]; then \
		echo "Error: CSV_PATH environment variable is not set."; \
		echo "Usage: make optimize CSV_PATH=/path/to/your/trades.csv"; \
		exit 1; \
	fi
	sudo -E docker compose run --rm optimizer
