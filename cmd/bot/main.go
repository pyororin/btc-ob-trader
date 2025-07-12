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

	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/http/handler"
	"github.com/your-org/obi-scalp-bot/internal/indicator"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
	"go.uber.org/zap"
)

func main() {
	// --- Configuration ---
	configPath := flag.String("config", "config/config.yaml", "Path to the configuration file")
	replayMode := flag.Bool("replay", false, "Enable replay mode")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// --- Logger ---
	logger.SetGlobalLogLevel(cfg.LogLevel)
	logger.Info("OBI Scalping Bot starting...")
	logger.Infof("Loaded configuration from: %s", *configPath)
	logger.Infof("Target pair: %s", cfg.Pair)

	// --- Health Check Server (only in non-replay mode) ---
	if !*replayMode {
		go func() {
			http.HandleFunc("/health", handler.HealthCheckHandler)
			logger.Info("Health check server starting on :8080")
			if err := http.ListenAndServe(":8080", nil); err != nil {
				logger.Fatalf("Health check server failed: %v", err)
			}
		}()
	}

	// --- TimescaleDB Writer (Optional) ---
	var dbWriter *dbwriter.Writer
	if cfg.DBWriter.BatchSize > 0 { // Use BatchSize > 0 as a proxy for being enabled
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
		defer func() {
			if err := zapLogger.Sync(); err != nil {
				// We can't use the logger here because it's being synced.
				// Print to stderr instead.
				fmt.Fprintf(os.Stderr, "Failed to sync zap logger: %v\n", err)
			}
		}()

		dbWriter, err = dbwriter.NewWriter(ctx, cfg.Database, cfg.DBWriter, zapLogger)
		if err != nil {
			logger.Fatalf("Failed to initialize TimescaleDB writer: %v", err)
		}
		defer dbWriter.Close()
		logger.Info("TimescaleDB writer initialized successfully.")
	}

	// --- OrderBook and OBI Calculator ---
	orderBook := indicator.NewOrderBook()
	obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)

	orderBookHandler := func(data coincheck.OrderBookData) {
		orderBook.ApplyUpdate(data)
	}

	tradeHandler := func(data coincheck.TradeData) {
		if dbWriter != nil {
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
				Time:        time.Now().UTC(), // Or use a timestamp from the trade data if available
				Pair:        data.Pair(),
				Side:        data.TakerSide(),
				Price:       price,
				Size:        size,
				TransactionID: txID,
			}
			dbWriter.SaveTrade(trade)
		}
		logger.Debugf("Trade received: Pair=%s, Side=%s, Price=%s, Amount=%s", data.Pair(), data.TakerSide(), data.Rate(), data.Amount())
	}

	// --- Graceful Shutdown Setup ---
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// --- Start Services ---
	obiCalculator.Start(ctx)

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

	// --- Main Execution Loop ---
	if *replayMode {
		go runReplay(ctx, cfg, orderBookHandler, tradeHandler, sigs)
	} else {
		wsClient := coincheck.NewWebSocketClient(orderBookHandler, tradeHandler)
		go func() {
			logger.Info("Attempting to connect to Coincheck WebSocket API...")
			if err := wsClient.Connect(); err != nil {
				logger.Errorf("WebSocket client exited with error: %v", err)
				sigs <- syscall.SIGTERM
			}
		}()
	}

	// Wait for shutdown signal
	sig := <-sigs
	logger.Infof("Received signal: %s, initiating shutdown...", sig)

	// Trigger graceful shutdown
	cancel()
	// In a real scenario, you might need to close other resources here as well.
	time.Sleep(1 * time.Second)
	logger.Info("OBI Scalping Bot shut down gracefully.")
}

// runReplay simulates market data based on the replay configuration.
func runReplay(ctx context.Context, cfg *config.Config, obHandler coincheck.OrderBookHandler, tradeHandler coincheck.TradeHandler, sigs chan<- os.Signal) {
	logger.Info("Starting replay mode...")
	// This is a placeholder for the actual replay logic.
	// In a real implementation, you would read from a CSV or database.
	// For now, we just log a message and exit after a short period.

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	replayEndTime := time.Now().Add(10 * time.Second) // Run replay for 10 seconds

	for {
		select {
		case <-ticker.C:
			if time.Now().After(replayEndTime) {
				logger.Info("Replay finished.")
				sigs <- syscall.SIGTERM // Signal main goroutine to shut down
				return
			}
			logger.Info("Replay tick...")
			// Here you would generate or read data and call handlers:
			// obHandler(mockOrderBookData)
			// tradeHandler(mockTradeData)
		case <-ctx.Done():
			logger.Info("Replay mode shutting down due to context cancellation.")
			return
		}
	}
}
