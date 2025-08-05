package datastore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

// Repository defines the interface for database operations for fetching backtest data.
type Repository interface {
	FetchTradesForReportSince(ctx context.Context, lastTradeID int64) ([]Trade, error)
	FetchLatestPnlReportTradeID(ctx context.Context) (int64, error)
	DeleteOldPnlReports(ctx context.Context, maxAgeHours int) (int64, error)
	FetchLatestPerformanceMetrics(ctx context.Context) (*PerformanceMetrics, error)
}

// Pool is an interface that abstracts the pgxpool.Pool for testability.
// It includes only the methods used by the TimescaleRepository.
type Pool interface {
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...interface{}) (pgconn.CommandTag, error)
}

// TimescaleRepository handles database operations for fetching backtest data.
type TimescaleRepository struct {
	db Pool
}

// NewTimescaleRepository creates a new TimescaleRepository.
func NewTimescaleRepository(db Pool) Repository {
	return &TimescaleRepository{db: db}
}

// Trade はデータベースからの取引を表します。
type Trade struct {
	Time            time.Time       `json:"time"`
	Pair            string          `json:"pair"`
	Side            string          `json:"side"`
	Price           decimal.Decimal `json:"price"`
	Size            decimal.Decimal `json:"size"`
	TransactionID   int64           `json:"transaction_id"`
	IsCancelled     bool            `json:"is_cancelled"`
	IsMyTrade       bool            `json:"is_my_trade"`
}

// FetchTradesForReportSince は指定された trade_id 以降の自分自身のトレードのみを取得します。
func (r *TimescaleRepository) FetchTradesForReportSince(ctx context.Context, lastTradeID int64) ([]Trade, error) {
	query := `
		SELECT time, pair, side, price, size, transaction_id, is_cancelled, is_my_trade
		FROM trades
		WHERE is_my_trade = TRUE AND transaction_id > $1
		ORDER BY time ASC;
	`
	rows, err := r.db.Query(ctx, query, lastTradeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trades []Trade
	for rows.Next() {
		var t Trade
		if err := rows.Scan(&t.Time, &t.Pair, &t.Side, &t.Price, &t.Size, &t.TransactionID, &t.IsCancelled, &t.IsMyTrade); err != nil {
			return nil, err
		}
		trades = append(trades, t)
	}

	return trades, rows.Err()
}

// FetchLatestPnlReportTradeID は pnl_reports テーブルから最新の last_trade_id を取得します。
func (r *TimescaleRepository) FetchLatestPnlReportTradeID(ctx context.Context) (int64, error) {
	query := `
		SELECT last_trade_id
		FROM pnl_reports
		ORDER BY time DESC
		LIMIT 1;
	`
	var lastTradeID int64
	err := r.db.QueryRow(ctx, query).Scan(&lastTradeID)
	if err != nil {
		return 0, err
	}
	return lastTradeID, nil
}

// DeleteOldPnlReports は指定した期間より古いPnLレポートを削除します。
func (r *TimescaleRepository) DeleteOldPnlReports(ctx context.Context, maxAgeHours int) (int64, error) {
	threshold := time.Now().Add(-time.Duration(maxAgeHours) * time.Hour)
	query := `
        DELETE FROM pnl_reports
        WHERE time < $1;
    `
	result, err := r.db.Exec(ctx, query, threshold)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// PerformanceMetrics はドリフトモニターが必要とする主要なパフォーマンス指標を保持します。
type PerformanceMetrics struct {
	SharpeRatio  float64
	ProfitFactor float64
	MaxDrawdown  decimal.Decimal
}

// FetchLatestPerformanceMetrics は最新のパフォーマンス指標を取得します。
func (r *TimescaleRepository) FetchLatestPerformanceMetrics(ctx context.Context) (*PerformanceMetrics, error) {
	query := `
        SELECT
            sharpe_ratio,
            profit_factor,
            max_drawdown
        FROM pnl_reports
        ORDER BY time DESC
        LIMIT 1;
    `
	row := r.db.QueryRow(ctx, query)
	var metrics PerformanceMetrics
	err := row.Scan(
		&metrics.SharpeRatio,
		&metrics.ProfitFactor,
		&metrics.MaxDrawdown,
	)
	if err != nil {
		return nil, err
	}
	return &metrics, nil
}
