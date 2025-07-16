package benchmark

import (
	"context"
	"testing"

	"github.com/pashagolub/pgxmock/v2"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDBBenchmarkService_Tick(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	service := NewDBBenchmarkService(mock)

	strategyID := "buy_and_hold"
	value := decimal.NewFromFloat(12345.67)

	// pgxmockでは、引数マッチャーにAnyArg()を使うのが一般的です。
	// time.Now()は実行のたびに変わるため、具体的な値での比較が難しいためです。
	mock.ExpectExec("INSERT INTO benchmark_values").
		WithArgs(pgxmock.AnyArg(), strategyID, value).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = service.Tick(context.Background(), strategyID, value)
	assert.NoError(t, err)

	// すべての期待が満たされたか確認
	err = mock.ExpectationsWereMet()
	assert.NoError(t, err, "there were unfulfilled expectations")
}
