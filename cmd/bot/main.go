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
	"strconv"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
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
	setupLogger(cfg.LogLevel, f.configPath, cfg.Pair)
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

// watchSignals sets up a handler for SIGHUP to reload the configuration.
func watchSignals(configPath string) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)

	for {
		<-sigChan
		logger.Info("SIGHUP received, attempting to reload configuration...")
		newCfg, err := config.ReloadConfig(configPath)
		if err != nil {
			logger.Errorf("Failed to reload configuration: %v", err)
		} else {
			logger.SetGlobalLogLevel(newCfg.LogLevel)
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
func processSignalsAndExecute(ctx context.Context, obiCalculator *indicator.OBICalculator, execEngine engine.ExecutionEngine) {
	cfg := config.GetConfig()
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
				currentCfg := config.GetConfig()
				if result.BestBid <= 0 || result.BestAsk <= 0 {
					logger.Warnf("Skipping signal evaluation due to invalid best bid/ask: BestBid=%.2f, BestAsk=%.2f", result.BestBid, result.BestAsk)
					continue
				}
				midPrice := (result.BestAsk + result.BestBid) / 2
				signalEngine.UpdateMarketData(result.Timestamp, midPrice, result.BestBid, result.BestAsk, 1.0, 1.0)

				tradingSignal := signalEngine.Evaluate(result.Timestamp, result.OBI8)
				if tradingSignal != nil {
					orderType := ""
					if tradingSignal.Type == tradingsignal.SignalLong {
						orderType = "buy"
					} else if tradingSignal.Type == tradingsignal.SignalShort {
						orderType = "sell"
					}

					if orderType != "" {
						orderBook := obiCalculator.OrderBook()
						const liquidityCheckPriceRange = 0.001
						const liquidityThresholdBtc = 0.1

						var depth float64
						if orderType == "buy" {
							depth = orderBook.CalculateDepth("ask", liquidityCheckPriceRange)
						} else {
							depth = orderBook.CalculateDepth("bid", liquidityCheckPriceRange)
						}

						finalPrice := tradingSignal.EntryPrice
						var orderLogMsg string

						if depth >= liquidityThresholdBtc {
							if orderType == "buy" {
								finalPrice = result.BestAsk
								orderLogMsg = fmt.Sprintf("Sufficient liquidity (%.4f BTC). Placing aggressive buy order at ask price.", depth)
							} else {
								finalPrice = result.BestBid
								orderLogMsg = fmt.Sprintf("Sufficient liquidity (%.4f BTC). Placing aggressive sell order at bid price.", depth)
							}
						} else {
							orderLogMsg = fmt.Sprintf("Insufficient liquidity (%.4f BTC). Placing standard limit order.", depth)
						}

						logger.Infof("Executing trade for signal: %s. %s", tradingSignal.Type.String(), orderLogMsg)
						orderAmount := 0.01

						if currentCfg.Twap.Enabled && orderAmount > currentCfg.Twap.MaxOrderSizeBtc {
							logger.Infof("Order amount %.8f exceeds max size %.8f. Executing with TWAP.", orderAmount, currentCfg.Twap.MaxOrderSizeBtc)
							go executeTwapOrder(ctx, execEngine, currentCfg.Pair, orderType, finalPrice, orderAmount)
						} else {
							_, err := execEngine.PlaceOrder(ctx, currentCfg.Pair, orderType, finalPrice, orderAmount, false)
							if err != nil {
								logger.Errorf("Failed to place order for signal: %v", err)
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
	cfg := config.GetConfig()
	numOrders := int(math.Floor(totalAmount / cfg.Twap.MaxOrderSizeBtc))
	lastOrderSize := totalAmount - float64(numOrders)*cfg.Twap.MaxOrderSizeBtc
	chunkSize := cfg.Twap.MaxOrderSizeBtc
	interval := time.Duration(cfg.Twap.IntervalSeconds) * time.Second

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
	if f.replayMode {
		go runReplay(ctx, dbWriter, sigs)
	} else if f.simulateMode {
		go runSimulation(ctx, f, sigs)
	} else {
		// Live trading setup
		client := coincheck.NewClient(cfg.APIKey, cfg.APISecret)
		execEngine := engine.NewLiveExecutionEngine(client, cfg, dbWriter)

		orderBook := indicator.NewOrderBook()
		obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
		orderBookHandler, tradeHandler := setupHandlers(orderBook, dbWriter, cfg.Pair)

		// Benchmark Service
		dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
			cfg.Database.User, cfg.Database.Password, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name, cfg.Database.SSLMode)
		dbpool, err := pgxpool.New(ctx, dbURL)
		if err != nil {
			logger.Fatalf("Benchmark service unable to connect to database: %v", err)
		}
		benchmarkSvc := benchmark.NewDBBenchmarkService(dbpool)
		go startBenchmarkReporter(ctx, benchmarkSvc, orderBook)


		obiCalculator.Start(ctx)
		go processSignalsAndExecute(ctx, cfg, obiCalculator, execEngine)
		go orderMonitor(ctx, cfg, execEngine, client, orderBook)
		go startPnlReporter(ctx, cfg, dbpool)

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
func startPnlReporter(ctx context.Context, cfg *config.Config, dbpool *pgxpool.Pool) {
	if cfg.PnlReport.IntervalMinutes <= 0 {
		logger.Info("PnL reporter is disabled.")
		return
	}

	logger.Infof("Starting PnL reporter: interval=%d minutes, maxAge=%d hours",
		cfg.PnlReport.IntervalMinutes, cfg.PnlReport.MaxAgeHours)

		logger.Infof("Starting PnL reporter: interval=%d minutes, maxAge=%d hours",
			cfg.PnlReport.IntervalMinutes, cfg.PnlReport.MaxAgeHours)

	repo := datastore.NewRepository(dbpool)

			dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
				cfg.Database.User, cfg.Database.Password, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name, cfg.Database.SSLMode)
			dbpool, err := pgxpool.New(ctx, dbURL)
			if err != nil {
				logger.Errorf("PnL reporter unable to connect to database: %v", err)
				return // Exit goroutine if DB connection fails
			}
			defer dbpool.Close()
			repo := datastore.NewRepository(dbpool)

			// Initial run
			generateAndSaveReport(ctx, repo)
			deleteOldReports(ctx, repo, cfg.PnlReport.MaxAgeHours)

			for {
				select {
				case <-ctx.Done():
					logger.Info("Stopping PnL reporter.")
					return
				case <-ticker.C:
					// Re-fetch config inside the loop to get the latest values
					loopCfg := config.GetConfig()
					generateAndSaveReport(ctx, repo)
					deleteOldReports(ctx, repo, loopCfg.PnlReport.MaxAgeHours)
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
	logger.Infof("Pair: %s", cfg.Pair)
	logger.Infof("Long Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Long.OBIThreshold, cfg.Long.TP, cfg.Long.SL)
	logger.Infof("Short Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Short.OBIThreshold, cfg.Short.TP, cfg.Short.SL)
	logger.Info("--------------------")

	replayEngine := engine.NewReplayExecutionEngine(nil)
	var execEngine engine.ExecutionEngine = replayEngine

	orderBook := indicator.NewOrderBook()
	obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
	orderBookHandler, _ := setupHandlers(orderBook, nil, cfg.Pair)

	obiCalculator.Start(ctx)
	go processSignalsAndExecute(ctx, obiCalculator, execEngine)

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

	go func() {
		defer func() {
			logger.Info("Simulation finished.")
			printSimulationSummary(replayEngine)
			sigs <- syscall.SIGTERM
		}()

		for i, event := range events {
			select {
			case <-ctx.Done():
				logger.Info("Simulation cancelled.")
				return
			default:
				if e, ok := event.(datastore.OrderBookEvent); ok {
					orderBookHandler(e.OrderBook)
				}
				if (i+1)%100 == 0 {
					logger.Infof("Processed snapshot %d/%d", i+1, len(events))
				}
			}
		}
		logger.Infof("Finished processing all %d snapshots.", len(events))
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
					_, err = execEngine.PlaceOrder(ctx, currentCfg.Pair, order.OrderType, newRate, pendingAmount, false)
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

// startBenchmarkReporter は、ベンチマーク（例：単純保有戦略）の値を定期的に記録します。
func startBenchmarkReporter(ctx context.Context, svc benchmark.BenchmarkService, book *indicator.OrderBook) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	var initialPrice float64
	logger.Info("Starting benchmark reporter...")

	for {
		select {
		case <-ctx.Done():
			logger.Info("Stopping benchmark reporter.")
			return
		case <-ticker.C:
			midPrice := (book.BestBid() + book.BestAsk()) / 2
			if midPrice <= 0 {
				continue
			}

			if initialPrice == 0 {
				initialPrice = midPrice
				logger.Infof("Benchmark initial price set to: %.2f", initialPrice)
			}

			// 単純保有戦略の現在価値を計算
			// (現在の価格 / 初期価格) * 初期投資額(ここでは1とする)
			// 簡単のため、価値を価格そのものとして記録する
			currentValue := decimal.NewFromFloat(midPrice)

			err := svc.Tick(ctx, "buy_and_hold", currentValue)
			if err != nil {
				logger.Errorf("Failed to record benchmark tick: %v", err)
			} else {
				logger.Debugf("Recorded benchmark tick for buy_and_hold: %s", currentValue.String())
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
	logger.Infof("Pair: %s", cfg.Pair)
	logger.Infof("Time Range: %s -> %s", cfg.Replay.StartTime, cfg.Replay.EndTime)
	logger.Infof("Long Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Long.OBIThreshold, cfg.Long.TP, cfg.Long.SL)
	logger.Infof("Short Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Short.OBIThreshold, cfg.Short.TP, cfg.Short.SL)
	logger.Info("--------------------")

	execEngine := engine.NewReplayExecutionEngine(dbWriter)

	orderBook := indicator.NewOrderBook()
	obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
	orderBookHandler, tradeHandler := setupHandlers(orderBook, dbWriter, cfg.Pair)

	obiCalculator.Start(ctx)
	go processSignalsAndExecute(ctx, obiCalculator, execEngine)

	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.Database.User, cfg.Database.Password, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name, cfg.Database.SSLMode)
	dbpool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Fatalf("Unable to connect to database: %v", err)
	}
	defer dbpool.Close()

	repo := datastore.NewRepository(dbpool)

	startTime, err := time.Parse(time.RFC3339, cfg.Replay.StartTime)
	if err != nil {
		logger.Fatalf("Invalid start_time format: %v", err)
	}
	endTime, err := time.Parse(time.RFC3339, cfg.Replay.EndTime)
	if err != nil {
		logger.Fatalf("Invalid end_time format: %v", err)
	}

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
