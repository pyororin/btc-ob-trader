package datastore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// PnlReport represents an in-memory pnl_reports table record for testing.
type PnlReport struct {
	Time        time.Time
	LastTradeID int64
}

// InMemRepository is an in-memory implementation of the Repository interface for testing.
type InMemRepository struct {
	mu          sync.RWMutex
	trades      map[int64]Trade
	pnlReports  []PnlReport
	perfMetrics *PerformanceMetrics
}

// NewInMemRepository creates a new InMemRepository.
func NewInMemRepository() *InMemRepository {
	return &InMemRepository{
		trades:     make(map[int64]Trade),
		pnlReports: make([]PnlReport, 0),
	}
}

// SeedTrades allows adding trades for test setup.
func (r *InMemRepository) SeedTrades(trades []Trade) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range trades {
		r.trades[t.TransactionID] = t
	}
}

// SeedPnlReports allows adding pnl reports for test setup.
func (r *InMemRepository) SeedPnlReports(reports []PnlReport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pnlReports = append(r.pnlReports, reports...)
	sort.Slice(r.pnlReports, func(i, j int) bool {
		return r.pnlReports[i].Time.After(r.pnlReports[j].Time) // most recent first
	})
}

// SeedPerformanceMetrics allows adding performance metrics for test setup.
func (r *InMemRepository) SeedPerformanceMetrics(metrics *PerformanceMetrics) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.perfMetrics = metrics
}

// FetchTradesForReportSince fetches trades from the in-memory store.
func (r *InMemRepository) FetchTradesForReportSince(ctx context.Context, lastTradeID int64) ([]Trade, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []Trade
	for _, t := range r.trades {
		if t.IsMyTrade && t.TransactionID > lastTradeID {
			result = append(result, t)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Time.Before(result[j].Time)
	})

	return result, nil
}

// FetchLatestPnlReportTradeID fetches the latest trade ID from the in-memory store.
func (r *InMemRepository) FetchLatestPnlReportTradeID(ctx context.Context) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.pnlReports) == 0 {
		// To match DB behavior, return 0 and no error, or should we return an error?
		// pgx.ErrNoRows is the typical DB error. Let's return 0, nil for simplicity.
		return 0, nil
	}
	return r.pnlReports[0].LastTradeID, nil
}

// DeleteOldPnlReports removes old PnL reports from the in-memory store.
func (r *InMemRepository) DeleteOldPnlReports(ctx context.Context, maxAgeHours int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	threshold := time.Now().Add(-time.Duration(maxAgeHours) * time.Hour)
	var remainingReports []PnlReport
	deletedCount := 0

	for _, report := range r.pnlReports {
		if report.Time.Before(threshold) {
			deletedCount++
		} else {
			remainingReports = append(remainingReports, report)
		}
	}

	r.pnlReports = remainingReports
	return int64(deletedCount), nil
}

// FetchLatestPerformanceMetrics fetches the latest performance metrics from the in-memory store.
func (r *InMemRepository) FetchLatestPerformanceMetrics(ctx context.Context) (*PerformanceMetrics, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.perfMetrics == nil {
		// Return a zero-value struct and no error to simulate a "no rows" case without erroring.
		return &PerformanceMetrics{
			SharpeRatio:  0.0,
			ProfitFactor: 0.0,
			MaxDrawdown:  decimal.Zero,
		}, nil
	}
	return r.perfMetrics, nil
}

// Clear clears all data from the in-memory repository.
func (r *InMemRepository) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trades = make(map[int64]Trade)
	r.pnlReports = make([]PnlReport, 0)
	r.perfMetrics = nil
}
