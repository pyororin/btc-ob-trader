package cvd

import (
	"testing"
	"time"
)

const floatTolerance = 1e-9 // Tolerance for float comparisons

func TestCVDCalculator_Update(t *testing.T) {
	calc := NewCVDCalculator(1 * time.Minute)
	now := time.Now()

	trades1 := []Trade{
		{ID: "1", Side: "buy", Size: 1.0, Timestamp: now},
		{ID: "2", Side: "sell", Size: 0.5, Timestamp: now},
	}
	cvd := calc.Update(trades1, now)
	if absFloat(cvd-0.5) > floatTolerance {
		t.Errorf("Expected CVD 0.5, got %f", cvd)
	}

	trades2 := []Trade{
		{ID: "3", Side: "buy", Size: 0.2, Timestamp: now},
	}
	cvd = calc.Update(trades2, now)
	if absFloat(cvd-0.7) > floatTolerance {
		t.Errorf("Expected CVD 0.7, got %f", cvd)
	}
}

func TestCVDCalculator_Window(t *testing.T) {
	calc := NewCVDCalculator(1 * time.Minute)
	now := time.Now()

	trades := []Trade{
		{ID: "1", Side: "buy", Size: 1.0, Timestamp: now.Add(-2 * time.Minute)},
		{ID: "2", Side: "sell", Size: 0.5, Timestamp: now.Add(-90 * time.Second)},
		{ID: "3", Side: "buy", Size: 0.2, Timestamp: now},
	}
	cvd := calc.Update(trades, now)

	// Only trade 3 should be in the window
	if absFloat(cvd-0.2) > floatTolerance {
		t.Errorf("Expected CVD 0.2, got %f", cvd)
	}
}

func TestCVDCalculator_DuplicateTrades(t *testing.T) {
	calc := NewCVDCalculator(1 * time.Minute)
	now := time.Now()

	trades1 := []Trade{
		{ID: "1", Side: "buy", Size: 1.0, Timestamp: now},
	}
	cvd := calc.Update(trades1, now)
	if absFloat(cvd-1.0) > floatTolerance {
		t.Errorf("Expected CVD 1.0, got %f", cvd)
	}

	// Update with the same trade again
	cvd = calc.Update(trades1, now)
	if absFloat(cvd-1.0) > floatTolerance {
		t.Errorf("Expected CVD to remain 1.0 after duplicate update, got %f", cvd)
	}
}

func TestCVDCalculator_RingBufferOverflow(t *testing.T) {
	// Create a calculator with a small ring buffer capacity
	calc := NewCVDCalculator(1 * time.Minute)
	// Manually set a small capacity for testing
	calc.trades = NewRingBuffer(3)
	calc.processedTrades = make(map[string]time.Time)

	now := time.Now()
	trades := []Trade{
		{ID: "1", Side: "buy", Size: 1.0, Timestamp: now.Add(-50 * time.Second)}, // Should be kept
		{ID: "2", Side: "buy", Size: 2.0, Timestamp: now.Add(-40 * time.Second)}, // Should be kept
		{ID: "3", Side: "buy", Size: 3.0, Timestamp: now.Add(-30 * time.Second)}, // Should be kept
	}
	calc.Update(trades, now)
	if absFloat(calc.GetCVD()-6.0) > floatTolerance {
		t.Fatalf("Expected initial CVD 6.0, got %f", calc.GetCVD())
	}

	// Add more trades to overflow the buffer
	overflowTrades := []Trade{
		{ID: "4", Side: "sell", Size: 0.5, Timestamp: now.Add(-20 * time.Second)}, // Overwrites trade 1
		{ID: "5", Side: "sell", Size: 1.5, Timestamp: now.Add(-10 * time.Second)}, // Overwrites trade 2
	}
	cvd := calc.Update(overflowTrades, now)

	// Expected trades in buffer: 3, 4, 5
	// Expected CVD = 3.0 (buy) - 0.5 (sell) - 1.5 (sell) = 1.0
	if absFloat(cvd-1.0) > floatTolerance {
		t.Errorf("Expected CVD 1.0 after overflow, got %f", cvd)
	}
	if calc.trades.Size() != 3 {
		t.Errorf("Expected ring buffer size to be 3, got %d", calc.trades.Size())
	}
}

func TestCVDCalculator_ProcessedIDCleanup(t *testing.T) {
	calc := NewCVDCalculator(1 * time.Minute)
	calc.trades = NewRingBuffer(10) // smaller capacity to test cleanup trigger

	now := time.Now()

	// Add enough trades to trigger the cleanup logic ( > capacity * 2 )
	tradesToProcess := make([]Trade, 0, 25)
	for i := 0; i < 15; i++ {
		// These are old and should be cleaned up
		trade := Trade{ID: "old_" + string(rune(i)), Side: "buy", Size: 1.0, Timestamp: now.Add(-11 * time.Minute)}
		tradesToProcess = append(tradesToProcess, trade)
	}
	for i := 0; i < 10; i++ {
		// These are recent and should remain
		trade := Trade{ID: "recent_" + string(rune(i)), Side: "buy", Size: 1.0, Timestamp: now.Add(-30 * time.Second)}
		tradesToProcess = append(tradesToProcess, trade)
	}

	// Process all trades at once. This will fill up processedTrades.
	// The cleanup logic runs at the end of the Update function.
	calc.Update(tradesToProcess, now)

	// After the update and subsequent cleanup, check the state
	// Expected: 10 recent trades in processedTrades, old ones removed.
	if len(calc.processedTrades) != 10 {
		t.Errorf("Expected processedTrades map to have 10 items after cleanup, got %d", len(calc.processedTrades))
	}

	// Verify that old trades are gone and recent ones remain
	for i := 0; i < 15; i++ {
		id := "old_" + string(rune(i))
		if _, exists := calc.processedTrades[id]; exists {
			t.Errorf("Expected old trade ID %s to be cleaned up, but it still exists", id)
		}
	}
	for i := 0; i < 10; i++ {
		id := "recent_" + string(rune(i))
		if _, exists := calc.processedTrades[id]; !exists {
			t.Errorf("Expected recent trade ID %s to remain, but it was cleaned up", id)
		}
	}

	// Check the ring buffer content
	if calc.trades.Size() != 10 {
		t.Errorf("Expected ring buffer to contain 10 recent trades, got %d", calc.trades.Size())
	}
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
