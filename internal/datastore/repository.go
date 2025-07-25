package datastore

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)


// Repository handles database operations for fetching backtest data.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
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

// FetchAllTradesForReport はデータベースから自分自身のトレードのみを取得します。
func (r *Repository) FetchAllTradesForReport(ctx context.Context) ([]Trade, error) {
	query := `
        SELECT time, pair, side, price, size, transaction_id, is_cancelled, is_my_trade
        FROM trades
        WHERE is_my_trade = TRUE
        ORDER BY time ASC;
    `
	rows, err := r.db.Query(ctx, query)
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

// DeleteOldPnlReports は指定した期間より古いPnLレポートを削除します。
func (r *Repository) DeleteOldPnlReports(ctx context.Context, maxAgeHours int) (int64, error) {
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
func (r *Repository) FetchLatestPerformanceMetrics(ctx context.Context) (*PerformanceMetrics, error) {
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
