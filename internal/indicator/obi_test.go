package indicator

/*
import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockOrderBook is a mock implementation of the OrderBookProvider for testing.
type MockOrderBook struct {
	bids []OrderBookEntry
	asks []OrderBookEntry
	mu   sync.RWMutex
}

func (m *MockOrderBook) BestBid() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.bids) == 0 {
		return 0
	}
	return m.bids[0].Price
}

func (m *MockOrderBook) BestAsk() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.asks) == 0 {
		return 0
	}
	return m.asks[0].Price
}

func (m *MockOrderBook) Bids() []OrderBookEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return a copy to avoid race conditions
	bidsCopy := make([]OrderBookEntry, len(m.bids))
	copy(bidsCopy, m.bids)
	return bidsCopy
}

func (m *MockOrderBook) Asks() []OrderBookEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return a copy
	asksCopy := make([]OrderBookEntry, len(m.asks))
	copy(asksCopy, m.asks)
	return asksCopy
}

// SetBids is a helper for tests to update the mock's bids.
func (m *MockOrderBook) SetBids(bids []OrderBookEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bids = bids
}

// SetAsks is a helper for tests to update the mock's asks.
func (m *MockOrderBook) SetAsks(asks []OrderBookEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asks = asks
}

func TestOBICalculator(t *testing.T) {
	// Create a mock order book
	mockBook := &MockOrderBook{
		bids: []OrderBookEntry{{Price: 100, Size: 1.0}},
		asks: []OrderBookEntry{{Price: 101, Size: 2.0}},
	}

	// Create a buffered channel for OBI results
	resultsChan := make(chan OBIResult, 10)
	calculator, err := NewOBICalculator(mockBook, 1*time.Millisecond, resultsChan)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		calculator.Run(ctx)
	}()

	// Helper function to check for a specific OBI result
	assertOBIResult := func(expectedOBI float64, expectedBestBid, expectedBestAsk float64) {
		select {
		case result := <-resultsChan:
			assert.InDelta(t, expectedOBI, result.OBI, 1e-9, "OBI value mismatch")
			assert.Equal(t, expectedBestBid, result.BestBid, "BestBid mismatch")
			assert.Equal(t, expectedBestAsk, result.BestAsk, "BestAsk mismatch")
			// For simplicity, we're not checking the timestamp strictly here,
			// as it can be tricky with timing. We just check it's not zero.
			assert.False(t, result.Timestamp.IsZero(), "Timestamp should not be zero")
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for the first OBI result")
		}
	}

	// Initial OBI calculation
	assertOBIResult(0.333333333, 100, 101)

	// Update the order book to trigger a new calculation
	mockBook.SetBids([]OrderBookEntry{{Price: 100, Size: 3.0}}) // Bids stronger
	mockBook.SetAsks([]OrderBookEntry{{Price: 101, Size: 1.0}})
	calculator.Update() // Manually trigger update

	// Check the new OBI
	assertOBIResult(0.75, 100, 101)

	// Update again
	mockBook.SetBids([]OrderBookEntry{{Price: 100, Size: 1.0}})
	mockBook.SetAsks([]OrderBookEntry{{Price: 101, Size: 3.0}}) // Asks stronger
	calculator.Update()

	// Check the new OBI
	assertOBIResult(0.25, 100, 101)

	// Test edge case: no bids
	mockBook.SetBids([]OrderBookEntry{})
	calculator.Update()
	assertOBIResult(0.0, 0, 101)

	// Test edge case: no asks
	mockBook.SetBids([]OrderBookEntry{{Price: 100, Size: 1.0}})
	mockBook.SetAsks([]OrderBookEntry{})
	calculator.Update()
	assertOBIResult(1.0, 100, 0)

	cancel()
	wg.Wait()
}
*/
