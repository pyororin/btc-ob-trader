package indicator

import (
	"sync"
	"time"

	"github.com/your-org/obi-scalp-bot/pkg/cvd"
)

// TradeHandler processes trades and calculates CVD periodically.
type TradeHandler struct {
	mu         sync.RWMutex
	ringBuffer *cvd.RingBuffer
	currentCVD float64
	windowSize time.Duration // e.g., 500 * time.Millisecond
}

// NewTradeHandler creates a new TradeHandler.
// bufferCap defines the capacity of the internal ring buffer for trades.
// windowSize defines the time window for CVD calculation (e.g., 500ms).
// Trades older than this window will be considered for CVD calculation
// if they are still within the ring buffer's capacity.
// The ring buffer should ideally be sized to hold more trades than typically
// occur in windowSize to ensure data availability.
func NewTradeHandler(bufferCap int, windowSize time.Duration) *TradeHandler {
	return &TradeHandler{
		ringBuffer: cvd.NewRingBuffer(bufferCap),
		windowSize: windowSize,
	}
}

// AddTrade adds a new trade to the handler.
// This method is safe for concurrent use.
func (th *TradeHandler) AddTrade(trade cvd.Trade) {
	th.mu.Lock()
	defer th.mu.Unlock()
	th.ringBuffer.Add(trade)
}

// UpdateCVD calculates the CVD based on trades within the defined window.
// This method should be called periodically (e.g., every 500ms) by an external mechanism.
func (th *TradeHandler) UpdateCVD() {
	th.mu.Lock() // Lock for both reading ringBuffer and writing currentCVD
	defer th.mu.Unlock()

	allTrades := th.ringBuffer.GetChronologicalTrades()
	now := time.Now()
	windowStartTime := now.Add(-th.windowSize)

	relevantTrades := make([]cvd.Trade, 0)
	for _, t := range allTrades {
		if !t.Timestamp.Before(windowStartTime) {
			relevantTrades = append(relevantTrades, t)
		}
	}
	th.currentCVD = cvd.CalculateCVD(relevantTrades)
}

// GetCVD returns the most recently calculated CVD.
// This method is safe for concurrent use.
func (th *TradeHandler) GetCVD() float64 {
	th.mu.RLock()
	defer th.mu.RUnlock()
	return th.currentCVD
}

// GetRingBuffer exposes the ring buffer primarily for testing or advanced scenarios.
// Direct manipulation of the returned buffer from outside is not recommended
// as it might bypass the TradeHandler's synchronization.
func (th *TradeHandler) GetRingBuffer() *cvd.RingBuffer {
	// This is primarily for testing or specific scenarios.
	// Consider if read-only access or a copy is more appropriate.
	return th.ringBuffer
}
