package datastore

import (
	"context"
	"testing"

	"github.com/pashagolub/pgxmock/v2"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimescaleRepository_FetchLatestPerformanceMetrics(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	repo := NewTimescaleRepository(mock)

	t.Run("success", func(t *testing.T) {
		expectedMetrics := &PerformanceMetrics{
			SharpeRatio:  1.5,
			ProfitFactor: 2.3,
			MaxDrawdown:  decimal.NewFromFloat(-100.50),
		}

		rows := pgxmock.NewRows([]string{"sharpe_ratio", "profit_factor", "max_drawdown"}).
			AddRow(expectedMetrics.SharpeRatio, expectedMetrics.ProfitFactor, expectedMetrics.MaxDrawdown)

		mock.ExpectQuery(`
        SELECT
            sharpe_ratio,
            profit_factor,
            max_drawdown
        FROM pnl_reports
        ORDER BY time DESC
        LIMIT 1;
    `).WillReturnRows(rows)

		metrics, err := repo.FetchLatestPerformanceMetrics(ctx)
		require.NoError(t, err)
		assert.Equal(t, expectedMetrics, metrics)

		// ensure all expectations were met
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		mock.ExpectQuery(".*").WillReturnError(assert.AnError)

		_, err := repo.FetchLatestPerformanceMetrics(ctx)
		assert.Error(t, err)

		// ensure all expectations were met
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
