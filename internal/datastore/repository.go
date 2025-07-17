package datastore

import (
	"context"
	"fmt"
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
}

// Report は損益分析の結果を保持します。
type Report struct {
	StartDate          time.Time       `json:"start_date"`
	EndDate            time.Time       `json:"end_date"`
	TotalTrades        int             `json:"total_trades"`        // Executed trades
	CancelledTrades    int             `json:"cancelled_trades"`    // Cancelled trades
	CancellationRate   float64         `json:"cancellation_rate"`   // Cancellation rate
	WinningTrades      int             `json:"winning_trades"`
	LosingTrades       int             `json:"losing_trades"`
	WinRate            float64         `json:"win_rate"`
	LongWinningTrades  int             `json:"long_winning_trades"`
	LongLosingTrades   int             `json:"long_losing_trades"`
	LongWinRate        float64         `json:"long_win_rate"`
	ShortWinningTrades int             `json:"short_winning_trades"`
	ShortLosingTrades  int             `json:"short_losing_trades"`
	ShortWinRate       float64         `json:"short_win_rate"`
	TotalPnL           decimal.Decimal `json:"total_pnl"`
	AverageProfit      decimal.Decimal `json:"average_profit"`
	AverageLoss        decimal.Decimal `json:"average_loss"`
	RiskRewardRatio    float64         `json:"risk_reward_ratio"`
}

// FetchAllTradesForReport はデータベースからすべてのトレードを取得します。
func (r *Repository) FetchAllTradesForReport(ctx context.Context) ([]Trade, error) {
	query := `
        SELECT time, pair, side, price, size, transaction_id, is_cancelled
        FROM trades
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
		if err := rows.Scan(&t.Time, &t.Pair, &t.Side, &t.Price, &t.Size, &t.TransactionID, &t.IsCancelled); err != nil {
			return nil, err
		}
		trades = append(trades, t)
	}

	return trades, rows.Err()
}

// AnalyzeTrades はトレードリストを分析してレポートを作成します。
func AnalyzeTrades(trades []Trade) (Report, error) {
	if len(trades) == 0 {
		return Report{}, fmt.Errorf("no trades to analyze")
	}

	var executedTrades []Trade
	cancelledCount := 0
	for _, t := range trades {
		if t.IsCancelled {
			cancelledCount++
		} else {
			executedTrades = append(executedTrades, t)
		}
	}

	if len(executedTrades) == 0 {
		return Report{
			StartDate:       trades[0].Time,
			EndDate:         trades[len(trades)-1].Time,
			CancelledTrades: cancelledCount,
			TotalTrades:     0,
		}, nil
	}

	var buys, sells []Trade
	var totalPnL decimal.Decimal
	var longWinningTrades, longLosingTrades int
	var shortWinningTrades, shortLosingTrades int
	var totalProfit, totalLoss decimal.Decimal

	startDate := executedTrades[0].Time
	endDate := executedTrades[len(executedTrades)-1].Time

	for _, trade := range executedTrades {
		if trade.Side == "buy" {
			for len(sells) > 0 && trade.Size.IsPositive() {
				sell := sells[0]
				matchSize := decimal.Min(sell.Size, trade.Size)
				pnl := sell.Price.Sub(trade.Price).Mul(matchSize)
				totalPnL = totalPnL.Add(pnl)
				if pnl.IsPositive() {
					shortWinningTrades++
					totalProfit = totalProfit.Add(pnl)
				} else {
					shortLosingTrades++
					totalLoss = totalLoss.Add(pnl.Abs())
				}
				sell.Size = sell.Size.Sub(matchSize)
				trade.Size = trade.Size.Sub(matchSize)
				if sell.Size.IsZero() {
					sells = sells[1:]
				} else {
					sells[0] = sell
				}
			}
			if trade.Size.IsPositive() {
				buys = append(buys, trade)
			}
		} else if trade.Side == "sell" {
			for len(buys) > 0 && trade.Size.IsPositive() {
				buy := buys[0]
				matchSize := decimal.Min(buy.Size, trade.Size)
				pnl := trade.Price.Sub(buy.Price).Mul(matchSize)
				totalPnL = totalPnL.Add(pnl)
				if pnl.IsPositive() {
					longWinningTrades++
					totalProfit = totalProfit.Add(pnl)
				} else {
					longLosingTrades++
					totalLoss = totalLoss.Add(pnl.Abs())
				}
				buy.Size = buy.Size.Sub(matchSize)
				trade.Size = trade.Size.Sub(matchSize)
				if buy.Size.IsZero() {
					buys = buys[1:]
				} else {
					buys[0] = buy
				}
			}
			if trade.Size.IsPositive() {
				sells = append(sells, trade)
			}
		}
	}

	winningTrades := longWinningTrades + shortWinningTrades
	losingTrades := longLosingTrades + shortLosingTrades
	totalExecutedTrades := winningTrades + losingTrades

	cancellationRate := 0.0
	if cancelledCount+totalExecutedTrades > 0 {
		cancellationRate = float64(cancelledCount) / float64(cancelledCount+totalExecutedTrades) * 100
	}

	winRate := 0.0
	if totalExecutedTrades > 0 {
		winRate = float64(winningTrades) / float64(totalExecutedTrades) * 100
	}

	longWinRate := 0.0
	if longWinningTrades+longLosingTrades > 0 {
		longWinRate = float64(longWinningTrades) / float64(longWinningTrades+longLosingTrades) * 100
	}

	shortWinRate := 0.0
	if shortWinningTrades+shortLosingTrades > 0 {
		shortWinRate = float64(shortWinningTrades) / float64(shortWinningTrades+shortLosingTrades) * 100
	}

	avgProfit := decimal.Zero
	if winningTrades > 0 {
		avgProfit = totalProfit.Div(decimal.NewFromInt(int64(winningTrades)))
	}

	avgLoss := decimal.Zero
	if losingTrades > 0 {
		avgLoss = totalLoss.Div(decimal.NewFromInt(int64(losingTrades)))
	}

	riskRewardRatio := 0.0
	if !avgLoss.IsZero() {
		riskRewardRatio = avgProfit.Div(avgLoss).InexactFloat64()
	}

	return Report{
		StartDate:          startDate,
		EndDate:            endDate,
		TotalTrades:        totalExecutedTrades,
		CancelledTrades:    cancelledCount,
		CancellationRate:   cancellationRate,
		WinningTrades:      winningTrades,
		LosingTrades:       losingTrades,
		WinRate:            winRate,
		LongWinningTrades:  longWinningTrades,
		LongLosingTrades:   longLosingTrades,
		LongWinRate:        longWinRate,
		ShortWinningTrades: shortWinningTrades,
		ShortLosingTrades:  shortLosingTrades,
		ShortWinRate:       shortWinRate,
		TotalPnL:           totalPnL,
		AverageProfit:      avgProfit,
		AverageLoss:        avgLoss,
		RiskRewardRatio:    riskRewardRatio,
	}, nil
}

// SavePnlReport は分析レポートをデータベースに保存します。
func (r *Repository) SavePnlReport(ctx context.Context, report Report) error {
	query := `
        INSERT INTO pnl_reports (
            time, start_date, end_date, total_trades, cancelled_trades,
            cancellation_rate, winning_trades, losing_trades, win_rate,
            long_winning_trades, long_losing_trades, long_win_rate,
            short_winning_trades, short_losing_trades, short_win_rate,
            total_pnl, average_profit, average_loss, risk_reward_ratio
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19
        );
    `
	_, err := r.db.Exec(ctx, query,
		time.Now(), report.StartDate, report.EndDate, report.TotalTrades, report.CancelledTrades,
		report.CancellationRate, report.WinningTrades, report.LosingTrades, report.WinRate,
		report.LongWinningTrades, report.LongLosingTrades, report.LongWinRate,
		report.ShortWinningTrades, report.ShortLosingTrades, report.ShortWinRate,
		report.TotalPnL, report.AverageProfit, report.AverageLoss, report.RiskRewardRatio,
	)
	return err
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
