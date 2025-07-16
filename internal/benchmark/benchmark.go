package benchmark

import (
	"context"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

var (
	buyAndHoldStrategyID = "buy_and_hold"
	btcPair              = "btc_jpy"
	tickInterval         = 1 * time.Minute
)

// Service manages the calculation and storage of benchmark data.
type Service struct {
	config     *config.Config
	dbWriter   dbwriter.Repository
	logger     logger.Logger
	wg         sync.WaitGroup
	cancelFunc context.CancelFunc

	// State for the buy-and-hold benchmark
	initialInvestment decimal.Decimal
	btcHoldings       decimal.Decimal
	firstPrice        decimal.Decimal
	isFirstTick       bool
	mutex             sync.RWMutex
}

// NewService creates a new benchmark service.
func NewService(cfg *config.Config, dbw dbwriter.Repository, l logger.Logger) *Service {
	initialInvestment := decimal.NewFromFloat(1_000_000.0) // 1,000,000 JPY
	return &Service{
		config:            cfg,
		dbWriter:          dbw,
		logger:            l,
		initialInvestment: initialInvestment,
		isFirstTick:       true,
	}
}

// Run starts the benchmark service's main loop.
func (s *Service) Run(ctx context.Context) {
	ctx, s.cancelFunc = context.WithCancel(ctx)
	s.wg.Add(1)
	defer s.wg.Done()

	s.logger.Info("Benchmark service started")
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.tick(ctx)
		case <-ctx.Done():
			s.logger.Info("Benchmark service stopping")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// Stop gracefully stops the benchmark service.
func (s *Service) Stop() {
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	s.wg.Wait()
	s.logger.Info("Benchmark service stopped")
}

// tick performs a single benchmark calculation and saves the result.
func (s *Service) tick(ctx context.Context) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// In a real scenario, this would come from a shared price feed.
	// For now, we simulate a price.
	currentPrice := decimal.NewFromFloat(10_000_000.0) // Placeholder

	if s.isFirstTick {
		s.firstPrice = currentPrice
		s.btcHoldings = s.initialInvestment.Div(s.firstPrice)
		s.isFirstTick = false
		s.logger.Infof("Benchmark first tick. Initial price: %s, BTC holdings: %s", s.firstPrice, s.btcHoldings)
	}

	currentValue := s.btcHoldings.Mul(currentPrice)

	benchmarkResult := &dbwriter.BenchmarkResult{
		Time:       time.Now(),
		StrategyID: buyAndHoldStrategyID,
		Pair:       btcPair,
		Value:      currentValue,
	}

	if err := s.dbWriter.SaveBenchmarkResult(ctx, benchmarkResult); err != nil {
		s.logger.Errorf("Failed to save benchmark result: %v", err)
	} else {
		s.logger.Infof("Saved benchmark result: strategy=%s, value=%s", buyAndHoldStrategyID, currentValue.StringFixed(2))
	}
}

// SetInitialPrice is a helper for testing to set the first price.
func (s *Service) SetInitialPrice(price float64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.firstPrice = decimal.NewFromFloat(price)
	s.btcHoldings = s.initialInvestment.Div(s.firstPrice)
	s.isFirstTick = false
}
