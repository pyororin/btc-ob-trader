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

	// Case 1: No existing position. The trade opens a new one.
	if p.Size == 0 {
		p.Size = tradeSize
		p.AvgEntryPrice = tradePrice
		return 0.0
	}

	// Case 2: Trade is in the same direction as the existing position (increasing the position).
	isSameDirection := (p.Size > 0 && tradeSize > 0) || (p.Size < 0 && tradeSize < 0)
	if isSameDirection {
		currentValue := p.Size * p.AvgEntryPrice
		tradeValue := tradeSize * tradePrice
		newSize := p.Size + tradeSize
		p.AvgEntryPrice = (currentValue + tradeValue) / newSize
		p.Size = newSize
		return 0.0
	}

	// Case 3: Trade is in the opposite direction (reducing, closing, or flipping the position).
	tradeAbs := tradeSize
	if tradeAbs < 0 {
		tradeAbs = -tradeAbs
	}
	positionAbs := p.Size
	if positionAbs < 0 {
		positionAbs = -positionAbs
	}

	// Subcase 3a: Trade reduces the position but does not close it.
	if tradeAbs < positionAbs {
		closedSize := tradeAbs
		pnlPerUnit := tradePrice - p.AvgEntryPrice
		if p.Size < 0 { // Short position
			pnlPerUnit = -pnlPerUnit
		}
		realizedPnL = pnlPerUnit * closedSize
		p.Size += tradeSize // AvgEntryPrice remains the same
		return realizedPnL
	}

	// Subcase 3b: Trade closes the position exactly.
	// Subcase 3c: Trade closes the position and opens a new one in the opposite direction (flip).
	closedSize := positionAbs
	pnlPerUnit := tradePrice - p.AvgEntryPrice
	if p.Size < 0 { // Short position
		pnlPerUnit = -pnlPerUnit
	}
	realizedPnL = pnlPerUnit * closedSize

	// Update position state
	p.Size += tradeSize

	if p.Size == 0 {
		// Position is now flat
		p.AvgEntryPrice = 0
	} else {
		// Position has been flipped
		p.AvgEntryPrice = tradePrice
	}

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
