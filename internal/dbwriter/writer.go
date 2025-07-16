package dbwriter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
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
	RealizedPnL     float64   `db:"realized_pnl"`
}

// PnLSummary はデータベースに保存するPnL情報の構造体です。
type PnLSummary struct {
	Time            time.Time `db:"time"`
	ReplaySessionID *string   `db:"replay_session_id"`
	StrategyID      string    `db:"strategy_id"`
	Pair            string    `db:"pair"`
	RealizedPnL     float64   `db:"realized_pnl"`
	UnrealizedPnL   float64   `db:"unrealized_pnl"`
	TotalPnL        float64   `db:"total_pnl"`
	PositionSize    float64   `db:"position_size"`
	AvgEntryPrice   float64   `db:"avg_entry_price"`
}

// Writer はTimescaleDBへのデータ書き込みを担当します。
type writer struct {
	pool            *pgxpool.Pool
	logger          logger.Logger
	config          config.DBWriterConfig
	replaySessionID *string
	orderBookBuffer []OrderBookUpdate
	tradeBuffer     []Trade
	bufferMutex     sync.Mutex
	flushTicker     *time.Ticker
	shutdownChan    chan struct{}
}

// NewWriter は新しいWriterインスタンスを作成します。
func NewWriter(ctx context.Context, dbConfig config.DatabaseConfig, writerConfig config.DBWriterConfig, logger logger.Logger) (Repository, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		dbConfig.User, dbConfig.Password, dbConfig.Host, dbConfig.Port, dbConfig.Name, dbConfig.SSLMode)

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		logger.Errorf("Unable to parse database DSN: %v", err)
		return nil, fmt.Errorf("failed to parse DSN: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		logger.Errorf("Unable to create connection pool: %v", err)
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		logger.Warnf("Failed to ping database, creating dummy writer: %v", err)
		return NewDummyWriter(logger), nil
	}

	w := &writer{
		pool:            pool,
		logger:          logger,
		config:          writerConfig,
		orderBookBuffer: make([]OrderBookUpdate, 0, writerConfig.BatchSize),
		tradeBuffer:     make([]Trade, 0, writerConfig.BatchSize),
		shutdownChan:    make(chan struct{}),
	}

	if w.config.BatchSize <= 0 {
		w.config.BatchSize = 100
		logger.Warnf("DBWriter BatchSize is zero or negative, defaulting to %d.", w.config.BatchSize)
	}
	if w.config.WriteIntervalSeconds <= 0 {
		w.config.WriteIntervalSeconds = 1
		logger.Warnf("DBWriter WriteIntervalSeconds is zero or negative, defaulting to %d.", w.config.WriteIntervalSeconds)
	}

	batchInterval := time.Duration(w.config.WriteIntervalSeconds) * time.Second
	w.flushTicker = time.NewTicker(batchInterval)
	go w.run()
	logger.Info("Successfully connected to TimescaleDB and started batch writer")

	return w, nil
}

func (w *writer) SetReplaySessionID(sessionID string) {
	w.replaySessionID = &sessionID
}

func (w *writer) Close() {
	w.logger.Info("Closing TimescaleDB writer...")
	close(w.shutdownChan)
	w.flushTicker.Stop()
	w.flushBuffers()
	if w.pool != nil {
		w.pool.Close()
		w.logger.Info("TimescaleDB connection pool closed")
	}
}

func (w *writer) run() {
	for {
		select {
		case <-w.flushTicker.C:
			w.flushBuffers()
		case <-w.shutdownChan:
			return
		}
	}
}

func (w *writer) SaveOrderBookUpdate(obu OrderBookUpdate) {
	w.bufferMutex.Lock()
	w.orderBookBuffer = append(w.orderBookBuffer, obu)
	shouldFlush := len(w.orderBookBuffer) >= w.config.BatchSize
	w.bufferMutex.Unlock()

	if shouldFlush {
		w.flushBuffers()
	}
}

func (w *writer) SaveTrade(trade Trade) {
	w.bufferMutex.Lock()
	trade.ReplaySessionID = w.replaySessionID
	w.tradeBuffer = append(w.tradeBuffer, trade)
	shouldFlush := len(w.tradeBuffer) >= w.config.BatchSize
	w.bufferMutex.Unlock()

	if shouldFlush {
		w.flushBuffers()
	}
}

func (w *writer) flushBuffers() {
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
}

func (w *writer) batchInsertOrderBookUpdates(ctx context.Context, updates []OrderBookUpdate) {
	if len(updates) == 0 {
		return
	}
	w.logger.Infof("Flushing %d order book updates", len(updates))
	_, err := w.pool.CopyFrom(
		ctx,
		pgx.Identifier{"order_book_updates"},
		[]string{"time", "pair", "side", "price", "size", "is_snapshot"},
		pgx.CopyFromRows(toOrderBookInterfaces(updates)),
	)
	if err != nil {
		w.logger.Errorf("Failed to batch insert order book updates: %v", err)
	}
}

func (w *writer) batchInsertTrades(ctx context.Context, trades []Trade) {
	if len(trades) == 0 {
		return
	}
	w.logger.Infof("Flushing %d trades", len(trades))
	_, err := w.pool.CopyFrom(
		ctx,
		pgx.Identifier{"trades"},
		[]string{"time", "replay_session_id", "pair", "side", "price", "size", "transaction_id", "is_cancelled"},
		pgx.CopyFromRows(toTradeInterfaces(trades)),
	)
	if err != nil {
		w.logger.Errorf("Failed to batch insert trades: %v", err)
	}
}

func toOrderBookInterfaces(updates []OrderBookUpdate) [][]interface{} {
	rows := make([][]interface{}, len(updates))
	for i, u := range updates {
		rows[i] = []interface{}{u.Time, u.Pair, u.Side, u.Price, u.Size, u.IsSnapshot}
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

func (w *writer) SavePnLSummary(ctx context.Context, pnl PnLSummary) error {
	pnl.ReplaySessionID = w.replaySessionID
	query := `INSERT INTO pnl_summary (time, replay_session_id, strategy_id, pair, realized_pnl, unrealized_pnl, total_pnl, position_size, avg_entry_price)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := w.pool.Exec(ctx, query,
		pnl.Time, pnl.ReplaySessionID, pnl.StrategyID, pnl.Pair,
		pnl.RealizedPnL, pnl.UnrealizedPnL, pnl.TotalPnL,
		pnl.PositionSize, pnl.AvgEntryPrice,
	)
	if err != nil {
		w.logger.Errorf("Failed to insert PnL summary: %v", err)
		return fmt.Errorf("failed to insert PnL summary: %w", err)
	}
	return nil
}

func (w *writer) SaveBenchmarkResult(ctx context.Context, result *BenchmarkResult) error {
	query := `INSERT INTO benchmark_results (time, strategy_id, pair, value)
	          VALUES ($1, $2, $3, $4)`
	_, err := w.pool.Exec(ctx, query,
		result.Time, result.StrategyID, result.Pair, result.Value,
	)
	if err != nil {
		w.logger.Errorf("Failed to insert benchmark result: %v", err)
		return fmt.Errorf("failed to insert benchmark result: %w", err)
	}
	return nil
}
