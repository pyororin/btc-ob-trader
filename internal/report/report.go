package report

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

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

// Report は損益分析の結果を保持します。
type Report struct {
	StartDate                        time.Time       `json:"start_date"`
	EndDate                          time.Time       `json:"end_date"`
	TotalTrades                      int             `json:"total_trades"`        // Executed trades
	CancelledTrades                  int             `json:"cancelled_trades"`    // Cancelled trades
	CancellationRate                 float64         `json:"cancellation_rate"`   // Cancellation rate
	WinningTrades                    int             `json:"winning_trades"`
	LosingTrades                     int             `json:"losing_trades"`
	WinRate                          float64         `json:"win_rate"`
	LongWinningTrades                int             `json:"long_winning_trades"`
	LongLosingTrades                 int             `json:"long_losing_trades"`
	LongWinRate                      float64         `json:"long_win_rate"`
	ShortWinningTrades               int             `json:"short_winning_trades"`
	ShortLosingTrades                int             `json:"short_losing_trades"`
	ShortWinRate                     float64         `json:"short_win_rate"`
	TotalPnL                         decimal.Decimal `json:"total_pnl"`
	AverageProfit                    decimal.Decimal `json:"average_profit"`
	AverageLoss                      decimal.Decimal `json:"average_loss"`
	RiskRewardRatio                  float64         `json:"risk_reward_ratio"`
	ProfitFactor                     float64         `json:"profit_factor"`
	MaxDrawdown                      decimal.Decimal `json:"max_drawdown"`
	RecoveryFactor                   float64         `json:"recovery_factor"`
	SharpeRatio                      float64         `json:"sharpe_ratio"`
	SortinoRatio                     float64         `json:"sortino_ratio"`
	CalmarRatio                      float64         `json:"calmar_ratio"`
	MaxConsecutiveWins               int             `json:"max_consecutive_wins"`
	MaxConsecutiveLosses             int             `json:"max_consecutive_losses"`
	AverageHoldingPeriodSeconds      float64         `json:"average_holding_period_seconds"`
	AverageWinningHoldingPeriodSeconds float64       `json:"average_winning_holding_period_seconds"`
	AverageLosingHoldingPeriodSeconds  float64       `json:"average_losing_holding_period_seconds"`
	BuyAndHoldReturn                 decimal.Decimal `json:"buy_and_hold_return"`
	ReturnVsBuyAndHold               decimal.Decimal `json:"return_vs_buy_and_hold"`
	LastTradeID                      int64           `json:"last_trade_id"`
}

// Service handles report generation.
type Service struct {
	db *pgxpool.Pool
}

// NewService creates a new report service.
func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

// AnalyzeTrades はトレードリストを分析してレポートを作成します。
func (s *Service) AnalyzeTrades(trades []Trade) (Report, error) {
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
		// If there are no executed trades, we shouldn't generate a report.
		// Return an error to prevent a "zero trade" report from being saved.
		return Report{}, fmt.Errorf("no executed trades to analyze, only %d cancelled trades found", cancelledCount)
	}

	var buys, sells []Trade
	var totalPnL decimal.Decimal
	var longWinningTrades, longLosingTrades int
	var shortWinningTrades, shortLosingTrades int
	var totalProfit, totalLoss decimal.Decimal
	var pnlHistory []decimal.Decimal
	var holdingPeriods, winningHoldingPeriods, losingHoldingPeriods []float64
	var consecutiveWins, consecutiveLosses, maxConsecutiveWins, maxConsecutiveLosses int

	startDate := executedTrades[0].Time
	endDate := executedTrades[len(executedTrades)-1].Time

	for _, trade := range executedTrades {
		if trade.Side == "buy" {
			for len(sells) > 0 && trade.Size.IsPositive() {
				sell := sells[0]
				matchSize := decimal.Min(sell.Size, trade.Size)
				pnl := sell.Price.Sub(trade.Price).Mul(matchSize)
				totalPnL = totalPnL.Add(pnl)
				pnlHistory = append(pnlHistory, pnl)
				holdingPeriod := trade.Time.Sub(sell.Time).Seconds()
				holdingPeriods = append(holdingPeriods, holdingPeriod)

				if pnl.IsPositive() {
					shortWinningTrades++
					totalProfit = totalProfit.Add(pnl)
					winningHoldingPeriods = append(winningHoldingPeriods, holdingPeriod)
					consecutiveWins++
					consecutiveLosses = 0
					if consecutiveWins > maxConsecutiveWins {
						maxConsecutiveWins = consecutiveWins
					}
				} else if pnl.IsNegative() {
					shortLosingTrades++
					totalLoss = totalLoss.Add(pnl)
					losingHoldingPeriods = append(losingHoldingPeriods, holdingPeriod)
					consecutiveLosses++
					consecutiveWins = 0
					if consecutiveLosses > maxConsecutiveLosses {
						maxConsecutiveLosses = consecutiveLosses
					}
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
				pnlHistory = append(pnlHistory, pnl)
				holdingPeriod := trade.Time.Sub(buy.Time).Seconds()
				holdingPeriods = append(holdingPeriods, holdingPeriod)

				if pnl.IsPositive() {
					longWinningTrades++
					totalProfit = totalProfit.Add(pnl)
					winningHoldingPeriods = append(winningHoldingPeriods, holdingPeriod)
					consecutiveWins++
					consecutiveLosses = 0
					if consecutiveWins > maxConsecutiveWins {
						maxConsecutiveWins = consecutiveWins
					}
				} else if pnl.IsNegative() {
					longLosingTrades++
					totalLoss = totalLoss.Add(pnl)
					losingHoldingPeriods = append(losingHoldingPeriods, holdingPeriod)
					consecutiveLosses++
					consecutiveWins = 0
					if consecutiveLosses > maxConsecutiveLosses {
						maxConsecutiveLosses = consecutiveLosses
					}
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
		riskRewardRatio = avgProfit.Div(avgLoss.Abs()).InexactFloat64()
	}

	profitFactor := 0.0
	if totalLoss.IsNegative() {
		profitFactor = totalProfit.Div(totalLoss.Abs()).InexactFloat64()
	}

	// パフォーマンス指標の計算
	equityCurve := make([]decimal.Decimal, len(pnlHistory)+1)
	equityCurve[0] = decimal.Zero
	for i, pnl := range pnlHistory {
		equityCurve[i+1] = equityCurve[i].Add(pnl)
	}

	maxDrawdown := decimal.Zero
	peak := decimal.Zero
	for _, equity := range equityCurve {
		if equity.GreaterThan(peak) {
			peak = equity
		}
		drawdown := peak.Sub(equity)
		if drawdown.GreaterThan(maxDrawdown) {
			maxDrawdown = drawdown
		}
	}

	recoveryFactor := 0.0
	if maxDrawdown.IsPositive() {
		recoveryFactor = totalPnL.Div(maxDrawdown).InexactFloat64()
	}

	// pnlHistory を []float64 に変換
	pnlFloats := make([]float64, len(pnlHistory))
	for i, pnl := range pnlHistory {
		pnlFloats[i] = pnl.InexactFloat64()
	}

	sharpeRatio := calculateSharpeRatio(pnlFloats, 0.0)
	sortinoRatio := calculateSortinoRatio(pnlFloats, 0.0)

	calmarRatio := 0.0
	if maxDrawdown.IsPositive() {
		totalPnLFloat := totalPnL.InexactFloat64()
		maxDrawdownFloat := maxDrawdown.InexactFloat64()
		// 年率リターンを計算 (単純化のため、期間全体のリターンを使用)
		// 正確な年率リターンを計算するには、取引期間が必要
		durationYears := endDate.Sub(startDate).Hours() / 24 / 365.25
		annualizedReturn := 0.0
		if durationYears > 0 {
			annualizedReturn = totalPnLFloat / durationYears
		} else if totalExecutedTrades > 0 {
			// 期間が1年未満の場合は、単純なリターンを使用
			annualizedReturn = totalPnLFloat
		}
		calmarRatio = annualizedReturn / maxDrawdownFloat
	}

	avgHoldingPeriod := 0.0
	if len(holdingPeriods) > 0 {
		sum := 0.0
		for _, h := range holdingPeriods {
			sum += h
		}
		avgHoldingPeriod = sum / float64(len(holdingPeriods))
	}

	avgWinningHoldingPeriod := 0.0
	if len(winningHoldingPeriods) > 0 {
		sum := 0.0
		for _, h := range winningHoldingPeriods {
			sum += h
		}
		avgWinningHoldingPeriod = sum / float64(len(winningHoldingPeriods))
	}

	avgLosingHoldingPeriod := 0.0
	if len(losingHoldingPeriods) > 0 {
		sum := 0.0
		for _, h := range losingHoldingPeriods {
			sum += h
		}
		avgLosingHoldingPeriod = sum / float64(len(losingHoldingPeriods))
	}

	buyAndHoldReturn := decimal.Zero
	if len(executedTrades) > 1 {
		initialPrice := executedTrades[0].Price
		finalPrice := executedTrades[len(executedTrades)-1].Price
		if initialPrice.IsPositive() {
			buyAndHoldReturn = finalPrice.Sub(initialPrice).Div(initialPrice)
		}
	}

	returnVsBuyAndHold := totalPnL.Sub(buyAndHoldReturn)

	lastTradeID := int64(0)
	if len(executedTrades) > 0 {
		lastTradeID = executedTrades[len(executedTrades)-1].TransactionID
	}

	return Report{
		StartDate:                        startDate,
		EndDate:                          endDate,
		TotalTrades:                      totalExecutedTrades,
		CancelledTrades:                  cancelledCount,
		CancellationRate:                 cancellationRate,
		WinningTrades:                    winningTrades,
		LosingTrades:                     losingTrades,
		WinRate:                          winRate,
		LongWinningTrades:                longWinningTrades,
		LongLosingTrades:                 longLosingTrades,
		LongWinRate:                      longWinRate,
		ShortWinningTrades:               shortWinningTrades,
		ShortLosingTrades:                shortLosingTrades,
		ShortWinRate:                     shortWinRate,
		TotalPnL:                         totalPnL,
		AverageProfit:                    avgProfit,
		AverageLoss:                      avgLoss,
		RiskRewardRatio:                  riskRewardRatio,
		ProfitFactor:                     profitFactor,
		MaxDrawdown:                      maxDrawdown,
		RecoveryFactor:                   recoveryFactor,
		SharpeRatio:                      sharpeRatio,
		SortinoRatio:                     sortinoRatio,
		CalmarRatio:                      calmarRatio,
		MaxConsecutiveWins:               maxConsecutiveWins,
		MaxConsecutiveLosses:             maxConsecutiveLosses,
		AverageHoldingPeriodSeconds:      avgHoldingPeriod,
		AverageWinningHoldingPeriodSeconds: avgWinningHoldingPeriod,
		AverageLosingHoldingPeriodSeconds:  avgLosingHoldingPeriod,
		BuyAndHoldReturn:                 buyAndHoldReturn,
		ReturnVsBuyAndHold:               returnVsBuyAndHold,
		LastTradeID:                      lastTradeID,
	}, nil
}

// SavePnlReport は分析レポートをデータベースに保存します。
func (s *Service) SavePnlReport(ctx context.Context, report Report) error {
	query := `
        INSERT INTO pnl_reports (
            time, start_date, end_date, total_trades, cancelled_trades,
            cancellation_rate, winning_trades, losing_trades, win_rate,
            long_winning_trades, long_losing_trades, long_win_rate,
            short_winning_trades, short_losing_trades, short_win_rate,
            total_pnl, average_profit, average_loss, risk_reward_ratio,
            profit_factor, max_drawdown, recovery_factor, sharpe_ratio,
            sortino_ratio, calmar_ratio, max_consecutive_wins, max_consecutive_losses,
            average_holding_period_seconds, average_winning_holding_period_seconds,
            average_losing_holding_period_seconds, buy_and_hold_return, return_vs_buy_and_hold,
            last_trade_id
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19,
            $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33
        );
    `
	_, err := s.db.Exec(ctx, query,
		time.Now(), report.StartDate, report.EndDate, report.TotalTrades, report.CancelledTrades,
		report.CancellationRate, report.WinningTrades, report.LosingTrades, report.WinRate,
		report.LongWinningTrades, report.LongLosingTrades, report.LongWinRate,
		report.ShortWinningTrades, report.ShortLosingTrades, report.ShortWinRate,
		report.TotalPnL, report.AverageProfit, report.AverageLoss, report.RiskRewardRatio,
		report.ProfitFactor, report.MaxDrawdown, report.RecoveryFactor, report.SharpeRatio,
		report.SortinoRatio, report.CalmarRatio, report.MaxConsecutiveWins, report.MaxConsecutiveLosses,
		report.AverageHoldingPeriodSeconds, report.AverageWinningHoldingPeriodSeconds,
		report.AverageLosingHoldingPeriodSeconds, report.BuyAndHoldReturn, report.ReturnVsBuyAndHold,
		report.LastTradeID,
	)
	return err
}

// calculateStandardDeviation はリターンの標準偏差を計算します。
func calculateStandardDeviation(returns []float64, mean float64) float64 {
	if len(returns) == 0 {
		return 0.0
	}
	variance := 0.0
	for _, r := range returns {
		variance += math.Pow(r-mean, 2)
	}
	return math.Sqrt(variance / float64(len(returns)))
}

// calculateDownsideDeviation は下方偏差を計算します。
func calculateDownsideDeviation(returns []float64, target float64) float64 {
	if len(returns) == 0 {
		return 0.0
	}
	downsideVariance := 0.0
	downsideCount := 0
	for _, r := range returns {
		if r < target {
			downsideVariance += math.Pow(r-target, 2)
			downsideCount++
		}
	}
	if downsideCount == 0 {
		return 0.0
	}
	return math.Sqrt(downsideVariance / float64(downsideCount))
}

// calculateSharpeRatio はシャープレシオを計算します。
func calculateSharpeRatio(returns []float64, riskFreeRate float64) float64 {
	if len(returns) == 0 {
		return 0.0
	}
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))

	stdDev := calculateStandardDeviation(returns, mean)
	if stdDev == 0 {
		return 0.0
	}
	return (mean - riskFreeRate) / stdDev
}

// calculateSortinoRatio はソルティノレシオを計算します。
func calculateSortinoRatio(returns []float64, riskFreeRate float64) float64 {
	if len(returns) == 0 {
		return 0.0
	}
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))

	downsideDev := calculateDownsideDeviation(returns, 0) // ターゲットリターンを0と仮定
	if downsideDev == 0 {
		return 0.0
	}
	return (mean - riskFreeRate) / downsideDev
}
