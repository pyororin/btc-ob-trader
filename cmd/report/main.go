package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/shopspring/decimal"
)

// SimulationSummary はシミュレーション結果のサマリーです。
type SimulationSummary struct {
	TotalProfit         float64 `json:"TotalProfit"`
	TotalTrades         int     `json:"TotalTrades"`
	WinningTrades       int     `json:"WinningTrades"`
	LosingTrades        int     `json:"LosingTrades"`
	WinRate             float64 `json:"WinRate"`
	LongWinningTrades   int     `json:"LongWinningTrades"`
	LongLosingTrades    int     `json:"LongLosingTrades"`
	LongWinRate         float64 `json:"LongWinRate"`
	ShortWinningTrades  int     `json:"ShortWinningTrades"`
	ShortLosingTrades   int     `json:"ShortLosingTrades"`
	ShortWinRate        float64 `json:"ShortWinRate"`
	AverageWin          float64 `json:"AverageWin"`
	AverageLoss         float64 `json:"AverageLoss"`
	RiskRewardRatio     float64 `json:"RiskRewardRatio"`
	ProfitFactor        float64 `json:"ProfitFactor"`
	MaxDrawdown         float64 `json:"MaxDrawdown"`
	RecoveryFactor      float64 `json:"RecoveryFactor"`
	SharpeRatio         float64 `json:"SharpeRatio"`
	SortinoRatio        float64 `json:"SortinoRatio"`
	CalmarRatio         float64 `json:"CalmarRatio"`
	MaxConsecutiveWins  int     `json:"MaxConsecutiveWins"`
	MaxConsecutiveLosses int    `json:"MaxConsecutiveLosses"`
	AverageHoldingPeriodSeconds float64 `json:"average_holding_period_seconds"`
	AverageWinningHoldingPeriodSeconds float64 `json:"average_winning_holding_period_seconds"`
	AverageLosingHoldingPeriodSeconds  float64 `json:"average_losing_holding_period_seconds"`
	BuyAndHoldReturn    float64 `json:"BuyAndHoldReturn"`
	ReturnVsBuyAndHold  float64 `json:"ReturnVsBuyAndHold"`
}

func main() {
	// 標準入力からJSONデータを読み込む
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading from stdin: %v\n", err)
		os.Exit(1)
	}

	// JSONデータをパース
	var summary SimulationSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		fmt.Fprintf(os.Stderr, "Error unmarshaling json: %v\n", err)
		os.Exit(1)
	}

	// レポートをコンソールに出力
	printReport(summary)

	// JSTの現在時刻を取得
	jst := time.FixedZone("Asia/Tokyo", 9*60*60)
	now := time.Now().In(jst)
	filename := fmt.Sprintf("report_%s.json", now.Format("20060102_150405"))
	filepath := "./report/" + filename

	// レポートをJSONファイルに出力
	if err := writeReportToJSON(summary, filepath); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing JSON report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Report successfully written to %s\n", filepath)
}

// printReport は分析レポートをコンソールに出力します。
func printReport(s SimulationSummary) {
	fmt.Println("--- 損益分析レポート ---")
	fmt.Println()
	fmt.Println("--- 全体 ---")
	fmt.Printf("約定済み取引数: %d\n", s.TotalTrades)
	fmt.Println()
	fmt.Println("--- 約定済み取引の分析 ---")
	if s.TotalTrades > 0 {
		fmt.Printf("勝ちトレード数: %d\n", s.WinningTrades)
		fmt.Printf("負けトレード数: %d\n", s.LosingTrades)
		fmt.Printf("勝率: %.2f%%\n", s.WinRate)
		fmt.Printf("合計損益: %s\n", decimal.NewFromFloat(s.TotalProfit).StringFixed(2))
		fmt.Printf("平均利益: %s\n", decimal.NewFromFloat(s.AverageWin).StringFixed(2))
		fmt.Printf("平均損失: %s\n", decimal.NewFromFloat(s.AverageLoss).StringFixed(2))
		fmt.Printf("リスクリワードレシオ: %.2f\n", s.RiskRewardRatio)
		fmt.Println()
		fmt.Println("--- ロング戦略 ---")
		fmt.Printf("勝ちトレード数: %d\n", s.LongWinningTrades)
		fmt.Printf("負けトレード数: %d\n", s.LongLosingTrades)
		fmt.Printf("勝率: %.2f%%\n", s.LongWinRate)
		fmt.Println()
		fmt.Println("--- ショート戦略 ---")
		fmt.Printf("勝ちトレード数: %d\n", s.ShortWinningTrades)
		fmt.Printf("負けトレード数: %d\n", s.ShortLosingTrades)
		fmt.Printf("勝率: %.2f%%\n", s.ShortWinRate)
	} else {
		fmt.Println("約定済みの取引はありません。")
	}
	fmt.Println()
	fmt.Println("--- パフォーマンス指標 ---")
	if s.TotalTrades > 0 {
		fmt.Printf("プロフィットファクター: %.2f\n", s.ProfitFactor)
		fmt.Printf("シャープレシオ: %.2f\n", s.SharpeRatio)
		fmt.Printf("ソルティノレシオ: %.2f\n", s.SortinoRatio)
		fmt.Printf("カルマーレシオ: %.2f\n", s.CalmarRatio)
		fmt.Printf("最大ドローダウン: %s\n", decimal.NewFromFloat(s.MaxDrawdown).StringFixed(2))
		fmt.Printf("リカバリーファクター: %.2f\n", s.RecoveryFactor)
		fmt.Printf("平均保有期間: %.2f 秒\n", s.AverageHoldingPeriodSeconds)
		fmt.Printf("勝ちトレードの平均保有期間: %.2f 秒\n", s.AverageWinningHoldingPeriodSeconds)
		fmt.Printf("負けトレードの平均保有期間: %.2f 秒\n", s.AverageLosingHoldingPeriodSeconds)
		fmt.Printf("最大連勝数: %d\n", s.MaxConsecutiveWins)
		fmt.Printf("最大連敗数: %d\n", s.MaxConsecutiveLosses)
		fmt.Printf("バイ・アンド・ホールドリターン: %s\n", decimal.NewFromFloat(s.BuyAndHoldReturn).StringFixed(2))
		fmt.Printf("リターン vs バイ・アンド・ホールド: %s\n", decimal.NewFromFloat(s.ReturnVsBuyAndHold).StringFixed(2))
	}
	fmt.Println()
	fmt.Println("----------------------")
}

// writeReportToJSON はレポートをJSONファイルに書き込みます。
func writeReportToJSON(s SimulationSummary, filepath string) error {
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
	return encoder.Encode(s)
}
