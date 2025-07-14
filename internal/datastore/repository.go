package datastore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
)

// MarketEvent is an interface for market events (trades, order book updates).
type MarketEvent interface {
	GetTime() time.Time
}

// TradeEvent represents a single trade event from the database.
type TradeEvent struct {
	Trade coincheck.TradeData
	Time  time.Time
}

func (e TradeEvent) GetTime() time.Time { return e.Time }

// OrderBookEvent represents an order book snapshot/update from the database.
type OrderBookEvent struct {
	OrderBook coincheck.OrderBookData
	Time      time.Time
}

func (e OrderBookEvent) GetTime() time.Time { return e.Time }

// Repository handles database operations for fetching backtest data.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// FetchMarketEvents fetches trades and order book updates from the database
// within the given time range and returns them as a sorted slice of MarketEvent.
func (r *Repository) FetchMarketEvents(ctx context.Context, pair string, startTime, endTime time.Time) ([]MarketEvent, error) {
	trades, err := r.fetchTrades(ctx, pair, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch trades: %w", err)
	}

	orderBooks, err := r.fetchOrderBookUpdates(ctx, pair, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch order book updates: %w", err)
	}

	// Combine and sort events
	events := make([]MarketEvent, 0, len(trades)+len(orderBooks))
	for _, t := range trades {
		events = append(events, t)
	}
	for _, ob := range orderBooks {
		events = append(events, ob)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].GetTime().Before(events[j].GetTime())
	})

	return events, nil
}

// Trade はデータベースからの取引を表します。
type Trade struct {
	Time            time.Time       `json:"time"`
	ReplaySessionID *string         `json:"replay_session_id"`
	Pair            string          `json:"pair"`
	Side            string          `json:"side"`
	Price           decimal.Decimal `json:"price"`
	Size            decimal.Decimal `json:"size"`
	TransactionID   int64           `json:"transaction_id"`
	IsCancelled     bool            `json:"is_cancelled"`
}

// Report は損益分析の結果を保持します。
type Report struct {
	ReplaySessionID    *string         `json:"replay_session_id"`
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
        SELECT time, replay_session_id, pair, side, price, size, transaction_id, is_cancelled
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
		if err := rows.Scan(&t.Time, &t.ReplaySessionID, &t.Pair, &t.Side, &t.Price, &t.Size, &t.TransactionID, &t.IsCancelled); err != nil {
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
	var replaySessionID *string
	cancelledCount := 0
	for _, t := range trades {
		if t.IsCancelled {
			cancelledCount++
		} else {
			executedTrades = append(executedTrades, t)
		}
		if t.ReplaySessionID != nil {
			replaySessionID = t.ReplaySessionID
		}
	}

	if len(executedTrades) == 0 {
		return Report{
			ReplaySessionID: replaySessionID,
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
		ReplaySessionID:    replaySessionID,
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
            time, replay_session_id, start_date, end_date, total_trades, cancelled_trades,
            cancellation_rate, winning_trades, losing_trades, win_rate,
            long_winning_trades, long_losing_trades, long_win_rate,
            short_winning_trades, short_losing_trades, short_win_rate,
            total_pnl, average_profit, average_loss, risk_reward_ratio
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
        );
    `
	_, err := r.db.Exec(ctx, query,
		time.Now(), report.ReplaySessionID, report.StartDate, report.EndDate, report.TotalTrades, report.CancelledTrades,
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

func (r *Repository) fetchTrades(ctx context.Context, pair string, startTime, endTime time.Time) ([]TradeEvent, error) {
	query := `
        SELECT transaction_id, pair, price, size, side, time
        FROM trades
        WHERE pair = $1 AND time >= $2 AND time < $3
        ORDER BY time ASC;
    `
	rows, err := r.db.Query(ctx, query, pair, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trades []TradeEvent
	for rows.Next() {
		var t struct {
			TransactionID int64
			Pair          string
			Price         float64
			Size          float64
			Side          string
			Time          time.Time
		}
		if err := rows.Scan(&t.TransactionID, &t.Pair, &t.Price, &t.Size, &t.Side, &t.Time); err != nil {
			return nil, err
		}
		trades = append(trades, TradeEvent{
			Trade: coincheck.TradeData{
				ID:        fmt.Sprintf("%d", t.TransactionID),
				PairStr:   t.Pair,
				RateStr:   fmt.Sprintf("%f", t.Price),
				AmountStr: fmt.Sprintf("%f", t.Size),
				SideStr:   t.Side,
			},
			Time: t.Time,
		})
	}
	return trades, rows.Err()
}

func (r *Repository) fetchOrderBookUpdates(ctx context.Context, pair string, startTime, endTime time.Time) ([]OrderBookEvent, error) {
	// This is a simplified implementation. A real implementation would need to
	// reconstruct the order book state at each point in time.
	// For now, we fetch snapshots and treat them as individual events.
	query := `
        SELECT time, side, price, size
        FROM order_book_updates
        WHERE pair = $1 AND time >= $2 AND time < $3
        ORDER BY time ASC;
    `
	// This query is likely insufficient for a proper backtest.
	// A full backtest would require reconstructing the book from a snapshot and subsequent diffs.
	// For this iteration, we are assuming simplified logic where each "update" can be treated as a state.
	rows, err := r.db.Query(ctx, query, pair, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Group updates by timestamp
	updatesByTime := make(map[time.Time][][]string)
	for rows.Next() {
		var t time.Time
		var side string
		var price, size float64
		if err := rows.Scan(&t, &side, &price, &size); err != nil {
			return nil, err
		}
		rateStr := fmt.Sprintf("%f", price)
		amountStr := fmt.Sprintf("%f", size)
		updatesByTime[t] = append(updatesByTime[t], []string{rateStr, amountStr, side})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var events []OrderBookEvent
	for t, updates := range updatesByTime {
		var bids, asks [][]string
		for _, u := range updates {
			if u[2] == "bid" {
				bids = append(bids, u[:2])
			} else {
				asks = append(asks, u[:2])
			}
		}
		events = append(events, OrderBookEvent{
			OrderBook: coincheck.OrderBookData{
				PairStr: pair,
				Bids:    bids,
				Asks:    asks,
			},
			Time: t,
		})
	}

	return events, nil
}
