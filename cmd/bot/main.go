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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/datastore"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/internal/engine"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/http/handler"
	"github.com/your-org/obi-scalp-bot/internal/indicator"
	tradingsignal "github.com/your-org/obi-scalp-bot/internal/signal"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
	"go.uber.org/zap"
)

type flags struct {
	configPath    string
	replayMode    bool
	simulateMode  bool
	csvPath       string
}

func main() {
	// --- Initialization ---
	f := parseFlags()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := setupConfig(f.configPath)
	setupLogger(cfg.LogLevel, f.configPath, cfg.Pair)

	if f.simulateMode && f.csvPath == "" {
		logger.Fatal("CSV file path must be provided in simulation mode using --csv flag")
	}

	if !f.replayMode && !f.simulateMode {
		startHealthCheckServer()
	}

	// --- Main Execution Loop ---
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	var dbWriter *dbwriter.Writer
	if !f.simulateMode {
		dbWriter = setupDBWriter(ctx, cfg)
		if dbWriter != nil {
			defer dbWriter.Close()
		}
	}

	runMainLoop(ctx, f, cfg, dbWriter, sigs)

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
	simulateMode := flag.Bool("simulate", false, "Enable simulation mode from CSV")
	csvPath := flag.String("csv", "", "Path to the trade data CSV file for simulation")
	flag.Parse()
	return flags{
		configPath:   *configPath,
		replayMode:   *replayMode,
		simulateMode: *simulateMode,
		csvPath:      *csvPath,
	}
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
func setupHandlers(orderBook *indicator.OrderBook, dbWriter *dbwriter.Writer, pair string) (coincheck.OrderBookHandler, coincheck.TradeHandler) {
	orderBookHandler := func(data coincheck.OrderBookData) {
		orderBook.ApplyUpdate(data)

		if dbWriter == nil {
			return
		}

		now := time.Now().UTC()

		// Helper function to parse and save levels
		saveLevels := func(levels [][]string, side string, isSnapshot bool) {
			for _, level := range levels {
				price, err := strconv.ParseFloat(level[0], 64)
				if err != nil {
					logger.Errorf("Failed to parse order book price: %v", err)
					continue
				}
				size, err := strconv.ParseFloat(level[1], 64)
				if err != nil {
					logger.Errorf("Failed to parse order book size: %v", err)
					continue
				}
				update := dbwriter.OrderBookUpdate{
					Time:       now,
					Pair:       pair,
					Side:       side,
					Price:      price,
					Size:       size,
					IsSnapshot: isSnapshot,
				}
				dbWriter.SaveOrderBookUpdate(update)
			}
		}

		// For Coincheck, each update is a snapshot
		saveLevels(data.Bids, "bid", true)
		saveLevels(data.Asks, "ask", true)
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

// processSignalsAndExecute subscribes to indicators, evaluates signals, and executes trades.
func processSignalsAndExecute(ctx context.Context, cfg *config.Config, obiCalculator *indicator.OBICalculator, execEngine engine.ExecutionEngine) {
	signalEngine, err := tradingsignal.NewSignalEngine(cfg)
	if err != nil {
		logger.Fatalf("Failed to create signal engine: %v", err)
	}

	resultsCh := obiCalculator.Subscribe()
	go func() {
		for {
			select {
			case <-ctx.Done():
				logger.Info("Signal processing and execution goroutine shutting down.")
				return
			case result := <-resultsCh:
				// TODO: This is a simplified view. We need more data for a robust signal.
				// For now, we use a placeholder for mid-price and other data points.
				// A proper implementation would get this from the order book.
				midPrice := (result.BestAsk + result.BestBid) / 2
				signalEngine.UpdateMarketData(result.Timestamp, midPrice, result.BestBid, result.BestAsk, 1.0, 1.0) // Placeholder sizes

				tradingSignal := signalEngine.Evaluate(result.Timestamp, result.OBI8) // Using OBI8 for signals
				if tradingSignal != nil {
					orderType := ""
					if tradingSignal.Type == tradingsignal.SignalLong {
						orderType = "buy"
					} else if tradingSignal.Type == tradingsignal.SignalShort {
						orderType = "sell"
					}

					if orderType != "" {
						logger.Infof("Executing trade for signal: %s", tradingSignal.Type.String())
						_, err := execEngine.PlaceOrder(ctx, cfg.Pair, orderType, tradingSignal.EntryPrice, 0.01, false) // Placeholder amount
						if err != nil {
							logger.Errorf("Failed to place order for signal: %v", err)
						}
					}
				}
			}
		}
	}()
}

// runMainLoop starts either the live trading, replay, or simulation mode.
func runMainLoop(ctx context.Context, f flags, cfg *config.Config, dbWriter *dbwriter.Writer, sigs chan<- os.Signal) {
	if f.replayMode {
		go runReplay(ctx, cfg, dbWriter, sigs)
	} else if f.simulateMode {
		go runSimulation(ctx, f, cfg, sigs)
	} else {
		// Live trading setup
		client := coincheck.NewClient(cfg.APIKey, cfg.APISecret)

		// Fetch initial balance
		balance, err := client.GetBalance()
		if err != nil {
			logger.Fatalf("Failed to get account balance: %v", err)
		}
		availableJpy, err := strconv.ParseFloat(balance.Jpy, 64)
		if err != nil {
			logger.Fatalf("Failed to parse JPY balance: %v", err)
		}
		availableBtc, err := strconv.ParseFloat(balance.Btc, 64)
		if err != nil {
			logger.Fatalf("Failed to parse BTC balance: %v", err)
		}
		logger.Infof("Initial balance: JPY=%.2f, BTC=%.8f", availableJpy, availableBtc)

		if availableJpy <= 0 {
			logger.Warnf("Available JPY balance is %.2f. Trading may be limited.", availableJpy)
		}
		if availableBtc <= 0 {
			logger.Warnf("Available BTC balance is %.8f. Selling will not be possible.", availableBtc)
		}

		execEngine := engine.NewLiveExecutionEngine(client, availableJpy, availableBtc)

		orderBook := indicator.NewOrderBook()
		obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
		orderBookHandler, tradeHandler := setupHandlers(orderBook, dbWriter, cfg.Pair)

		obiCalculator.Start(ctx)
		go processSignalsAndExecute(ctx, cfg, obiCalculator, execEngine)

		wsClient := coincheck.NewWebSocketClient(orderBookHandler, tradeHandler)
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

// printSimulationSummary calculates and prints the performance metrics of the simulation.
func printSimulationSummary(replayEngine *engine.ReplayExecutionEngine) {
	executedTrades := replayEngine.ExecutedTrades
	totalProfit := replayEngine.GetTotalRealizedPnL()
	totalTrades := len(executedTrades)

	if totalTrades == 0 {
		fmt.Println("\n==== シミュレーション結果 ====")
		fmt.Println("取引は実行されませんでした。")
		fmt.Println("==========================")
		return
	}

	var wins, losses, closingTrades int
	var totalWinAmount, totalLossAmount float64

	for _, trade := range executedTrades {
		if trade.RealizedPnL != 0 {
			closingTrades++
			if trade.RealizedPnL > 0 {
				wins++
				totalWinAmount += trade.RealizedPnL
			} else {
				losses++
				totalLossAmount += trade.RealizedPnL
			}
		}
	}

	winRate := 0.0
	if closingTrades > 0 {
		winRate = float64(wins) / float64(closingTrades) * 100
	}

	avgWin := 0.0
	if wins > 0 {
		avgWin = totalWinAmount / float64(wins)
	}
	avgLoss := 0.0
	if losses > 0 {
		avgLoss = totalLossAmount / float64(losses)
	}

	fmt.Println("\n==== シミュレーション結果 ====")
	fmt.Printf("総損益　     : %.2f JPY\n", totalProfit)
	fmt.Printf("取引回数     : %d回\n", totalTrades)
	fmt.Printf("勝率         : %.2f%% (%d勝/%d敗)\n", winRate, wins, losses)
	fmt.Printf("平均利益/損失: %.2f JPY / %.2f JPY\n", avgWin, avgLoss)
	// NOTE: Max Drawdown calculation is complex and requires tracking equity curve, omitted for now.
	fmt.Println("最大ドローダウン: N/A")
	fmt.Println("==========================")
}

// runSimulation runs a backtest using data from a CSV file.
func runSimulation(ctx context.Context, f flags, cfg *config.Config, sigs chan<- os.Signal) {
	// --- Log Simulation Configuration ---
	logger.Info("--- SIMULATION MODE ---")
	logger.Infof("CSV File: %s", f.csvPath)
	logger.Infof("Pair: %s", cfg.Pair)
	logger.Infof("Long Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Long.OBIThreshold, cfg.Long.TP, cfg.Long.SL)
	logger.Infof("Short Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Short.OBIThreshold, cfg.Short.TP, cfg.Short.SL)
	logger.Info("--------------------")

	// --- Execution Engine ---
	// In simulation mode, we don't write to the DB, so we pass nil for the writer.
	replayEngine := engine.NewReplayExecutionEngine(nil)
	var execEngine engine.ExecutionEngine = replayEngine

	// --- Setup Handlers and Indicators ---
	orderBook := indicator.NewOrderBook()
	obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
	// In simulation mode, we don't have a dbWriter.
	orderBookHandler, _ := setupHandlers(orderBook, nil, cfg.Pair)

	// --- Start Services ---
	obiCalculator.Start(ctx)
	go processSignalsAndExecute(ctx, cfg, obiCalculator, execEngine)

	// --- Fetch Events from CSV ---
	logger.Infof("Fetching market events from %s", f.csvPath)
	events, err := datastore.FetchMarketEventsFromCSV(ctx, f.csvPath)
	if err != nil {
		logger.Fatalf("Failed to fetch market events from CSV: %v", err)
	}
	if len(events) == 0 {
		logger.Info("No market events found in the CSV file.")
		sigs <- syscall.SIGTERM
		return
	}
	logger.Infof("Fetched %d market snapshots.", len(events))

	// --- Event Loop ---
	go func() {
		defer func() {
			logger.Info("Simulation finished.")
			printSimulationSummary(replayEngine)
			sigs <- syscall.SIGTERM // Notify main thread to exit
		}()

		for i, event := range events {
			select {
			case <-ctx.Done():
				logger.Info("Simulation cancelled.")
				return
			default:
				// In this mode, we only have OrderBookEvents.
				if e, ok := event.(datastore.OrderBookEvent); ok {
					orderBookHandler(e.OrderBook)
				}
				if (i+1)%100 == 0 { // Log progress every 100 events (snapshots are less frequent)
					logger.Infof("Processed snapshot %d/%d", i+1, len(events))
				}
			}
		}
		logger.Infof("Finished processing all %d snapshots.", len(events))
	}()
}

// runReplay runs the backtest simulation using data from the database.
func runReplay(ctx context.Context, cfg *config.Config, dbWriter *dbwriter.Writer, sigs chan<- os.Signal) {
	// --- Replay Session ID & Logger ---
	replaySessionID, err := uuid.NewRandom()
	if err != nil {
		logger.Fatalf("Failed to generate replay session ID: %v", err)
	}
	logger.SetReplayMode(replaySessionID.String())

	// --- DB Writer ---
	if dbWriter != nil {
		dbWriter.SetReplaySessionID(replaySessionID.String())
	}

	// --- Log Replay Configuration ---
	logger.Info("--- REPLAY MODE ---")
	logger.Infof("Session ID: %s", replaySessionID.String())
	logger.Infof("Pair: %s", cfg.Pair)
	logger.Infof("Time Range: %s -> %s", cfg.Replay.StartTime, cfg.Replay.EndTime)
	logger.Infof("Long Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Long.OBIThreshold, cfg.Long.TP, cfg.Long.SL)
	logger.Infof("Short Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Short.OBIThreshold, cfg.Short.TP, cfg.Short.SL)
	logger.Info("--------------------")

	// --- Execution Engine ---
	execEngine := engine.NewReplayExecutionEngine(dbWriter)

	// --- Setup Handlers and Indicators ---
	orderBook := indicator.NewOrderBook()
	obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
	orderBookHandler, tradeHandler := setupHandlers(orderBook, dbWriter, cfg.Pair)

	// --- Start Services ---
	obiCalculator.Start(ctx)
	go processSignalsAndExecute(ctx, cfg, obiCalculator, execEngine)

	// --- DB Connection for Data Fetching ---
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

	// --- Fetch Events ---
	logger.Infof("Fetching market events from %s to %s", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))
	events, err := repo.FetchMarketEvents(ctx, cfg.Pair, startTime, endTime)
	if err != nil {
		logger.Fatalf("Failed to fetch market events: %v", err)
	}
	if len(events) == 0 {
		logger.Info("No market events found in the specified time range.")
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
				case datastore.OrderBookEvent:
					orderBookHandler(e.OrderBook)
				}
				if (i+1)%1000 == 0 { // Log progress every 1000 events
					logger.Infof("Processed event %d/%d", i+1, len(events))
				}
			}
		}
		logger.Infof("Finished processing all %d events.", len(events))
	}()
}
