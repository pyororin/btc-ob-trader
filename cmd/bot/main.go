// Package main is the entry point of the OBI Scalping Bot.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/your-org/obi-scalp-bot/internal/benchmark"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/datastore"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/internal/engine"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/http/handler"
	"sync"

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
	_ = pgxpool.Config{}
	f := parseFlags()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := setupConfig(f.configPath)
	setupLogger(cfg.App.LogLevel, f.configPath, cfg.Trade.Pair)
	go watchSignals(f.configPath)

	if f.simulateMode && f.csvPath == "" {
		logger.Fatal("CSV file path must be provided in simulation mode using --csv flag")
	}

	if !f.replayMode && !f.simulateMode {
		startHealthCheckServer()
	}

	// --- Main Execution Loop ---
	// Use a separate channel for graceful shutdown signals
	shutdownSigs := make(chan os.Signal, 1)
	signal.Notify(shutdownSigs, syscall.SIGINT, syscall.SIGTERM)

	var dbWriter *dbwriter.Writer
	if !f.simulateMode {
		dbWriter = setupDBWriter(ctx)
		if dbWriter != nil {
			defer dbWriter.Close()
		}
	}

	runMainLoop(ctx, f, dbWriter, shutdownSigs)

	// --- Graceful Shutdown ---
	waitForShutdownSignal(shutdownSigs)
	logger.Info("Initiating graceful shutdown...")
	cancel()
	time.Sleep(1 * time.Second) // Allow time for services to shut down
	logger.Info("OBI Scalping Bot shut down gracefully.")
}

// parseFlags parses command-line flags.
func parseFlags() flags {
	configPath := flag.String("config", "config/app_config.yaml", "Path to the application configuration file")
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
func setupConfig(appConfigPath string) *config.Config {
	dir := filepath.Dir(appConfigPath)
	tradeConfigPath := filepath.Join(dir, "trade_config.yaml")
	cfg, err := config.LoadConfig(appConfigPath, tradeConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration from %s and %s: %v\n", appConfigPath, tradeConfigPath, err)
		os.Exit(1)
	}
	return cfg
}

// watchSignals sets up a handler for SIGHUP to reload the configuration.
func watchSignals(appConfigPath string) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)

	for {
		<-sigChan
		logger.Info("SIGHUP received, attempting to reload configuration...")
		dir := filepath.Dir(appConfigPath)
		tradeConfigPath := filepath.Join(dir, "trade_config.yaml")
		newCfg, err := config.ReloadConfig(appConfigPath, tradeConfigPath)
		if err != nil {
			logger.Errorf("Failed to reload configuration: %v", err)
		} else {
			logger.SetGlobalLogLevel(newCfg.App.LogLevel)
			logger.Info("Configuration reloaded successfully.")
		}
	}
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
func setupDBWriter(ctx context.Context) *dbwriter.Writer {
	cfg := config.GetConfig()
	if cfg.App.DBWriter.BatchSize <= 0 {
		return nil
	}

	var zapLogger *zap.Logger
	var zapErr error
	if cfg.App.LogLevel == "debug" {
		zapLogger, zapErr = zap.NewDevelopment()
	} else {
		zapLogger, zapErr = zap.NewProduction()
	}
	if zapErr != nil {
		logger.Fatalf("Failed to initialize Zap logger for DBWriter: %v", zapErr)
	}

	logger.Infof("Initializing DBWriter with config: BatchSize=%d, WriteIntervalSeconds=%d", cfg.App.DBWriter.BatchSize, cfg.App.DBWriter.WriteIntervalSeconds)

	dbWriter, err := dbwriter.NewWriter(ctx, cfg.App.Database, cfg.App.DBWriter, zapLogger)
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
func processSignalsAndExecute(ctx context.Context, obiCalculator *indicator.OBICalculator, execEngine engine.ExecutionEngine, benchmarkService *benchmark.Service) {
	cfg := config.GetConfig()
	signalEngine, err := tradingsignal.NewSignalEngine(&cfg.Trade)
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
				currentCfg := config.GetConfig()
				if result.BestBid <= 0 || result.BestAsk <= 0 {
					logger.Warnf("Skipping signal evaluation due to invalid best bid/ask: BestBid=%.2f, BestAsk=%.2f", result.BestBid, result.BestAsk)
					continue
				}
				midPrice := (result.BestAsk + result.BestBid) / 2
				if benchmarkService != nil {
					benchmarkService.Tick(ctx, midPrice)
				}
				signalEngine.UpdateMarketData(result.Timestamp, midPrice, result.BestBid, result.BestAsk, 1.0, 1.0)

				logger.Infof("Evaluating OBI: %.4f, Long Threshold: %.4f, Short Threshold: %.4f",
					result.OBI8, signalEngine.GetCurrentLongOBIThreshold(), signalEngine.GetCurrentShortOBIThreshold())

				tradingSignal := signalEngine.Evaluate(result.Timestamp, result.OBI8)
				if tradingSignal != nil {
					orderType := ""
					if tradingSignal.Type == tradingsignal.SignalLong {
						orderType = "buy"
					} else if tradingSignal.Type == tradingsignal.SignalShort {
						orderType = "sell"
					}

					if orderType != "" {
						const liquidityCheckPriceRange = 0.001
						const liquidityThresholdBtc = 0.1

						finalPrice := 0.0
						orderLogMsg := ""
						if orderType == "buy" {
							finalPrice = result.BestAsk
							orderLogMsg = "Placing aggressive buy order at ask price."
						} else {
							finalPrice = result.BestBid
							orderLogMsg = "Placing aggressive sell order at bid price."
						}

						logger.Infof("Executing trade for signal: %s. %s", tradingSignal.Type.String(), orderLogMsg)
						orderAmount := 0.01

						if currentCfg.Trade.Twap.Enabled && orderAmount > currentCfg.Trade.Twap.MaxOrderSizeBtc {
							logger.Infof("Order amount %.8f exceeds max size %.8f. Executing with TWAP.", orderAmount, currentCfg.Trade.Twap.MaxOrderSizeBtc)
							go executeTwapOrder(ctx, execEngine, currentCfg.Trade.Pair, orderType, finalPrice, orderAmount)
						} else {
							logger.Infof("Calling PlaceOrder with: type=%s, price=%.2f, amount=%.2f", orderType, finalPrice, orderAmount)
							resp, err := execEngine.PlaceOrder(ctx, currentCfg.Trade.Pair, orderType, finalPrice, orderAmount, false)
							if err != nil {
								logger.Errorf("Failed to place order for signal: %v", err)
							}
							if resp != nil && !resp.Success {
								logger.Warnf("Order placement was not successful: %s", resp.Error)
							}
						}
					}
				}
			}
		}
	}()
}

// executeTwapOrder executes a large order by splitting it into smaller chunks over time.
func executeTwapOrder(ctx context.Context, execEngine engine.ExecutionEngine, pair, orderType string, price, totalAmount float64) {
	if liveEngine, ok := execEngine.(*engine.LiveExecutionEngine); ok {
		defer liveEngine.SetPartialExitStatus(false)
	}

	cfg := config.GetConfig()
	numOrders := int(math.Floor(totalAmount / cfg.Trade.Twap.MaxOrderSizeBtc))
	lastOrderSize := totalAmount - float64(numOrders)*cfg.Trade.Twap.MaxOrderSizeBtc
	chunkSize := cfg.Trade.Twap.MaxOrderSizeBtc
	interval := time.Duration(cfg.Trade.Twap.IntervalSeconds) * time.Second

	logger.Infof("TWAP execution started: Total=%.8f, Chunks=%d, ChunkSize=%.8f, LastChunk=%.8f, Interval=%v",
		totalAmount, numOrders, chunkSize, lastOrderSize, interval)

	for i := 0; i < numOrders; i++ {
		select {
		case <-ctx.Done():
			logger.Warnf("TWAP execution cancelled for order type %s.", orderType)
			return
		default:
			logger.Infof("TWAP chunk %d/%d: Placing order for %.8f BTC.", i+1, numOrders, chunkSize)
			_, err := execEngine.PlaceOrder(ctx, pair, orderType, price, chunkSize, false)
			if err != nil {
				logger.Errorf("Error in TWAP chunk %d: %v. Aborting.", i+1, err)
				return
			}
			logger.Infof("TWAP chunk %d/%d placed. Waiting for %v.", i+1, numOrders, interval)
			time.Sleep(interval)
		}
	}

	if lastOrderSize > 1e-8 { // Avoid placing virtually zero-sized orders
		select {
		case <-ctx.Done():
			logger.Warnf("TWAP execution cancelled before placing the last chunk for order type %s.", orderType)
			return
		default:
			logger.Infof("TWAP last chunk: Placing order for %.8f BTC.", lastOrderSize)
			_, err := execEngine.PlaceOrder(ctx, pair, orderType, price, lastOrderSize, false)
			if err != nil {
				logger.Errorf("Error in TWAP last chunk: %v. Aborting.", err)
			} else {
				logger.Info("TWAP execution finished successfully.")
			}
		}
	} else {
		logger.Info("TWAP execution finished successfully.")
	}
}

// runMainLoop starts either the live trading, replay, or simulation mode.
func runMainLoop(ctx context.Context, f flags, dbWriter *dbwriter.Writer, sigs chan<- os.Signal) {
	cfg := config.GetConfig()

	var benchmarkService *benchmark.Service
	if dbWriter != nil {
		var zapLogger *zap.Logger
		// Assuming logger is already configured, but if not, initialize it.
		// For simplicity, re-using the logic from setupDBWriter.
		if cfg.App.LogLevel == "debug" {
			zapLogger, _ = zap.NewDevelopment()
		} else {
			zapLogger, _ = zap.NewProduction()
		}
		benchmarkService = benchmark.NewService(zapLogger, dbWriter)
	}

	if f.replayMode {
		go runReplay(ctx, dbWriter, sigs)
	} else if f.simulateMode {
		go runSimulation(ctx, f, sigs)
	} else {
		// Live trading setup
		client := coincheck.NewClient(cfg.APIKey, cfg.APISecret)
		execEngine := engine.NewLiveExecutionEngine(client, &cfg.Trade, &cfg.App.Order, dbWriter)

		orderBook := indicator.NewOrderBook()
		obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
		orderBookHandler, tradeHandler := setupHandlers(orderBook, dbWriter, cfg.Trade.Pair)

		obiCalculator.Start(ctx)
		go processSignalsAndExecute(ctx, obiCalculator, execEngine, benchmarkService)
		go orderMonitor(ctx, execEngine, client, orderBook)
		go positionMonitor(ctx, execEngine, orderBook) // Added for partial exit
		go startPnlReporter(ctx)

		wsClient := coincheck.NewWebSocketClient(orderBookHandler, tradeHandler)
		go func() {
			logger.Info("Connecting to Coincheck WebSocket API...")
			if err := wsClient.Connect(); err != nil {
				logger.Errorf("WebSocket client exited with error: %v", err)
				sigs <- syscall.SIGTERM
			}
		}()
	}
}

// startPnlReporter は定期的にPnLレポートを生成し、古いレポートを削除します。
func startPnlReporter(ctx context.Context) {
	// Use a ticker that can be stopped
	var ticker *time.Ticker
	// Use a wait group to ensure goroutine finishes
	var wg sync.WaitGroup

	// Function to start the reporter logic
	start := func() {
		cfg := config.GetConfig()
		if cfg.App.PnlReport.IntervalMinutes <= 0 {
			logger.Info("PnL reporter is disabled.")
			return
		}

		logger.Infof("Starting PnL reporter: interval=%d minutes, maxAge=%d hours",
			cfg.App.PnlReport.IntervalMinutes, cfg.App.PnlReport.MaxAgeHours)

		ticker = time.NewTicker(time.Duration(cfg.App.PnlReport.IntervalMinutes) * time.Minute)
		wg.Add(1)

		go func() {
			defer wg.Done()
			defer ticker.Stop()

			dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
				cfg.App.Database.User, cfg.App.Database.Password, cfg.App.Database.Host, cfg.App.Database.Port, cfg.App.Database.Name, cfg.App.Database.SSLMode)
			dbpool, err := pgxpool.New(ctx, dbURL)
			if err != nil {
				logger.Errorf("PnL reporter unable to connect to database: %v", err)
				return // Exit goroutine if DB connection fails
			}
			defer dbpool.Close()
			repo := datastore.NewRepository(dbpool)

			// Initial run
			generateAndSaveReport(ctx, repo)
			deleteOldReports(ctx, repo, cfg.App.PnlReport.MaxAgeHours)

			for {
				select {
				case <-ctx.Done():
					logger.Info("Stopping PnL reporter.")
					return
				case <-ticker.C:
					// Re-fetch config inside the loop to get the latest values
					loopCfg := config.GetConfig()
					generateAndSaveReport(ctx, repo)
					deleteOldReports(ctx, repo, loopCfg.App.PnlReport.MaxAgeHours)
				}
			}
		}()
	}

	// Initial start
	start()

	// Watch for config changes
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigChan:
				logger.Info("Restarting PnL reporter due to config reload.")
				// Stop the current ticker and wait for the goroutine to exit
				if ticker != nil {
					ticker.Stop()
				}
				wg.Wait()
				// Restart the reporter
				start()
			}
		}
	}()
}


func generateAndSaveReport(ctx context.Context, repo *datastore.Repository) {
	logger.Info("Generating PnL report...")
	trades, err := repo.FetchAllTradesForReport(ctx)
	if err != nil {
		logger.Errorf("Failed to fetch trades for PnL report: %v", err)
		return
	}

	if len(trades) == 0 {
		logger.Info("No trades found for PnL report.")
		return
	}

	report, err := datastore.AnalyzeTrades(trades)
	if err != nil {
		logger.Errorf("Failed to analyze trades for PnL report: %v", err)
		return
	}

	if err := repo.SavePnlReport(ctx, report); err != nil {
		logger.Errorf("Failed to save PnL report: %v", err)
		return
	}
	logger.Info("Successfully generated and saved PnL report.")
}

func deleteOldReports(ctx context.Context, repo *datastore.Repository, maxAgeHours int) {
	if maxAgeHours <= 0 {
		return
	}
	logger.Infof("Deleting PnL reports older than %d hours...", maxAgeHours)
	deletedCount, err := repo.DeleteOldPnlReports(ctx, maxAgeHours)
	if err != nil {
		logger.Errorf("Failed to delete old PnL reports: %v", err)
		return
	}
	if deletedCount > 0 {
		logger.Infof("Successfully deleted %d old PnL reports.", deletedCount)
	} else {
		logger.Info("No old PnL reports to delete.")
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
	fmt.Println("最大ドローダウン: N/A")
	fmt.Println("==========================")
}

// runSimulation runs a backtest using data from a CSV file.
func runSimulation(ctx context.Context, f flags, sigs chan<- os.Signal) {
	cfg := config.GetConfig()
	logger.Info("--- SIMULATION MODE ---")
	logger.Infof("CSV File: %s", f.csvPath)
	logger.Infof("Pair: %s", cfg.Trade.Pair)
	logger.Infof("Long Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Trade.Long.OBIThreshold, cfg.Trade.Long.TP, cfg.Trade.Long.SL)
	logger.Infof("Short Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Trade.Short.OBIThreshold, cfg.Trade.Short.TP, cfg.Trade.Short.SL)
	logger.Info("--------------------")

	orderBook := indicator.NewOrderBook()
	// In simulation mode, override signal hold duration to 0 for instant signal confirmation.
	cfg.Trade.Signal.HoldDurationMs = 0
	cfg.Trade.Signal.SlopeFilter.Enabled = false
	replayEngine := engine.NewReplayExecutionEngine(nil, orderBook)
	var execEngine engine.ExecutionEngine = replayEngine
	obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
	orderBookHandler, _ := setupHandlers(orderBook, nil, cfg.Trade.Pair)

	obiCalculator.Start(ctx)
	go processSignalsAndExecute(ctx, obiCalculator, execEngine, nil)

	logger.Infof("Streaming market events from %s", f.csvPath)
	eventCh, errCh := datastore.StreamMarketEventsFromCSV(ctx, f.csvPath)

	go func() {
		defer func() {
			logger.Info("Simulation finished.")
			printSimulationSummary(replayEngine)
			sigs <- syscall.SIGTERM
		}()

		var snapshotCount int
		for {
			select {
			case event, ok := <-eventCh:
				if !ok {
					logger.Infof("Finished processing all %d snapshots.", snapshotCount)
					return
				}
				if e, ok := event.(datastore.OrderBookEvent); ok {
					orderBookHandler(e.OrderBook)
					snapshotCount++
					if snapshotCount%1000 == 0 {
						logger.Infof("Processed snapshot %d", snapshotCount)
					}
				}
			case err := <-errCh:
				if err != nil {
					logger.Fatalf("Error while streaming market events: %v", err)
				}
				// If err is nil, the channel was closed, which is a success signal here.
				return
			case <-ctx.Done():
				logger.Info("Simulation cancelled.")
				return
			}
		}
	}()
}

// orderMonitor periodically checks open orders and adjusts them if necessary.
func orderMonitor(ctx context.Context, execEngine engine.ExecutionEngine, client *coincheck.Client, orderBook *indicator.OrderBook) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	logger.Info("Starting order monitor.")

	for {
		select {
		case <-ctx.Done():
			logger.Info("Stopping order monitor.")
			return
		case <-ticker.C:
			openOrders, err := client.GetOpenOrders()
			if err != nil {
				logger.Errorf("Order monitor failed to get open orders: %v", err)
				continue
			}

			if len(openOrders.Orders) == 0 {
				continue
			}

			logger.Infof("Order monitor found %d open orders.", len(openOrders.Orders))

			bestBid, bestAsk := orderBook.BestBid(), orderBook.BestAsk()
			if bestBid == 0 || bestAsk == 0 {
				logger.Warn("Order monitor: Invalid best bid/ask, skipping adjustment check.")
				continue
			}

			for _, order := range openOrders.Orders {
				orderRate, err := strconv.ParseFloat(order.Rate, 64)
				if err != nil {
					logger.Errorf("Failed to parse order rate '%s' for order ID %d", order.Rate, order.ID)
					continue
				}

				const priceAdjustmentThresholdRatio = 0.001

				var needsAdjustment bool
				var newRate float64

				if order.OrderType == "buy" {
					if orderRate < bestBid*(1-priceAdjustmentThresholdRatio) {
						needsAdjustment = true
						newRate = bestBid
						logger.Infof("Buy order %d (%.2f) is not competitive (Best Bid: %.2f). Adjusting price.", order.ID, orderRate, bestBid)
					}
				} else if order.OrderType == "sell" {
					if orderRate > bestAsk*(1+priceAdjustmentThresholdRatio) {
						needsAdjustment = true
						newRate = bestAsk
						logger.Infof("Sell order %d (%.2f) is not competitive (Best Ask: %.2f). Adjusting price.", order.ID, orderRate, bestAsk)
					}
				}

				if needsAdjustment {
					logger.Infof("Adjusting order %d. Cancelling...", order.ID)
					_, err := execEngine.CancelOrder(ctx, order.ID)
					if err != nil {
						logger.Errorf("Failed to cancel order %d for adjustment: %v", order.ID, err)
						continue
					}

					time.Sleep(500 * time.Millisecond)

					pendingAmount, err := strconv.ParseFloat(order.PendingAmount, 64)
					if err != nil {
						logger.Errorf("Failed to parse pending amount for order %d: %v", order.ID, err)
						continue
					}
					currentCfg := config.GetConfig()
					logger.Infof("Re-placing order for pair %s, type %s, new rate %.2f, amount %.8f", order.Pair, order.OrderType, newRate, pendingAmount)
					_, err = execEngine.PlaceOrder(ctx, currentCfg.Trade.Pair, order.OrderType, newRate, pendingAmount, false)
					if err != nil {
						logger.Errorf("Failed to re-place order after adjustment for original ID %d: %v", order.ID, err)
					} else {
						logger.Infof("Successfully re-placed order for original ID %d with new rate.", order.ID)
					}
				}
			}
		}
	}
}

// positionMonitor periodically checks the current position for potential partial profit taking.
func positionMonitor(ctx context.Context, execEngine engine.ExecutionEngine, orderBook *indicator.OrderBook) {
	cfg := config.GetConfig()
	if !cfg.Trade.Twap.PartialExitEnabled {
		logger.Info("Position monitor (partial exit) is disabled.")
		return
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	logger.Info("Starting position monitor for partial exits.")

	liveEngine, ok := execEngine.(*engine.LiveExecutionEngine)
	if !ok {
		logger.Info("Position monitor only works with LiveExecutionEngine, exiting.")
		return
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("Stopping position monitor.")
			return
		case <-ticker.C:
			midPrice := (orderBook.BestBid() + orderBook.BestAsk()) / 2
			if midPrice == 0 {
				logger.Warn("Position monitor: Invalid mid-price, skipping check.")
				continue
			}

			if exitOrder := liveEngine.CheckAndTriggerPartialExit(midPrice); exitOrder != nil {
				currentCfg := config.GetConfig()
				go executeTwapOrder(ctx, execEngine, currentCfg.Trade.Pair, exitOrder.OrderType, exitOrder.Price, exitOrder.Size)
			}
		}
	}
}

// runReplay runs the backtest simulation using data from the database.
func runReplay(ctx context.Context, dbWriter *dbwriter.Writer, sigs chan<- os.Signal) {
	cfg := config.GetConfig()
	replaySessionID, err := uuid.NewRandom()
	if err != nil {
		logger.Fatalf("Failed to generate replay session ID: %v", err)
	}
	logger.SetReplayMode(replaySessionID.String())

	if dbWriter != nil {
		dbWriter.SetReplaySessionID(replaySessionID.String())
	}

	logger.Info("--- REPLAY MODE ---")
	logger.Infof("Session ID: %s", replaySessionID.String())
	logger.Infof("Pair: %s", cfg.Trade.Pair)
	logger.Infof("Time Range: %s -> %s", cfg.App.Replay.StartTime, cfg.App.Replay.EndTime)
	logger.Infof("Long Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Trade.Long.OBIThreshold, cfg.Trade.Long.TP, cfg.Trade.Long.SL)
	logger.Infof("Short Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Trade.Short.OBIThreshold, cfg.Trade.Short.TP, cfg.Trade.Short.SL)
	logger.Info("--------------------")

	orderBook := indicator.NewOrderBook()
	execEngine := engine.NewReplayExecutionEngine(dbWriter, orderBook)
	obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
	orderBookHandler, tradeHandler := setupHandlers(orderBook, dbWriter, cfg.Trade.Pair)

	var benchmarkService *benchmark.Service
	if dbWriter != nil {
		var zapLogger *zap.Logger
		if cfg.App.LogLevel == "debug" {
			zapLogger, _ = zap.NewDevelopment()
		} else {
			zapLogger, _ = zap.NewProduction()
		}
		benchmarkService = benchmark.NewService(zapLogger, dbWriter)
	}

	obiCalculator.Start(ctx)
	go processSignalsAndExecute(ctx, obiCalculator, execEngine, benchmarkService)

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.App.Database.User, cfg.App.Database.Password, cfg.App.Database.Host, cfg.App.Database.Port, cfg.App.Database.Name, cfg.App.Database.SSLMode)
	dbpool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Fatalf("Unable to connect to database: %v", err)
	}
	defer dbpool.Close()

	repo := datastore.NewRepository(dbpool)

	startTime, err := time.Parse(time.RFC3339, cfg.App.Replay.StartTime)
	if err != nil {
		logger.Fatalf("Invalid start_time format: %v", err)
	}
	endTime, err := time.Parse(time.RFC3339, cfg.App.Replay.EndTime)
	if err != nil {
		logger.Fatalf("Invalid end_time format: %v", err)
	}

	logger.Infof("Fetching market events from %s to %s", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))
	events, err := repo.FetchMarketEvents(ctx, cfg.Trade.Pair, startTime, endTime)
	if err != nil {
		logger.Fatalf("Failed to fetch market events: %v", err)
	}
	if len(events) == 0 {
		logger.Info("No market events found in the specified time range.")
		sigs <- syscall.SIGTERM
		return
	}
	logger.Infof("Fetched %d market events.", len(events))

	go func() {
		defer func() {
			logger.Info("Replay finished.")
			sigs <- syscall.SIGTERM
		}()

		for i, event := range events {
			select {
			case <-ctx.Done():
				logger.Info("Replay cancelled.")
				return
			default:
				switch e := event.(type) {
				case datastore.TradeEvent:
					tradeHandler(e.Trade)
				case datastore.OrderBookEvent:
					orderBookHandler(e.OrderBook)
				}
				if (i+1)%1000 == 0 {
					logger.Infof("Processed event %d/%d", i+1, len(events))
				}
			}
		}
		logger.Infof("Finished processing all %d events.", len(events))
	}()
}
