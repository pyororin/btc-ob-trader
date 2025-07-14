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

// Update updates the position based on a trade.
func (p *Position) Update(tradeSize float64, tradePrice float64) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// current total value of the position
	currentValue := p.Size * p.AvgEntryPrice
	// value of the new trade
	tradeValue := tradeSize * tradePrice

	// new total size of the position
	newSize := p.Size + tradeSize

	if newSize == 0 {
		p.AvgEntryPrice = 0
	} else {
		// new average entry price
		p.AvgEntryPrice = (currentValue + tradeValue) / newSize
	}

	p.Size = newSize
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
