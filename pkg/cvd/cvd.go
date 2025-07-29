package cvd

import (
	"strings"
	"time"
)

// Trade represents a single market trade.
type Trade struct {
	ID        string
	Side      string
	Price     float64
	Size      float64
	Timestamp time.Time
}

// CVDCalculator calculates Cumulative Volume Delta over a rolling window.
type CVDCalculator struct {
	trades      []Trade
	windowSize  time.Duration
	currentCVD  float64
	lastTradeID string
}

// NewCVDCalculator creates a new CVDCalculator.
func NewCVDCalculator(windowSize time.Duration) *CVDCalculator {
	return &CVDCalculator{
		windowSize: windowSize,
	}
}

// Update adds new trades and recalculates the CVD.
// It avoids double-counting by checking trade IDs.
func (c *CVDCalculator) Update(newTrades []Trade, currentTime time.Time) float64 {
	// Add new trades, avoiding duplicates
	for _, trade := range newTrades {
		isNew := true
		for _, existingTrade := range c.trades {
			if trade.ID == existingTrade.ID {
				isNew = false
				break
			}
		}
		if isNew {
			c.trades = append(c.trades, trade)
		}
	}

	// Remove old trades that are outside the window
	firstValidIndex := 0
	for i, trade := range c.trades {
		if currentTime.Sub(trade.Timestamp) > c.windowSize {
			firstValidIndex = i + 1
		} else {
			break
		}
	}
	c.trades = c.trades[firstValidIndex:]

	// Recalculate CVD from the trades within the window
	c.currentCVD = 0
	for _, trade := range c.trades {
		side := strings.ToLower(trade.Side)
		if side == "buy" {
			c.currentCVD += trade.Size
		} else if side == "sell" {
			c.currentCVD -= trade.Size
		}
	}

	return c.currentCVD
}

// GetCVD returns the current CVD value.
func (c *CVDCalculator) GetCVD() float64 {
	return c.currentCVD
}
