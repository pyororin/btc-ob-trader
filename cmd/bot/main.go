// Package main is the entry point of the OBI Scalping Bot.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

func main() {
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
	// logger.SetLevel(cfg.LogLevel) // This function would need to be added to logger pkg
	_ = logger.NewLogger(cfg.LogLevel) // This initializes the global logger in our current setup

	logger.Info("OBI Scalping Bot starting...")
	logger.Infof("Loaded configuration from: %s", *configPath)
	logger.Infof("Target pair: %s", cfg.Pair)

	// TODO: Initialize other components like RiskManager, SignalEngine, TimescaleDB writer etc.

	// Initialize WebSocket client
	wsClient := coincheck.NewWebSocketClient()

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
