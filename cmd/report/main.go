package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

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

func main() {
	// 環境変数からデータベース接続情報を取得
	dbHost := os.Getenv("DB_HOST")
	dbPortStr := os.Getenv("DB_PORT")
	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	if dbHost == "" || dbPortStr == "" || dbUser == "" || dbPassword == "" || dbName == "" {
		fmt.Fprintln(os.Stderr, "Database environment variables are not set.")
		os.Exit(1)
	}

	dbPort, err := strconv.Atoi(dbPortStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid DB_PORT: %v\n", err)
		os.Exit(1)
	}

	// データベース接続
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		dbUser, dbPassword, dbHost, dbPort, dbName)
	dbpool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer dbpool.Close()

	// すべてのトレードを取得
	trades, err := fetchAllTrades(context.Background(), dbpool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching trades: %v\n", err)
		os.Exit(1)
	}

	if len(trades) == 0 {
		fmt.Println("No trades found.")
		return
	}

	// 損益分析
	report, err := analyzeTrades(trades)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error analyzing trades: %v\n", err)
		os.Exit(1)
	}

	// レポートをコンソールに出力
	printReport(report)

	// JSTの現在時刻を取得
	jst := time.FixedZone("Asia/Tokyo", 9*60*60)
	now := time.Now().In(jst)
	filename := fmt.Sprintf("report_%s.json", now.Format("20060102_150405"))
	filepath := "./report/" + filename

	// レポートをJSONファイルに出力
	if err := writeReportToJSON(report, filepath); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Report successfully written to %s\n", filepath)

	// レポートをデータベースに保存
	if err := saveReportToDB(context.Background(), dbpool, report); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving report to database: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Report successfully saved to database.")
}

// fetchAllTrades はデータベースからすべてのトレードを取得します。
func fetchAllTrades(ctx context.Context, dbpool *pgxpool.Pool) ([]Trade, error) {
	query := `
        SELECT time, replay_session_id, pair, side, price, size, transaction_id, is_cancelled
        FROM trades
        ORDER BY time ASC;
    `
	rows, err := dbpool.Query(ctx, query)
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

// analyzeTrades はトレードリストを分析してレポートを作成します。
func analyzeTrades(trades []Trade) (Report, error) {
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

// printReport は分析レポートをコンソールに出力します。
func printReport(r Report) {
	fmt.Println("--- 損益分析レポート ---")
	fmt.Printf("集計期間: %s から %s\n", r.StartDate.Format(time.RFC3339), r.EndDate.Format(time.RFC3339))
	fmt.Println()
	fmt.Println("--- 全体 ---")
	fmt.Printf("約定済み取引数: %d\n", r.TotalTrades)
	fmt.Printf("キャンセル済み取引数: %d\n", r.CancelledTrades)
	fmt.Printf("キャンセル率: %.2f%%\n", r.CancellationRate)
	fmt.Println()
	fmt.Println("--- 約定済み取引の分析 ---")
	if r.TotalTrades > 0 {
		fmt.Printf("勝ちトレード数: %d\n", r.WinningTrades)
		fmt.Printf("負けトレード数: %d\n", r.LosingTrades)
		fmt.Printf("勝率: %.2f%%\n", r.WinRate)
		fmt.Printf("合計損益: %s\n", r.TotalPnL.StringFixed(2))
		fmt.Printf("平均利益: %s\n", r.AverageProfit.StringFixed(2))
		fmt.Printf("平均損失: %s\n", r.AverageLoss.StringFixed(2))
		fmt.Printf("リスクリワードレシオ: %.2f\n", r.RiskRewardRatio)
		fmt.Println()
		fmt.Println("--- ロング戦略 ---")
		fmt.Printf("勝ちトレード数: %d\n", r.LongWinningTrades)
		fmt.Printf("負けトレード数: %d\n", r.LongLosingTrades)
		fmt.Printf("勝率: %.2f%%\n", r.LongWinRate)
		fmt.Println()
		fmt.Println("--- ショート戦略 ---")
		fmt.Printf("勝ちトレード数: %d\n", r.ShortWinningTrades)
		fmt.Printf("負けトレード数: %d\n", r.ShortLosingTrades)
		fmt.Printf("勝率: %.2f%%\n", r.ShortWinRate)
	} else {
		fmt.Println("約定済みの取引はありません。")
	}
	fmt.Println("----------------------")
}

// writeReportToJSON はレポートをJSONファイルに書き込みます。
func writeReportToJSON(r Report, filepath string) error {
	// ディレクトリが存在しない場合は作成
	dir := "./report"
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	file, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r)
}

// saveReportToDB は分析レポートをデータベースに保存します。
func saveReportToDB(ctx context.Context, dbpool *pgxpool.Pool, r Report) error {
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
	_, err := dbpool.Exec(ctx, query,
		time.Now(), r.ReplaySessionID, r.StartDate, r.EndDate, r.TotalTrades, r.CancelledTrades,
		r.CancellationRate, r.WinningTrades, r.LosingTrades, r.WinRate,
		r.LongWinningTrades, r.LongLosingTrades, r.LongWinRate,
		r.ShortWinningTrades, r.ShortLosingTrades, r.ShortWinRate,
		r.TotalPnL, r.AverageProfit, r.AverageLoss, r.RiskRewardRatio,
	)
	return err
}
