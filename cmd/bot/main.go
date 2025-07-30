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
	"github.com/go-chi/chi/v5"
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
	"github.com/your-org/obi-scalp-bot/pkg/cvd"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
	"go.uber.org/zap"
	"reflect"
)

import (
	"bufio"
	"sync"
)

// SimulationRequest defines the structure for a single simulation run.
type SimulationRequest struct {
	TrialID     int                 `json:"trial_id"`
	TradeConfig *config.TradeConfig `json:"trade_config"`
}

// OptimizationRequest defines the structure for a batch of simulations.
type OptimizationRequest struct {
	CSVPath     string              `json:"csv_path"`
	Simulations []SimulationRequest `json:"simulations"`
}

// SimulationResult wraps the result of a single simulation.
type SimulationResult struct {
	TrialID int                    `json:"trial_id"`
	Summary map[string]interface{} `json:"summary"`
	Error   string                 `json:"error,omitempty"`
}

type flags struct {
	configPath      string
	tradeConfigPath string
	simulateMode    bool
	serveMode       bool
	csvPath         string
	jsonOutput      bool
	cpuProfile      string
	memProfile      string
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
	// In serve mode, we will have minimal logging to avoid polluting stdout
	if !f.serveMode {
		setupLogger(cfg.App.LogLevel, f.configPath, cfg.Trade.Pair)
	}
	go watchConfigFiles(f.configPath, f.tradeConfigPath)

	if (f.simulateMode || f.serveMode) && f.csvPath == "" && !f.serveMode { // In serveMode, csvPath comes with the request
		logger.Fatal("CSV file path must be provided in simulation mode using --csv flag")
	}

	if !f.simulateMode && !f.serveMode {
		startHTTPServer(ctx)
	}

	// --- Main Execution Loop ---
	// Use a separate channel for graceful shutdown signals
	shutdownSigs := make(chan os.Signal, 1)
	signal.Notify(shutdownSigs, syscall.SIGINT, syscall.SIGTERM)

	var dbWriter *dbwriter.Writer
	if !f.simulateMode && !f.serveMode {
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
	tradeConfigPath := flag.String("trade-config", "/data/params/trade_config.yaml", "Path to the trade configuration file")
	simulateMode := flag.Bool("simulate", false, "Enable simulation mode from CSV")
	serveMode := flag.Bool("serve", false, "Enable server mode for optimization")
	csvPath := flag.String("csv", "", "Path to the trade data CSV file for simulation")
	jsonOutput := flag.Bool("json-output", false, "Output simulation summary in JSON format")
	cpuProfile := flag.String("cpuprofile", "", "write cpu profile to `file`")
	memProfile := flag.String("memprofile", "", "write memory profile to `file`")
	flag.Parse()
	return flags{
		configPath:      *configPath,
		tradeConfigPath: *tradeConfigPath,
		simulateMode:    *simulateMode,
		serveMode:       *serveMode,
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
				logger.Warnf("Could not get absolute path for event %s: %v", event.Name, err)
				continue
			}

			if absEventPath == absAppConfigPath || absEventPath == absTradeConfigPath {
				if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					logger.Infof("Config file %s modified (%s). Attempting to reload...", event.Name, event.Op.String())
					time.Sleep(250 * time.Millisecond)

					oldCfg := config.GetConfig()
					newCfg, err := config.ReloadConfig(appConfigPath, tradeConfigPath)
					if err != nil {
						logger.Warnf("Failed to reload configuration: %v", err)
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
			logger.Warnf("File watcher error: %v", err)
		}
	}
}

func convertTrades(trades []coincheck.TradeData) []cvd.Trade {
	cvdTrades := make([]cvd.Trade, len(trades))
	for i, t := range trades {
		price, _ := strconv.ParseFloat(t.Rate(), 64)
		size, _ := strconv.ParseFloat(t.Amount(), 64)
		cvdTrades[i] = cvd.Trade{
			ID:        t.TransactionID(),
			Side:      t.TakerSide(),
			Price:     price,
			Size:      size,
			Timestamp: time.Now(), // This is an approximation
		}
	}
	return cvdTrades
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

// startHTTPServer starts the HTTP server for health checks and other API endpoints.
func startHTTPServer(ctx context.Context) {
	cfg := config.GetConfig()

	// Create a new chi router
	r := chi.NewRouter()

	// Health check endpoint
	r.Get("/health", handler.HealthCheckHandler)

	// PnL report endpoint
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&timezone=Asia/Tokyo",
		cfg.App.Database.User, cfg.App.Database.Password, cfg.App.Database.Host, cfg.App.Database.Port, cfg.App.Database.Name, cfg.App.Database.SSLMode)
	dbpool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Warnf("HTTP server unable to connect to database for PnL handler: %v", err)
	} else {
		repo := datastore.NewRepository(dbpool)
		pnlHandler := handler.NewPnlHandler(repo)
		pnlHandler.RegisterRoutes(r)
		logger.Info("PnL report endpoint /pnl/latest_report registered.")
	}

	go func() {
		logger.Info("HTTP server starting on :8080")
		if err := http.ListenAndServe(":8080", r); err != nil {
			logger.Fatalf("HTTP server failed: %v", err)
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
					logger.Warnf("Failed to parse order book price: %v", err)
					continue
				}
				size, err := strconv.ParseFloat(level[1], 64)
				if err != nil {
					logger.Warnf("Failed to parse order book size: %v", err)
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
			logger.Warnf("Failed to parse trade price: %v", err)
			return
		}
		size, err := strconv.ParseFloat(data.Amount(), 64)
		if err != nil {
			logger.Warnf("Failed to parse trade size: %v", err)
			return
		}
		txID, err := strconv.ParseInt(data.TransactionID(), 10, 64)
		if err != nil {
			logger.Warnf("Failed to parse transaction ID: %v", err)
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
func processSignalsAndExecute(ctx context.Context, obiCalculator *indicator.OBICalculator, execEngine engine.ExecutionEngine, benchmarkService *benchmark.Service, webSocketClient *coincheck.WebSocketClient, wg *sync.WaitGroup, cfg *config.Config) {
	signalEngine, err := tradingsignal.NewSignalEngine(&cfg.Trade)
	if err != nil {
		logger.Fatalf("Failed to create signal engine: %v", err)
	}

	resultsCh := obiCalculator.Subscribe()
	go func() {
		if wg != nil {
			defer wg.Done()
		}
		for {
			select {
			case <-ctx.Done():
				logger.Info("Signal processing and execution goroutine shutting down.")
				return
			case result := <-resultsCh:
				currentCfg := cfg
				if result.BestBid <= 0 || result.BestAsk <= 0 {
					logger.Warnf("Skipping signal evaluation due to invalid best bid/ask: BestBid=%.2f, BestAsk=%.2f", result.BestBid, result.BestAsk)
					continue
				}
				midPrice := (result.BestAsk + result.BestBid) / 2
				if benchmarkService != nil {
					benchmarkService.Tick(ctx, midPrice)
				}
				var trades []coincheck.TradeData
				if webSocketClient != nil {
					trades = webSocketClient.GetTrades()
				}
				cvdTrades := convertTrades(trades)

				signalEngine.UpdateMarketData(result.Timestamp, midPrice, result.BestBid, result.BestAsk, 1.0, 1.0, cvdTrades)

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

						orderAmount := currentCfg.Trade.OrderAmount
						if liveEngine, ok := execEngine.(*engine.LiveExecutionEngine); ok {
							// In live trading, calculate the amount based on balance and ratio
							balance, err := liveEngine.GetBalance()
							if err != nil {
								logger.Warnf("Failed to get balance for order sizing: %v", err)
								continue
							}
							currentBtc, _ := strconv.ParseFloat(balance.Btc, 64)
							availableBtc := currentBtc // Simplified for now
							orderAmount = availableBtc * currentCfg.Trade.OrderRatio
						}

						// Only proceed if the order amount is above the minimum
						if orderAmount >= 0.001 {
							logger.Debugf("Executing trade for signal: %s. %s", tradingSignal.Type.String(), orderLogMsg)
							if bool(currentCfg.Trade.Twap.Enabled) && orderAmount > currentCfg.Trade.Twap.MaxOrderSizeBtc {
								logger.Debugf("Order amount %.8f exceeds max size %.8f. Executing with TWAP.", orderAmount, currentCfg.Trade.Twap.MaxOrderSizeBtc)
								go executeTwapOrder(ctx, execEngine, currentCfg.Trade.Pair, orderType, finalPrice, orderAmount)
							} else {
								logger.Debugf("Calling PlaceOrder with: type=%s, price=%.2f, amount=%.8f", orderType, finalPrice, orderAmount)
								resp, err := execEngine.PlaceOrder(ctx, currentCfg.Trade.Pair, orderType, finalPrice, orderAmount, false)
								if err != nil {
									if _, ok := err.(*engine.RiskCheckError); ok {
										// This is a risk check failure, which is expected, so log as debug
										logger.Debugf("Failed to place order for signal: %v", err)
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
				logger.Warnf("Error in TWAP chunk %d: %v. Aborting.", i+1, err)
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
				logger.Warnf("Error in TWAP last chunk: %v. Aborting.", err)
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

	if f.serveMode {
		runServerMode(ctx, f, sigs)
	} else if f.simulateMode {
		summaryCh := make(chan map[string]interface{}, 1)
		go runSimulation(ctx, f, sigs, summaryCh)

		// Block until simulation is done and summary is received
		summary := <-summaryCh
		output, err := json.Marshal(summary)
		if err != nil {
			fmt.Printf(`{"error": "failed to marshal summary: %v"}`, err)
		} else {
			// The final JSON output for the optimizer
			fmt.Println(string(output))
		}
		// The simulation goroutine will send a signal to shut down.
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
	wsClient := coincheck.NewWebSocketClient(orderBookHandler, tradeHandler)
	go processSignalsAndExecute(ctx, obiCalculator, execEngine, benchmarkService, wsClient, nil, config.GetConfig())
		go orderMonitor(ctx, execEngine, client, orderBook)
		go positionMonitor(ctx, execEngine, orderBook) // Added for partial exit

		go func() {
			logger.Info("Connecting to Coincheck WebSocket API...")
			if err := wsClient.Connect(ctx); err != nil {
				logger.Warnf("WebSocket client exited with error: %v", err)
				sigs <- syscall.SIGTERM
			}
		}()

		// Wait for the WebSocket client to be ready before proceeding.
		<-wsClient.Ready()
		logger.Info("WebSocket client is ready.")
	}
}


// waitForShutdownSignal blocks until a shutdown signal is received.
func waitForShutdownSignal(sigs <-chan os.Signal) {
	sig := <-sigs
	logger.Infof("Received signal: %s", sig)
}

// runSimulation runs a backtest using data from a CSV file and sends the summary through a channel.
func runSimulation(ctx context.Context, f flags, sigs chan<- os.Signal, summaryCh chan<- map[string]interface{}) {
	defer close(summaryCh) // Ensure channel is closed when done.
	rand.Seed(1)
	cfg := config.GetConfig()
	if !f.jsonOutput {
		logger.Info("--- SIMULATION MODE ---")
		logger.Infof("CSV File: %s", f.csvPath)
		logger.Infof("Config File: %s", f.configPath)
		logger.Infof("Trade Config File: %s", f.tradeConfigPath)
		logger.Infof("Pair: %s", cfg.Trade.Pair)
		logger.Info("--------------------")
	}

	orderBook := indicator.NewOrderBook()
	replayEngine := engine.NewReplayExecutionEngine(orderBook)
	signalEngine, err := tradingsignal.NewSignalEngine(&cfg.Trade)
	if err != nil {
		logger.Fatalf("Failed to create signal engine: %v", err)
	}

	simCtx, cancelSim := context.WithCancel(ctx)
	defer cancelSim()

	logger.Infof("Streaming market events from %s", f.csvPath)
	eventCh, errCh := datastore.StreamMarketEventsFromCSV(simCtx, f.csvPath)

	var eventCount int
	var processingDone bool

	// Hold the state of the last book update
	var lastMidPrice, lastBestBid, lastBestAsk, lastBestBidSize, lastBestAskSize float64
	var lastBookTime time.Time
	var lastObiResult indicator.OBIResult

	for !processingDone {
		select {
		case marketEvent, ok := <-eventCh:
			if !ok {
				logger.Infof("Finished processing all %d events.", eventCount)
				processingDone = true
				continue
			}

			eventCount++
			if eventCount%10000 == 0 && !f.jsonOutput {
				logger.Infof("Processed event %d at time %s", eventCount, marketEvent.GetTime().Format(time.RFC3339))
			}

			switch event := marketEvent.(type) {
			case datastore.OrderBookEvent:
				orderBook.ApplyUpdate(event.OrderBookData)
				obiResult, ok := orderBook.CalculateOBI(indicator.OBILevels...)
				if !ok || obiResult.BestBid <= 0 || obiResult.BestAsk <= 0 {
					continue // Skip if book is invalid
				}
				// Update last known book state
				lastBookTime = event.GetTime()
				lastMidPrice = (obiResult.BestAsk + obiResult.BestBid) / 2
				lastBestBid, lastBestAsk = obiResult.BestBid, obiResult.BestAsk
				lastBestBidSize, lastBestAskSize = orderBook.GetBestBidAskSize()
				lastObiResult = obiResult

				// Evaluate signal on book update
				signalEngine.UpdateMarketData(lastBookTime, lastMidPrice, lastBestBid, lastBestAsk, lastBestBidSize, lastBestAskSize, nil)
				tradingSignal := signalEngine.Evaluate(lastBookTime, lastObiResult.OBI8)
				if tradingSignal != nil {
					orderType := ""
					if tradingSignal.Type == tradingsignal.SignalLong {
						orderType = "buy"
					} else if tradingSignal.Type == tradingsignal.SignalShort {
						orderType = "sell"
					}
					if orderType != "" {
						_, err := replayEngine.PlaceOrder(ctx, cfg.Trade.Pair, orderType, tradingSignal.EntryPrice, cfg.Trade.OrderAmount, false)
						if err != nil && !f.jsonOutput {
							logger.Warnf("Replay engine failed to place order: %v", err)
						}
					}
				}

			case datastore.TradeEvent:
				if lastBookTime.IsZero() {
					continue // No book data yet
				}
				replayEngine.UpdateLastPrice(event.Price)
				signalEngine.UpdateMarketData(event.GetTime(), lastMidPrice, lastBestBid, lastBestAsk, lastBestBidSize, lastBestAskSize, []cvd.Trade{event.Trade})

				tradingSignal := signalEngine.Evaluate(event.GetTime(), lastObiResult.OBI8)
				if tradingSignal != nil {
					orderType := ""
					if tradingSignal.Type == tradingsignal.SignalLong {
						orderType = "buy"
					} else if tradingSignal.Type == tradingsignal.SignalShort {
						orderType = "sell"
					}
					if orderType != "" {
						_, err := replayEngine.PlaceOrder(ctx, cfg.Trade.Pair, orderType, tradingSignal.EntryPrice, cfg.Trade.OrderAmount, false)
						if err != nil && !f.jsonOutput {
							logger.Warnf("Replay engine failed to place order: %v", err)
						}
					}
				}
			}

		case err := <-errCh:
			if err != nil {
				logger.Fatalf("Error while streaming market events: %v", err)
			}
			processingDone = true
		case <-ctx.Done():
			logger.Info("Simulation cancelled by parent context.")
			processingDone = true
		}
	}

	if !f.jsonOutput {
		logger.Info("Simulation finished.")
	}
	summary := getSimulationSummaryMap(replayEngine)
	summaryCh <- summary

	// Signal that the main application can shut down.
	sigs <- syscall.SIGTERM
}

// runServerMode runs the bot in a loop, accepting new configurations via stdin.
func runServerMode(ctx context.Context, f flags, sigs chan<- os.Signal) {
	logger.Info("--- SERVER MODE ---")

	marketDataCache := make(map[string][]datastore.MarketEvent)
	scanner := bufio.NewScanner(os.Stdin)
	// Increase the scanner's buffer size to handle potentially large JSON inputs
	const maxCapacity = 4 * 1024 * 1024 // 4MB
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	fmt.Println("READY")

	for scanner.Scan() {
		line := scanner.Text()
		if line == "EXIT" {
			logger.Info("Received EXIT command. Shutting down server mode.")
			break
		}
		if len(line) == 0 {
			continue
		}

		var optRequest OptimizationRequest
		if err := json.Unmarshal([]byte(line), &optRequest); err != nil {
			logger.Warnf("Invalid JSON format received: %s. Error: %v", line, err)
			fmt.Println(`{"error": "invalid json format"}`)
			continue
		}

		csvPath := optRequest.CSVPath
		marketEvents, ok := marketDataCache[csvPath]
		if !ok {
			logger.Infof("Cache miss for %s. Loading market data...", csvPath)
			var err error
			marketEvents, err = datastore.LoadMarketEventsFromCSV(csvPath)
			if err != nil {
				logger.Warnf("Failed to load market events from %s: %v", csvPath, err)
				fmt.Printf(`{"error": "failed to load market data from %s: %v"}`, csvPath, err)
				continue
			}
			marketDataCache[csvPath] = marketEvents
			logger.Infof("Finished loading and caching %d market events from %s.", len(marketEvents), csvPath)
		} else {
			logger.Debugf("Cache hit for %s.", csvPath)
		}

		// Run simulations in parallel
		results := runParallelSimulations(ctx, marketEvents, optRequest.Simulations)

		output, err := json.Marshal(results)
		if err != nil {
			fmt.Printf(`{"error": "failed to marshal results: %v"}`, err)
		} else {
			fmt.Println(string(output))
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Fatalf("Error reading from stdin: %v", err)
	}

	logger.Info("Server mode finished.")
	sigs <- syscall.SIGTERM
}

// runParallelSimulations executes multiple simulations in parallel using goroutines.
func runParallelSimulations(ctx context.Context, marketEvents []datastore.MarketEvent, requests []SimulationRequest) []SimulationResult {
	var wg sync.WaitGroup
	resultsChan := make(chan SimulationResult, len(requests))

	numWorkers := runtime.NumCPU()
	if len(requests) < numWorkers {
		numWorkers = len(requests)
	}
	sem := make(chan struct{}, numWorkers)

	for _, req := range requests {
		wg.Add(1)
		sem <- struct{}{} // Acquire a semaphore slot

		go func(request SimulationRequest) {
			defer wg.Done()
			defer func() { <-sem }() // Release the slot

			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("Recovered from panic in simulation goroutine: %v", r)
					buf := make([]byte, 1024)
					n := runtime.Stack(buf, false)
					logger.Errorf("Stack trace: %s", string(buf[:n]))
					resultsChan <- SimulationResult{
						TrialID: request.TrialID,
						Error:   fmt.Sprintf("panic recovered: %v", r),
					}
				}
			}()

			simCtx, cancelSim := context.WithCancel(ctx)
			defer cancelSim()

			summary := runSingleSimulationInMemory(simCtx, request.TradeConfig, marketEvents)
			resultsChan <- SimulationResult{
				TrialID: request.TrialID,
				Summary: summary,
			}
		}(req)
	}

	wg.Wait()
	close(resultsChan)

	var allResults []SimulationResult
	for result := range resultsChan {
		allResults = append(allResults, result)
	}

	return allResults
}

// runSingleSimulationInMemory runs a backtest with a given config and pre-loaded market data.
func runSingleSimulationInMemory(ctx context.Context, tradeCfg *config.TradeConfig, marketEvents []datastore.MarketEvent) map[string]interface{} {
	rand.Seed(1) // Ensure reproducibility

	simConfig := config.GetConfigCopy()
	simConfig.Trade = *tradeCfg

	orderBook := indicator.NewOrderBook()
	replayEngine := engine.NewReplayExecutionEngine(orderBook)
	signalEngine, err := tradingsignal.NewSignalEngine(&simConfig.Trade)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("failed to create signal engine: %v", err)}
	}

	// Hold the state of the last book update
	var lastMidPrice, lastBestBid, lastBestAsk, lastBestBidSize, lastBestAskSize float64
	var lastBookTime time.Time
	var lastObiResult indicator.OBIResult

	for _, marketEvent := range marketEvents {
		select {
		case <-ctx.Done():
			logger.Warn("Simulation run cancelled.")
			return getSimulationSummaryMap(replayEngine)
		default:
			switch event := marketEvent.(type) {
			case datastore.OrderBookEvent:
				orderBook.ApplyUpdate(event.OrderBookData)
				obiResult, ok := orderBook.CalculateOBI(indicator.OBILevels...)
				if !ok || obiResult.BestBid <= 0 || obiResult.BestAsk <= 0 {
					continue // Skip if book is invalid
				}
				// Update last known book state
				lastBookTime = event.GetTime()
				lastMidPrice = (obiResult.BestAsk + obiResult.BestBid) / 2
				lastBestBid, lastBestAsk = obiResult.BestBid, obiResult.BestAsk
				lastBestBidSize, lastBestAskSize = orderBook.GetBestBidAskSize()
				lastObiResult = obiResult

				// Evaluate signal on book update
				signalEngine.UpdateMarketData(lastBookTime, lastMidPrice, lastBestBid, lastBestAsk, lastBestBidSize, lastBestAskSize, nil) // No trades to process here
				tradingSignal := signalEngine.Evaluate(lastBookTime, lastObiResult.OBI8)
				if tradingSignal != nil {
					orderType := ""
					if tradingSignal.Type == tradingsignal.SignalLong {
						orderType = "buy"
					} else if tradingSignal.Type == tradingsignal.SignalShort {
						orderType = "sell"
					}
					if orderType != "" {
						replayEngine.PlaceOrder(ctx, simConfig.Trade.Pair, orderType, tradingSignal.EntryPrice, simConfig.Trade.OrderAmount, false)
					}
				}

			case datastore.TradeEvent:
				// A trade happened. We need to update CVD and re-evaluate the signal
				// using the last known book state.
				if lastBookTime.IsZero() {
					continue // No book data yet
				}
				replayEngine.UpdateLastPrice(event.Price)
				// Update signal engine with the new trade data
				signalEngine.UpdateMarketData(event.GetTime(), lastMidPrice, lastBestBid, lastBestAsk, lastBestBidSize, lastBestAskSize, []cvd.Trade{event.Trade})

				// Re-evaluate the signal
				tradingSignal := signalEngine.Evaluate(event.GetTime(), lastObiResult.OBI8)
				if tradingSignal != nil {
					orderType := ""
					if tradingSignal.Type == tradingsignal.SignalLong {
						orderType = "buy"
					} else if tradingSignal.Type == tradingsignal.SignalShort {
						orderType = "sell"
					}
					if orderType != "" {
						replayEngine.PlaceOrder(ctx, simConfig.Trade.Pair, orderType, tradingSignal.EntryPrice, simConfig.Trade.OrderAmount, false)
					}
				}
			}
		}
	}

	summary := getSimulationSummaryMap(replayEngine)
	if trades, ok := summary["TotalTrades"].(int); ok && trades == 0 {
		logger.Debug("Simulation finished with 0 trades.")
	}
	return summary
}

// getSimulationSummaryMap is a modified version of printSimulationSummary that returns a map.
func getSimulationSummaryMap(replayEngine *engine.ReplayExecutionEngine) map[string]interface{} {
	executedTrades := replayEngine.ExecutedTrades
	totalProfit := replayEngine.GetTotalRealizedPnL()
	positionSize, avgEntryPrice := replayEngine.GetPosition().Get()
	unrealizedPnL := replayEngine.GetPnLCalculator().CalculateUnrealizedPnL(positionSize, avgEntryPrice, replayEngine.GetLastPrice())
	totalProfit += unrealizedPnL // Add unrealized PnL to the final profit for summary purposes

	totalTrades := len(executedTrades)

	summary := map[string]interface{}{
		"TotalProfit":         totalProfit,
		"TotalTrades":         0,
		"WinningTrades":       0,
		"LosingTrades":        0,
		"WinRate":             0.0,
		"LongWinningTrades":   0,
		"LongLosingTrades":    0,
		"LongWinRate":         0.0,
		"ShortWinningTrades":  0,
		"ShortLosingTrades":   0,
		"ShortWinRate":        0.0,
		"AverageWin":          0.0,
		"AverageLoss":         0.0,
		"RiskRewardRatio":     0.0,
		"ProfitFactor":        0.0,
		"MaxDrawdown":         0.0,
		"RecoveryFactor":      0.0,
		"SharpeRatio":         0.0,
		"SortinoRatio":        0.0,
		"CalmarRatio":         0.0,
		"MaxConsecutiveWins":  0,
		"MaxConsecutiveLosses": 0,
		"AverageHoldingPeriodSeconds": 0.0,
		"AverageWinningHoldingPeriodSeconds": 0.0,
		"AverageLosingHoldingPeriodSeconds":  0.0,
		"BuyAndHoldReturn":    0.0,
		"ReturnVsBuyAndHold":  0.0,
	}

	if totalTrades == 0 {
		return summary
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

			if trade.RealizedPnL > 0 {
				wins++
				totalWinAmount += trade.RealizedPnL
				consecutiveWins++
				consecutiveLosses = 0
				if consecutiveWins > maxConsecutiveWins {
					maxConsecutiveWins = consecutiveWins
				}
				if trade.PositionSide == "long" {
					longWins++
				} else if trade.PositionSide == "short" {
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
				if trade.PositionSide == "long" {
					longLosses++
				} else if trade.PositionSide == "short" {
					shortLosses++
				}
			}
		}
	}

	summary["TotalTrades"] = closingTrades
	summary["WinningTrades"] = wins
	summary["LosingTrades"] = losses
	summary["LongWinningTrades"] = longWins
	summary["LongLosingTrades"] = longLosses
	summary["ShortWinningTrades"] = shortWins
	summary["ShortLosingTrades"] = shortLosses
	summary["MaxConsecutiveWins"] = maxConsecutiveWins
	summary["MaxConsecutiveLosses"] = maxConsecutiveLosses

	winRate := 0.0
	if closingTrades > 0 {
		winRate = float64(wins) / float64(closingTrades) * 100
	}
	summary["WinRate"] = winRate

	longWinRate := 0.0
	if longWins+longLosses > 0 {
		longWinRate = float64(longWins) / float64(longWins+longLosses) * 100
	}
	summary["LongWinRate"] = longWinRate

	shortWinRate := 0.0
	if shortWins+shortLosses > 0 {
		shortWinRate = float64(shortWins) / float64(shortWins+shortLosses) * 100
	}
	summary["ShortWinRate"] = shortWinRate

	avgWin := 0.0
	if wins > 0 {
		avgWin = totalWinAmount / float64(wins)
	}
	summary["AverageWin"] = avgWin

	avgLoss := 0.0
	if losses > 0 {
		avgLoss = math.Abs(totalLossAmount / float64(losses))
	}
	summary["AverageLoss"] = avgLoss

	riskRewardRatio := 0.0
	if avgLoss != 0 {
		riskRewardRatio = avgWin / avgLoss
	}
	summary["RiskRewardRatio"] = riskRewardRatio

	profitFactor := 0.0
	if totalLossAmount != 0 {
		profitFactor = math.Abs(totalWinAmount / totalLossAmount)
	} else if totalWinAmount > 0 {
		// If there are profits but no losses, this can skew optimization.
		// Setting PF to 0.0 effectively penalizes such scenarios.
		profitFactor = 0.0
	}
	summary["ProfitFactor"] = profitFactor

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
	summary["MaxDrawdown"] = maxDrawdown

	recoveryFactor := 0.0
	if maxDrawdown > 0 {
		recoveryFactor = totalProfit / maxDrawdown
	}
	summary["RecoveryFactor"] = recoveryFactor

	// Sharpe Ratio
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
	summary["SharpeRatio"] = sharpeRatio

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
	summary["SortinoRatio"] = sortinoRatio

	// Calmar Ratio
	calmarRatio := 0.0
	if maxDrawdown > 0 {
		calmarRatio = totalProfit / maxDrawdown
	}
	summary["CalmarRatio"] = calmarRatio

	avgHoldingPeriod, avgWinningHoldingPeriod, avgLosingHoldingPeriod := 0.0, 0.0, 0.0
	if len(holdingPeriods) > 0 {
		sum := 0.0
		for _, hp := range holdingPeriods { sum += hp }
		avgHoldingPeriod = sum / float64(len(holdingPeriods))
	}
	summary["AverageHoldingPeriodSeconds"] = avgHoldingPeriod
	if len(winningHoldingPeriods) > 0 {
		sum := 0.0
		for _, hp := range winningHoldingPeriods { sum += hp }
		avgWinningHoldingPeriod = sum / float64(len(winningHoldingPeriods))
	}
	summary["AverageWinningHoldingPeriodSeconds"] = avgWinningHoldingPeriod
	if len(losingHoldingPeriods) > 0 {
		sum := 0.0
		for _, hp := range losingHoldingPeriods { sum += hp }
		avgLosingHoldingPeriod = sum / float64(len(losingHoldingPeriods))
	}
	summary["AverageLosingHoldingPeriodSeconds"] = avgLosingHoldingPeriod

	// Buy & Hold Return
	buyAndHoldReturn := 0.0
	initialPrice := 0.0
	if len(executedTrades) > 0 {
		initialPrice = executedTrades[0].Price
	}
	if initialPrice > 0 && lastPrice > 0 {
		buyAndHoldReturn = lastPrice - initialPrice
	}
	summary["BuyAndHoldReturn"] = buyAndHoldReturn
	summary["ReturnVsBuyAndHold"] = totalProfit - buyAndHoldReturn

	return summary
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
				logger.Warnf("Order monitor failed to get open orders: %v", err)
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
					logger.Warnf("Failed to parse order rate '%s' for order ID %d", order.Rate, order.ID)
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
						logger.Warnf("Failed to cancel order %d for adjustment: %v", order.ID, err)
						continue
					}

					time.Sleep(500 * time.Millisecond)

					pendingAmount, err := strconv.ParseFloat(order.PendingAmount, 64)
					if err != nil {
						logger.Warnf("Failed to parse pending amount for order %d: %v", order.ID, err)
						continue
					}
					currentCfg := config.GetConfig()
					logger.Infof("Re-placing order for pair %s, type %s, new rate %.2f, amount %.8f", order.Pair, order.OrderType, newRate, pendingAmount)
					_, err = execEngine.PlaceOrder(ctx, currentCfg.Trade.Pair, order.OrderType, newRate, pendingAmount, false)
					if err != nil {
						logger.Warnf("Failed to re-place order after adjustment for original ID %d: %v", order.ID, err)
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
	if !bool(cfg.Trade.Twap.PartialExitEnabled) {
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
