package benchmark

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

// PgxPoolIface は、*pgxpool.Poolが満たすべきメソッドのインターフェースです。
// これにより、テストでモックを注入できます。
type PgxPoolIface interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
	QueryRow(context.Context, string, ...interface{}) pgx.Row
}

// BenchmarkService は、ベンチマークデータの記録を担当します。
type BenchmarkService interface {
	// Tick は、現在のベンチマーク値を記録します。
	Tick(ctx context.Context, strategyID string, value decimal.Decimal) error
}

// DBBenchmarkService は、データベースにベンチマーク値を保存するBenchmarkServiceの実装です。
type DBBenchmarkService struct {
	pool PgxPoolIface
}

// NewDBBenchmarkService は、新しいDBBenchmarkServiceを生成します。
func NewDBBenchmarkService(pool PgxPoolIface) *DBBenchmarkService {
	return &DBBenchmarkService{pool: pool}
}

// Tick は、指定された戦略IDと値でベンチマークデータをデータベースに挿入します。
func (s *DBBenchmarkService) Tick(ctx context.Context, strategyID string, value decimal.Decimal) error {
	const query = `
		INSERT INTO benchmark_values (time, strategy_id, value)
		VALUES ($1, $2, $3)
	`
	_, err := s.pool.Exec(ctx, query, time.Now(), strategyID, value)
	return err
}
