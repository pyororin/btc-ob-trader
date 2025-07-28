// Package indicator provides types and functions for calculating various market indicators.
package indicator

import (
	"context"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
	"sync"
	"time"
)

// OBILevels specifies which levels of OBI to calculate.
var OBILevels = []int{8, 16}

// OBICalculator is responsible for periodically calculating OBI from an OrderBook.
type OBICalculator struct {
	orderBook *OrderBook
	interval  time.Duration
	ticker    *time.Ticker
	done      chan struct{}
	output    chan OBIResult
	mu        sync.RWMutex
	started   bool
}

// NewOBICalculator creates a new OBI calculator.
func NewOBICalculator(ob *OrderBook, interval time.Duration) *OBICalculator {
	return &OBICalculator{
		orderBook: ob,
		interval:  interval,
		done:      make(chan struct{}),
		output:    make(chan OBIResult, 100), // Buffered channel to avoid blocking
	}
}

// Calculate performs a one-time calculation of OBI with a given timestamp.
func (c *OBICalculator) Calculate(timestamp time.Time) {
	c.orderBook.RLock()
	isBookReady := !c.orderBook.Time.IsZero()
	c.orderBook.RUnlock()

	logger.Debugf("OBICalculator.Calculate called. isBookReady: %v", isBookReady)

	if isBookReady {
		obiResult, ok := c.orderBook.CalculateOBI(OBILevels...)
		if !ok {
			logger.Debug("c.orderBook.CalculateOBI returned ok=false, creating a partial result.")
			// Even if OBI calculation fails (e.g., empty book side), we should still send a heartbeat
			// with the best bid/ask we have, so the signal engine can use it for other indicators.
			obiResult.BestBid = c.orderBook.BestBid()
			obiResult.BestAsk = c.orderBook.BestAsk()
		}

		logger.Debugf("OBI calculated. OBI8: %.4f, ok: %v", obiResult.OBI8, ok)
		// Override the timestamp with the one provided, crucial for simulations
		obiResult.Timestamp = timestamp
		select {
		case c.output <- obiResult:
		default:
			// Channel is full, indicating that the consumer is not keeping up.
			// In a simulation, this might be fine, but in live trading, it could be an issue.
			logger.Warn("OBICalculator output channel is full, skipping send.")
		}
	}
}

// Start begins the periodic calculation of OBI for live trading.
func (c *OBICalculator) Start(ctx context.Context) {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.ticker = time.NewTicker(c.interval)
	c.done = make(chan struct{})
	c.mu.Unlock()

	go func() {
		defer func() {
			c.mu.Lock()
			c.ticker.Stop()
			c.started = false
			c.mu.Unlock()
		}()

		for {
			select {
			case <-ctx.Done():
				close(c.done)
				return
			case t := <-c.ticker.C:
				// For live trading, use the ticker's time.
				c.Calculate(t.UTC())
			case <-c.done:
				return
			}
		}
	}()
}

// Stop ceases the OBI calculation.
func (c *OBICalculator) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		close(c.done)
	}
}

// Subscribe returns a channel to receive OBI results.
func (c *OBICalculator) Subscribe() <-chan OBIResult {
	return c.output
}

// OrderBook returns the underlying OrderBook instance.
func (c *OBICalculator) OrderBook() *OrderBook {
	return c.orderBook
}
