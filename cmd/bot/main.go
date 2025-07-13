// Package main is the entry point of the OBI Scalping Bot.
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/config"
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

// runReplay reads trade data from a CSV file and simulates the market for backtesting.
func runReplay(ctx context.Context, cfg *config.Config, obHandler coincheck.OrderBookHandler, tradeHandler coincheck.TradeHandler, sigs chan<- os.Signal) {
	logger.Info("Starting replay mode...")

	file, err := os.Open(cfg.Replay.CSVPath)
	if err != nil {
		logger.Fatalf("Failed to open replay CSV file: %v", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	// ヘッダーを読み飛ばす
	if _, err := reader.Read(); err != nil {
		logger.Fatalf("Failed to read header from CSV: %v", err)
	}

	go func() {
		defer func() {
			logger.Info("Replay finished.")
			sigs <- syscall.SIGTERM // 完了を通知
		}()

		for {
			record, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				logger.Errorf("Error reading CSV record: %v", err)
				continue
			}

			tradeData, err := parseTradeData(record, cfg.Pair)
			if err != nil {
				logger.Errorf("Failed to parse trade data: %v", err)
				continue
			}

			tradeHandler(tradeData)
			logger.Debugf("Processed trade: ID=%s, Rate=%s, Amount=%s", tradeData.TransactionID(), tradeData.Rate(), tradeData.Amount())

			// Simulate some delay
			time.Sleep(10 * time.Millisecond)

			select {
			case <-ctx.Done():
				logger.Info("Replay cancelled.")
				return
			default:
			}
		}
	}()
}

// parseTradeData converts a CSV record to a coincheck.TradeData object.
// CSV format: [transaction_id, pair, rate, amount, taker_side]
// Our sample CSV has: id,order_id,created_at,amount,rate,side
func parseTradeData(record []string, pair string) (coincheck.TradeData, error) {
	if len(record) < 6 {
		return coincheck.TradeData{}, fmt.Errorf("invalid record: %+v", record)
	}
	// Mapping: [transaction_id, pair, rate, amount, taker_side]
	// CSV:       [0:id,         1:pair, 2:rate, 3:amount, 4:taker_side] - simplified mapping
	// Our CSV:   [0:id,         ...,    4:rate, 3:amount, 5:side]
	return coincheck.TradeData{
		record[0], // TransactionID
		pair,      // Pair
		record[4], // Rate
		record[3], // Amount
		record[5], // TakerSide
	}, nil
}
