//go:build sqltest
// +build sqltest

package datastore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupTestDB は、テスト用のPostgreSQLコンテナをセットアップし、スキーマを適用します。
func setupTestDB(t *testing.T) *pgxpool.Pool {
	ctx := context.Background()

	// TimescaleDBのDockerイメージを指定
	pgContainer, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("timescale/timescaledb:latest-pg14"),
		postgres.WithDatabase("test-db"),
		postgres.WithUsername("user"),
		postgres.WithPassword("password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate container: %s", err)
		}
	})

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxpool.New(ctx, connStr)
	require.NoError(t, err)

	// スキーマとビューを適用
	applySQLFile(t, pool, "../../db/schema/001_tables.sql")
	applySQLFile(t, pool, "../../migrations/20250715_create_benchmark.sql")
	applySQLFile(t, pool, "../../db/schema/002_views.sql")

	return pool
}

func applySQLFile(t *testing.T, pool *pgxpool.Pool, filePath string) {
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), string(content))
	require.NoError(t, err, fmt.Sprintf("Failed to apply SQL file: %s", filePath))
}

func TestVPnlWithBenchmarkView(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	ctx := context.Background()

	// テストデータを挿入
	strategyID := "test_strategy"
	now := time.Now()

	// pnl_summary
	_, err := pool.Exec(ctx, `
		INSERT INTO pnl_summary (time, strategy_id, pair, total_pnl, position_size, avg_entry_price) VALUES
		($1, $2, 'btc_jpy', 1000, 1, 5000000),
		($3, $2, 'btc_jpy', 1100, 1, 5000000)
	`, now.Add(-2*time.Minute), strategyID, now.Add(-1*time.Minute))
	require.NoError(t, err)

	// benchmark_values
	_, err = pool.Exec(ctx, `
		INSERT INTO benchmark_values (time, strategy_id, value) VALUES
		($1, $2, 5000000),
		($3, $2, 5050000)
	`, now.Add(-2*time.Minute), strategyID, now.Add(-1*time.Minute))
	require.NoError(t, err)

	// ビューからデータを取得して検証
	var normalizedPnl, normalizedBenchmark decimal.Decimal
	err = pool.QueryRow(ctx, `
		SELECT normalized_pnl, normalized_benchmark
		FROM v_pnl_with_benchmark
		WHERE strategy_id = $1
		ORDER BY time DESC
		LIMIT 1
	`, strategyID).Scan(&normalizedPnl, &normalizedBenchmark)
	require.NoError(t, err)

	// 期待値の計算
	// PnL: (1 + (1100 - 1000) / 1000) * 100 = 110
	// Benchmark: (1 + (5050000 - 5000000) / 5000000) * 100 = 101
	expectedPnl := decimal.NewFromInt(110)
	expectedBenchmark := decimal.NewFromInt(101)

	require.True(t, expectedPnl.Equal(normalizedPnl), "Expected PnL %s, but got %s", expectedPnl, normalizedPnl)
	require.True(t, expectedBenchmark.Equal(normalizedBenchmark), "Expected Benchmark %s, but got %s", expectedBenchmark, normalizedBenchmark)
}
