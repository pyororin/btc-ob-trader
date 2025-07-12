// Package main is the entry point of the OBI Scalping Bot.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
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
	// --- Health Check Server ---
	go func() {
		http.HandleFunc("/health", handler.HealthCheckHandler)
		logger.Info("Health check server starting on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			logger.Fatalf("Health check server failed: %v", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// --- Configuration ---
	configPath := flag.String("config", "config/config.yaml", "Path to the configuration file")
	flag.Parse()
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
		defer zapLogger.Sync()

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

	// --- WebSocket Client ---
	wsClient := coincheck.NewWebSocketClient(func(data coincheck.OrderBookData) {
		orderBook.ApplyUpdate(data)
	})

	// --- Graceful Shutdown Setup ---
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// --- Start Services ---
	obiCalculator.Start(ctx)

	// Goroutine to process OBI results
	go func() {
		resultsCh := obiCalculator.Subscribe()
		for {
			select {
			case <-ctx.Done():
				logger.Info("OBI processing goroutine shutting down.")
				return
			case result := <-resultsCh:
				// For now, we just log the result.
				// In the future, this could be sent to the SignalEngine or DBWriter.
				logger.Infof("OBI Calculated: OBI8=%.4f, OBI16=%.4f, Timestamp=%v", result.OBI8, result.OBI16, result.Timestamp)
			}
		}
	}()

	// --- Main Execution Loop ---
	go func() {
		logger.Info("Attempting to connect to Coincheck WebSocket API...")
		if err := wsClient.Connect(); err != nil {
			logger.Errorf("WebSocket client exited with error: %v", err)
			// Signal main goroutine to shut down if WebSocket connection fails permanently
			sigs <- syscall.SIGTERM
		}
	}()

	// Wait for shutdown signal
	sig := <-sigs
	logger.Infof("Received signal: %s, initiating shutdown...", sig)

	// Trigger graceful shutdown
	cancel()
	if err := wsClient.Close(); err != nil {
		logger.Errorf("Error closing WebSocket client: %v", err)
	}

	// Allow a moment for goroutines to clean up
	time.Sleep(1 * time.Second)

	logger.Info("OBI Scalping Bot shut down gracefully.")
}
