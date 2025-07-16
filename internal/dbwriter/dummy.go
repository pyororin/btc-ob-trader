package dbwriter

import (
	"context"

	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

// dummyWriter is a no-op implementation of the Repository interface.
// It is used when the database connection is not available.
type dummyWriter struct {
	logger logger.Logger
}

// NewDummyWriter creates a new dummy writer.
func NewDummyWriter(l logger.Logger) Repository {
	l.Info("Creating dummy DB writer because no database connection is available.")
	return &dummyWriter{logger: l}
}

// SetReplaySessionID does nothing.
func (d *dummyWriter) SetReplaySessionID(sessionID string) {
	d.logger.Debug("Dummy writer: SetReplaySessionID called", "sessionID", sessionID)
}

// SaveOrderBookUpdate does nothing.
func (d *dummyWriter) SaveOrderBookUpdate(obu OrderBookUpdate) {
	// No-op
}

// SaveTrade does nothing.
func (d *dummyWriter) SaveTrade(trade Trade) {
	// No-op
}

// SavePnLSummary does nothing and returns nil.
func (d *dummyWriter) SavePnLSummary(ctx context.Context, pnl PnLSummary) error {
	d.logger.Debug("Dummy writer: SavePnLSummary called", "pnl", pnl)
	return nil
}

// SaveBenchmarkResult does nothing and returns nil.
func (d *dummyWriter) SaveBenchmarkResult(ctx context.Context, result *BenchmarkResult) error {
	d.logger.Debug("Dummy writer: SaveBenchmarkResult called", "result", result)
	return nil
}

// Close does nothing.
func (d *dummyWriter) Close() {
	d.logger.Debug("Dummy writer: Close called")
}
