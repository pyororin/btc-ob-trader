package position

import (
	"fmt"
	"sync"
)

// Position holds the state of a trading position.
type Position struct {
	Size           float64
	AvgEntryPrice  float64
	mutex          sync.RWMutex
}

// NewPosition creates a new Position instance.
func NewPosition() *Position {
	return &Position{}
}

// Update updates the position based on a trade and returns the realized PnL.
func (p *Position) Update(tradeSize float64, tradePrice float64) (realizedPnL float64) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// If there is no existing position, the trade simply opens a new one.
	if p.Size == 0 {
		p.Size = tradeSize
		p.AvgEntryPrice = tradePrice
		return 0.0
	}

	// If the trade is in the same direction, it adds to the position.
	if (p.Size > 0 && tradeSize > 0) || (p.Size < 0 && tradeSize < 0) {
		currentValue := p.Size * p.AvgEntryPrice
		tradeValue := tradeSize * tradePrice
		newSize := p.Size + tradeSize
		p.AvgEntryPrice = (currentValue + tradeValue) / newSize
		p.Size = newSize
		return 0.0
	}

	// If the trade is in the opposite direction, it may close or reduce the position.
	closedSize := 0.0
	if tradeSize > 0 { // Closing a short position
		closedSize = min(tradeSize, -p.Size)
	} else { // Closing a long position
		closedSize = min(-tradeSize, p.Size)
	}

	realizedPnL = (tradePrice - p.AvgEntryPrice) * closedSize
	if p.Size < 0 { // If it was a short position, PnL is inverted
		realizedPnL = -realizedPnL
	}

	newSize := p.Size + tradeSize
	if newSize == 0 {
		p.AvgEntryPrice = 0
	}
	// If the position is not fully closed, AvgEntryPrice remains the same.
	p.Size = newSize

	return realizedPnL
}

// min returns the smaller of two floats.
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Get returns the current size and average entry price of the position.
func (p *Position) Get() (float64, float64) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.Size, p.AvgEntryPrice
}

// String returns a string representation of the position.
func (p *Position) String() string {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return fmt.Sprintf("Position{Size: %.4f, AvgEntryPrice: %.2f}", p.Size, p.AvgEntryPrice)
}
