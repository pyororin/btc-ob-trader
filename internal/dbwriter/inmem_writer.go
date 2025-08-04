package dbwriter

import (
	"context"
	"sync"
)

// InMemWriter is an in-memory implementation of the DBWriter interface for testing.
type InMemWriter struct {
	mu               sync.RWMutex
	Trades           []Trade
	PnlSummaries     []PnLSummary
	TradePnls        []TradePnL
	OrderBookUpdates []OrderBookUpdate
	BenchmarkValues  []BenchmarkValue
	IsClosed         bool
}

// NewInMemWriter creates a new InMemWriter.
func NewInMemWriter() *InMemWriter {
	return &InMemWriter{
		Trades:           make([]Trade, 0),
		PnlSummaries:     make([]PnLSummary, 0),
		TradePnls:        make([]TradePnL, 0),
		OrderBookUpdates: make([]OrderBookUpdate, 0),
		BenchmarkValues:  make([]BenchmarkValue, 0),
	}
}

// SaveTrade appends a trade to the in-memory slice.
func (w *InMemWriter) SaveTrade(trade Trade) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Trades = append(w.Trades, trade)
}

// SavePnLSummary appends a PnL summary to the in-memory slice.
func (w *InMemWriter) SavePnLSummary(ctx context.Context, pnl PnLSummary) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.PnlSummaries = append(w.PnlSummaries, pnl)
	return nil
}

// SaveTradePnL appends a trade PnL to the in-memory slice.
func (w *InMemWriter) SaveTradePnL(ctx context.Context, tradePnl TradePnL) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.TradePnls = append(w.TradePnls, tradePnl)
	return nil
}

// SaveOrderBookUpdate appends an order book update to the in-memory slice.
func (w *InMemWriter) SaveOrderBookUpdate(obu OrderBookUpdate) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.OrderBookUpdates = append(w.OrderBookUpdates, obu)
}

// SaveBenchmarkValue appends a benchmark value to the in-memory slice.
func (w *InMemWriter) SaveBenchmarkValue(ctx context.Context, value BenchmarkValue) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.BenchmarkValues = append(w.BenchmarkValues, value)
}

// Close marks the writer as closed.
func (w *InMemWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.IsClosed = true
}

// Clear resets all the in-memory slices.
func (w *InMemWriter) Clear() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Trades = make([]Trade, 0)
	w.PnlSummaries = make([]PnLSummary, 0)
	w.TradePnls = make([]TradePnL, 0)
	w.OrderBookUpdates = make([]OrderBookUpdate, 0)
	w.BenchmarkValues = make([]BenchmarkValue, 0)
	w.IsClosed = false
}
