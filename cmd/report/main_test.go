package main

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestAnalyzeTrades(t *testing.T) {
	t.Run("1注文1約定の勝ちトレード", func(t *testing.T) {
		trades := []Trade{
			{Time: time.Now(), Pair: "BTC/JPY", Side: "buy", Price: decimal.NewFromFloat(100), Size: decimal.NewFromFloat(1), TransactionID: 1},
			{Time: time.Now(), Pair: "BTC/JPY", Side: "sell", Price: decimal.NewFromFloat(110), Size: decimal.NewFromFloat(1), TransactionID: 2},
		}
		report, err := analyzeTrades(trades)
		assert.NoError(t, err)
		assert.Equal(t, 1, report.WinningTrades)
		assert.Equal(t, 0, report.LosingTrades)
		assert.Equal(t, 1, report.LongWinningTrades)
		assert.Equal(t, 0, report.LongLosingTrades)
		assert.Equal(t, 0, report.ShortWinningTrades)
		assert.Equal(t, 0, report.ShortLosingTrades)
		assert.Equal(t, "10.00", report.TotalPnL.StringFixed(2))
	})

	t.Run("1注文1約定の負けトレード", func(t *testing.T) {
		trades := []Trade{
			{Time: time.Now(), Pair: "BTC/JPY", Side: "buy", Price: decimal.NewFromFloat(110), Size: decimal.NewFromFloat(1), TransactionID: 1},
			{Time: time.Now(), Pair: "BTC/JPY", Side: "sell", Price: decimal.NewFromFloat(100), Size: decimal.NewFromFloat(1), TransactionID: 2},
		}
		report, err := analyzeTrades(trades)
		assert.NoError(t, err)
		assert.Equal(t, 0, report.WinningTrades)
		assert.Equal(t, 1, report.LosingTrades)
		assert.Equal(t, 0, report.LongWinningTrades)
		assert.Equal(t, 1, report.LongLosingTrades)
		assert.Equal(t, 0, report.ShortWinningTrades)
		assert.Equal(t, 0, report.ShortLosingTrades)
		assert.Equal(t, "-10.00", report.TotalPnL.StringFixed(2))
	})

	t.Run("1注文複数約定の勝ちトレード", func(t *testing.T) {
		trades := []Trade{
			{Time: time.Now(), Pair: "BTC/JPY", Side: "buy", Price: decimal.NewFromFloat(100), Size: decimal.NewFromFloat(1), TransactionID: 1},
			{Time: time.Now(), Pair: "BTC/JPY", Side: "sell", Price: decimal.NewFromFloat(110), Size: decimal.NewFromFloat(0.5), TransactionID: 2},
			{Time: time.Now(), Pair: "BTC/JPY", Side: "sell", Price: decimal.NewFromFloat(120), Size: decimal.NewFromFloat(0.5), TransactionID: 3},
		}
		report, err := analyzeTrades(trades)
		assert.NoError(t, err)
		assert.Equal(t, 1, report.WinningTrades)
		assert.Equal(t, 0, report.LosingTrades)
		assert.Equal(t, 1, report.LongWinningTrades)
		assert.Equal(t, 0, report.LongLosingTrades)
		assert.Equal(t, 0, report.ShortWinningTrades)
		assert.Equal(t, 0, report.ShortLosingTrades)
		assert.Equal(t, "15.00", report.TotalPnL.StringFixed(2))
	})

	t.Run("1注文複数約定で勝ち負け混在（全体で勝ち）", func(t *testing.T) {
		trades := []Trade{
			{Time: time.Now(), Pair: "BTC/JPY", Side: "buy", Price: decimal.NewFromFloat(100), Size: decimal.NewFromFloat(1), TransactionID: 1},
			{Time: time.Now(), Pair: "BTC/JPY", Side: "sell", Price: decimal.NewFromFloat(120), Size: decimal.NewFromFloat(0.5), TransactionID: 2},
			{Time: time.Now(), Pair: "BTC/JPY", Side: "sell", Price: decimal.NewFromFloat(90), Size: decimal.NewFromFloat(0.5), TransactionID: 3},
		}
		report, err := analyzeTrades(trades)
		assert.NoError(t, err)
		assert.Equal(t, 1, report.WinningTrades)
		assert.Equal(t, 0, report.LosingTrades)
		assert.Equal(t, 1, report.LongWinningTrades)
		assert.Equal(t, 0, report.LongLosingTrades)
		assert.Equal(t, 0, report.ShortWinningTrades)
		assert.Equal(t, 0, report.ShortLosingTrades)
		assert.Equal(t, "5.00", report.TotalPnL.StringFixed(2))
	})

	t.Run("1注文複数約定で勝ち負け混在（全体で負け）", func(t *testing.T) {
		trades := []Trade{
			{Time: time.Now(), Pair: "BTC/JPY", Side: "buy", Price: decimal.NewFromFloat(100), Size: decimal.NewFromFloat(1), TransactionID: 1},
			{Time: time.Now(), Pair: "BTC/JPY", Side: "sell", Price: decimal.NewFromFloat(110), Size: decimal.NewFromFloat(0.5), TransactionID: 2},
			{Time: time.Now(), Pair: "BTC/JPY", Side: "sell", Price: decimal.NewFromFloat(80), Size: decimal.NewFromFloat(0.5), TransactionID: 3},
		}
		report, err := analyzeTrades(trades)
		assert.NoError(t, err)
		assert.Equal(t, 0, report.WinningTrades)
		assert.Equal(t, 1, report.LosingTrades)
		assert.Equal(t, 0, report.LongWinningTrades)
		assert.Equal(t, 1, report.LongLosingTrades)
		assert.Equal(t, 0, report.ShortWinningTrades)
		assert.Equal(t, 0, report.ShortLosingTrades)
		assert.Equal(t, "-5.00", report.TotalPnL.StringFixed(2))
	})

	t.Run("損益ゼロのトレード", func(t *testing.T) {
		trades := []Trade{
			{Time: time.Now(), Pair: "BTC/JPY", Side: "buy", Price: decimal.NewFromFloat(100), Size: decimal.NewFromFloat(1), TransactionID: 1},
			{Time: time.Now(), Pair: "BTC/JPY", Side: "sell", Price: decimal.NewFromFloat(100), Size: decimal.NewFromFloat(1), TransactionID: 2},
		}
		report, err := analyzeTrades(trades)
		assert.NoError(t, err)
		assert.Equal(t, 0, report.WinningTrades)
		assert.Equal(t, 0, report.LosingTrades)
		assert.Equal(t, "0.00", report.TotalPnL.StringFixed(2))
	})

	t.Run("ショートトレード", func(t *testing.T) {
		trades := []Trade{
			{Time: time.Now(), Pair: "BTC/JPY", Side: "sell", Price: decimal.NewFromFloat(110), Size: decimal.NewFromFloat(1), TransactionID: 1},
			{Time: time.Now(), Pair: "BTC/JPY", Side: "buy", Price: decimal.NewFromFloat(100), Size: decimal.NewFromFloat(1), TransactionID: 2},
		}
		report, err := analyzeTrades(trades)
		assert.NoError(t, err)
		assert.Equal(t, 1, report.WinningTrades)
		assert.Equal(t, 0, report.LosingTrades)
		assert.Equal(t, 0, report.LongWinningTrades)
		assert.Equal(t, 0, report.LongLosingTrades)
		assert.Equal(t, 1, report.ShortWinningTrades)
		assert.Equal(t, 0, report.ShortLosingTrades)
		assert.Equal(t, "10.00", report.TotalPnL.StringFixed(2))
	})
}
