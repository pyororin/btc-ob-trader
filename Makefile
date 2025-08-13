# Default docker command
DOCKER_CMD = docker compose

# If JULES_ENV is set, use sudo
ifeq ($(JULES_ENV),true)
  DOCKER_CMD = sudo docker compose
endif

-include .env
export

.PHONY: help up down logs shell clean test build monitor report backup restore

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
# MAIN DOCKER COMPOSE
# ==============================================================================
up: ## Start all services. Use NO_RESTORE=1 to skip restoring DB from backup.
	@echo "Starting all services (bot, optimizer, drift-monitor)..."
	$(DOCKER_CMD) up -d --build bot drift-monitor optimizer grafana adminer report-generator
	$(MAKE) restore
	$(MAKE) migrate

update: ## Pull the latest changes and restart the bot, optimizer, and drift-monitor services.
	@echo "Pulling latest changes..."
	git pull
	$(MAKE) build
	@echo "Rebuilding and restarting services: bot, optimizer, drift-monitor..."
	$(DOCKER_CMD) up -d --build bot drift-monitor optimizer grafana adminer report-generator

migrate: ## Run database migrations
	@echo "Running database migrations..."
	$(DOCKER_CMD) exec -T timescaledb sh -c '\
	for dir in /docker-entrypoint-initdb.d/01_schema; do \
		for f in $$dir/*.sql; do \
			if [ -f "$$f" ]; then \
				echo "Applying $$f..."; \
				psql -v ON_ERROR_STOP=1 --username="$(DB_USER)" --dbname="$(DB_NAME)" -f "$$f"; \
			fi; \
		done; \
	done'

monitor: ## Start monitoring services (DB, Grafana) without the bot.
	@echo "Starting monitoring services (TimescaleDB, Grafana)..."
	$(DOCKER_CMD) up -d timescaledb grafana

down: ## Stop containers. Use NO_BACKUP=1 to skip DB backup.
	$(MAKE) backup
	@echo "Stopping application stack..."
	$(DOCKER_CMD) down

# ==============================================================================
# DATABASE BACKUP/RESTORE
# ==============================================================================
backup: ## Create a backup of the database.
ifeq ($(NO_BACKUP),1)
	@echo "Skipping database backup because NO_BACKUP is set."
else
	@echo "Backing up database..."
	@mkdir -p ./db/backup
	$(DOCKER_CMD) exec -T timescaledb pg_dump -U $(DB_USER) -d $(DB_NAME) -c > ./db/backup/backup.sql
	@echo "Database backup created at ./db/backup/backup.sql"
endif

restore: ## Restore the database from backup.
ifeq ($(NO_RESTORE),1)
	@echo "Skipping database restore because NO_RESTORE is set."
else
	@if [ -f ./db/backup/backup.sql ]; then \
		echo "Restoring database from backup..."; \
		cat ./db/backup/backup.sql | $(DOCKER_CMD) exec -T timescaledb psql -U $(DB_USER) -d $(DB_NAME); \
		echo "Database restored successfully."; \
	else \
		echo "No backup file found, skipping restore."; \
	fi
endif

logs: ## Follow logs from the bot, optimizer, and drift-monitor services.
	@echo "Following logs for 'bot', 'optimizer', and 'drift-monitor' services..."
	$(DOCKER_CMD) logs -f bot optimizer drift-monitor

shell: ## Access a shell inside the running bot container.
	@echo "Accessing shell in 'bot' container..."
	$(DOCKER_CMD) exec bot /bin/sh

clean: ## Stop, remove containers, and remove volumes.
	@echo "Stopping application stack and removing volumes..."
	$(DOCKER_CMD) down -v --remove-orphans

# ==============================================================================
# SIMULATE
# ==============================================================================
simulate: ## Run a backtest using trade data from a local CSV file.
	@echo "Running simulation task..."
	@if [ -z "$(CSV_PATH)" ]; then \
		echo "Error: CSV_PATH environment variable is not set."; \
		echo "Usage: make simulate CSV_PATH=/path/to/your/trades.csv"; \
		exit 1; \
	fi
	@mkdir -p ./simulation
	@chmod 777 ./simulation
	@if echo "$(CSV_PATH)" | grep -q ".zip$$"; then \
		echo "Unzipping $(CSV_PATH) to ./simulation..."; \
		unzip -o $(CSV_PATH) -d ./simulation; \
		UNZIPPED_CSV_PATH=/simulation/$$(basename $(CSV_PATH) .zip).csv; \
		echo "Using unzipped file: $$UNZIPPED_CSV_PATH"; \
		$(DOCKER_CMD) run --rm --no-deps \
			-v $$(pwd)/simulation:/simulation \
			bot-simulate \
			--simulate --config=config/app_config.yaml \
			--csv=$$UNZIPPED_CSV_PATH --json-output; \
	else \
		HOST_CSV_PATH=$$(realpath $(CSV_PATH)); \
		CONTAINER_CSV_PATH=/simulation/$$(basename $(CSV_PATH)); \
		$(DOCKER_CMD) run --rm --no-deps \
			-v $$HOST_CSV_PATH:$$CONTAINER_CSV_PATH \
			-v $$(pwd)/simulation:/simulation \
			bot-simulate \
			--simulate --config=config/app_config.yaml \
			--csv=$$CONTAINER_CSV_PATH --json-output; \
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
	$(DOCKER_CMD) run --rm \
		-v $$(pwd)/simulation:/app/simulation \
		-e DB_USER=$(DB_USER) \
		-e DB_PASSWORD=$(DB_PASSWORD) \
		-e DB_NAME=$(DB_NAME) \
		-e DB_HOST=$(DB_HOST) \
		builder sh -c "cd /app && go run cmd/export/main.go $$FLAGS"
	@echo "Export complete. Check the 'simulation' directory."

report: ## Generate and display a PnL report from the latest simulation CSV.
	@echo "Generating PnL report..."
	@if [ -n "$(HOURS_BEFORE)" ]; then \
		echo "Exporting data for the last $(HOURS_BEFORE) hours..."; \
		$(MAKE) export-sim-data HOURS_BEFORE=$(HOURS_BEFORE) NO_ZIP=true; \
	fi
	@SIM_CSV_PATH=$$(find simulation -name "*.csv" -print0 | xargs -0 ls -t | head -n 1); \
	if [ -z "$$SIM_CSV_PATH" ]; then \
		echo "Error: No CSV file found in simulation directory. Run 'make export-sim-data' first."; \
		exit 1; \
	fi; \
	echo "Running simulation on $$SIM_CSV_PATH..."; \
	$(DOCKER_CMD) up -d --wait report-generator; \
	SIM_OUTPUT=$$(make simulate CSV_PATH=$$SIM_CSV_PATH); \
	echo "$${SIM_OUTPUT}" | $(DOCKER_CMD) exec -T report-generator build/report

optimize: ## Manually trigger a 'scheduled' optimization run.
	@echo "Manually triggering a 'scheduled' optimization run..."
	@echo "Ensure services are running with 'make monitor' or 'make up'."
	@mkdir -p ./data/params
	@if [ -f "./data/params/optimization_job.json" ]; then \
		echo "Error: An optimization job is already in progress. Please wait for it to complete or remove the job file."; \
		exit 1; \
	fi
	@echo '{"trigger_type": "manual", "window_is_hours": 4, "window_oos_hours": 1, "timestamp": '$(shell date +%s)'}' > ./data/params/optimization_job.json
	@echo "Job file created. Tailing optimizer logs..."
	@echo "Press Ctrl+C to stop tailing."
	@$(DOCKER_CMD) logs -f optimizer

force_optimize: ## Force a new optimization run by removing any existing job file.
	@echo "Forcibly starting a new optimization run..."
	@echo "Ensure services are running with 'make monitor' or 'make up'."
	@rm -f ./data/params/optimization_job.json
	$(MAKE) optimize

# ==============================================================================
# GO BUILDS & TESTS
# ==============================================================================
# Define a helper to run commands inside a temporary Go builder container
DOCKER_RUN_GO = $(DOCKER_CMD) run --rm --service-ports --entrypoint "" -v /var/run/docker.sock:/var/run/docker.sock builder

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

local-build: ## Build the Go application binary locally without Docker.
	@echo "Building Go application binary locally..."
	@mkdir -p build
	go build -ldflags="-s -w" -o build/obi-scalp-bot cmd/bot/main.go
	go build -ldflags="-s -w" -o build/report cmd/report/main.go

build: ## Build the Go application binary inside the container.
	@echo "Building Go application binary..."
	@mkdir -p build
	$(DOCKER_RUN_GO) go build -a -ldflags="-s -w" -o build/obi-scalp-bot cmd/bot/main.go
	$(DOCKER_RUN_GO) go build -a -ldflags="-s -w" -o build/report cmd/report/main.go

build-image: ## Build the Docker image for the bot.
	@echo "Building Docker image..."
	docker build -t obi-scalp-bot-image:latest .

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

tools: ## Install required local tools (jb, jsonnet).
	@echo "Skipping tool installation in this environment."

vendor: ## Install jsonnet dependencies into the vendor directory.
	@echo "Installing jsonnet dependencies..."
	@cd $(JSONNET_DIR) && go run github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb@latest install

grafana-render: vendor ## Render Grafana dashboards from Jsonnet to JSON.
	@echo "Rendering Grafana dashboards..."
	@mkdir -p $(DASHBOARDS_DIR)
	@for file in $(JSONNET_FILES); do \
		output_file="$(DASHBOARDS_DIR)/$$(basename $${file%.jsonnet}.json)"; \
		echo "Rendering $$file -> $$output_file"; \
		go run github.com/google/go-jsonnet/cmd/jsonnet@latest -J $(JSONNET_DIR)/vendor $$file > $$output_file; \
	done

grafana-lint: ## Lint and validate Grafana dashboards.
	@echo "Linting and validating Grafana dashboards..."
	@if ! [ -d "grafana/jsonnet/vendor" ]; then \
		echo "Vendor directory not found. Running 'make vendor' first..."; \
		$(MAKE) vendor; \
	fi
	$(DOCKER_RUN_GO) go test -v ./grafana/jsonnet/...
