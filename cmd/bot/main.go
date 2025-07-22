// Package main is the entry point of the OBI Scalping Bot.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"runtime"
	"runtime/pprof"

	"github.com/fsnotify/fsnotify"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/your-org/obi-scalp-bot/internal/alert"
	"github.com/your-org/obi-scalp-bot/internal/benchmark"
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
	"reflect"
	"sync"
)

type flags struct {
	configPath    string
	tradeConfigPath string
	simulateMode  bool
	csvPath       string
	jsonOutput    bool
	cpuProfile    string
	memProfile    string
}

func main() {
	// --- Initialization ---
	_ = pgxpool.Config{}
	f := parseFlags()

	if f.cpuProfile != "" {
		file, err := os.Create(f.cpuProfile)
		if err != nil {
			logger.Fatalf("could not create CPU profile: %v", err)
		}
		defer file.Close()
		if err := pprof.StartCPUProfile(file); err != nil {
			logger.Fatalf("could not start CPU profile: %v", err)
		}
		defer pprof.StopCPUProfile()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := setupConfig(f.configPath, f.tradeConfigPath)
	setupLogger(cfg.App.LogLevel, f.configPath, cfg.Trade.Pair)
	go watchConfigFiles(f.configPath, f.tradeConfigPath)

	if f.simulateMode && f.csvPath == "" {
		logger.Fatal("CSV file path must be provided in simulation mode using --csv flag")
	}

	if !f.simulateMode {
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

	if f.memProfile != "" {
		file, err := os.Create(f.memProfile)
		if err != nil {
			logger.Fatalf("could not create memory profile: %v", err)
		}
		defer file.Close()
		runtime.GC() // get up-to-date statistics
		if err := pprof.WriteHeapProfile(file); err != nil {
			logger.Fatalf("could not write memory profile: %v", err)
		}
	}

	cancel()
	time.Sleep(1 * time.Second) // Allow time for services to shut down
	logger.Info("OBI Scalping Bot shut down gracefully.")
}

// parseFlags parses command-line flags.
func parseFlags() flags {
	configPath := flag.String("config", "config/app_config.yaml", "Path to the application configuration file")
	tradeConfigPath := flag.String("trade-config", "config/trade_config.yaml", "Path to the trade configuration file")
	simulateMode := flag.Bool("simulate", false, "Enable simulation mode from CSV")
	csvPath := flag.String("csv", "", "Path to the trade data CSV file for simulation")
	jsonOutput := flag.Bool("json-output", false, "Output simulation summary in JSON format")
	cpuProfile := flag.String("cpuprofile", "", "write cpu profile to `file`")
	memProfile := flag.String("memprofile", "", "write memory profile to `file`")
	flag.Parse()
	return flags{
		configPath:      *configPath,
		tradeConfigPath: *tradeConfigPath,
		simulateMode:    *simulateMode,
		csvPath:         *csvPath,
		jsonOutput:      *jsonOutput,
		cpuProfile:      *cpuProfile,
		memProfile:      *memProfile,
	}
}

// setupConfig loads the application configuration.
func setupConfig(appConfigPath, tradeConfigPath string) *config.Config {
	if tradeConfigPath == "" {
		dir := filepath.Dir(appConfigPath)
		tradeConfigPath = filepath.Join(dir, "trade_config.yaml")
	}
	cfg, err := config.LoadConfig(appConfigPath, tradeConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration from %s and %s: %v\n", appConfigPath, tradeConfigPath, err)
		os.Exit(1)
	}
	return cfg
}

// watchConfigFiles sets up a file watcher to automatically reload the configuration on change.
func watchConfigFiles(appConfigPath, tradeConfigPath string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Fatalf("Failed to create file watcher: %v", err)
	}
	defer watcher.Close()

	absAppConfigPath, err := filepath.Abs(appConfigPath)
	if err != nil {
		logger.Fatalf("Failed to get absolute path for app config: %v", err)
	}
	absTradeConfigPath, err := filepath.Abs(tradeConfigPath)
	if err != nil {
		logger.Fatalf("Failed to get absolute path for trade config: %v", err)
	}

	dirsToWatch := make(map[string]bool)
	dirsToWatch[filepath.Dir(absAppConfigPath)] = true
	dirsToWatch[filepath.Dir(absTradeConfigPath)] = true

	for dir := range dirsToWatch {
		logger.Infof("Watching directory %s for config changes...", dir)
		if err := watcher.Add(dir); err != nil {
			logger.Fatalf("Failed to watch directory %s: %v", dir, err)
		}
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			absEventPath, err := filepath.Abs(event.Name)
			if err != nil {
				logger.Errorf("Could not get absolute path for event %s: %v", event.Name, err)
				continue
			}

			if absEventPath == absAppConfigPath || absEventPath == absTradeConfigPath {
				if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					logger.Infof("Config file %s modified (%s). Attempting to reload...", event.Name, event.Op.String())
					time.Sleep(250 * time.Millisecond)

					oldCfg := config.GetConfig()
					newCfg, err := config.ReloadConfig(appConfigPath, tradeConfigPath)
					if err != nil {
						logger.Errorf("Failed to reload configuration: %v", err)
					} else {
						logger.Info("Configuration reloaded successfully.")
						logConfigChanges(oldCfg, newCfg)
						logger.SetGlobalLogLevel(newCfg.App.LogLevel)
					}
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			logger.Errorf("File watcher error: %v", err)
		}
	}
}

// logConfigChanges compares two config structs and logs the differences.
func logConfigChanges(oldCfg, newCfg *config.Config) {
	if oldCfg == nil || newCfg == nil {
		return
	}

	// Compare AppConfig
	compareStructs(reflect.ValueOf(oldCfg.App), reflect.ValueOf(newCfg.App), "App")
	// Compare TradeConfig
	compareStructs(reflect.ValueOf(oldCfg.Trade), reflect.ValueOf(newCfg.Trade), "Trade")
}

func compareStructs(v1, v2 reflect.Value, prefix string) {
	if v1.Kind() != reflect.Struct || v2.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < v1.NumField(); i++ {
		field1 := v1.Field(i)
		field2 := v2.Field(i)
		fieldName := v1.Type().Field(i).Name
		currentPrefix := fmt.Sprintf("%s.%s", prefix, fieldName)

		if field1.Kind() == reflect.Struct {
			compareStructs(field1, field2, currentPrefix)
			continue
		}

		if !reflect.DeepEqual(field1.Interface(), field2.Interface()) {
			logger.Infof("Config changed: %s from '%v' to '%v'", currentPrefix, field1.Interface(), field2.Interface())
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

				logger.Debugf("Evaluating OBI: %.4f, Long Threshold: %.4f, Short Threshold: %.4f",
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
								if _, ok := err.(*engine.RiskCheckError); ok {
									logger.Warnf("Failed to place order for signal: %v", err)
								} else {
									logger.Warnf("Failed to place order for signal: %v", err)
								}
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

	if f.simulateMode {
		go runSimulation(ctx, f, sigs)
	} else {
		// Live trading setup
		notifier, err := alert.NewDiscordNotifier(cfg.App.Alert.Discord)
		if err != nil {
			logger.Warnf("Failed to initialize Discord notifier: %v", err)
		} else {
			logger.Info("Discord notifier initialized successfully.")
		}

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
		client := coincheck.NewClient(cfg.APIKey, cfg.APISecret)
		execEngine := engine.NewLiveExecutionEngine(client, dbWriter, notifier)

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

			dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&timezone=Asia/Tokyo",
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

}


func generateAndSaveReport(ctx context.Context, repo *datastore.Repository) {
	logger.Debug("Generating PnL report...")
	trades, err := repo.FetchAllTradesForReport(ctx)
	if err != nil {
		logger.Errorf("Failed to fetch trades for PnL report: %v", err)
		return
	}

	if len(trades) == 0 {
		logger.Debug("No trades found for PnL report.")
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
	logger.Debug("Successfully generated and saved PnL report.")
}

func deleteOldReports(ctx context.Context, repo *datastore.Repository, maxAgeHours int) {
	if maxAgeHours <= 0 {
		return
	}
	logger.Debugf("Deleting PnL reports older than %d hours...", maxAgeHours)
	deletedCount, err := repo.DeleteOldPnlReports(ctx, maxAgeHours)
	if err != nil {
		logger.Errorf("Failed to delete old PnL reports: %v", err)
		return
	}
	if deletedCount > 0 {
		logger.Debugf("Successfully deleted %d old PnL reports.", deletedCount)
	} else {
		logger.Debug("No old PnL reports to delete.")
	}
}

// waitForShutdownSignal blocks until a shutdown signal is received.
func waitForShutdownSignal(sigs <-chan os.Signal) {
	sig := <-sigs
	logger.Infof("Received signal: %s", sig)
}

// printSimulationSummary calculates and prints the performance metrics of the simulation.
func printSimulationSummary(replayEngine *engine.ReplayExecutionEngine, jsonOutput bool) {
	executedTrades := replayEngine.ExecutedTrades
	totalProfit := replayEngine.GetTotalRealizedPnL()
	totalTrades := len(executedTrades)

	if totalTrades == 0 {
		if jsonOutput {
			fmt.Println(`{"error": "No trades were executed"}`)
		} else {
			fmt.Println("\n--- 損益分析レポート ---")
			fmt.Println("取引は実行されませんでした。")
			fmt.Println("----------------------")
		}
		return
	}

	var wins, losses, closingTrades int
	var longWins, longLosses, shortWins, shortLosses int
	var totalWinAmount, totalLossAmount float64
	var pnlHistory []float64
	var holdingPeriods, winningHoldingPeriods, losingHoldingPeriods []float64
	var consecutiveWins, consecutiveLosses, maxConsecutiveWins, maxConsecutiveLosses int
	var firstTradeTime, lastTradeTime time.Time
	var lastPrice float64

	if len(executedTrades) > 0 {
		firstTradeTime = executedTrades[0].EntryTime
		if firstTradeTime.IsZero() && len(executedTrades) > 1 {
			firstTradeTime = executedTrades[1].EntryTime
		}
	}

	for _, trade := range executedTrades {
		if trade.RealizedPnL != 0 {
			closingTrades++
			pnlHistory = append(pnlHistory, trade.RealizedPnL)

			if !trade.EntryTime.IsZero() && !trade.ExitTime.IsZero() {
				holdingPeriod := trade.ExitTime.Sub(trade.EntryTime).Seconds()
				holdingPeriods = append(holdingPeriods, holdingPeriod)
				if trade.RealizedPnL > 0 {
					winningHoldingPeriods = append(winningHoldingPeriods, holdingPeriod)
				} else {
					losingHoldingPeriods = append(losingHoldingPeriods, holdingPeriod)
				}
			}

			if trade.ExitTime.After(lastTradeTime) {
				lastTradeTime = trade.ExitTime
				lastPrice = trade.Price
			}


			isLongTrade := trade.Side == "buy"

			if trade.RealizedPnL > 0 {
				wins++
				totalWinAmount += trade.RealizedPnL
				consecutiveWins++
				consecutiveLosses = 0
				if consecutiveWins > maxConsecutiveWins {
					maxConsecutiveWins = consecutiveWins
				}
				if isLongTrade {
					longWins++
				} else {
					shortWins++
				}
			} else {
				losses++
				totalLossAmount += trade.RealizedPnL
				consecutiveLosses++
				consecutiveWins = 0
				if consecutiveLosses > maxConsecutiveLosses {
					maxConsecutiveLosses = consecutiveLosses
				}
				if isLongTrade {
					longLosses++
				} else {
					shortLosses++
				}
			}
		}
	}


	winRate := 0.0
	if closingTrades > 0 {
		winRate = float64(wins) / float64(closingTrades) * 100
	}

	longWinRate := 0.0
	if longWins+longLosses > 0 {
		longWinRate = float64(longWins) / float64(longWins+longLosses) * 100
	}

	shortWinRate := 0.0
	if shortWins+shortLosses > 0 {
		shortWinRate = float64(shortWins) / float64(shortWins+shortLosses) * 100
	}

	avgWin := 0.0
	if wins > 0 {
		avgWin = totalWinAmount / float64(wins)
	}
	avgLoss := 0.0
	if losses > 0 {
		// totalLossAmount is negative
		avgLoss = math.Abs(totalLossAmount / float64(losses))
	}

	riskRewardRatio := 0.0
	if avgLoss != 0 {
		riskRewardRatio = avgWin / avgLoss
	}

	profitFactor := 0.0
	if totalLossAmount != 0 {
		profitFactor = math.Abs(totalWinAmount / totalLossAmount)
	}

	// Max Drawdown
	var peakPnl, maxDrawdown float64
	var currentCumulativePnl float64
	for _, pnl := range pnlHistory {
		currentCumulativePnl += pnl
		if currentCumulativePnl > peakPnl {
			peakPnl = currentCumulativePnl
		}
		drawdown := peakPnl - currentCumulativePnl
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}
	}

	recoveryFactor := 0.0
	if maxDrawdown > 0 {
		recoveryFactor = totalProfit / maxDrawdown
	}

	// Sharpe Ratio (assuming risk-free rate is 0)
	sharpeRatio := 0.0
	if len(pnlHistory) > 1 {
		mean := totalProfit / float64(len(pnlHistory))
		stdDev := 0.0
		for _, pnl := range pnlHistory {
			stdDev += math.Pow(pnl-mean, 2)
		}
		stdDev = math.Sqrt(stdDev / float64(len(pnlHistory)-1))
		if stdDev > 0 {
			sharpeRatio = mean / stdDev
		}
	}

	// Sortino Ratio
	sortinoRatio := 0.0
	if len(pnlHistory) > 1 {
		mean := totalProfit / float64(len(pnlHistory))
		downsideDeviation := 0.0
		downsideCount := 0
		for _, pnl := range pnlHistory {
			if pnl < mean {
				downsideDeviation += math.Pow(pnl-mean, 2)
				downsideCount++
			}
		}
		if downsideCount > 1 {
			downsideDeviation = math.Sqrt(downsideDeviation / float64(downsideCount-1))
			if downsideDeviation > 0 {
				sortinoRatio = mean / downsideDeviation
			}
		}
	}

	// Calmar Ratio
	calmarRatio := 0.0
	if maxDrawdown > 0 {
		calmarRatio = totalProfit / maxDrawdown
	}

	avgHoldingPeriod, avgWinningHoldingPeriod, avgLosingHoldingPeriod := 0.0, 0.0, 0.0
	if len(holdingPeriods) > 0 {
		sum := 0.0
		for _, hp := range holdingPeriods { sum += hp }
		avgHoldingPeriod = sum / float64(len(holdingPeriods))
	}
	if len(winningHoldingPeriods) > 0 {
		sum := 0.0
		for _, hp := range winningHoldingPeriods { sum += hp }
		avgWinningHoldingPeriod = sum / float64(len(winningHoldingPeriods))
	}
	if len(losingHoldingPeriods) > 0 {
		sum := 0.0
		for _, hp := range losingHoldingPeriods { sum += hp }
		avgLosingHoldingPeriod = sum / float64(len(losingHoldingPeriods))
	}

	// Buy & Hold Return
	buyAndHoldReturn := 0.0
	initialPrice := 0.0
	if len(executedTrades) > 0 {
		initialPrice = executedTrades[0].Price
	}
	if initialPrice > 0 && lastPrice > 0 {
		buyAndHoldReturn = lastPrice - initialPrice
	}
	returnVsBuyAndHold := totalProfit - buyAndHoldReturn


	if jsonOutput {
		summary := map[string]interface{}{
			"TotalProfit":         totalProfit,
			"TotalTrades":         closingTrades,
			"WinningTrades":       wins,
			"LosingTrades":        losses,
			"WinRate":             winRate,
			"LongWinningTrades":   longWins,
			"LongLosingTrades":    longLosses,
			"LongWinRate":         longWinRate,
			"ShortWinningTrades":  shortWins,
			"ShortLosingTrades":   shortLosses,
			"ShortWinRate":        shortWinRate,
			"AverageWin":          avgWin,
			"AverageLoss":         avgLoss,
			"RiskRewardRatio":     riskRewardRatio,
			"ProfitFactor":        profitFactor,
			"MaxDrawdown":         maxDrawdown,
			"RecoveryFactor":      recoveryFactor,
			"SharpeRatio":         sharpeRatio,
			"SortinoRatio":        sortinoRatio,
			"CalmarRatio":         calmarRatio,
			"MaxConsecutiveWins":  maxConsecutiveWins,
			"MaxConsecutiveLosses": maxConsecutiveLosses,
			"AverageHoldingPeriodSeconds": avgHoldingPeriod,
			"AverageWinningHoldingPeriodSeconds": avgWinningHoldingPeriod,
			"AverageLosingHoldingPeriodSeconds":  avgLosingHoldingPeriod,
			"BuyAndHoldReturn":    buyAndHoldReturn,
			"ReturnVsBuyAndHold":  returnVsBuyAndHold,
		}
		output, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			fmt.Printf(`{"error": "failed to marshal summary: %v"}`, err)
		} else {
			fmt.Println(string(output))
		}
	} else {
		fmt.Println("--- 損益分析レポート ---")
		if !firstTradeTime.IsZero() && !lastTradeTime.IsZero() {
			fmt.Printf("集計期間: %s から %s\n", firstTradeTime.Format(time.RFC3339), lastTradeTime.Format(time.RFC3339))
		}
		fmt.Println()
		fmt.Println("--- 全体 ---")
		fmt.Printf("約定済み取引数: %d\n", closingTrades)
		fmt.Println()
		fmt.Println("--- 約定済み取引の分析 ---")
		fmt.Printf("勝ちトレード数: %d\n", wins)
		fmt.Printf("負けトレード数: %d\n", losses)
		fmt.Printf("勝率: %.2f%%\n", winRate)
		fmt.Printf("合計損益: %.2f\n", totalProfit)
		fmt.Printf("平均利益: %.2f\n", avgWin)
		fmt.Printf("平均損失: %.2f\n", avgLoss)
		fmt.Printf("リスクリワードレシオ: %.2f\n", riskRewardRatio)
		fmt.Println()
		fmt.Println("--- ロング戦略 ---")
		fmt.Printf("勝ちトレード数: %d\n", longWins)
		fmt.Printf("負けトレード数: %d\n", longLosses)
		fmt.Printf("勝率: %.2f%%\n", longWinRate)
		fmt.Println()
		fmt.Println("--- ショート戦略 ---")
		fmt.Printf("勝ちトレード数: %d\n", shortWins)
		fmt.Printf("負けトレード数: %d\n", shortLosses)
		fmt.Printf("勝率: %.2f%%\n", shortWinRate)
		fmt.Println()
		fmt.Println("--- パフォーマンス指標 ---")
		fmt.Printf("プロフィットファクター: %.2f\n", profitFactor)
		fmt.Printf("シャープレシオ: %.2f\n", sharpeRatio)
		fmt.Printf("ソルティノレシオ: %.2f\n", sortinoRatio)
		fmt.Printf("カルマーレシオ: %.2f\n", calmarRatio)
		fmt.Printf("最大ドローダウン: %.2f\n", maxDrawdown)
		fmt.Printf("リカバリーファクター: %.2f\n", recoveryFactor)
		fmt.Printf("平均保有期間: %.2f 秒\n", avgHoldingPeriod)
		fmt.Printf("勝ちトレードの平均保有期間: %.2f 秒\n", avgWinningHoldingPeriod)
		fmt.Printf("負けトレードの平均保有期間: %.2f 秒\n", avgLosingHoldingPeriod)
		fmt.Printf("最大連勝数: %d\n", maxConsecutiveWins)
		fmt.Printf("最大連敗数: %d\n", maxConsecutiveLosses)
		fmt.Printf("バイ・アンド・ホールドリターン: %.2f\n", buyAndHoldReturn)
		fmt.Printf("リターン vs バイ・アンド・ホールド: %.2f\n", returnVsBuyAndHold)
		fmt.Println("----------------------")
		// For optimizer parsing
		fmt.Printf("\nTotalProfit: %.2f\n", totalProfit)
	}
}


// runSimulation runs a backtest using data from a CSV file.
func runSimulation(ctx context.Context, f flags, sigs chan<- os.Signal) {
	rand.Seed(1)
	cfg := config.GetConfig()
	if !f.jsonOutput {
		logger.Info("--- SIMULATION MODE ---")
		logger.Infof("CSV File: %s", f.csvPath)
		logger.Infof("Config File: %s", f.configPath)
		logger.Infof("Trade Config File: %s", f.tradeConfigPath)
		logger.Infof("Pair: %s", cfg.Trade.Pair)
		logger.Infof("Long Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Trade.Long.OBIThreshold, cfg.Trade.Long.TP, cfg.Trade.Long.SL)
		logger.Infof("Short Strategy: OBI=%.2f, TP=%.f, SL=%.f", cfg.Trade.Short.OBIThreshold, cfg.Trade.Short.TP, cfg.Trade.Short.SL)
		logger.Info("--------------------")
	}

	orderBook := indicator.NewOrderBook()
	replayEngine := engine.NewReplayExecutionEngine(orderBook)
	var execEngine engine.ExecutionEngine = replayEngine
	obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)
	orderBookHandler, _ := setupHandlers(orderBook, nil, cfg.Trade.Pair)

	// In simulation, we do not start the periodic calculator. Instead, we manually trigger calculations.
	// obiCalculator.Start(ctx)
	go processSignalsAndExecute(ctx, obiCalculator, execEngine, nil)

	logger.Infof("Streaming market events from %s", f.csvPath)
	eventCh, errCh := datastore.StreamMarketEventsFromCSV(ctx, f.csvPath)

	go func() {
		defer func() {
			if !f.jsonOutput {
				logger.Info("Simulation finished.")
			}
			printSimulationSummary(replayEngine, f.jsonOutput)
			sigs <- syscall.SIGTERM
		}()

		var snapshotCount int
		for {
			select {
			case orderBookData, ok := <-eventCh:
				if !ok {
					logger.Infof("Finished processing all %d snapshots.", snapshotCount)
					return
				}
				orderBookHandler(orderBookData)
				// Manually trigger OBI calculation with the timestamp from the event
				obiCalculator.Calculate(orderBookData.Time)
				snapshotCount++
				if snapshotCount%1000 == 0 && !f.jsonOutput {
					logger.Infof("Processed snapshot %d at time %s", snapshotCount, orderBookData.Time.Format(time.RFC3339))
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
