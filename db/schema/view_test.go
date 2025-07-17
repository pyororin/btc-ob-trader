//go:build sqltest
// +build sqltest

package schema

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestViewPerformanceVsBenchmark(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("Skipping test in CI environment")
	}
	ctx := context.Background()

	// 1. PostgreSQLコンテナの起動 (TimescaleDBイメージを使用)
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
	if err != nil {
		t.Fatalf("failed to start postgres container: %s", err)
	}
	defer func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate postgres container: %s", err)
		}
	}()

	// 2. DB接続
	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %s", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect to database: %s", err)
	}
	defer pool.Close()

	// 3. スキーマの適用
	applyAllSchemas(t, pool, ".")

	// 4. テストデータの挿入
	insertTestData(t, pool)

	// 5. ビューのクエリ
	rows, err := pool.Query(ctx, "SELECT bucket, last_pnl, normalized_price, alpha FROM v_performance_vs_benchmark ORDER BY bucket")
	if err != nil {
		t.Fatalf("failed to query view: %s", err)
	}
	defer rows.Close()

	// 6. 結果の検証
	type resultRow struct {
		Bucket          time.Time
		LastPnl         float64
		NormalizedPrice float64
		Alpha           float64
	}

	var results []resultRow
	for rows.Next() {
		var r resultRow
		err := rows.Scan(&r.Bucket, &r.LastPnl, &r.NormalizedPrice, &r.Alpha)
		if err != nil {
			t.Fatalf("failed to scan row: %s", err)
		}
		results = append(results, r)
	}

	assert.Len(t, results, 2, "should have 2 rows of results")

	// 期待値の検証 (ロジックはビューの定義に依存)
	// 1分目
	assert.InDelta(t, 100.0, results[0].LastPnl, 0.01)
	assert.InDelta(t, 101.0, results[0].NormalizedPrice, 0.01) // 10100 / 10000 * 100
	assert.InDelta(t, -1.0, results[0].Alpha, 0.01)             // 100.0 - 101.0

	// 2分目
	assert.InDelta(t, 150.0, results[1].LastPnl, 0.01)
	assert.InDelta(t, 102.0, results[1].NormalizedPrice, 0.01) // 10200 / 10000 * 100
	assert.InDelta(t, 48.0, results[1].Alpha, 0.01)             // 150.0 - 102.0
}

func applyAllSchemas(t *testing.T, pool *pgxpool.Pool, schemaDir string) {
	t.Helper()

	// スキーマディレクトリを決定
	// テストがどこから実行されてもいいように、まず `db/schema` を試す
	var effectiveSchemaDir string
	if _, err := os.Stat(filepath.Join("db", "schema")); !os.IsNotExist(err) {
		effectiveSchemaDir = filepath.Join("db", "schema")
	} else if _, err := os.Stat(schemaDir); !os.IsNotExist(err) {
		effectiveSchemaDir = schemaDir
	} else {
		t.Fatalf("schema directory not found at %s or db/schema", schemaDir)
	}

	files, err := filepath.Glob(filepath.Join(effectiveSchemaDir, "*.sql"))
	if err != nil {
		t.Fatalf("failed to glob sql files: %s", err)
	}
	// ファイル名でソートして実行順を保証
	// sort.Strings(files) // filepath.Globはソート済みのはずだが念のため

	for _, file := range files {
		schema, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("failed to read schema file %s: %s", file, err)
		}
		_, err = pool.Exec(context.Background(), string(schema))
		if err != nil {
			t.Fatalf("failed to apply schema %s: %s", file, err)
		}
		t.Logf("Applied schema: %s", file)
	}
}

func insertTestData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	// pnl_summary データ
	pnlData := []struct {
		Time     time.Time
		TotalPnl float64
	}{
		{now.Add(-2 * time.Minute), 50.0},
		{now.Add(-1 * time.Minute), 100.0}, // 1分目の最後の値
		{now.Add(-30 * time.Second), 120.0},
		{now.Add(0 * time.Second), 150.0}, // 2分目の最後の値
	}
	for _, d := range pnlData {
		_, err := pool.Exec(ctx, "INSERT INTO pnl_summary (time, strategy_id, pair, realized_pnl, unrealized_pnl, total_pnl) VALUES ($1, 'test', 'btc_jpy', 0, 0, $2)", d.Time, d.TotalPnl)
		if err != nil {
			t.Fatalf("failed to insert pnl data: %s", err)
		}
	}

	// benchmark_values データ
	benchmarkData := []struct {
		Time  time.Time
		Price float64
	}{
		{now.Add(-2 * time.Minute), 10000.0}, // 最初の価格
		{now.Add(-1 * time.Minute), 10100.0}, // 1分目の最後の価格
		{now.Add(-30 * time.Second), 10150.0},
		{now.Add(0 * time.Second), 10200.0}, // 2分目の最後の価格
	}
	for _, d := range benchmarkData {
		_, err := pool.Exec(ctx, "INSERT INTO benchmark_values (time, price) VALUES ($1, $2)", d.Time, d.Price)
		if err != nil {
			t.Fatalf("failed to insert benchmark data: %s", err)
		}
	}
}
