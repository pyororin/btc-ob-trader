// Package indicator provides types and functions for calculating various market indicators.
package indicator

import (
	"context"
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
		output:    make(chan OBIResult, 1), // Buffered channel to avoid blocking
	}
}

// Start begins the periodic calculation of OBI.
// It's safe to call Start multiple times; it will only start the calculator once.
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
			case <-c.ticker.C:
				// Ensure the book has been initialized before calculating
				c.orderBook.RLock()
				isBookReady := !c.orderBook.Time.IsZero()
				c.orderBook.RUnlock()

				if isBookReady {
					if obiResult, ok := c.orderBook.CalculateOBI(OBILevels...); ok {
						// Non-blocking send
						select {
						case c.output <- obiResult:
						default:
							// If the channel is full, the previous value will be overwritten.
							// This is often acceptable for real-time indicators where the latest value is most important.
						}
					}
				}
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
