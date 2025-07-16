package benchmark

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

// mockDbWriter is a mock implementation of the dbwriter.Repository for testing.
type mockDbWriter struct {
	mu           sync.Mutex
	savedResults []*dbwriter.BenchmarkResult
}

func (m *mockDbWriter) SetReplaySessionID(string) {}
func (m *mockDbWriter) SaveOrderBookUpdate(dbwriter.OrderBookUpdate) {}
func (m *mockDbWriter) SaveTrade(dbwriter.Trade) {}
func (m *mockDbWriter) SavePnLSummary(context.Context, dbwriter.PnLSummary) error { return nil }
func (m *mockDbWriter) Close() {}

func (m *mockDbWriter) SaveBenchmarkResult(ctx context.Context, result *dbwriter.BenchmarkResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.savedResults = append(m.savedResults, result)
	return nil
}

func (m *mockDbWriter) GetSavedResults() []*dbwriter.BenchmarkResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	resultsCopy := make([]*dbwriter.BenchmarkResult, len(m.savedResults))
	copy(resultsCopy, m.savedResults)
	return resultsCopy
}

func newTestService(t *testing.T) (*Service, *mockDbWriter) {
	log := logger.NewLogger("debug")
	cfg := &config.Config{} // Default config is sufficient
	mockDB := &mockDbWriter{}
	service := NewService(cfg, mockDB, log)
	require.NotNil(t, service)
	return service, mockDB
}

func TestService_Tick(t *testing.T) {
	service, mockDB := newTestService(t)

	// --- First Tick ---
	// Manually set the initial state that would be set by the first price observation
	service.isFirstTick = true // Force the first tick logic

	// In the real service, a price feed would update the price.
	// In the test, we can simulate this by changing the hardcoded price in `tick`
	// or by making the price an argument. Since we can't change the implementation now,
	// we will rely on the hardcoded value inside tick().

	// Directly call tick() to test its logic
	service.tick(context.Background())

	// Assertions for the first tick
	results := mockDB.GetSavedResults()
	require.Len(t, results, 1, "Expected one result after the first tick")
	firstResult := results[0]

	assert.Equal(t, buyAndHoldStrategyID, firstResult.StrategyID)
	assert.Equal(t, btcPair, firstResult.Pair)

	// Calculation check:
	// Initial investment: 1,000,000 JPY
	// First price (hardcoded in tick): 10,000,000 JPY
	// BTC holdings = 1,000,000 / 10,000,000 = 0.1 BTC
	// Current value = 0.1 BTC * 10,000,000 JPY = 1,000,000 JPY
	expectedHoldings := decimal.NewFromFloat(0.1)
	expectedValue := decimal.NewFromFloat(1_000_000.0)

	assert.False(t, service.isFirstTick, "isFirstTick should be false after the first tick")
	assert.True(t, expectedHoldings.Equal(service.btcHoldings), "BTC holdings calculation is incorrect")
	assert.True(t, expectedValue.Equal(firstResult.Value), "Value calculation is incorrect for the first tick")

	// --- Second Tick ---
	// The price is still hardcoded, so the value should be the same.
	service.tick(context.Background())
	results = mockDB.GetSavedResults()
	require.Len(t, results, 2, "Expected two results after the second tick")
	secondResult := results[1]
	assert.True(t, expectedValue.Equal(secondResult.Value), "Value should be the same for the second tick if price doesn't change")
}


func TestService_RunAndStop(t *testing.T) {
	service, _ := newTestService(t)

	// Use a very short interval for testing the run loop
	tickInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	go service.Run(ctx)

	// Let it run for a bit longer than the tick interval
	time.Sleep(15 * time.Millisecond)

	cancel()
	service.Stop() // This should wait for the goroutine to finish

	// The main point of this test is to ensure the service can be started and stopped
	// without deadlocking or crashing. Asserting on the number of ticks can be flaky,
	// so we just ensure it stops gracefully.
	t.Log("Service started and stopped successfully.")
}

func TestService_Stop_Immediate(t *testing.T) {
	service, mockDB := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())

	go service.Run(ctx)

	// Immediately cancel and stop
	cancel()

	stopDone := make(chan struct{})
	go func() {
		service.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// success
	case <-time.After(1 * time.Second):
		t.Fatal("Service.Stop() timed out")
	}

	// Because the tick interval is much longer than the test duration,
	// no ticks should have occurred.
	assert.Empty(t, mockDB.GetSavedResults(), "No results should be saved if stopped immediately")
}
