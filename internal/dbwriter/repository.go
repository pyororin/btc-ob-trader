package dbwriter

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// BenchmarkResult はデータベースに保存するベンチマーク結果の構造体です。
type BenchmarkResult struct {
	Time       time.Time       `db:"time"`
	StrategyID string          `db:"strategy_id"`
	Pair       string          `db:"pair"`
	Value      decimal.Decimal `db:"value"`
}

// Repository defines the interface for database writing operations.
// This allows for mocking in tests and abstracting the writer implementation.
type Repository interface {
	// SetReplaySessionID sets the replay session ID for the writer.
	SetReplaySessionID(sessionID string)

	// SaveOrderBookUpdate adds an order book update to the buffer.
	SaveOrderBookUpdate(obu OrderBookUpdate)

	// SaveTrade adds a trade to the buffer.
	SaveTrade(trade Trade)

	// SavePnLSummary saves a single PnL summary to the database.
	SavePnLSummary(ctx context.Context, pnl PnLSummary) error

	// SaveBenchmarkResult saves a single benchmark result to the database.
	SaveBenchmarkResult(ctx context.Context, result *BenchmarkResult) error

	// Close flushes any buffered data and closes the database connection.
	Close()
}
