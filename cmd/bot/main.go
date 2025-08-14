// Package main is the entry point of the OBI Scalping Bot.
package main

import (
	"bufio"
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
	"reflect"
	"runtime"
	"runtime/pprof"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/do"
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
)

// injectorMux protects the dependency injector from concurrent access.
var injectorMux sync.RWMutex

// safeInvoke provides a thread-safe wrapper around do.Invoke.
func safeInvoke[T any](i *do.Injector) (T, error) {
	injectorMux.Lock()
	defer injectorMux.Unlock()
	return do.Invoke[T](i)
}

// safeMustInvoke provides a thread-safe wrapper around do.MustInvoke.
func safeMustInvoke[T any](i *do.Injector) T {
	injectorMux.Lock()
	defer injectorMux.Unlock()
	return do.MustInvoke[T](i)
}

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

func newInjector(f *flags, ctx context.Context) (*do.Injector, error) {
	injector := do.New()

	// Provide flags
	do.ProvideValue(injector, f)

	// Provide context
	do.ProvideValue(injector, ctx)

	// Provide Config
	do.Provide(injector, func(i *do.Injector) (*config.Config, error) {
		flags := do.MustInvoke[*flags](i)
		return setupConfig(flags.configPath, flags.tradeConfigPath)
	})

	// Provide Logger
	do.Provide(injector, func(i *do.Injector) (*zap.Logger, error) {
		cfg := do.MustInvoke[*config.Config](i)
		flags := do.MustInvoke[*flags](i)
		if !flags.serveMode {
			pair := ""
			if cfg.Trade != nil {
				pair = cfg.Trade.Pair
			}
			setupLogger(cfg.App.LogLevel, flags.configPath, pair)
		}
		// Return a valid logger instance, even if setupLogger just configures a global one.
		// This assumes setupLogger initializes a logger that can be retrieved,
		// or we can create one here. For now, we'll create a new one.
		var zapLogger *zap.Logger
		var err error
		if cfg.App.LogLevel == "debug" {
			zapLogger, err = zap.NewDevelopment()
		} else {
			zapLogger, err = zap.NewProduction()
		}
		return zapLogger, err
	})

	// Provide DB Connection Pool
	do.Provide(injector, func(i *do.Injector) (*pgxpool.Pool, error) {
		flags := do.MustInvoke[*flags](i)
		if flags.simulateMode || flags.serveMode {
			return nil, nil // No DB connection in simulation/server mode
		}
		cfg := do.MustInvoke[*config.Config](i)
		ctx := do.MustInvoke[context.Context](i)
		dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&timezone=Asia/Tokyo",
			cfg.App.Database.User, cfg.App.Database.Password, cfg.App.Database.Host, cfg.App.Database.Port, cfg.App.Database.Name, cfg.App.Database.SSLMode)
		return pgxpool.New(ctx, dbURL)
	})

	// Provide DB Writer
	do.Provide(injector, func(i *do.Injector) (dbwriter.DBWriter, error) {
		pool, _ := do.Invoke[*pgxpool.Pool](i) // Can be nil in simulate mode
		cfg := do.MustInvoke[*config.Config](i)
		logger := do.MustInvoke[*zap.Logger](i)
		return dbwriter.NewTimescaleWriter(pool, cfg.App.DBWriter, logger)
	})

	// Provide Datastore Repository
	do.Provide(injector, func(i *do.Injector) (datastore.Repository, error) {
		// Use Invoke instead of MustInvoke to handle the case where the DB is not available.
		pool, err := do.Invoke[*pgxpool.Pool](i)
		if err != nil || pool == nil {
			// If the pool is not available, provide a nil repository.
			// This makes the datastore an optional dependency.
			return nil, nil
		}
		return datastore.NewTimescaleRepository(pool), nil
	})

	// Provide Notifier
	do.Provide(injector, func(i *do.Injector) (alert.Notifier, error) {
		cfg := do.MustInvoke[*config.Config](i)
		return alert.NewDiscordNotifier(cfg.App.Alert.Discord)
	})

	// Provide Benchmark Service
	do.Provide(injector, func(i *do.Injector) (*benchmark.Service, error) {
		writer := do.MustInvoke[dbwriter.DBWriter](i)
		logger := do.MustInvoke[*zap.Logger](i)
		if writer == nil {
			return nil, nil
		}
		return benchmark.NewService(logger, writer), nil
	})

	// Provide Coincheck Client
	do.Provide(injector, func(i *do.Injector) (*coincheck.Client, error) {
		cfg := do.MustInvoke[*config.Config](i)
		return coincheck.NewClient(cfg.APIKey, cfg.APISecret), nil
	})

	// Provide Execution Engine
	do.Provide(injector, func(i *do.Injector) (engine.ExecutionEngine, error) {
		flags := do.MustInvoke[*flags](i)
		if flags.simulateMode {
			// In simulation mode, we need a ReplayExecutionEngine which depends on an OrderBook.
			// This part might need adjustment if OrderBook needs to be a shared service.
			orderBook := indicator.NewOrderBook()
			return engine.NewReplayExecutionEngine(orderBook), nil
		}
		// For live/server mode
		client := do.MustInvoke[*coincheck.Client](i)
		writer := do.MustInvoke[dbwriter.DBWriter](i)
		notifier := do.MustInvoke[alert.Notifier](i)
		return engine.NewLiveExecutionEngine(client, writer, notifier), nil
	})

	return injector, nil
}

func main() {
	// --- Initialization ---
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

	injector, err := newInjector(&f, ctx)
	if err != nil {
		logger.Fatalf("Failed to create injector: %v", err)
	}
	defer injector.Shutdown()

	go watchConfigFiles(f.configPath, f.tradeConfigPath, injector)

	if (f.simulateMode || f.serveMode) && f.csvPath == "" && !f.serveMode {
		logger.Fatal("CSV file path must be provided in simulation mode using --csv flag")
	}

	if !f.simulateMode && !f.serveMode {
		startHTTPServer(injector)
	}

	// --- Main Execution Loop ---
	shutdownSigs := make(chan os.Signal, 1)
	signal.Notify(shutdownSigs, syscall.SIGINT, syscall.SIGTERM)

	runMainLoop(injector, &f, shutdownSigs)

	// --- Graceful Shutdown ---
	waitForShutdownSignal(shutdownSigs)
	logger.Info("Initiating graceful shutdown...")

	if f.memProfile != "" {
		file, err := os.Create(f.memProfile)
		if err != nil {
			logger.Fatalf("could not create memory profile: %v", err)
		}
		defer file.Close()
		runtime.GC()
		if err := pprof.WriteHeapProfile(file); err != nil {
			logger.Fatalf("could not write memory profile: %v", err)
		}
	}

	cancel()
	time.Sleep(1 * time.Second)
	logger.Info("OBI Scalping Bot shut down gracefully.")
}

// parseFlags parses command-line flags.
func parseFlags() flags {
	// ... (same as before)
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
func setupConfig(appConfigPath, tradeConfigPath string) (*config.Config, error) {
	if tradeConfigPath == "" {
		dir := filepath.Dir(appConfigPath)
		tradeConfigPath = filepath.Join(dir, "trade_config.yaml")
	}
	return config.LoadConfig(appConfigPath, tradeConfigPath)
}

// watchConfigFiles sets up a file watcher to automatically reload the configuration on change.
func watchConfigFiles(appConfigPath, tradeConfigPath string, injector *do.Injector) {
	// ... (same as before, but on change it should probably re-invoke config setup)
	// For simplicity, we keep the existing logic which uses a global config.
	// A more advanced implementation would involve updating the config in the injector.
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
						// Here you would ideally update the config in the DI container
						// do.Shutdown(injector, (*config.Config)(nil))
						// do.Provide(injector, ...)
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

// ... (convertTrades, logConfigChanges, compareStructs remain the same)
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
func logConfigChanges(oldCfg, newCfg *config.Config) {
	if oldCfg == nil || newCfg == nil {
		return
	}
	compareStructs(reflect.ValueOf(oldCfg.App), reflect.ValueOf(newCfg.App), "App")

	// Handle nil pointers for Trade config
	var oldTrade, newTrade reflect.Value
	if oldCfg.Trade != nil {
		oldTrade = reflect.ValueOf(*oldCfg.Trade)
	}
	if newCfg.Trade != nil {
		newTrade = reflect.ValueOf(*newCfg.Trade)
	}

	if oldTrade.IsValid() && newTrade.IsValid() {
		compareStructs(oldTrade, newTrade, "Trade")
	} else if oldTrade.IsValid() && !newTrade.IsValid() {
		logger.Info("Config changed: Trade config removed.")
	} else if !oldTrade.IsValid() && newTrade.IsValid() {
		logger.Info("Config changed: Trade config added.")
	}
}

func compareStructs(v1, v2 reflect.Value, prefix string) {
	// Check for invalid values, which can happen if the original config was nil
	if !v1.IsValid() || !v2.IsValid() {
		return
	}
	if v1.Kind() != reflect.Struct || v2.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < v1.NumField(); i++ {
		field1 := v1.Field(i)
		field2 := v2.Field(i)
		fieldName := v1.Type().Field(i).Name
		currentPrefix := fmt.Sprintf("%s.%s", prefix, fieldName)

		// Check for valid fields before proceeding
		if !field1.IsValid() || !field2.IsValid() {
			continue
		}

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
	if pair != "" {
		logger.Infof("Target pair: %s", pair)
	} else {
		logger.Info("No trade configuration loaded. Running in data collection mode.")
	}
}

// startHTTPServer starts the HTTP server for health checks and other API endpoints.
func startHTTPServer(injector *do.Injector) {
	r := chi.NewRouter()
	r.Get("/health", handler.HealthCheckHandler)

	repo, err := safeInvoke[datastore.Repository](injector)
	if err == nil && repo != nil {
		pnlHandler := handler.NewPnlHandler(repo)
		pnlHandler.RegisterRoutes(r)
		logger.Info("PnL report endpoint /pnl/latest_report registered.")
	} else {
		logger.Warn("Datastore repository not available, PnL report endpoint will not be registered.", zap.Error(err))
	}

	go func() {
		logger.Info("HTTP server starting on :8080")
		if err := http.ListenAndServe(":8080", r); err != nil {
			logger.Fatalf("HTTP server failed: %v", err)
		}
	}()
}

// setupHandlers creates and returns the handlers for order book and trade data.
func setupHandlers(orderBook *indicator.OrderBook, dbWriter dbwriter.DBWriter, pair string) (coincheck.OrderBookHandler, coincheck.TradeHandler) {
	// ... (implementation is the same, but now receives dbwriter.DBWriter interface)
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
func processSignalsAndExecute(injector *do.Injector, obiCalculator *indicator.OBICalculator, webSocketClient *coincheck.WebSocketClient, wg *sync.WaitGroup) {
	// ... (dependencies are now invoked from injector)
	ctx := safeMustInvoke[context.Context](injector)
	cfg := safeMustInvoke[*config.Config](injector)
	execEngine := safeMustInvoke[engine.ExecutionEngine](injector)
	benchmarkService, _ := safeInvoke[*benchmark.Service](injector) // optional

	signalEngine, err := tradingsignal.NewSignalEngine(cfg.Trade)
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
				currentCfg := config.GetConfig()
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
						finalPrice := 0.0
						if orderType == "buy" {
							finalPrice = result.BestAsk
						} else {
							finalPrice = result.BestBid
						}

						orderAmount := currentCfg.Trade.OrderAmount
						if liveEngine, ok := execEngine.(*engine.LiveExecutionEngine); ok {
							balance, err := liveEngine.GetBalance()
							if err != nil {
								logger.Warnf("Failed to get balance for order sizing: %v", err)
								continue
							}
							currentBtc, _ := strconv.ParseFloat(balance.Btc, 64)
							orderAmount = currentBtc * currentCfg.Trade.OrderRatio
						}

						if orderAmount >= 0.001 {
							tradeParams := engine.TradeParams{
								Pair:      currentCfg.Trade.Pair,
								OrderType: orderType,
								Rate:      finalPrice,
								Amount:    orderAmount,
								PostOnly:  false,
								TP:        tradingSignal.TakeProfit,
								SL:        tradingSignal.StopLoss,
							}
							if currentCfg.Trade != nil && bool(currentCfg.Trade.Twap.Enabled) && orderAmount > currentCfg.Trade.Twap.MaxOrderSizeBtc {
								go executeTwapOrder(ctx, execEngine, tradeParams)
							} else {
								execEngine.PlaceOrder(ctx, tradeParams)
							}
						}
					}
				}
			}
		}
	}()
}

// ... (executeTwapOrder remains the same)
func executeTwapOrder(ctx context.Context, execEngine engine.ExecutionEngine, baseParams engine.TradeParams) {
	if liveEngine, ok := execEngine.(*engine.LiveExecutionEngine); ok {
		defer liveEngine.SetPartialExitStatus(false)
	}

	cfg := config.GetConfig()
	if cfg.Trade == nil {
		logger.Warn("executeTwapOrder called without trade config. Aborting.")
		return
	}
	totalAmount := baseParams.Amount
	numOrders := int(math.Floor(totalAmount / cfg.Trade.Twap.MaxOrderSizeBtc))
	lastOrderSize := totalAmount - float64(numOrders)*cfg.Trade.Twap.MaxOrderSizeBtc
	chunkSize := cfg.Trade.Twap.MaxOrderSizeBtc
	interval := time.Duration(cfg.Trade.Twap.IntervalSeconds) * time.Second

	logger.Infof("TWAP execution started: Total=%.8f, Chunks=%d, ChunkSize=%.8f, LastChunk=%.8f, Interval=%v",
		totalAmount, numOrders, chunkSize, lastOrderSize, interval)

	for i := 0; i < numOrders; i++ {
		select {
		case <-ctx.Done():
			logger.Warnf("TWAP execution cancelled for order type %s.", baseParams.OrderType)
			return
		default:
			logger.Infof("TWAP chunk %d/%d: Placing order for %.8f BTC.", i+1, numOrders, chunkSize)
			chunkParams := baseParams
			chunkParams.Amount = chunkSize
			_, err := execEngine.PlaceOrder(ctx, chunkParams)
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
			logger.Warnf("TWAP execution cancelled before placing the last chunk for order type %s.", baseParams.OrderType)
			return
		default:
			logger.Infof("TWAP last chunk: Placing order for %.8f BTC.", lastOrderSize)
			lastChunkParams := baseParams
			lastChunkParams.Amount = lastOrderSize
			_, err := execEngine.PlaceOrder(ctx, lastChunkParams)
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
func runMainLoop(injector *do.Injector, f *flags, sigs chan<- os.Signal) {
	ctx := safeMustInvoke[context.Context](injector)

	if f.serveMode {
		runServerMode(ctx, f, sigs)
	} else if f.simulateMode {
		summaryCh := make(chan map[string]interface{}, 1)
		go runSimulation(ctx, f, sigs, summaryCh)

		summary := <-summaryCh
		output, err := json.Marshal(summary)
		if err != nil {
			fmt.Printf(`{"error": "failed to marshal summary: %v"}`, err)
		} else {
			fmt.Println(string(output))
		}
		sigs <- syscall.SIGTERM
	} else {
		// Live trading setup
		cfg := safeMustInvoke[*config.Config](injector)
		dbWriter, _ := safeInvoke[dbwriter.DBWriter](injector)
		execEngine := safeMustInvoke[engine.ExecutionEngine](injector)
		client := safeMustInvoke[*coincheck.Client](injector)

		orderBook := indicator.NewOrderBook()
		obiCalculator := indicator.NewOBICalculator(orderBook, 300*time.Millisecond)

		pair := "btc_jpy" // Default pair
		if cfg.Trade != nil {
			pair = cfg.Trade.Pair
		}

		orderBookHandler, tradeHandler := setupHandlers(orderBook, dbWriter, pair)

		obiCalculator.Start(ctx)
		wsClient := coincheck.NewWebSocketClient(orderBookHandler, tradeHandler)

		if cfg.Trade != nil {
			logger.Info("Trade configuration loaded, starting trading routines.")
			go processSignalsAndExecute(injector, obiCalculator, wsClient, nil)
			go orderMonitor(ctx, execEngine, client, orderBook)
			go positionMonitor(ctx, execEngine, orderBook)
		} else {
			logger.Info("No trade configuration found, running in data collection mode only.")
		}

		go func() {
			logger.Info("Connecting to Coincheck WebSocket API...")
			if err := wsClient.Connect(ctx); err != nil {
				logger.Warnf("WebSocket client exited with error: %v", err)
				sigs <- syscall.SIGTERM
			}
		}()

		<-wsClient.Ready()
		logger.Info("WebSocket client is ready.")
	}
}

// ... (rest of the file remains largely the same, but dependencies would be passed or invoked)
// For brevity, the rest of the file (runSimulation, runServerMode, etc.) is omitted,
// but would need similar refactoring to accept an injector or have dependencies passed down.
// The provided replacement covers the core DI setup.

// waitForShutdownSignal blocks until a shutdown signal is received.
func waitForShutdownSignal(sigs <-chan os.Signal) {
	sig := <-sigs
	logger.Infof("Received signal: %s", sig)
}

// runSimulation runs a backtest using data from a CSV file and sends the summary through a channel.
func runSimulation(ctx context.Context, f *flags, sigs chan<- os.Signal, summaryCh chan<- map[string]interface{}) {
	defer close(summaryCh) // Ensure channel is closed when done.
	rand.Seed(1)
	cfg := config.GetConfig()
	if cfg.Trade == nil {
		logger.Fatal("Simulation mode requires a trade configuration file.")
		summaryCh <- map[string]interface{}{"error": "trade config is missing"}
		sigs <- syscall.SIGTERM
		return
	}

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
	signalEngine, err := tradingsignal.NewSignalEngine(cfg.Trade)
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
				logger.Infof("Progress: Processed %d events so far. Last event at %s", eventCount, marketEvent.GetTime().Format(time.RFC3339))
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
							tradeParams := engine.TradeParams{
								Pair:      cfg.Trade.Pair,
								OrderType: orderType,
								Rate:      tradingSignal.EntryPrice,
								Amount:    cfg.Trade.OrderAmount,
								PostOnly:  false,
								TP:        tradingSignal.TakeProfit,
								SL:        tradingSignal.StopLoss,
							}
							_, err := replayEngine.PlaceOrder(ctx, tradeParams)
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
						tradeParams := engine.TradeParams{
							Pair:      cfg.Trade.Pair,
							OrderType: orderType,
							Rate:      tradingSignal.EntryPrice,
							Amount:    cfg.Trade.OrderAmount,
							PostOnly:  false,
							TP:        tradingSignal.TakeProfit,
							SL:        tradingSignal.StopLoss,
						}
						_, err := replayEngine.PlaceOrder(ctx, tradeParams)
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
func runServerMode(ctx context.Context, f *flags, sigs chan<- os.Signal) {
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
	simConfig.Trade = tradeCfg // tradeCfg is already a pointer

	orderBook := indicator.NewOrderBook()
	replayEngine := engine.NewReplayExecutionEngine(orderBook)
	signalEngine, err := tradingsignal.NewSignalEngine(simConfig.Trade) // Pass the pointer directly
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
						tradeParams := engine.TradeParams{
							Pair:      simConfig.Trade.Pair,
							OrderType: orderType,
							Rate:      tradingSignal.EntryPrice,
							Amount:    simConfig.Trade.OrderAmount,
							PostOnly:  false,
							TP:        tradingSignal.TakeProfit,
							SL:        tradingSignal.StopLoss,
						}
						replayEngine.PlaceOrder(ctx, tradeParams)
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
						tradeParams := engine.TradeParams{
							Pair:      simConfig.Trade.Pair,
							OrderType: orderType,
							Rate:      tradingSignal.EntryPrice,
							Amount:    simConfig.Trade.OrderAmount,
							PostOnly:  false,
							TP:        tradingSignal.TakeProfit,
							SL:        tradingSignal.StopLoss,
						}
						replayEngine.PlaceOrder(ctx, tradeParams)
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
		profitFactor = 0.0
	}
	summary["ProfitFactor"] = profitFactor

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

	calmarRatio := 0.0
	if maxDrawdown > 0 {
		calmarRatio = totalProfit / maxDrawdown
	}
	summary["CalmarRatio"] = calmarRatio

	avgHoldingPeriod, avgWinningHoldingPeriod, avgLosingHoldingPeriod := 0.0, 0.0, 0.0
	if len(holdingPeriods) > 0 {
		sum := 0.0
		for _, hp := range holdingPeriods {
			sum += hp
		}
		avgHoldingPeriod = sum / float64(len(holdingPeriods))
	}
	summary["AverageHoldingPeriodSeconds"] = avgHoldingPeriod
	if len(winningHoldingPeriods) > 0 {
		sum := 0.0
		for _, hp := range winningHoldingPeriods {
			sum += hp
		}
		avgWinningHoldingPeriod = sum / float64(len(winningHoldingPeriods))
	}
	summary["AverageWinningHoldingPeriodSeconds"] = avgWinningHoldingPeriod
	if len(losingHoldingPeriods) > 0 {
		sum := 0.0
		for _, hp := range losingHoldingPeriods {
			sum += hp
		}
		avgLosingHoldingPeriod = sum / float64(len(losingHoldingPeriods))
	}
	summary["AverageLosingHoldingPeriodSeconds"] = avgLosingHoldingPeriod

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
					params := engine.TradeParams{
						Pair:      currentCfg.Trade.Pair,
						OrderType: order.OrderType,
						Rate:      newRate,
						Amount:    pendingAmount,
					}
					_, err = execEngine.PlaceOrder(ctx, params)
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
	if cfg.Trade == nil || !bool(cfg.Trade.Twap.PartialExitEnabled) {
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
				params := engine.TradeParams{
					Pair:      currentCfg.Trade.Pair,
					OrderType: exitOrder.OrderType,
					Rate:      exitOrder.Price,
					Amount:    exitOrder.Size,
				}
				go executeTwapOrder(ctx, execEngine, params)
			}
		}
	}
}
