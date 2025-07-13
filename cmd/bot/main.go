// Package main is the entry point of the OBI Scalping Bot.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/datastore"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/http/handler"
	"github.com/your-org/obi-scalp-bot/internal/indicator"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
	"go.uber.org/zap"
)

type flags struct {
	configPath string
	replayMode bool
}

func main() {
	// --- Initialization ---
	f := parseFlags()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := setupConfig(f.configPath)
	setupLogger(cfg.LogLevel, f.configPath, cfg.Pair)

	if !f.replayMode {
		startHealthCheckServer()
	}

	dbWriter := setupDBWriter(ctx, cfg)
	if dbWriter != nil {
		defer dbWriter.Close()
	}

	// --- Setup Handlers and Indicators ---
	orderBook := indicator.NewOrderBook()
	obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
	orderBookHandler, tradeHandler := setupHandlers(orderBook, dbWriter)

	// --- Start Services ---
	obiCalculator.Start(ctx)
	go processOBICalculations(ctx, obiCalculator)

	// --- Main Execution Loop ---
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	runMainLoop(ctx, f, cfg, orderBookHandler, tradeHandler, sigs)

	// --- Graceful Shutdown ---
	waitForShutdownSignal(sigs)
	logger.Info("Initiating graceful shutdown...")
	cancel()
	time.Sleep(1 * time.Second) // Allow time for services to shut down
	logger.Info("OBI Scalping Bot shut down gracefully.")
}

// parseFlags parses command-line flags.
func parseFlags() flags {
	configPath := flag.String("config", "config/config.yaml", "Path to the configuration file")
	replayMode := flag.Bool("replay", false, "Enable replay mode")
	flag.Parse()
	return flags{configPath: *configPath, replayMode: *replayMode}
}

// setupConfig loads the application configuration.
func setupConfig(configPath string) *config.Config {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

// setupLogger initializes the global logger.
func setupLogger(logLevel, configPath, pair string) {
	logger.SetGlobalLogLevel(logLevel)
	logger.Info("OBI Scalping Bot starting...")
	logger.Infof("Loaded configuration from: %s", configPath)
	logger.Infof("Target pair: %s", pair)
}

// startHealthCheckServer starts the HTTP server for health checks.
func startHealthCheckServer() {
	go func() {
		http.HandleFunc("/health", handler.HealthCheckHandler)
		logger.Info("Health check server starting on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			logger.Fatalf("Health check server failed: %v", err)
		}
	}()
}

// setupDBWriter initializes the TimescaleDB writer if enabled.
func setupDBWriter(ctx context.Context, cfg *config.Config) *dbwriter.Writer {
	if cfg.DBWriter.BatchSize <= 0 {
		return nil
	}

	var zapLogger *zap.Logger
	var zapErr error
	if cfg.LogLevel == "debug" {
		zapLogger, zapErr = zap.NewDevelopment()
	} else {
		zapLogger, zapErr = zap.NewProduction()
	}
	if zapErr != nil {
		logger.Fatalf("Failed to initialize Zap logger for DBWriter: %v", zapErr)
	}
	// It's idiomatic to handle the sync in main's defer, but since we are encapsulating,
	// we will rely on the application's main defer to handle process-wide concerns.
	// A more robust solution might involve a dedicated lifecycle management component.

	logger.Infof("Initializing DBWriter with config: BatchSize=%d, WriteIntervalSeconds=%d", cfg.DBWriter.BatchSize, cfg.DBWriter.WriteIntervalSeconds)

	dbWriter, err := dbwriter.NewWriter(ctx, cfg.Database, cfg.DBWriter, zapLogger)
	if err != nil {
		logger.Fatalf("Failed to initialize TimescaleDB writer: %v", err)
	}
	logger.Info("TimescaleDB writer initialized successfully.")
	return dbWriter
}

// setupHandlers creates and returns the handlers for order book and trade data.
func setupHandlers(orderBook *indicator.OrderBook, dbWriter *dbwriter.Writer) (coincheck.OrderBookHandler, coincheck.TradeHandler) {
	orderBookHandler := func(data coincheck.OrderBookData) {
		orderBook.ApplyUpdate(data)
	}

	tradeHandler := func(data coincheck.TradeData) {
		logger.Debugf("Trade: Pair=%s, Side=%s, Price=%s, Amount=%s", data.Pair(), data.TakerSide(), data.Rate(), data.Amount())
		if dbWriter == nil {
			return
		}

		price, err := strconv.ParseFloat(data.Rate(), 64)
		if err != nil {
			logger.Errorf("Failed to parse trade price: %v", err)
			return
		}
		size, err := strconv.ParseFloat(data.Amount(), 64)
		if err != nil {
			logger.Errorf("Failed to parse trade size: %v", err)
			return
		}
		txID, err := strconv.ParseInt(data.TransactionID(), 10, 64)
		if err != nil {
			logger.Errorf("Failed to parse transaction ID: %v", err)
			return
		}

		trade := dbwriter.Trade{
			Time:          time.Now().UTC(),
			Pair:          data.Pair(),
			Side:          data.TakerSide(),
			Price:         price,
			Size:          size,
			TransactionID: txID,
		}
		dbWriter.SaveTrade(trade)
	}

	return orderBookHandler, tradeHandler
}

// processOBICalculations subscribes to and logs OBI calculation results.
func processOBICalculations(ctx context.Context, obiCalculator *indicator.OBICalculator) {
	go func() {
		resultsCh := obiCalculator.Subscribe()
		for {
			select {
			case <-ctx.Done():
				logger.Info("OBI processing goroutine shutting down.")
				return
			case result := <-resultsCh:
				logger.Infof("OBI Calculated: OBI8=%.4f, OBI16=%.4f, Timestamp=%v", result.OBI8, result.OBI16, result.Timestamp)
			}
		}
	}()
}

// runMainLoop starts either the live trading or replay mode.
func runMainLoop(ctx context.Context, f flags, cfg *config.Config, obHandler coincheck.OrderBookHandler, tradeHandler coincheck.TradeHandler, sigs chan<- os.Signal) {
	if f.replayMode {
		go runReplay(ctx, cfg, obHandler, tradeHandler, sigs)
	} else {
		wsClient := coincheck.NewWebSocketClient(obHandler, tradeHandler)
		go func() {
			logger.Info("Connecting to Coincheck WebSocket API...")
			if err := wsClient.Connect(); err != nil {
				logger.Errorf("WebSocket client exited with error: %v", err)
				sigs <- syscall.SIGTERM // Trigger shutdown on connection error
			}
		}()
	}
}

// waitForShutdownSignal blocks until a shutdown signal is received.
func waitForShutdownSignal(sigs <-chan os.Signal) {
	sig := <-sigs
	logger.Infof("Received signal: %s", sig)
}

// runReplay runs the backtest simulation using data from the database.
func runReplay(ctx context.Context, cfg *config.Config, obHandler coincheck.OrderBookHandler, tradeHandler coincheck.TradeHandler, sigs chan<- os.Signal) {
	logger.Info("Starting replay mode...")

	// --- DB Connection ---
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.Database.User, cfg.Database.Password, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name, cfg.Database.SSLMode)
	dbpool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Fatalf("Unable to connect to database: %v", err)
	}
	defer dbpool.Close()

	repo := datastore.NewRepository(dbpool)

	// --- Time Range ---
	startTime, err := time.Parse(time.RFC3339, cfg.Replay.StartTime)
	if err != nil {
		logger.Fatalf("Invalid start_time format: %v", err)
	}
	endTime, err := time.Parse(time.RFC3339, cfg.Replay.EndTime)
	if err != nil {
		logger.Fatalf("Invalid end_time format: %v", err)
	}
	logger.Infof("Fetching data from %s to %s for pair %s", startTime, endTime, cfg.Pair)

	// --- Fetch Events ---
	events, err := repo.FetchMarketEvents(ctx, cfg.Pair, startTime, endTime)
	if err != nil {
		logger.Fatalf("Failed to fetch market events: %v", err)
	}
	if len(events) == 0 {
		logger.Infof("No market events found in the specified time range.")
		sigs <- syscall.SIGTERM
		return
	}
	logger.Infof("Fetched %d market events.", len(events))

	// --- Event Loop ---
	go func() {
		defer func() {
			logger.Info("Replay finished.")
			sigs <- syscall.SIGTERM // Notify main thread to exit
		}()

		for i, event := range events {
			select {
			case <-ctx.Done():
				logger.Info("Replay cancelled.")
				return
			default:
				// Process event
				switch e := event.(type) {
				case datastore.TradeEvent:
					tradeHandler(e.Trade)
					logger.Debugf("Processed Trade: Time=%s, Price=%s, Size=%s", e.GetTime(), e.Trade.Rate(), e.Trade.Amount())
				case datastore.OrderBookEvent:
					obHandler(e.OrderBook)
					logger.Debugf("Processed OrderBook: Time=%s, Bids=%d, Asks=%d", e.GetTime(), len(e.OrderBook.Bids), len(e.OrderBook.Asks))
				}
				logger.Infof("Processed event %d/%d", i+1, len(events))
			}
		}
	}()
}
