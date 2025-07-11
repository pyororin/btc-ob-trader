package cvd

import "strings"

// CalculateCVD calculates the Cumulative Volume Delta from a slice of trades.
// Buy trades contribute positively to CVD, sell trades contribute negatively.
func CalculateCVD(trades []Trade) float64 {
	var totalCVD float64
	for _, trade := range trades {
		// Normalize side to lowercase for consistent comparison
		side := strings.ToLower(trade.Side)
		if side == "buy" {
			totalCVD += trade.Size
		} else if side == "sell" {
			totalCVD -= trade.Size
		}
		// Trades with other side strings (if any) are ignored.
	}
	return totalCVD
}
