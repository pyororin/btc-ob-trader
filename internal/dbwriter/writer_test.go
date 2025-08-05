package dbwriter

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"go.uber.org/zap"
)

// TestTimescaleWriter_ImplementsDBWriter は TimescaleWriter が DBWriter インターフェースを実装していることを確認します。
func TestTimescaleWriter_ImplementsDBWriter(t *testing.T) {
	assert.Implements(t, (*DBWriter)(nil), new(TimescaleWriter))
}

func TestTimescaleWriter_SaveTrade(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	writerConfig := config.DBWriterConfig{
		BatchSize:            1, // Set batch size to 1 to trigger flush immediately
		WriteIntervalSeconds: 1,
	}

	writer, err := NewTimescaleWriter(mock, writerConfig, zap.NewNop())
	require.NoError(t, err)

	tradeToSave := Trade{
		Time:          time.Now(),
		Pair:          "btc_jpy",
		Side:          "buy",
		Price:         6000000,
		Size:          0.01,
		TransactionID: 12345,
		IsMyTrade:     true,
	}

	// Expect the CopyFrom operation for the trades table
	mock.ExpectCopyFrom(
		pgx.Identifier{"trades"},
		[]string{"time", "pair", "side", "price", "size", "transaction_id", "is_cancelled", "is_my_trade"},
	)

	writer.SaveTrade(tradeToSave)

	// In the test, we need to manually flush the buffer to check the expectation.
	// The real writer does this in a background goroutine.
	// We can't access flushBuffers directly. Let's use Close() to trigger a final flush.
	writer.Close()

	// ensure all expectations were met
	require.NoError(t, mock.ExpectationsWereMet(), "there were unfulfilled expectations")
}
