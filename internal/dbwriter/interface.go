package dbwriter

import (
	"context"
)

// DBWriter defines the interface for writing data to the database.
// This allows for mocking in tests.
type DBWriter interface {
	SaveLatency(latency Latency)
	SaveTrade(trade Trade)
	SavePnLSummary(ctx context.Context, pnl PnLSummary) error
	SaveTradePnL(ctx context.Context, tradePnl TradePnL) error
	SaveOrderBookUpdate(obu OrderBookUpdate)
	SaveBenchmarkValue(ctx context.Context, value BenchmarkValue)
	Close()
}
