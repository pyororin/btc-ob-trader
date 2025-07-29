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

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
