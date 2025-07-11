package dbwriter

import (
	"context"
	"fmt"
	"time"

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

// PnLSummary はデータベースに保存するPnL情報の構造体です。
type PnLSummary struct {
	Time           time.Time `db:"time"`
	StrategyID     string    `db:"strategy_id"`
	Pair           string    `db:"pair"`
	RealizedPnL    float64   `db:"realized_pnl"`
	UnrealizedPnL  float64   `db:"unrealized_pnl"`
	TotalPnL       float64   `db:"total_pnl"`
	PositionSize   float64   `db:"position_size"`
	AvgEntryPrice  float64   `db:"avg_entry_price"`
}

// Writer はTimescaleDBへのデータ書き込みを担当します。
type Writer struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
	config config.DBWriterConfig
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
		logger.Error("Failed to ping database", zap.Error(err))
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logger.Info("Successfully connected to TimescaleDB")
	return &Writer{
		pool:   pool,
		logger: logger,
		config: writerConfig,
	}, nil
}

// Close はデータベース接続プールをクローズします。
func (w *Writer) Close() {
	if w.pool != nil {
		w.pool.Close()
		w.logger.Info("TimescaleDB connection pool closed")
	}
}

// SaveOrderBookUpdate は単一の板情報更新をデータベースに保存します。
func (w *Writer) SaveOrderBookUpdate(ctx context.Context, obu OrderBookUpdate) error {
	query := `INSERT INTO order_book_updates (time, pair, side, price, size, is_snapshot)
	          VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := w.pool.Exec(ctx, query, obu.Time, obu.Pair, obu.Side, obu.Price, obu.Size, obu.IsSnapshot)
	if err != nil {
		w.logger.Error("Failed to insert order book update", zap.Error(err), zap.Any("update", obu))
		return fmt.Errorf("failed to insert order book update: %w", err)
	}
	return nil
}

// SavePnLSummary は単一のPnLサマリーをデータベースに保存します。
func (w *Writer) SavePnLSummary(ctx context.Context, pnl PnLSummary) error {
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
	return nil
}
