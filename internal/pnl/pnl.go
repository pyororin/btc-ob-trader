package pnl

import (
	"sync"
)

// Calculator handles PnL calculations.
type Calculator struct {
	RealizedPnL   float64
	mutex         sync.RWMutex
}

// NewCalculator creates a new PnL Calculator.
func NewCalculator() *Calculator {
	return &Calculator{}
}

// UpdateRealizedPnL updates the realized PnL.
func (c *Calculator) UpdateRealizedPnL(pnl float64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.RealizedPnL += pnl
}

// CalculateUnrealizedPnL calculates the unrealized PnL.
func (c *Calculator) CalculateUnrealizedPnL(positionSize float64, avgEntryPrice float64, currentPrice float64) float64 {
	if positionSize == 0 {
		return 0
	}
	return (currentPrice - avgEntryPrice) * positionSize
}

// GetRealizedPnL returns the current realized PnL.
func (c *Calculator) GetRealizedPnL() float64 {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.RealizedPnL
}
