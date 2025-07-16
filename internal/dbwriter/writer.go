package dbwriter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	ReplaySessionID *string   `db:"replay_session_id"` // Use pointer to handle NULL
	Pair            string    `db:"pair"`
	Side            string    `db:"side"` // "buy" or "sell"
	Price           float64   `db:"price"`
	Size            float64   `db:"size"`
	TransactionID   int64     `db:"transaction_id"`
	IsCancelled     bool      `db:"is_cancelled"`
	RealizedPnL   float64   `db:"realized_pnl"` // Not always stored in DB, but used for simulation summary
}

// PnLSummary はデータベースに保存するPnL情報の構造体です。
type PnLSummary struct {
	Time            time.Time `db:"time"`
	ReplaySessionID *string   `db:"replay_session_id"` // Use pointer to handle NULL
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

// Writer はTimescaleDBへのデータ書き込みを担当します。
type Writer struct {
	pool             *pgxpool.Pool
	logger           *zap.Logger
	config           config.DBWriterConfig
	replaySessionID  *string // Use pointer to handle NULL
	orderBookBuffer  []OrderBookUpdate
	tradeBuffer      []Trade
	benchmarkBuffer  []BenchmarkValue
	bufferMutex      sync.Mutex
	flushTicker      *time.Ticker
	shutdownChan     chan struct{}
}

// SetReplaySessionID sets the replay session ID for the writer.
func (w *Writer) SetReplaySessionID(sessionID string) {
	w.replaySessionID = &sessionID
}

// NewWriter は新しいWriterインスタンスを作成します。
func NewWriter(ctx context.Context, dbConfig config.DatabaseConfig, writerConfig config.DBWriterConfig, logger *zap.Logger) (*Writer, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		dbConfig.User,
		dbConfig.Password,
		dbConfig.Host,
		dbConfig.Port,
		dbConfig.Name,
		dbConfig.SSLMode,
	)

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		logger.Error("Unable to parse database DSN", zap.Error(err), zap.String("dsn", dsn)) // Log DSN for debugging
		return nil, fmt.Errorf("failed to parse DSN: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		logger.Error("Unable to create connection pool", zap.Error(err))
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		logger.Warn("Failed to ping database, creating dummy writer", zap.Error(err))
		// Return a "dummy" writer that does nothing if DB connection fails.
		return &Writer{
			pool:         nil, // Explicitly nil
			logger:       logger,
			shutdownChan: make(chan struct{}), // Still need a valid channel
		}, nil
	}

	writer := &Writer{
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
func (w *Writer) Close() {
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

func (w *Writer) run() {
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
func (w *Writer) SaveOrderBookUpdate(obu OrderBookUpdate) {
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
func (w *Writer) SaveTrade(trade Trade) {
	if w.pool == nil {
		return
	}

	w.bufferMutex.Lock()
	trade.ReplaySessionID = w.replaySessionID
	w.tradeBuffer = append(w.tradeBuffer, trade)
	shouldFlush := len(w.tradeBuffer) >= w.config.BatchSize
	w.bufferMutex.Unlock()

	if shouldFlush {
		w.flushBuffers()
	}
}

func (w *Writer) flushBuffers() {
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

func (w *Writer) batchInsertOrderBookUpdates(ctx context.Context, updates []OrderBookUpdate) {
	if w.pool == nil || len(updates) == 0 {
		return
	}
	w.logger.Info("Flushing order book updates", zap.Int("count", len(updates)))

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

func (w *Writer) batchInsertTrades(ctx context.Context, trades []Trade) {
	if w.pool == nil || len(trades) == 0 {
		return
	}
	w.logger.Info("Flushing trades", zap.Int("count", len(trades)))
	_, err := w.pool.CopyFrom(
		ctx,
		pgx.Identifier{"trades"},
		[]string{"time", "replay_session_id", "pair", "side", "price", "size", "transaction_id", "is_cancelled"},
		pgx.CopyFromRows(toTradeInterfaces(trades)),
	)
	if err != nil {
		w.logger.Error("Failed to batch insert trades", zap.Error(err))
	}
}

// SaveBenchmarkValue はベンチマーク値をバッファに追加します。
func (w *Writer) SaveBenchmarkValue(ctx context.Context, value BenchmarkValue) {
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

func (w *Writer) batchInsertBenchmarkValues(ctx context.Context, values []BenchmarkValue) {
	if w.pool == nil || len(values) == 0 {
		return
	}
	w.logger.Info("Flushing benchmark values", zap.Int("count", len(values)))
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
		rows[i] = []interface{}{t.Time, t.ReplaySessionID, t.Pair, t.Side, t.Price, t.Size, t.TransactionID, t.IsCancelled}
	}
	return rows
}

// SavePnLSummary は単一のPnLサマリーをデータベースに保存します。
func (w *Writer) SavePnLSummary(ctx context.Context, pnl PnLSummary) error {
	if w.pool == nil {
		w.logger.Info("Skipping PnL summary save for dummy writer", zap.Any("pnl", pnl))
		return nil
	}

	pnl.ReplaySessionID = w.replaySessionID

	query := `INSERT INTO pnl_summary (time, replay_session_id, strategy_id, pair, realized_pnl, unrealized_pnl, total_pnl, position_size, avg_entry_price)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := w.pool.Exec(ctx, query,
		pnl.Time, pnl.ReplaySessionID, pnl.StrategyID, pnl.Pair,
		pnl.RealizedPnL, pnl.UnrealizedPnL, pnl.TotalPnL,
		pnl.PositionSize, pnl.AvgEntryPrice,
	)
	if err != nil {
		w.logger.Error("Failed to insert PnL summary", zap.Error(err), zap.Any("pnl", pnl))
		return fmt.Errorf("failed to insert PnL summary: %w", err)
	}
	return nil
}
