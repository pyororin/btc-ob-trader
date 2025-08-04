package dbwriter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"github.com/your-org/obi-scalp-bot/internal/config" // Ensure this path matches your go.mod module name
)

// OrderBookUpdate はデータベースに保存する板情報の構造体です。
type OrderBookUpdate struct {
	Time       time.Time `db:"time"`
	Pair       string    `db:"pair"`
	Side       string    `db:"side"` // "bid" or "ask"
	Price      float64   `db:"price"`
	Size       float64   `db:"size"`
	IsSnapshot bool      `db:"is_snapshot"`
}

// Trade はデータベースに保存する約定情報の構造体です。
type Trade struct {
	Time            time.Time `db:"time"`
	Pair            string    `db:"pair"`
	Side            string    `db:"side"` // "buy" or "sell"
	Price           float64   `db:"price"`
	Size            float64   `db:"size"`
	TransactionID   int64     `db:"transaction_id"`
	IsCancelled     bool      `db:"is_cancelled"`
	IsMyTrade       bool      `db:"is_my_trade"` // 自分の取引かどうか
	RealizedPnL   float64   // Not stored in DB, but used for simulation summary
	EntryTime     time.Time // Not stored in DB
	ExitTime      time.Time // Not stored in DB
	PositionSide  string    // "long" or "short", not stored in DB, for simulation summary
}

// TradePnL はデータベースに保存する個別の取引のPnL情報です。
type TradePnL struct {
	TradeID       int64     `db:"trade_id"`
	Pnl           float64   `db:"pnl"`
	CumulativePnl float64   `db:"cumulative_pnl"`
	CreatedAt     time.Time `db:"created_at"`
}

// PnLSummary はデータベースに保存するPnL情報の構造体です。
type PnLSummary struct {
	Time            time.Time `db:"time"`
	StrategyID      string    `db:"strategy_id"`
	Pair            string    `db:"pair"`
	RealizedPnL     float64   `db:"realized_pnl"`
	UnrealizedPnL   float64   `db:"unrealized_pnl"`
	TotalPnL        float64   `db:"total_pnl"`
	PositionSize    float64   `db:"position_size"`
	AvgEntryPrice   float64   `db:"avg_entry_price"`
}


// BenchmarkValue はデータベースに保存するベンチマーク情報の構造体です。
type BenchmarkValue struct {
	Time  time.Time `db:"time"`
	Price float64   `db:"price"`
}

// Pool is an interface that abstracts the pgxpool.Pool for testability.
type Pool interface {
	CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Close()
}

// TimescaleWriter はTimescaleDBへのデータ書き込みを担当します。
type TimescaleWriter struct {
	pool             Pool
	logger           *zap.Logger
	config           config.DBWriterConfig
	orderBookBuffer  []OrderBookUpdate
	tradeBuffer      []Trade
	benchmarkBuffer  []BenchmarkValue
	bufferMutex      sync.Mutex
	flushTicker      *time.Ticker
	shutdownChan     chan struct{}
}

// NewTimescaleWriter は新しいTimescaleWriterインスタンスを作成します。
// このコンストラクタは、外部から提供されたDB接続プールを使用します。
func NewTimescaleWriter(pool Pool, writerConfig config.DBWriterConfig, logger *zap.Logger) (DBWriter, error) {
	if pool == nil {
		// If the pool is nil (e.g., in simulation mode), return a dummy writer.
		logger.Info("pgxpool.Pool is nil, creating dummy DB writer.")
		return &TimescaleWriter{
			pool:         nil,
			logger:       logger,
			shutdownChan: make(chan struct{}),
		}, nil
	}

	writer := &TimescaleWriter{
		pool:            pool,
		logger:          logger,
		config:          writerConfig,
		orderBookBuffer: make([]OrderBookUpdate, 0, writerConfig.BatchSize),
		tradeBuffer:     make([]Trade, 0, writerConfig.BatchSize),
		benchmarkBuffer: make([]BenchmarkValue, 0, writerConfig.BatchSize),
		shutdownChan:    make(chan struct{}),
	}

	// Only start the background writer if the pool is valid
	if writer.pool != nil {
		// Fallback for zero or invalid values
		if writerConfig.WriteIntervalSeconds <= 0 {
			writerConfig.WriteIntervalSeconds = 1 // Default to 1s to avoid panic
			logger.Warn("WriteIntervalSeconds is zero or negative, defaulting to 1s.", zap.Int("originalValue", writerConfig.WriteIntervalSeconds))
		}
		if writer.config.BatchSize <= 0 {
			writer.config.BatchSize = 100 // Default to 100 to avoid issues
			logger.Warn("BatchSize is zero or negative, defaulting to 100.", zap.Int("originalValue", writer.config.BatchSize))
		}

		batchInterval := time.Duration(writerConfig.WriteIntervalSeconds) * time.Second
		writer.flushTicker = time.NewTicker(batchInterval)
		go writer.run()
		logger.Info("Successfully connected to TimescaleDB and started batch writer")
	}

	return writer, nil
}

// Close はデータベース接続プールをクローズし、バッファをフラッシュします。
func (w *TimescaleWriter) Close() {
	if w.pool == nil {
		w.logger.Info("Closing dummy DB writer.")
		return
	}

	w.logger.Info("Closing TimescaleDB writer...")
	close(w.shutdownChan)
	w.flushTicker.Stop()

	// Final flush
	w.flushBuffers()

	if w.pool != nil {
		w.pool.Close()
		w.logger.Info("TimescaleDB connection pool closed")
	}
}

func (w *TimescaleWriter) run() {
	if w.pool == nil {
		return // Do not run for dummy writer
	}
	for {
		select {
		case <-w.flushTicker.C:
			w.flushBuffers()
		case <-w.shutdownChan:
			return
		}
	}
}

// SaveOrderBookUpdate は板情報更新をバッファに追加します。
func (w *TimescaleWriter) SaveOrderBookUpdate(obu OrderBookUpdate) {
	if w.pool == nil {
		return
	}

	w.bufferMutex.Lock()
	w.orderBookBuffer = append(w.orderBookBuffer, obu)
	shouldFlush := len(w.orderBookBuffer) >= w.config.BatchSize
	w.bufferMutex.Unlock()

	if shouldFlush {
		w.flushBuffers()
	}
}

// SaveTrade は約定情報をバッファに追加します。
func (w *TimescaleWriter) SaveTrade(trade Trade) {
	if w.pool == nil {
		return
	}

	w.bufferMutex.Lock()
	w.tradeBuffer = append(w.tradeBuffer, trade)
	shouldFlush := len(w.tradeBuffer) >= w.config.BatchSize
	w.bufferMutex.Unlock()

	if shouldFlush {
		w.flushBuffers()
	}
}

func (w *TimescaleWriter) flushBuffers() {
	if w.pool == nil {
		return
	}
	w.bufferMutex.Lock()
	defer w.bufferMutex.Unlock()

	if len(w.orderBookBuffer) > 0 {
		w.batchInsertOrderBookUpdates(context.Background(), w.orderBookBuffer)
		w.orderBookBuffer = w.orderBookBuffer[:0]
	}

	if len(w.tradeBuffer) > 0 {
		w.batchInsertTrades(context.Background(), w.tradeBuffer)
		w.tradeBuffer = w.tradeBuffer[:0]
	}

	if len(w.benchmarkBuffer) > 0 {
		w.batchInsertBenchmarkValues(context.Background(), w.benchmarkBuffer)
		w.benchmarkBuffer = w.benchmarkBuffer[:0]
	}

}

func (w *TimescaleWriter) batchInsertOrderBookUpdates(ctx context.Context, updates []OrderBookUpdate) {
	if w.pool == nil || len(updates) == 0 {
		return
	}
	w.logger.Debug("Flushing order book updates", zap.Int("count", len(updates)))

	_, err := w.pool.CopyFrom(
		ctx,
		pgx.Identifier{"order_book_updates"},
		[]string{"time", "pair", "side", "price", "size", "is_snapshot"},
		pgx.CopyFromRows(toOrderBookInterfaces(updates)),
	)
	if err != nil {
		w.logger.Error("Failed to batch insert order book updates", zap.Error(err))
	}
}

func (w *TimescaleWriter) batchInsertTrades(ctx context.Context, trades []Trade) {
	if w.pool == nil || len(trades) == 0 {
		return
	}
	w.logger.Debug("Flushing trades", zap.Int("count", len(trades)))
	_, err := w.pool.CopyFrom(
		ctx,
		pgx.Identifier{"trades"},
		[]string{"time", "pair", "side", "price", "size", "transaction_id", "is_cancelled", "is_my_trade"},
		pgx.CopyFromRows(toTradeInterfaces(trades)),
	)
	if err != nil {
		w.logger.Error("Failed to batch insert trades", zap.Error(err))
	}
}


// SaveBenchmarkValue はベンチマーク値をバッファに追加します。
func (w *TimescaleWriter) SaveBenchmarkValue(ctx context.Context, value BenchmarkValue) {
	if w.pool == nil {
		return
	}

	w.bufferMutex.Lock()
	w.benchmarkBuffer = append(w.benchmarkBuffer, value)
	shouldFlush := len(w.benchmarkBuffer) >= w.config.BatchSize
	w.bufferMutex.Unlock()

	if shouldFlush {
		w.flushBuffers()
	}
}

func (w *TimescaleWriter) batchInsertBenchmarkValues(ctx context.Context, values []BenchmarkValue) {
	if w.pool == nil || len(values) == 0 {
		return
	}
	w.logger.Debug("Flushing benchmark values", zap.Int("count", len(values)))
	_, err := w.pool.CopyFrom(
		ctx,
		pgx.Identifier{"benchmark_values"},
		[]string{"time", "price"},
		pgx.CopyFromRows(toBenchmarkInterfaces(values)),
	)
	if err != nil {
		w.logger.Error("Failed to batch insert benchmark values", zap.Error(err))
	}
}

func toOrderBookInterfaces(updates []OrderBookUpdate) [][]interface{} {
	rows := make([][]interface{}, len(updates))
	for i, u := range updates {
		rows[i] = []interface{}{u.Time, u.Pair, u.Side, u.Price, u.Size, u.IsSnapshot}
	}
	return rows
}


func toBenchmarkInterfaces(values []BenchmarkValue) [][]interface{} {
	rows := make([][]interface{}, len(values))
	for i, v := range values {
		rows[i] = []interface{}{v.Time, v.Price}
	}
	return rows
}

func toTradeInterfaces(trades []Trade) [][]interface{} {
	rows := make([][]interface{}, len(trades))
	for i, t := range trades {
		rows[i] = []interface{}{t.Time, t.Pair, t.Side, t.Price, t.Size, t.TransactionID, t.IsCancelled, t.IsMyTrade}
	}
	return rows
}

// SavePnLSummary は単一のPnLサマリーをデータベースに保存します。
func (w *TimescaleWriter) SavePnLSummary(ctx context.Context, pnl PnLSummary) error {
	if w.pool == nil {
		w.logger.Debug("Skipping PnL summary save for dummy writer", zap.Any("pnl", pnl))
		return nil
	}

	query := `INSERT INTO pnl_summary (time, strategy_id, pair, realized_pnl, unrealized_pnl, total_pnl, position_size, avg_entry_price)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := w.pool.Exec(ctx, query,
		pnl.Time, pnl.StrategyID, pnl.Pair,
		pnl.RealizedPnL, pnl.UnrealizedPnL, pnl.TotalPnL,
		pnl.PositionSize, pnl.AvgEntryPrice,
	)
	if err != nil {
		w.logger.Error("Failed to insert PnL summary", zap.Error(err), zap.Any("pnl", pnl))
		return fmt.Errorf("failed to insert PnL summary: %w", err)
	}
	w.logger.Debug("[Live] Saved PnL summary to DB.")
	return nil
}

// SaveTradePnL は個別の取引のPnLをデータベースに保存します。
func (w *TimescaleWriter) SaveTradePnL(ctx context.Context, tradePnl TradePnL) error {
	if w.pool == nil {
		w.logger.Debug("Skipping trade PnL save for dummy writer", zap.Any("tradePnl", tradePnl))
		return nil
	}

	// 既存の最大のcumulative_pnlを取得
	var lastCumulativePnl float64
	err := w.pool.QueryRow(ctx, "SELECT cumulative_pnl FROM trades_pnl ORDER BY created_at DESC LIMIT 1").Scan(&lastCumulativePnl)
	if err != nil && err != pgx.ErrNoRows {
		w.logger.Error("Failed to get last cumulative PnL", zap.Error(err))
		return fmt.Errorf("failed to get last cumulative PnL: %w", err)
	}

	tradePnl.CumulativePnl = lastCumulativePnl + tradePnl.Pnl

	query := `INSERT INTO trades_pnl (trade_id, pnl, cumulative_pnl, created_at)
	          VALUES ($1, $2, $3, $4)`
	_, err = w.pool.Exec(ctx, query,
		tradePnl.TradeID,
		tradePnl.Pnl,
		tradePnl.CumulativePnl,
		tradePnl.CreatedAt,
	)
	if err != nil {
		w.logger.Error("Failed to insert trade PnL", zap.Error(err), zap.Any("tradePnl", tradePnl))
		return fmt.Errorf("failed to insert trade PnL: %w", err)
	}
	w.logger.Debug("[Live] Saved trade PnL to DB.")
	return nil
}
