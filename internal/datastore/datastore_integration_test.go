package datastore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/datastore"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"go.uber.org/zap"
)

// setupTestDatabase starts a TimescaleDB container and applies the schema.
func setupTestDatabase(t *testing.T) (pool *pgxpool.Pool, cleanup func()) {
	ctx := context.Background()

	// Define the container request
	container, err := postgres.Run(ctx,
		"timescale/timescaledb:2.14-pg15",
		postgres.WithDatabase("test-db"),
		postgres.WithUsername("test-user"),
		postgres.WithPassword("test-password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(t, err)

	// Get the connection string
	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Create a connection pool
	pool, err = pgxpool.New(ctx, connStr)
	require.NoError(t, err)

	// Apply schema
	schemaDir := "../../db/schema"
	files, err := os.ReadDir(schemaDir)
	require.NoError(t, err)

	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".sql") {
			t.Logf("Applying schema: %s", file.Name())
			schemaPath := filepath.Join(schemaDir, file.Name())
			sqlBytes, err := os.ReadFile(schemaPath)
			require.NoError(t, err)
			_, err = pool.Exec(ctx, string(sqlBytes))
			require.NoError(t, err, "failed to apply schema from file %s", file.Name())
		}
	}

	// Define the cleanup function
	cleanup = func() {
		pool.Close()
		err := container.Terminate(ctx)
		require.NoError(t, err)
	}

	return pool, cleanup
}

func TestDatastore_Integration_WriteAndReadTrade(t *testing.T) {
	// Skip this test in short mode as it's a slow integration test.
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool, cleanup := setupTestDatabase(t)
	defer cleanup()

	// Create writer and repository with the test DB pool
	logger := zap.NewNop()
	writerConfig := config.DBWriterConfig{BatchSize: 1, WriteIntervalSeconds: 1}

	writer, err := dbwriter.NewTimescaleWriter(pool, writerConfig, logger)
	require.NoError(t, err)
	repo := datastore.NewTimescaleRepository(pool)

	// --- Test Execution ---
	tradeToSave := dbwriter.Trade{
		Time:          time.Now().Truncate(time.Millisecond),
		Pair:          "btc_jpy",
		Side:          "buy",
		Price:         6000000,
		Size:          0.01,
		TransactionID: 12345,
		IsMyTrade:     true,
	}

	// 1. Write the trade using the writer
	writer.SaveTrade(tradeToSave)

	// The writer buffers trades. In a real scenario, a background goroutine flushes it.
	// In this test, we need to manually flush it to ensure the data is written.
	// We can't access flushBuffers as it's not on the interface.
	// So we will just wait a bit. A better solution would be to have a flush method on the interface.
	// For now, let's just Close() the writer which triggers a final flush.
	writer.Close()

	// 2. Read the trade back using the repository
	trades, err := repo.FetchTradesForReportSince(context.Background(), 12344)
	require.NoError(t, err)

	// --- Assertions ---
	require.Len(t, trades, 1, "should fetch exactly one trade")

	fetchedTrade := trades[0]
	assert.Equal(t, tradeToSave.Pair, fetchedTrade.Pair)
	assert.Equal(t, tradeToSave.Side, fetchedTrade.Side)
	assert.Equal(t, tradeToSave.TransactionID, fetchedTrade.TransactionID)
	assert.True(t, fetchedTrade.IsMyTrade)
	assert.False(t, fetchedTrade.IsCancelled)
	assert.Equal(t, tradeToSave.Time, fetchedTrade.Time.In(tradeToSave.Time.Location()).Truncate(time.Millisecond))
}
