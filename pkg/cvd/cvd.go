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
	trades          *RingBuffer
	windowSize      time.Duration
	currentCVD      float64
	processedTrades map[string]time.Time // Store trade ID and its timestamp
}

// NewCVDCalculator creates a new CVDCalculator.
func NewCVDCalculator(windowSize time.Duration) *CVDCalculator {
	estimatedTrades := int(10 * windowSize.Seconds() * 1.2)
	if estimatedTrades < 100 {
		estimatedTrades = 100
	}
	return &CVDCalculator{
		windowSize:      windowSize,
		trades:          NewRingBuffer(estimatedTrades),
		processedTrades: make(map[string]time.Time),
	}
}

// Update adds new trades and recalculates the CVD.
func (c *CVDCalculator) Update(newTrades []Trade, currentTime time.Time) float64 {
	// Add new trades, avoiding duplicates
	for _, trade := range newTrades {
		if _, exists := c.processedTrades[trade.ID]; !exists {
			c.trades.Add(trade)
			c.processedTrades[trade.ID] = trade.Timestamp
		}
	}

	// Recalculate CVD from trades within the window
	c.currentCVD = 0
	windowStart := currentTime.Add(-c.windowSize)

	validTrades := make([]Trade, 0, c.trades.Size())
	c.trades.Do(func(t interface{}) {
		if trade, ok := t.(Trade); ok {
			if !trade.Timestamp.Before(windowStart) {
				validTrades = append(validTrades, trade)
				side := strings.ToLower(trade.Side)
				if side == "buy" {
					c.currentCVD += trade.Size
				} else if side == "sell" {
					c.currentCVD -= trade.Size
				}
			}
		}
	})

	// Rebuild the ring buffer with only the valid trades
	newRingBuffer := NewRingBuffer(c.trades.Capacity())
	for _, trade := range validTrades {
		newRingBuffer.Add(trade)
	}
	c.trades = newRingBuffer

	// Cleanup old trade IDs from the processedTrades map
	// We keep IDs for 10x the window size to prevent reprocessing of old, duplicate events
	cleanupCutoff := currentTime.Add(-c.windowSize * 10)
	if len(c.processedTrades) > c.trades.Capacity()*2 { // Trigger cleanup only if map is getting large
		for id, ts := range c.processedTrades {
			if ts.Before(cleanupCutoff) {
				delete(c.processedTrades, id)
			}
		}
	}

	return c.currentCVD
}

// GetCVD returns the current CVD value.
func (c *CVDCalculator) GetCVD() float64 {
	return c.currentCVD
}
