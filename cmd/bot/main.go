// Package main is the entry point of the OBI Scalping Bot.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"context" // Added for dbwriter context

	"net/http" // Added for healthcheck endpoint

	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter" // Added for TimescaleDB
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/http/handler" // Added for healthcheck endpoint
	"github.com/your-org/obi-scalp-bot/pkg/logger"
	"go.uber.org/zap" // Added for Zap logger
)

func main() {
	// --- Health Check Server ---
	// Run the health check server in a separate goroutine.
	// This is for Docker's HEALTHCHECK instruction or other monitoring.
	go func() {
		http.HandleFunc("/health", handler.HealthCheckHandler)
		logger.Info("Health check server starting on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			logger.Fatalf("Health check server failed: %v", err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Command-line flags
	configPath := flag.String("config", "config/config.yaml", "Path to the configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		// Use standard log before logger is initialized
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	// Note: The logger package currently uses a global logger `std` initialized by default.
	// If NewLogger was to return an instance and we set it globally, that would happen here.
	// For now, we can assume the logger.Info, logger.Error, etc. will work.
	// If we need to set log level based on config:
	logger.SetGlobalLogLevel(cfg.LogLevel) // Use the new function to set the global logger's level

	logger.Info("OBI Scalping Bot starting...")
	logger.Infof("Loaded configuration from: %s", *configPath)
	logger.Infof("Target pair: %s", cfg.Pair)

	// Initialize TimescaleDB Writer
	// Create a new Zap logger instance for the dbWriter.
	// This is separate from the application's global logger in pkg/logger for now.
	var zapLogger *zap.Logger
	var zapErr error
	// TODO: Convert cfg.LogLevel (string) to zapcore.Level if needed for more granular control
	if cfg.LogLevel == "debug" { // Example level mapping
		zapLogger, zapErr = zap.NewDevelopment()
	} else {
		zapLogger, zapErr = zap.NewProduction()
	}
	if zapErr != nil {
		logger.Fatalf("Failed to initialize Zap logger for DBWriter: %v", zapErr)
	}
	defer func() {
		if err := zapLogger.Sync(); err != nil {
			// Use the application's main logger to report this, if possible,
			// or fmt.Fprintf if logger itself might be compromised.
			logger.Errorf("Failed to sync Zap logger for DBWriter: %v", err)
		}
	}()

	dbWriter, err := dbwriter.NewWriter(ctx, cfg.Database, cfg.DBWriter, zapLogger)
	if err != nil {
		logger.Fatalf("Failed to initialize TimescaleDB writer: %v", err)
	}
	defer dbWriter.Close()
	logger.Info("TimescaleDB writer initialized successfully.")

	// TODO: Initialize other components like RiskManager, SignalEngine, etc.
	// These components might need the dbWriter instance.

	// Initialize WebSocket client
	// Consider passing dbWriter to wsClient if it needs to directly write data,
	// or to a handler that receives data from wsClient.
	wsClient := coincheck.NewWebSocketClient(dbWriter)

	// Setup signal handling for graceful shutdown
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan bool, 1)

	go func() {
		sig := <-sigs
		logger.Infof("Received signal: %s, initiating shutdown...", sig)
		// TODO: Add graceful shutdown procedures for other components
		if err := wsClient.Close(); err != nil {
			logger.Errorf("Error closing WebSocket client: %v", err)
		}
		done <- true
	}()

	// Start WebSocket connection
	// The Connect method is blocking and handles its own reconnect logic.
	// It will only return on unrecoverable error or when Close() is called (e.g. by interrupt).
	logger.Info("Attempting to connect to Coincheck WebSocket API...")
	if err := wsClient.Connect(); err != nil {
		// Check if this error is due to a manual shutdown (Close() called)
		// The current wsClient.Connect() returns nil on graceful shutdown via interrupt.
		// If it returns an error, it's likely an unrecoverable one.
		logger.Errorf("WebSocket client exited with error: %v", err)
		// An unrecoverable error in Connect (e.g. failed all retries) should lead to exit.
		// However, if Connect is designed to run indefinitely until Close is called,
		// an error might mean something went very wrong.
	}

	// Wait for shutdown signal
	<-done
	logger.Info("OBI Scalping Bot shut down gracefully.")
}
